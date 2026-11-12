// Package insights is the AI analysis lane (T29): run lifecycle, budget
// gating, bounded input snapshots, result validation, and the provider
// adapter seam.
//
// Guardrails (03-contracts "AI, identification, and OCR"):
//   - Provider output is untrusted data. Nothing is persisted or rendered
//     unless it passes the validator (length/schema/enums/evidence refs).
//     The adapter never executes, fetches, or obeys provider output.
//   - Budget: every billable attempt reserves its max cost BEFORE
//     dispatch; timeouts/cancels after dispatch keep the encumbrance
//     until reconciliation; refunds only for proven-undispatched attempts.
//   - Worker fencing: every transition carries the row generation, so a
//     superseded worker's late answer cannot write (STALE, never
//     resurrected).
//   - No-provider mode: with no configured endpoint every capability is
//     honestly unavailable and no run can be requested.
package insights

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	dbgen "canopy/server/db/generated"
	"canopy/server/internal/platform"
)

// Problem codes (mirror the frozen ProblemCode enum in the contract SDL).
const (
	CodeValidation            = "VALIDATION"
	CodeConflict              = "CONFLICT"
	CodeNotFound              = "NOT_FOUND"
	CodeIdempotencyConflict   = "IDEMPOTENCY_CONFLICT"
	CodeCapabilityUnavailable = "CAPABILITY_UNAVAILABLE"
	CodeInternal              = "INTERNAL"
	CodeRateLimited           = "RATE_LIMITED"
)

// ReceiptRetention matches the other domains: 30 days.
const ReceiptRetention = 30 * 24 * time.Hour

// Problem is a payload-level result carried inside mutation payloads.
type Problem struct {
	Code            string
	Field           string
	Message         string
	CurrentRevision *int32
}

func problem(code, field, msg string) Problem {
	return Problem{Code: code, Field: field, Message: msg}
}

func problemWithRevision(p Problem, rev int32) Problem {
	p.CurrentRevision = &rev
	return p
}

// Actor identifies the requesting principal (same shape as care/collection).
type Actor struct {
	WorkspaceID pgtype.UUID
	OwnerID     pgtype.UUID
	SessionID   pgtype.UUID
}

// Limits is the operator-configured budget envelope.
type Limits struct {
	DailyRunLimit      int32
	ConcurrentRunLimit int32
	// MonthCeilingMicroUSD is the external-cost ceiling for the UTC
	// calendar month; 0 means no monetary ceiling (e.g. local provider).
	MonthCeilingMicroUSD int64
	// AttemptCostMicroUSD is the operator price bound for one provider
	// attempt; reserved before every dispatch (03: "operator-defined USD
	// price bounds or explicit bounded per-run approval").
	AttemptCostMicroUSD int64
}

// Service owns the run lifecycle use cases.
type Service struct {
	Pool  *pgxpool.Pool
	Log   *slog.Logger
	Limit Limits

	// Provider is the outbound adapter; a NoProvider value (or nil) is
	// no-provider mode: every capability is unavailable, requests fail
	// with CAPABILITY_UNAVAILABLE, and nothing is ever fetched.
	Provider Port

	// Queue inserts the analysis job in the same tx as the run row
	// (API process). Nil in the worker process, which only executes.
	Queue *platform.RiverClient

	// Adapter pins (stored on every committed result for provenance)
	// and per-attempt bounds, from operator config.
	PromptVersion  string
	SchemaVersion  string
	AttemptTimeout time.Duration
	MaxResultBytes int64

	uuid  platform.UUIDv4
	clock platform.SystemClock
}

func New(pool *pgxpool.Pool, log *slog.Logger, limit Limits, provider Port) *Service {
	if provider == nil {
		provider = NoProvider{}
	}
	return &Service{Pool: pool, Log: log, Limit: limit, Provider: provider}
}

func (s *Service) q() *dbgen.Queries { return dbgen.New(s.Pool) }

func (s *Service) now() time.Time { return s.clock.Now() }

func uuidString(u pgtype.UUID) string {
	if !u.Valid {
		return ""
	}
	b := u.Bytes
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

func parseUUID(s string) (pgtype.UUID, error) {
	var u pgtype.UUID
	if err := u.Scan(s); err != nil {
		return u, fmt.Errorf("insights: bad uuid %q", s)
	}
	if !u.Valid {
		return u, fmt.Errorf("insights: empty uuid")
	}
	return u, nil
}

func requestHash(v any) string {
	b, _ := json.Marshal(v)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// budgetLockID keys the per-workspace advisory lock that serializes
// budget check+insert and check+reserve across processes.
func budgetLockID(ws pgtype.UUID) string { return "analysis-budget|" + uuidString(ws) }

// utcMonth returns [start,end) of the UTC calendar month containing t.
func utcMonth(t time.Time) (time.Time, time.Time) {
	start := time.Date(t.UTC().Year(), t.UTC().Month(), 1, 0, 0, 0, 0, time.UTC)
	return start, start.AddDate(0, 1, 0)
}

// localDayStart returns the UTC instant of workspace-local midnight for
// the day containing now in the workspace timezone (SDL: daily limit is
// per workspace-local day). An unknown timezone degrades to UTC.
func localDayStart(tz string, now time.Time) time.Time {
	loc, err := time.LoadLocation(tz)
	if err != nil || loc == nil {
		loc = time.UTC
	}
	local := now.In(loc)
	midnight := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, loc)
	return midnight.UTC()
}

// semanticKey composes the dedup identity: kind + target + input hash.
// The active-only partial unique index (T02.ai) makes duplicates fail
// while a run is QUEUED/RUNNING; terminal runs free the key so reruns are
// new rows, never in-place resets.
func semanticKey(kind, targetKey, inputHash string) string {
	return kind + ":" + targetKey + ":" + inputHash
}

func targetKeyFor(kind dbgen.AnalysisKind, plant, receipt pgtype.UUID) string {
	switch kind {
	case dbgen.AnalysisKindRECEIPTEXTRACTION:
		return "receipt:" + uuidString(receipt)
	default:
		return "plant:" + uuidString(plant)
	}
}

// riverInsertTx inserts the analysis job inside the caller's tx so a
// rollback leaves neither run nor job.
func (s *Service) riverInsertTx(ctx context.Context, tx pgx.Tx, runID pgtype.UUID) error {
	if s.Queue == nil {
		return errors.New("insights: no queue wired for run insert")
	}
	_, err := s.Queue.InsertTx(ctx, tx, RunArgs{RunID: uuidString(runID)}, &river.InsertOpts{
		// One queue item per run; duplicate deliveries are fenced by the
		// generation guard in the worker. River-side job attempts cover
		// the bounded provider retries (snooze counts as a job attempt);
		// the DB attempts<3 claim guard is the hard bound.
		MaxAttempts: 5,
	})
	return err
}
