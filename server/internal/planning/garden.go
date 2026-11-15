package planning

import (
	dbgen "canopy/server/db/generated"
	"canopy/server/internal/collection/commands"
	"context"
	"errors"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"time"
)

func plantingKind(k dbgen.PlantingActionKind) bool {
	return contains(string(k), "START_INDOORS", "DIRECT_SOW", "TRANSPLANT", "PLANT_OUT")
}

func (s *Service) gardenCalendar(ctx context.Context, q *dbgen.Queries, a Actor) (time.Time, *time.Location, error) {
	ws, err := q.LockPlanningReminderWorkspace(ctx, a.WorkspaceID)
	if err != nil {
		return time.Time{}, nil, err
	}
	zone, err := time.LoadLocation(ws.Timezone)
	return s.Clock.Now(), zone, err
}
func actualDay(raw string, now time.Time, zone *time.Location) (time.Time, []Problem) {
	if !validDate(raw) || raw > now.In(zone).Format(time.DateOnly) {
		return time.Time{}, invalid("input.completedOn", "Choose a real day on or before today in your garden's timezone.")
	}
	d, _ := time.Parse(time.DateOnly, raw)
	return d, nil
}
func actualContext(in *commands.ContextInput, on *time.Time, now time.Time, zone *time.Location) (*commands.ContextInput, []Problem) {
	if in == nil {
		return nil, nil
	}
	out := *in
	if out.EffectiveFrom == nil {
		effective := now
		if on != nil {
			effective = time.Date(on.Year(), on.Month(), on.Day(), 12, 0, 0, 0, zone)
			if effective.After(now) {
				effective = now
			}
		}
		out.EffectiveFrom = &effective
	}
	if out.EffectiveFrom.After(now) || out.EffectiveFrom.Year() < 1 || out.EffectiveFrom.Year() > 9999 {
		return nil, invalid("input.actualContext.effectiveFrom", "Confirm where the plant actually is; a future move belongs in the plan.")
	}
	if on != nil && out.EffectiveFrom.In(zone).Format(time.DateOnly) != on.Format(time.DateOnly) {
		return nil, invalid("input.actualContext.effectiveFrom", "Use the actual planting day for this confirmed placement.")
	}
	return &out, nil
}
func reviewedPlant(in commands.CreatePlantInput, now time.Time, zone *time.Location) (commands.CreatePlantInput, []Problem) {
	today := now.In(zone).Format(time.DateOnly)
	dates := map[string]*time.Time{"plantedOn": in.PlantedOn}
	if in.Acquisition != nil {
		dates["acquisition.acquiredOn"] = in.Acquisition.AcquiredOn
	}
	if in.InitialPurchase != nil {
		dates["initialPurchase.purchasedOn"] = in.InitialPurchase.PurchasedOn
	}
	for name, d := range dates {
		if d != nil && (!validDate(d.Format(time.DateOnly)) || d.Format(time.DateOnly) > today) {
			return in, invalid("input.newPlant."+name, "Actual dates must be on or before today; keep future dates in your plan.")
		}
	}
	context, ps := actualContext(in.InitialContext, nil, now, zone)
	in.InitialContext = context
	return in, ps
}
func existingFulfillment(ctx context.Context, q *dbgen.Queries, a Actor, plan dbgen.PlantingPlan) (*dbgen.PlantingFulfillment, error) {
	f, err := q.GetPlantingFulfillment(ctx, dbgen.GetPlantingFulfillmentParams{WorkspaceID: a.WorkspaceID, PlanID: plan.ID})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return &f, err
}
func goneFulfillment() []Problem {
	return []Problem{{Code: "RESOURCE_GONE", Field: "input.planId", Message: "The plant originally linked to this plan was removed. Keep this history and make a new plan to start again."}}
}
func (s *Service) AddToGarden(ctx context.Context, a Actor, in AddToGardenInput, key *string) (Result, []Problem, error) {
	return s.mutate(ctx, a, "addPlantingToGarden", key, in, func(q *dbgen.Queries) (Result, []Problem, error) {
		p, ps, err := lockPlan(ctx, q, a, in.PlanID, in.ExpectedRevision, "input.planId")
		if err != nil || len(ps) > 0 {
			return Result{}, ps, err
		}
		if !active(p.State) {
			return Result{}, invalid("input.planId", "Only an active plan can be added to the garden."), nil
		}
		old, err := existingFulfillment(ctx, q, a, p)
		if err != nil {
			return Result{}, nil, err
		}
		if old != nil {
			if !old.PlantID.Valid {
				return Result{}, goneFulfillment(), nil
			}
			return Result{}, []Problem{{Code: "CONFLICT", Field: "input.planId", Message: "This plan already has a garden plant. Open it or complete its remaining actions."}}, nil
		}
		if (in.NewPlant == nil) == (in.ExistingPlantID == nil) {
			return Result{}, invalid("input.newPlant", "Review one new actual plant, or choose one existing plant."), nil
		}
		if (in.ActionID == nil) != (in.CompletedOn == nil) {
			return Result{}, invalid("input.actionId", "Confirm both the planting action and the actual day, or leave both blank for acquisition only."), nil
		}
		now, zone, err := s.gardenCalendar(ctx, q, a)
		if err != nil {
			return Result{}, nil, err
		}
		var action *dbgen.PlantingAction
		var on time.Time
		if in.ActionID != nil {
			row, ps, err := openAction(ctx, q, a, p, *in.ActionID)
			if err != nil || len(ps) > 0 {
				return Result{}, ps, err
			}
			if !plantingKind(row.Kind) {
				return Result{}, invalid("input.actionId", "Only sowing or planting can be completed here. Buying seeds or preparing space does not create a living plant."), nil
			}
			on, ps = actualDay(*in.CompletedOn, now, zone)
			if len(ps) > 0 {
				return Result{}, ps, nil
			}
			action = &row
		} else if in.ExpectedPlantRevision != nil {
			return Result{}, invalid("input.expectedPlantRevision", "A plant revision is only needed when confirming an existing plant's planting."), nil
		}
		var plantID pgtype.UUID
		mode := dbgen.PlantingLinkModeEXISTING
		if in.NewPlant != nil {
			if s.Garden == nil {
				return Result{}, []Problem{{Code: "CAPABILITY_UNAVAILABLE", Message: "Garden creation is unavailable."}}, nil
			}
			actual, ps := reviewedPlant(*in.NewPlant, now, zone)
			if len(ps) > 0 {
				return Result{}, ps, nil
			}
			if action != nil {
				if actual.PlantedOn != nil && actual.PlantedOn.Format(time.DateOnly) != on.Format(time.DateOnly) {
					return Result{}, invalid("input.newPlant.plantedOn", "Use the same actual planting day as the completed action."), nil
				}
				actual.PlantedOn = &on
				actual.InitialContext, ps = actualContext(in.NewPlant.InitialContext, &on, now, zone)
				if len(ps) > 0 {
					return Result{}, ps, nil
				}
			}
			created, ps, err := s.Garden.Create(ctx, q, a, actual)
			if err != nil || len(ps) > 0 {
				return Result{}, ps, err
			}
			var ok bool
			plantID, ok = parseID(created.ID)
			if !ok {
				return Result{}, nil, errors.New("garden returned an invalid plant reference")
			}
			mode = dbgen.PlantingLinkModeCREATED
		} else {
			var ok bool
			plantID, ok = parseID(*in.ExistingPlantID)
			if !ok {
				return Result{}, invalid("input.existingPlantId", "Choose a valid plant."), nil
			}
			plant, err := q.LockPlanningActualPlant(ctx, dbgen.LockPlanningActualPlantParams{WorkspaceID: a.WorkspaceID, ID: plantID})
			if errors.Is(err, pgx.ErrNoRows) {
				return Result{}, []Problem{{Code: "NOT_FOUND", Field: "input.existingPlantId", Message: "Plant not found in this garden."}}, nil
			}
			if err != nil {
				return Result{}, nil, err
			}
			if plant.Status != dbgen.PlantStatusACTIVE {
				return Result{}, invalid("input.existingPlantId", "Choose an active plant in this garden."), nil
			}
			if action != nil {
				if in.ExpectedPlantRevision == nil || *in.ExpectedPlantRevision != plant.Revision {
					return Result{}, plantRevisionProblem(plant.Revision), nil
				}
				if s.Garden == nil {
					return Result{}, []Problem{{Code: "CAPABILITY_UNAVAILABLE", Message: "Garden updates are unavailable."}}, nil
				}
				_, ps, err := s.Garden.RecordPlanting(ctx, q, a, ActualPlantingInput{PlantID: idString(plantID), ExpectedRevision: plant.Revision, On: on})
				if err != nil || len(ps) > 0 {
					return Result{}, ps, err
				}
			}
		}
		actionID := pgtype.UUID{}
		if action != nil {
			actionID = action.ID
			if err = s.completeActionRow(ctx, q, a, *action, on, now); err != nil {
				return Result{}, nil, err
			}
		}
		_, err = q.InsertPlantingFulfillment(ctx, dbgen.InsertPlantingFulfillmentParams{ID: s.IDs.New(), WorkspaceID: a.WorkspaceID, PlanID: p.ID, PlantID: plantID, ActionID: actionID, Mode: mode})
		if err != nil {
			return Result{}, nil, err
		}
		updated, err := q.StartPlantingPlan(ctx, dbgen.StartPlantingPlanParams{WorkspaceID: a.WorkspaceID, ID: p.ID})
		if err != nil {
			return Result{}, nil, err
		}
		if err = s.audit(ctx, q, a, "addPlantingToGarden", updated, p); err != nil {
			return Result{}, nil, err
		}
		r := result(updated)
		r.PlantID = idString(plantID)
		return r, nil, nil
	})
}
func plantRevisionProblem(current int32) []Problem {
	return []Problem{{Code: "CONFLICT", Field: "input.expectedPlantRevision", Message: "The actual plant changed. Reload before recording its planting.", CurrentRevision: &current}}
}
func (s *Service) completeActionRow(ctx context.Context, q *dbgen.Queries, a Actor, action dbgen.PlantingAction, on, now time.Time) error {
	_, err := q.CompletePlantingActionRow(ctx, dbgen.CompletePlantingActionRowParams{WorkspaceID: a.WorkspaceID, PlanID: action.PlanID, ID: action.ID, CompletedOn: pgtype.Date{Time: on, Valid: true}, CompletedAt: pgtype.Timestamptz{Time: now, Valid: true}})
	return err
}
func (s *Service) CompleteAction(ctx context.Context, a Actor, in CompleteActionInput, key *string) (Result, []Problem, error) {
	return s.mutate(ctx, a, "completePlantingAction", key, in, func(q *dbgen.Queries) (Result, []Problem, error) {
		p, ps, err := lockPlan(ctx, q, a, in.PlanID, in.ExpectedRevision, "input.planId")
		if err != nil || len(ps) > 0 {
			return Result{}, ps, err
		}
		action, ps, err := openAction(ctx, q, a, p, in.ID)
		if err != nil || len(ps) > 0 {
			return Result{}, ps, err
		}
		now, zone, err := s.gardenCalendar(ctx, q, a)
		if err != nil {
			return Result{}, nil, err
		}
		on, ps := actualDay(in.CompletedOn, now, zone)
		if len(ps) > 0 {
			return Result{}, ps, nil
		}
		plantID := ""
		if plantingKind(action.Kind) {
			f, err := existingFulfillment(ctx, q, a, p)
			if err != nil {
				return Result{}, nil, err
			}
			if f == nil {
				return Result{}, invalid("input.planId", "Add or link the living plant in Add to garden before completing a sowing or planting action."), nil
			}
			if !f.PlantID.Valid {
				return Result{}, goneFulfillment(), nil
			}
			plant, err := q.LockPlanningActualPlant(ctx, dbgen.LockPlanningActualPlantParams{WorkspaceID: a.WorkspaceID, ID: f.PlantID})
			if err != nil {
				return Result{}, nil, err
			}
			if plant.Status != dbgen.PlantStatusACTIVE {
				return Result{}, invalid("input.planId", "Restore the actual plant before recording its planting."), nil
			}
			if in.ExpectedPlantRevision == nil || *in.ExpectedPlantRevision != plant.Revision {
				return Result{}, plantRevisionProblem(plant.Revision), nil
			}
			context, ps := actualContext(in.ActualContext, &on, now, zone)
			if len(ps) > 0 {
				return Result{}, ps, nil
			}
			if s.Garden == nil {
				return Result{}, []Problem{{Code: "CAPABILITY_UNAVAILABLE", Message: "Garden updates are unavailable."}}, nil
			}
			_, ps, err = s.Garden.RecordPlanting(ctx, q, a, ActualPlantingInput{PlantID: idString(f.PlantID), ExpectedRevision: plant.Revision, On: on, Context: context})
			if err != nil || len(ps) > 0 {
				return Result{}, ps, err
			}
			plantID = idString(f.PlantID)
		} else if in.ActualContext != nil || in.ExpectedPlantRevision != nil {
			return Result{}, invalid("input.actualContext", "Preparing space or buying supplies does not change a living plant."), nil
		}
		if err = s.completeActionRow(ctx, q, a, action, on, now); err != nil {
			return Result{}, nil, err
		}
		updated, err := q.BumpPlantingPlan(ctx, dbgen.BumpPlantingPlanParams{WorkspaceID: a.WorkspaceID, ID: p.ID})
		if err != nil {
			return Result{}, nil, err
		}
		if err = s.audit(ctx, q, a, "completePlantingAction", updated, struct {
			Plan   dbgen.PlantingPlan
			Action dbgen.PlantingAction
		}{p, action}); err != nil {
			return Result{}, nil, err
		}
		r := result(updated)
		r.PlantID = plantID
		return r, nil, nil
	})
}
func (s *Service) DismissAction(ctx context.Context, a Actor, in DeleteActionInput, key *string) (Result, []Problem, error) {
	return s.mutate(ctx, a, "dismissPlantingAction", key, in, func(q *dbgen.Queries) (Result, []Problem, error) {
		p, ps, err := lockPlan(ctx, q, a, in.PlanID, in.ExpectedRevision, "input.planId")
		if err != nil || len(ps) > 0 {
			return Result{}, ps, err
		}
		action, ps, err := openAction(ctx, q, a, p, in.ID)
		if err != nil || len(ps) > 0 {
			return Result{}, ps, err
		}
		_, err = q.DismissPlantingActionRow(ctx, dbgen.DismissPlantingActionRowParams{WorkspaceID: a.WorkspaceID, PlanID: p.ID, ID: action.ID, DismissedAt: pgtype.Timestamptz{Time: s.Clock.Now(), Valid: true}})
		if err != nil {
			return Result{}, nil, err
		}
		updated, err := q.BumpPlantingPlan(ctx, dbgen.BumpPlantingPlanParams{WorkspaceID: a.WorkspaceID, ID: p.ID})
		if err != nil {
			return Result{}, nil, err
		}
		if err = s.audit(ctx, q, a, "dismissPlantingAction", updated, struct {
			Plan   dbgen.PlantingPlan
			Action dbgen.PlantingAction
		}{p, action}); err != nil {
			return Result{}, nil, err
		}
		return result(updated), nil, nil
	})
}
