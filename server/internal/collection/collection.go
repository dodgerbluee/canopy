// Package collection implements the R1 collection domain use cases:
// plant/location CRUD, context history, tags, archive/restore, acquisition
// provenance (unknown pricing in this release), revisions and idempotency.
//
// Every mutation runs in one transaction with a guarded revision bump;
// stale writes answer CONFLICT with the current revision and never a
// partial write. Mutations carrying a clientMutationId are replay-safe via
// mutation_receipts: a duplicate create returns the original result instead
// of creating twice.
//
// Profile/care integration goes through the explicit Hook interface only;
// nothing in this package schedules anything (that lands in T24).
package collection

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

// Problem is a payload-level result: it travels inside the mutation
// payload, never as a transport error.
type Problem struct {
	Code            string
	Field           string // input field path, e.g. "input.name"
	Message         string
	CurrentRevision *int32 // present on CONFLICT
}

func problem(code, field, msg string) Problem {
	return Problem{Code: code, Field: field, Message: msg}
}

// Actor identifies who is performing a use case.
type Actor struct {
	WorkspaceID pgtype.UUID
	OwnerID     pgtype.UUID
	SessionID   pgtype.UUID
}

// Hook is the explicit application interface through which the collection
// domain notifies other contexts (profile/care). Behavior is wired in T24;
// this package never schedules anything itself. Hooks run inside the
// mutation's transaction (they receive its Queries): a care-side failure
// rolls the collection write back with it, so archive/context state and
// care state never diverge.
type Hook interface {
	// PlantContextChanged invalidates future advice affected by the new
	// context (cancel + regenerate happens inside the care implementation).
	PlantContextChanged(ctx context.Context, q *dbgen.Queries, workspaceID, plantID pgtype.UUID, effectiveFrom time.Time) error
	// PlantArchived cancels the plant's open care work.
	PlantArchived(ctx context.Context, q *dbgen.Queries, workspaceID, plantID pgtype.UUID) error
	// PlantRestored regenerates the plant's future care work.
	PlantRestored(ctx context.Context, q *dbgen.Queries, workspaceID, plantID pgtype.UUID) error
}

// NoopHook satisfies Hook until the composition root connects real care
// behavior (T24 wires the care service implementation).
type NoopHook struct{}

func (NoopHook) PlantContextChanged(context.Context, *dbgen.Queries, pgtype.UUID, pgtype.UUID, time.Time) error {
	return nil
}

func (NoopHook) PlantArchived(context.Context, *dbgen.Queries, pgtype.UUID, pgtype.UUID) error {
	return nil
}

func (NoopHook) PlantRestored(context.Context, *dbgen.Queries, pgtype.UUID, pgtype.UUID) error {
	return nil
}

// Service carries the pool (mutations need transactions) and the hook.
type Service struct {
	Pool  *pgxpool.Pool
	Log   *slog.Logger
	Hooks Hook

	// Queue schedules the deferred asset-purge and export-cleanup jobs
	// that a plant purge leaves behind; nil is allowed only in unit
	// contexts (same convention as media.Service).
	Queue *platform.RiverClient

	uuid  platform.UUIDv4
	clock platform.SystemClock
}

func New(pool *pgxpool.Pool, log *slog.Logger, hooks Hook) *Service {
	if hooks == nil {
		hooks = NoopHook{}
	}
	return &Service{Pool: pool, Log: log, Hooks: hooks}
}

// q builds a query set bound to the pool for read paths (mutations use a
// transaction-scoped Queries inside mutate).
func (s *Service) q() *dbgen.Queries { return dbgen.New(s.Pool) }

// mutate runs fn inside one transaction with idempotency handling.
//
// With a clientMutationId: an advisory xact lock serializes concurrent
// replays; a stored receipt with a matching request hash returns the stored
// result (replay — no second execution); a different hash for the same key
// is IDEMPOTENCY_CONFLICT. Without problems, fn's result is recorded as the
// receipt and committed. Any problem rolls the whole transaction back.
func mutate[T any](s *Service, ctx context.Context, a Actor, op string, cmid *string, input any,
	fn func(ctx context.Context, q *dbgen.Queries) (T, []Problem),
) (T, []Problem, error) {
	return mutateTx(s, ctx, a, op, cmid, input,
		func(ctx context.Context, q *dbgen.Queries, _ pgx.Tx) (T, []Problem) { return fn(ctx, q) })
}

// mutateTx is the receipt protocol with the transaction exposed to the
// body; purgePlant needs it to enqueue river jobs atomically with the
// rows they clean up.
func mutateTx[T any](s *Service, ctx context.Context, a Actor, op string, cmid *string, input any,
	fn func(ctx context.Context, q *dbgen.Queries, tx pgx.Tx) (T, []Problem),
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

	res, problems := fn(ctx, q, tx)
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

func parseUUID(s string) (pgtype.UUID, bool) {
	var u pgtype.UUID
	if err := u.Scan(s); err != nil || !u.Valid {
		return pgtype.UUID{}, false
	}
	return u, true
}

// parseUUIDs validates a list; on the first bad entry it returns a problem
// bound to field.
func parseUUIDs(ids []string, field string) ([]pgtype.UUID, *Problem) {
	out := make([]pgtype.UUID, 0, len(ids))
	for _, s := range ids {
		u, ok := parseUUID(s)
		if !ok {
			return nil, ptrProblem(problem(CodeValidation, field, "malformed id"))
		}
		out = append(out, u)
	}
	return out, nil
}

func ptrProblem(p Problem) *Problem { return &p }

// numericSign returns -1/0/1 for a pgtype.Numeric (invalid/NaN counts as 0).
func numericSign(n *pgtype.Numeric) int {
	if n == nil || !n.Valid || n.NaN || n.Int == nil {
		return 0
	}
	if n.InfinityModifier == pgtype.Infinity {
		return 1
	}
	if n.InfinityModifier == pgtype.NegativeInfinity {
		return -1
	}
	return n.Int.Sign()
}

// isUniqueViolation reports a Postgres unique-constraint failure.
func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

// isForeignKeyViolation reports a Postgres FK-restiction failure.
func isForeignKeyViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23503"
}
