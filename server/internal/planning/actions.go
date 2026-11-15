package planning

import (
	"context"
	"errors"

	dbgen "canopy/server/db/generated"
	"github.com/jackc/pgx/v5"
)

func (s *Service) CreateAction(ctx context.Context, a Actor, in ActionInput, key *string) (Result, []Problem, error) {
	return s.mutate(ctx, a, "createPlantingAction", key, in, func(q *dbgen.Queries) (Result, []Problem, error) {
		plan, ps, err := lockPlan(ctx, q, a, in.PlanID, in.ExpectedRevision, "input.planId")
		if err != nil || len(ps) > 0 {
			return Result{}, ps, err
		}
		if !active(plan.State) {
			return Result{}, invalid("input.planId", "Actions can only change on an active plan."), nil
		}
		params, ps := validateAction(in)
		if len(ps) > 0 {
			return Result{}, ps, nil
		}
		count, err := q.CountPlantingActions(ctx, dbgen.CountPlantingActionsParams{WorkspaceID: a.WorkspaceID, PlanID: plan.ID})
		if err != nil {
			return Result{}, nil, err
		}
		if count >= MaxActions {
			return Result{}, invalid("input.planId", "A plan can contain at most 100 actions."), nil
		}
		params.ID = s.IDs.New()
		params.WorkspaceID = a.WorkspaceID
		params.PlanID = plan.ID
		if _, err = q.InsertPlantingAction(ctx, params); err != nil {
			return Result{}, nil, err
		}
		row, err := q.BumpPlantingPlan(ctx, dbgen.BumpPlantingPlanParams{WorkspaceID: a.WorkspaceID, ID: plan.ID})
		if err != nil {
			return Result{}, nil, err
		}
		if err = s.audit(ctx, q, a, "createPlantingAction", row, plan); err != nil {
			return Result{}, nil, err
		}
		return result(row), nil, nil
	})
}
func (s *Service) UpdateAction(ctx context.Context, a Actor, in ActionInput, key *string) (Result, []Problem, error) {
	return s.mutate(ctx, a, "updatePlantingAction", key, in, func(q *dbgen.Queries) (Result, []Problem, error) {
		plan, ps, err := lockPlan(ctx, q, a, in.PlanID, in.ExpectedRevision, "input.planId")
		if err != nil || len(ps) > 0 {
			return Result{}, ps, err
		}
		old, ps, err := openAction(ctx, q, a, plan, in.ID)
		if err != nil || len(ps) > 0 {
			return Result{}, ps, err
		}
		params, ps := validateAction(in)
		if len(ps) > 0 {
			return Result{}, ps, nil
		}
		changed := params.Kind != old.Kind || params.ScheduledOn != old.ScheduledOn || params.ReminderOn != old.ReminderOn
		if _, err = q.UpdatePlantingAction(ctx, dbgen.UpdatePlantingActionParams{WorkspaceID: a.WorkspaceID, PlanID: plan.ID, ID: old.ID, Kind: params.Kind, Title: params.Title, ScheduledOn: params.ScheduledOn, ReminderOn: params.ReminderOn, Notes: params.Notes, ScheduleChanged: changed}); err != nil {
			return Result{}, nil, err
		}
		row, err := q.BumpPlantingPlan(ctx, dbgen.BumpPlantingPlanParams{WorkspaceID: a.WorkspaceID, ID: plan.ID})
		if err != nil {
			return Result{}, nil, err
		}
		if err = s.audit(ctx, q, a, "updatePlantingAction", row, struct {
			Plan   dbgen.PlantingPlan
			Action dbgen.PlantingAction
		}{plan, old}); err != nil {
			return Result{}, nil, err
		}
		return result(row), nil, nil
	})
}
func (s *Service) DeleteAction(ctx context.Context, a Actor, in DeleteActionInput, key *string) (Result, []Problem, error) {
	return s.mutate(ctx, a, "deletePlantingAction", key, in, func(q *dbgen.Queries) (Result, []Problem, error) {
		plan, ps, err := lockPlan(ctx, q, a, in.PlanID, in.ExpectedRevision, "input.planId")
		if err != nil || len(ps) > 0 {
			return Result{}, ps, err
		}
		old, ps, err := openAction(ctx, q, a, plan, in.ID)
		if err != nil || len(ps) > 0 {
			return Result{}, ps, err
		}
		if err = q.DeletePlantingAction(ctx, dbgen.DeletePlantingActionParams{WorkspaceID: a.WorkspaceID, PlanID: plan.ID, ID: old.ID}); err != nil {
			return Result{}, nil, err
		}
		row, err := q.BumpPlantingPlan(ctx, dbgen.BumpPlantingPlanParams{WorkspaceID: a.WorkspaceID, ID: plan.ID})
		if err != nil {
			return Result{}, nil, err
		}
		if err = s.audit(ctx, q, a, "deletePlantingAction", row, struct {
			Plan   dbgen.PlantingPlan
			Action dbgen.PlantingAction
		}{plan, old}); err != nil {
			return Result{}, nil, err
		}
		return result(row), nil, nil
	})
}
func openAction(ctx context.Context, q *dbgen.Queries, a Actor, plan dbgen.PlantingPlan, rawID string) (dbgen.PlantingAction, []Problem, error) {
	var zero dbgen.PlantingAction
	if !active(plan.State) {
		return zero, invalid("input.planId", "Actions can only change on an active plan."), nil
	}
	id, ok := parseID(rawID)
	if !ok {
		return zero, invalid("input.id", "Use a valid action id."), nil
	}
	row, err := q.GetPlantingAction(ctx, dbgen.GetPlantingActionParams{WorkspaceID: a.WorkspaceID, PlanID: plan.ID, ID: id})
	if errors.Is(err, pgx.ErrNoRows) {
		return zero, []Problem{{Code: "NOT_FOUND", Field: "input.id", Message: "Action not found."}}, nil
	}
	if err != nil {
		return zero, nil, err
	}
	if row.State != dbgen.PlantingActionStateOPEN {
		return zero, invalid("input.id", "Only open actions can be edited or removed."), nil
	}
	return row, nil, nil
}
