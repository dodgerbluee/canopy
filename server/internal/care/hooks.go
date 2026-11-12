// Collection lifecycle hooks (T24). These satisfy collection.Hook
// structurally: the composition root wires them so plant archive/restore
// and context edits keep care state consistent, inside the collection
// mutation's own transaction (the Queries handed in is the tx's).
package care

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	dbgen "canopy/server/db/generated"
)

// CollectionHook carries the care service; every method is a no-op for
// plants without a care plan.
type CollectionHook struct {
	Svc *Service
}

// PlantArchived cancels every open occurrence for the plant's plan. The
// plan itself survives (restore regenerates); ticks also skip plans whose
// plant is not ACTIVE, so no new work appears while archived.
func (h CollectionHook) PlantArchived(ctx context.Context, q *dbgen.Queries, workspaceID, plantID pgtype.UUID) error {
	if _, err := q.CancelOpenOccurrencesForPlant(ctx, dbgen.CancelOpenOccurrencesForPlantParams{
		WorkspaceID: workspaceID, PlantID: plantID}); err != nil {
		return err
	}
	return nil
}

// PlantRestored regenerates the 60-day horizon from the plan-local today.
func (h CollectionHook) PlantRestored(ctx context.Context, q *dbgen.Queries, workspaceID, plantID pgtype.UUID) error {
	plan, err := q.GetCarePlanByPlant(ctx, plantID)
	if err != nil {
		if isNoRows(err) {
			return nil
		}
		return err
	}
	if plan.WorkspaceID != workspaceID || !plan.Enabled {
		return nil
	}
	tz, err := time.LoadLocation(plan.Timezone)
	if err != nil {
		return err
	}
	plan.LastGeneratedThrough = pgtype.Date{} // force regeneration
	return h.Svc.materializeTx(ctx, q, plan, todayIn(tz), time.Now().UTC())
}

// PlantContextChanged invalidates future advice affected by the edit:
// open slots due on/after the plan-local today are cancelled and the
// horizon regenerates against the new context. Past-due slots stay
// actionable (their advice was already delivered); history is untouched.
func (h CollectionHook) PlantContextChanged(ctx context.Context, q *dbgen.Queries, workspaceID, plantID pgtype.UUID, _ time.Time) error {
	// T33: advice produced before this context change must be re-reviewed
	// by the owner before acceptance (03 source/context-change rule).
	// This runs even when the plant has no care plan: suggestions live
	// independently of scheduling.
	if _, err := q.FlagSuggestionsNeedingReview(ctx, dbgen.FlagSuggestionsNeedingReviewParams{
		WorkspaceID: workspaceID, RunIds: []pgtype.UUID{}, PlantIds: []pgtype.UUID{plantID},
	}); err != nil {
		return err
	}
	plan, err := q.GetCarePlanByPlant(ctx, plantID)
	if err != nil {
		if isNoRows(err) {
			return nil
		}
		return err
	}
	if plan.WorkspaceID != workspaceID {
		return nil
	}
	tz, err := time.LoadLocation(plan.Timezone)
	if err != nil {
		return err
	}
	today := todayIn(tz)
	if _, err := q.CancelFutureOpenOccurrencesForPlant(ctx, dbgen.CancelFutureOpenOccurrencesForPlantParams{
		WorkspaceID: workspaceID, PlantID: plantID, FromDate: engToPg(today)}); err != nil {
		return err
	}
	if !plan.Enabled {
		return nil
	}
	plan.LastGeneratedThrough = pgtype.Date{} // force regeneration
	return h.Svc.materializeTx(ctx, q, plan, today, time.Now().UTC())
}

// flagReviewForRetiredVersion finds SUCCEEDED runs citing a care profile
// version (as guidance source or version) and forces owner review on the
// pending suggestions those runs produced (T33).
func flagReviewForRetiredVersion(ctx context.Context, q *dbgen.Queries, ws, profileID, versionID pgtype.UUID) error {
	runIDs, err := q.RunsCitingSource(ctx, dbgen.RunsCitingSourceParams{
		WorkspaceID: ws, SourceID: uuidString(profileID), SourceVersionID: uuidString(versionID),
	})
	if err != nil {
		return err
	}
	if len(runIDs) == 0 {
		return nil
	}
	_, err = q.FlagSuggestionsNeedingReview(ctx, dbgen.FlagSuggestionsNeedingReviewParams{
		WorkspaceID: ws, RunIds: runIDs, PlantIds: []pgtype.UUID{},
	})
	return err
}
