package planning

import (
	"context"
	"reflect"

	dbgen "canopy/server/db/generated"
	"github.com/jackc/pgx/v5/pgtype"
)

func (s *Service) Create(ctx context.Context, a Actor, in CreateInput, key *string) (Result, []Problem, error) {
	return s.mutate(ctx, a, "createPlantingPlan", key, in, func(q *dbgen.Queries) (Result, []Problem, error) {
		params, ps := validateIntent(in.Intent)
		if len(ps) > 0 {
			return Result{}, ps, nil
		}
		state := in.State
		if state == "" {
			state = "IDEA"
		}
		if !contains(state, "IDEA", "PLANNED") {
			return Result{}, invalid("input.state", "Create an idea or planned planting."), nil
		}
		if ps, err := validateLocation(ctx, q, a, params.IntendedLocationID); err != nil || len(ps) > 0 {
			return Result{}, ps, err
		}
		params.ID = s.IDs.New()
		params.WorkspaceID = a.WorkspaceID
		params.State = dbgen.PlantingPlanState(state)
		row, err := q.InsertPlantingPlan(ctx, params)
		if err != nil {
			return Result{}, nil, err
		}
		if err = s.audit(ctx, q, a, "createPlantingPlan", row, nil); err != nil {
			return Result{}, nil, err
		}
		return result(row), nil, nil
	})
}
func (s *Service) Update(ctx context.Context, a Actor, in UpdateInput, key *string) (Result, []Problem, error) {
	return s.mutate(ctx, a, "updatePlantingPlan", key, in, func(q *dbgen.Queries) (Result, []Problem, error) {
		old, ps, err := lockPlan(ctx, q, a, in.ID, in.ExpectedRevision, "input.id")
		if err != nil || len(ps) > 0 {
			return Result{}, ps, err
		}
		if !active(old.State) {
			return Result{}, invalid("input.id", "Restore a cancelled plan before editing; completed plans are final."), nil
		}
		params, ps := validateIntent(in.Intent)
		if len(ps) > 0 {
			return Result{}, ps, nil
		}
		if ps, err = validateLocation(ctx, q, a, params.IntendedLocationID); err != nil || len(ps) > 0 {
			return Result{}, ps, err
		}
		periodChanged := !reflect.DeepEqual(PeriodFromRow(old), in.TargetPeriod)
		contextChanged := periodChanged || old.Name != params.Name ||
			old.ScientificName != params.ScientificName || old.Cultivar != params.Cultivar ||
			old.Category != params.Category || old.DesiredQuantity != params.DesiredQuantity ||
			old.Method != params.Method || old.IntendedLocationID != params.IntendedLocationID ||
			old.IntendedPlacement != params.IntendedPlacement || old.Notes != params.Notes
		row, err := q.ReplacePlantingIntent(ctx, dbgen.ReplacePlantingIntentParams{
			WorkspaceID: a.WorkspaceID, ID: old.ID, Name: params.Name, ScientificName: params.ScientificName, Cultivar: params.Cultivar,
			Category: params.Category, DesiredQuantity: params.DesiredQuantity, Method: params.Method, IntendedLocationID: params.IntendedLocationID, IntendedPlacement: params.IntendedPlacement,
			TargetPrecision: params.TargetPrecision, TargetFrom: params.TargetFrom, TargetTo: params.TargetTo, TargetFromMonth: params.TargetFromMonth, TargetToMonth: params.TargetToMonth, TargetYear: params.TargetYear, TargetSeason: params.TargetSeason,
			BudgetMinor: params.BudgetMinor, BudgetCurrency: params.BudgetCurrency, Notes: params.Notes, ContextChanged: contextChanged, PeriodChanged: periodChanged,
		})
		if err != nil {
			return Result{}, nil, err
		}
		if err = s.audit(ctx, q, a, "updatePlantingPlan", row, old); err != nil {
			return Result{}, nil, err
		}
		return result(row), nil, nil
	})
}
func (s *Service) ChangeState(ctx context.Context, a Actor, in StateInput, key *string) (Result, []Problem, error) {
	return s.mutate(ctx, a, "changePlantingPlanState", key, in, func(q *dbgen.Queries) (Result, []Problem, error) {
		old, ps, err := lockPlan(ctx, q, a, in.ID, in.ExpectedRevision, "input.id")
		if err != nil || len(ps) > 0 {
			return Result{}, ps, err
		}
		if !LegalTransition(string(old.State), in.State) || in.State == "CANCELLED" || old.State == dbgen.PlantingPlanStateCANCELLED {
			return Result{}, invalid("input.state", "This state change is not available. Use cancel or restore where appropriate."), nil
		}
		if string(old.State) == in.State {
			return result(old), nil, nil
		}
		completedAt := pgtype.Timestamptz{}
		if in.State == "COMPLETED" {
			open, err := q.CountOpenPlantingActions(ctx, dbgen.CountOpenPlantingActionsParams{WorkspaceID: a.WorkspaceID, PlanID: old.ID})
			if err != nil {
				return Result{}, nil, err
			}
			if open > 0 {
				return Result{}, invalid("input.state", "Finish or remove open actions before completing the plan."), nil
			}
			completedAt = pgtype.Timestamptz{Time: s.Clock.Now(), Valid: true}
		}
		row, err := q.SetPlantingPlanState(ctx, dbgen.SetPlantingPlanStateParams{WorkspaceID: a.WorkspaceID, ID: old.ID, State: dbgen.PlantingPlanState(in.State), CompletedAt: completedAt})
		if err != nil {
			return Result{}, nil, err
		}
		if err = s.audit(ctx, q, a, "changePlantingPlanState", row, old); err != nil {
			return Result{}, nil, err
		}
		return result(row), nil, nil
	})
}
func (s *Service) Cancel(ctx context.Context, a Actor, in RevisionInput, key *string) (Result, []Problem, error) {
	return s.toggleCancellation(ctx, a, in, key, false)
}
func (s *Service) Restore(ctx context.Context, a Actor, in RevisionInput, key *string) (Result, []Problem, error) {
	return s.toggleCancellation(ctx, a, in, key, true)
}
func (s *Service) toggleCancellation(ctx context.Context, a Actor, in RevisionInput, key *string, restore bool) (Result, []Problem, error) {
	op := "cancelPlantingPlan"
	if restore {
		op = "restorePlantingPlan"
	}
	return s.mutate(ctx, a, op, key, in, func(q *dbgen.Queries) (Result, []Problem, error) {
		old, ps, err := lockPlan(ctx, q, a, in.ID, in.ExpectedRevision, "input.id")
		if err != nil || len(ps) > 0 {
			return Result{}, ps, err
		}
		params := dbgen.SetPlantingPlanStateParams{WorkspaceID: a.WorkspaceID, ID: old.ID}
		if restore {
			if old.State != dbgen.PlantingPlanStateCANCELLED {
				return Result{}, invalid("input.id", "Only cancelled plans can be restored."), nil
			}
			params.State = old.ResumeState.PlantingPlanState
		} else {
			if !active(old.State) {
				return Result{}, invalid("input.id", "Only active plans can be cancelled."), nil
			}
			params.State = dbgen.PlantingPlanStateCANCELLED
			params.ResumeState = dbgen.NullPlantingPlanState{PlantingPlanState: old.State, Valid: true}
			params.CancelledAt = pgtype.Timestamptz{Time: s.Clock.Now(), Valid: true}
		}
		row, err := q.SetPlantingPlanState(ctx, params)
		if err != nil {
			return Result{}, nil, err
		}
		// Eligibility changes invalidate pending reminders, without claiming actual
		// completion or destroying the user's selected future days.
		if err = q.AdvancePlantingActionSchedules(ctx, dbgen.AdvancePlantingActionSchedulesParams{WorkspaceID: a.WorkspaceID, PlanID: old.ID}); err != nil {
			return Result{}, nil, err
		}
		if err = s.audit(ctx, q, a, op, row, old); err != nil {
			return Result{}, nil, err
		}
		return result(row), nil, nil
	})
}
