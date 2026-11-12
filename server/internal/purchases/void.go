package purchases

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"

	dbgen "canopy/server/db/generated"
	"canopy/server/internal/journal"
)

// VoidPurchase marks a purchase voided under a revision guard and, in
// the same transaction, atomically reverts every acquisition priced from
// this purchase to UNKNOWN (provenance retained, 03 pricing rules).
// Voided purchases drop out of all spending aggregates.
func (s *Service) VoidPurchase(ctx context.Context, a Actor, id string, expectedRevision int32, cmid *string) (Result, []Problem, error) {
	pid, ok := parseUUID(id)
	if !ok {
		return Result{}, []Problem{problem(CodeValidation, "input.id", "malformed id")}, nil
	}
	wire := map[string]any{"id": id, "rev": expectedRevision}
	return mutate[Result](s, ctx, a, "voidPurchase", cmid, wire,
		func(ctx context.Context, q *dbgen.Queries) (Result, []Problem) {
			prow, err := q.LockPurchase(ctx, dbgen.LockPurchaseParams{ID: pid, WorkspaceID: a.WorkspaceID})
			if err != nil {
				if errors.Is(err, pgx.ErrNoRows) {
					return Result{}, notFound("input.id", "purchase not found")
				}
				s.Log.Error("lock purchase", "err", err)
				return Result{}, []Problem{problem(CodeValidation, "", "purchase lock failed")}
			}
			if prow.Revision != expectedRevision {
				return Result{}, conflictWith(prow.Revision)
			}
			if prow.VoidedAt.Valid {
				return Result{}, []Problem{problem(CodeConflict, "input.id", "purchase is already voided")}
			}
			// Cascade: acquisitions priced from this purchase go UNKNOWN,
			// in stable UUID order (query is ORDER BY a.id).
			linked, err := q.AcquisitionsForPurchase(ctx, dbgen.AcquisitionsForPurchaseParams{
				PurchaseID: pid, WorkspaceID: a.WorkspaceID,
			})
			if err != nil {
				s.Log.Error("linked acquisitions", "err", err)
				return Result{}, []Problem{problem(CodeValidation, "", "acquisition links could not be read")}
			}
			for _, acq := range linked {
				reverted, err := q.RevertAcquisitionUnknown(ctx, dbgen.RevertAcquisitionUnknownParams{
					ID: acq.ID, WorkspaceID: a.WorkspaceID, ExpectedRevision: acq.Revision,
				})
				if err != nil {
					return Result{}, s.acqConflict(ctx, q, acq.ID, a.WorkspaceID, err)
				}
				if err := s.audit(ctx, q, a, "voidPurchase.unlink", "acquisition", acq.ID, reverted.Revision, acq); err != nil {
					return Result{}, []Problem{problem(CodeValidation, "", "audit write failed")}
				}
			}
			voided, err := q.VoidPurchase(ctx, dbgen.VoidPurchaseParams{
				ID: pid, WorkspaceID: a.WorkspaceID, Revision: expectedRevision,
			})
			if err != nil {
				return Result{}, s.conflictProblem(ctx, q, pid, a.WorkspaceID, err)
			}
			if err := s.audit(ctx, q, a, "voidPurchase", "purchase", pid, voided.Revision, prow); err != nil {
				return Result{}, []Problem{problem(CodeValidation, "", "audit write failed")}
			}
			// journal tombstone (T18): the purchase's entries leave active
			// timelines but stay attributable with includeTombstoned.
			if err := journal.TombstonePurchaseEntries(ctx, q, a.WorkspaceID, pid); err != nil {
				s.Log.Error("tombstone purchase entries", "err", err)
				return Result{}, []Problem{problem(CodeValidation, "", "journal tombstone failed")}
			}
			return Result{ID: uuidString(pid), Revision: voided.Revision}, nil
		})
}
