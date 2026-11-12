// Package care implements the R2 knowledge-domain use cases: sources with
// append-only versions, reviewed care profiles with the draft/publish/retire
// lifecycle, and the starter-profile import (T21).
//
// Rules enforced here (03-contracts "Reviewed knowledge" + care.graphql):
//   - source updates append a new immutable SourceVersion; the aggregate is
//     revision-guarded, versions are never edited (DB trigger backs this).
//   - retired sources are excluded from new citations; pinned versions stay
//     inspectable.
//   - profile content is editable only while DRAFT; publish freezes the draft
//     version as REVIEWED with reviewer attribution (DB trigger requires the
//     fields); retire only touches status + retired_at.
//   - draft deletion is permitted only while unpublished and unreferenced by
//     any plan pin.
//   - recurrence accepts exactly the four frozen forms (exactly-one rule);
//     the canonical jsonb shape is documented in ADR-0002.
//   - every mutation is one transaction with a guarded revision bump and
//     optional clientMutationId replay receipts (same protocol as the other
//     domains).
//
// Plans/occurrences (T22/T23) and the GraphQL surface (T24) build on this
// package; nothing here touches HTTP.
package care

import (
	"canopy/server/internal/agentcontext"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	dbgen "canopy/server/db/generated"
	"canopy/server/internal/care/weather"
	"canopy/server/internal/email"
	"canopy/server/internal/platform"
)

// Problem codes (mirror the frozen ProblemCode enum in the contract SDL).
const (
	CodeValidation          = "VALIDATION"
	CodeConflict            = "CONFLICT"
	CodeNotFound            = "NOT_FOUND"
	CodeIdempotencyConflict = "IDEMPOTENCY_CONFLICT"
	CodeResourceGone        = "RESOURCE_GONE"
)

// ReceiptRetention is how long idempotency receipts live: 30 days
// (03-contracts deletion matrix; same protocol as the other domains).
const ReceiptRetention = 30 * 24 * time.Hour

// Problem is a payload-level result: it travels inside the mutation
// payload, never as a transport error.
type Problem struct {
	Code            string
	Field           string
	Message         string
	CurrentRevision *int32 // present on CONFLICT
}

func problem(code, field, msg string) Problem {
	return Problem{Code: code, Field: field, Message: msg}
}

func problemWithRevision(p Problem, rev int32) Problem {
	p.CurrentRevision = &rev
	return p
}

// Actor identifies who is performing a use case.
type Actor struct {
	WorkspaceID pgtype.UUID
	OwnerID     pgtype.UUID
	SessionID   pgtype.UUID
}

// Service carries the pool; mutations run in transactions.
type Service struct {
	Pool *pgxpool.Pool
	Log  *slog.Logger

	// Weather is the outbound forecast adapter (T26); nil means the
	// deployment opted out — locations simply never get snapshots and
	// advice proceeds on calendar cadence alone.
	Weather *weather.Client

	// Email is the outbound SMTP adapter (T27); nil means the deployment
	// opted out — EMAIL digest intents stay pending (visible, undelivered)
	// and in-app notification is unaffected. EmailTo is the single owner
	// recipient, from config, never user input.
	Email   email.Sender
	EmailTo string

	uuid  platform.UUIDv4
	clock platform.SystemClock
}

func New(pool *pgxpool.Pool, log *slog.Logger) *Service {
	return &Service{Pool: pool, Log: log}
}

// q builds a query set bound to the pool for read paths (mutations use a
// transaction-scoped Queries inside mutate).
func (s *Service) q() *dbgen.Queries { return dbgen.New(s.Pool) }

