package planning

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	dbgen "canopy/server/db/generated"
	"canopy/server/internal/agentcontext"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

func requestHash(input any) (string, error) {
	data, err := json.Marshal(input)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

// A receipt, audit revision, and every aggregate write commit together. A failed
// validation or audit leaves neither a partial child nor a receipt behind.
func (s *Service) mutate(ctx context.Context, a Actor, op string, cmid *string, input any,
	body func(*dbgen.Queries) (Result, []Problem, error)) (Result, []Problem, error) {
	var zero Result
	key := ""
	if cmid != nil {
		key = strings.TrimSpace(*cmid)
	}
	if utf8.RuneCountInString(key) > 200 {
		return zero, invalid("clientMutationId", "Keep the request key within 200 characters."), nil
	}
	hash, err := requestHash(input)
	if err != nil {
		return zero, nil, err
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return zero, nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err = agentcontext.Enter(ctx, tx); err != nil {
		return zero, nil, err
	}
	q := dbgen.New(tx)
	if key != "" {
		if err = q.LockMutationKey(ctx, idString(a.WorkspaceID)+"|"+op+"|"+key); err != nil {
			return zero, nil, err
		}
		receipt, e := q.GetMutationReceipt(ctx, dbgen.GetMutationReceiptParams{WorkspaceID: a.WorkspaceID, Operation: op, IdempotencyKey: key})
		switch {
		case e == nil:
			if receipt.RequestHash != hash {
				return zero, []Problem{{Code: "IDEMPOTENCY_CONFLICT", Field: "clientMutationId", Message: "This request key was used with different values."}}, nil
			}
			if receipt.Gone {
				return zero, []Problem{{Code: "RESOURCE_GONE", Field: "clientMutationId", Message: "The resource recorded by this request was removed."}}, nil
			}
			var replay Result
			if err = json.Unmarshal(receipt.Result, &replay); err != nil {
				return zero, nil, err
			}
			return replay, nil, nil
		case !errors.Is(e, pgx.ErrNoRows):
			return zero, nil, e
		}
	}
	res, problems, err := body(q)
	if err != nil || len(problems) > 0 {
		return zero, problems, err
	}
	if key != "" {
		data, e := json.Marshal(res)
		if e != nil {
			return zero, nil, e
		}
		// Reuse after TTL replaces only expired rows, under the same advisory lock.
		if err = q.InsertPlanningMutationReceipt(ctx, dbgen.InsertPlanningMutationReceiptParams{ID: s.IDs.New(), WorkspaceID: a.WorkspaceID, Operation: op, IdempotencyKey: key, RequestHash: hash, Result: data, ExpiresAt: pgtype.Timestamptz{Time: s.Clock.Now().Add(30 * 24 * time.Hour), Valid: true}}); err != nil {
			return zero, nil, err
		}
	}
	if err = agentcontext.Complete(ctx, tx, res); err != nil {
		return zero, nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return zero, nil, err
	}
	return res, nil, nil
}
func (s *Service) audit(ctx context.Context, q *dbgen.Queries, a Actor, op string, row dbgen.PlantingPlan, prior any) error {
	var data []byte
	var err error
	if prior != nil {
		data, err = json.Marshal(prior)
		if err != nil {
			return err
		}
	}
	_, err = q.InsertAuditRevision(ctx, dbgen.InsertAuditRevisionParams{ID: s.IDs.New(), WorkspaceID: a.WorkspaceID, ActorID: a.OwnerID, Action: op, AggregateType: "planting_plan", AggregateID: row.ID, Revision: row.Revision, RequestID: idString(a.SessionID), PriorValues: data})
	return err
}
func lockPlan(ctx context.Context, q *dbgen.Queries, a Actor, id string, expected int32, idField string) (dbgen.PlantingPlan, []Problem, error) {
	var zero dbgen.PlantingPlan
	uid, ok := parseID(id)
	if !ok {
		return zero, invalid(idField, "Use a valid plan id."), nil
	}
	if expected < 1 {
		return zero, invalid("input.expectedRevision", "Use the current positive plan revision."), nil
	}
	row, err := q.LockPlantingPlan(ctx, dbgen.LockPlantingPlanParams{WorkspaceID: a.WorkspaceID, ID: uid})
	if errors.Is(err, pgx.ErrNoRows) {
		return zero, []Problem{{Code: "NOT_FOUND", Field: idField, Message: "Plan not found."}}, nil
	}
	if err != nil {
		return zero, nil, err
	}
	if row.Revision != expected {
		rev := row.Revision
		return zero, []Problem{{Code: "CONFLICT", Field: "input.expectedRevision", Message: "This plan changed. Reload before saving.", CurrentRevision: &rev}}, nil
	}
	return row, nil, nil
}
func validateLocation(ctx context.Context, q *dbgen.Queries, a Actor, location pgtype.UUID) ([]Problem, error) {
	if !location.Valid {
		return nil, nil
	}
	_, err := q.LockPlantingLocation(ctx, dbgen.LockPlantingLocationParams{ID: location, WorkspaceID: a.WorkspaceID})
	if errors.Is(err, pgx.ErrNoRows) {
		return invalid("input.intendedLocationId", "Choose a location in this garden."), nil
	}
	return nil, err
}
