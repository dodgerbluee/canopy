// Package purchases implements the R1 money domain: purchase commands
// (create/update/void), acquisition pricing transitions, and the spending
// read model. Exact minor-unit arithmetic lives in internal/money; this
// package enforces conservation (line and overhead sums) atomically,
// per ADR-0002.
//
// Cross-record locking follows 03: purchases are locked first, then
// acquisitions, each in stable UUID order, so concurrent priceAcquisition
// and purchase edits serialize without deadlock.
package purchases

import (
	"canopy/server/internal/agentcontext"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	dbgen "canopy/server/db/generated"
	"canopy/server/internal/platform"
)

// Problem codes (mirror the frozen ProblemCode enum in the contract SDL).
const (
	CodeValidation            = "VALIDATION"
	CodeConflict              = "CONFLICT"
	CodeNotFound              = "NOT_FOUND"
	CodeIdempotencyConflict   = "IDEMPOTENCY_CONFLICT"
	CodeResourceGone          = "RESOURCE_GONE"
	CodeCapabilityUnavailable = "CAPABILITY_UNAVAILABLE"
)

// ReceiptRetention is how long idempotency receipts live: 30 days
// (03-contracts deletion matrix: op/key/input hash are retained until the
// original TTL even after the stored response becomes RESOURCE_GONE).
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

// Actor identifies who is performing a use case (same shape as the
// collection Actor; transport maps both).
type Actor struct {
	WorkspaceID pgtype.UUID
	OwnerID     pgtype.UUID
	SessionID   pgtype.UUID
}

// Service carries the pool; mutations own their transactions.
type Service struct {
	Pool *pgxpool.Pool
	Log  *slog.Logger

	uuid  platform.UUIDv4
	clock platform.SystemClock
}

func New(pool *pgxpool.Pool, log *slog.Logger) *Service {
	return &Service{Pool: pool, Log: log}
}

func (s *Service) q() *dbgen.Queries { return dbgen.New(s.Pool) }

// mutate runs fn inside one transaction with idempotency handling —
// same advisory-lock + mutation_receipts protocol as the collection
// service, kept package-local so domain packages stay acyclic.
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
		return zero, problems, nil
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

func requestHash(input any) string {
	h := sha256.New()
	enc := json.NewEncoder(h)
	_ = enc.Encode(input)
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

// conflictProblem maps a zero-row guarded update to CONFLICT with the
// current revision re-read inside the same transaction.
func (s *Service) conflictProblem(ctx context.Context, q *dbgen.Queries, id, workspaceID pgtype.UUID, err error) []Problem {
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		s.Log.Error("purchase guarded update", "err", err)
		return []Problem{problem(CodeValidation, "", "purchase update failed")}
	}
	cur, cerr := q.GetPurchase(ctx, dbgen.GetPurchaseParams{ID: id, WorkspaceID: workspaceID})
	if cerr != nil {
		return notFound("input.id", "purchase not found")
	}
	rev := cur.Revision
	return []Problem{Problem{Code: CodeConflict, Field: "input.expectedRevision",
		Message: "purchase was modified concurrently", CurrentRevision: &rev}}
}

func notFound(field, msg string) []Problem {
	return []Problem{problem(CodeNotFound, field, msg)}
}

// ---- small shared helpers (package-local mirrors of collection's) --------

func uuidString(u pgtype.UUID) string {
	if !u.Valid {
		return ""
	}
	h := hex.EncodeToString(u.Bytes[:])
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:32]
}

func parseUUID(s string) (pgtype.UUID, bool) {
	var u pgtype.UUID
	if err := u.Scan(s); err != nil || !u.Valid {
		return pgtype.UUID{}, false
	}
	return u, true
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

func textPtr(s *string) pgtype.Text {
	if s == nil {
		return pgtype.Text{}
	}
	return pgtype.Text{String: *s, Valid: true}
}

func datePtr(t *time.Time) pgtype.Date {
	if t == nil {
		return pgtype.Date{}
	}
	return pgtype.Date{Time: *t, Valid: true}
}

func minor(n int64) pgtype.Numeric {
	return pgtype.Numeric{Int: new(big.Int).SetInt64(n), Exp: 0, Valid: true}
}