// mutate runs fn inside one transaction with idempotency handling — the same
// receipt protocol as collection/journal: advisory xact lock serializes
// concurrent replays; a matching receipt replays the stored result; a
// different hash under the same key is IDEMPOTENCY_CONFLICT; any problem
// rolls the whole transaction back.
func mutate[T any](s *Service, ctx context.Context, a Actor, op string, cmid *string, input any,
	fn func(ctx context.Context, q *dbgen.Queries) (T, []Problem),
) (T, []Problem, error) {
	var zero T
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return zero, nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := dbgen.New(tx)
	if err := agentcontext.Enter(ctx, tx); err != nil {
		return zero, nil, err
	}

	key := ""
	if cmid != nil {
		key = strings.TrimSpace(*cmid)
	}
	if key != "" {
		lockID := uuidString(a.WorkspaceID) + "|" + op + "|" + key
		if err := q.LockMutationKey(ctx, lockID); err != nil {
			return zero, nil, err
		}
		rec, err := q.GetMutationReceipt(ctx, dbgen.GetMutationReceiptParams{
			WorkspaceID: a.WorkspaceID, Operation: op, IdempotencyKey: key,
		})
		switch {
		case err == nil:
			if rec.RequestHash != requestHash(input) {
				return zero, []Problem{problem(CodeIdempotencyConflict, "clientMutationId",
					"clientMutationId was already used for a different request.")}, nil
			}
			if rec.Gone {
				return zero, []Problem{problem(CodeResourceGone, "clientMutationId",
					"the resource this stored response referred to has been purged.")}, nil
			}
			var replay T
			if err := json.Unmarshal(rec.Result, &replay); err != nil {
				return zero, nil, fmt.Errorf("receipt decode: %w", err)
			}
			return replay, nil, nil
		case !errors.Is(err, pgx.ErrNoRows):
			return zero, nil, err
		}
	}

	res, problems := fn(ctx, q)
	if len(problems) > 0 {
		return zero, problems, nil // rollback: nothing was written
	}

	if key != "" {
		resultJSON, err := json.Marshal(res)
		if err != nil {
			return zero, nil, err
		}
		if _, err := q.InsertMutationReceipt(ctx, dbgen.InsertMutationReceiptParams{
			ID:             s.uuid.New(),
			WorkspaceID:    a.WorkspaceID,
			Operation:      op,
			IdempotencyKey: key,
			RequestHash:    requestHash(input),
			Result:         resultJSON,
			ExpiresAt:      pgtype.Timestamptz{Time: s.clock.Now().Add(ReceiptRetention), Valid: true},
		}); err != nil {
			return zero, nil, err
		}
	}
	if err := agentcontext.Complete(ctx, tx, res); err != nil {
		return zero, nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return zero, nil, err
	}
	return res, nil, nil
}

// requestHash is a stable hash of the request input for replay detection.
func requestHash(input any) string {
	h := sha256.New()
	enc := json.NewEncoder(h)
	_ = enc.Encode(input) // struct JSON never fails here
	return hex.EncodeToString(h.Sum(nil))
}

// audit records a bounded revision row for an aggregate change.
func (s *Service) audit(ctx context.Context, q *dbgen.Queries, a Actor, action, aggregateType string,
	aggregateID pgtype.UUID, revision int32, prior any,
) error {
	var priorJSON []byte
	if prior != nil {
		b, err := json.Marshal(prior)
		if err != nil {
			return err
		}
		priorJSON = b
	}
	_, err := q.InsertAuditRevision(ctx, dbgen.InsertAuditRevisionParams{
		ID:            s.uuid.New(),
		WorkspaceID:   a.WorkspaceID,
		ActorID:       a.OwnerID,
		Action:        action,
		AggregateType: aggregateType,
		AggregateID:   aggregateID,
		Revision:      revision,
		RequestID:     uuidString(a.SessionID),
		PriorValues:   priorJSON,
	})
	return err
}

// ---- small shared helpers -------------------------------------------------

func uuidString(u pgtype.UUID) string {
	if !u.Valid {
		return ""
	}
	h := hex.EncodeToString(u.Bytes[:])
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:32]
}

func parseID(s string) (pgtype.UUID, bool) {
	var u pgtype.UUID
	if err := u.Scan(s); err != nil || !u.Valid {
		return pgtype.UUID{}, false
	}
	return u, true
}

func textPtr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// charLen counts runes (contract text limits are character-based).
func charLen(s string) int { return len([]rune(s)) }
