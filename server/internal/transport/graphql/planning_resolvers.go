package graphql

import (
	"context"
	"errors"

	"canopy/server/internal/money"
	"canopy/server/internal/planning"
	"canopy/server/internal/transport/graphql/gql"
	"canopy/server/internal/transport/graphql/scalars"
	httpx "canopy/server/internal/transport/http"
)

func planningActor(p httpx.Principal) planning.Actor {
	return planning.Actor{WorkspaceID: p.WorkspaceID, OwnerID: p.OwnerID, SessionID: p.SessionID}
}
func plantingPeriodIn(in *gql.PlantingPeriodInput) *planning.Period {
	if in == nil {
		return nil
	}
	out := &planning.Period{Precision: planning.PeriodPrecision(in.Precision), Year: in.Year}
	if in.Dates != nil {
		out.Dates = &planning.DateRange{From: string(in.Dates.From), To: string(in.Dates.To)}
	}
	if in.Months != nil {
		out.Months = &planning.MonthRange{From: in.Months.From, To: in.Months.To}
	}
	if in.Season != nil {
		out.Season = &planning.SeasonPeriod{Season: planning.Season(in.Season.Season), Year: in.Season.Year}
	}
	return out
}
func plantingPeriodOut(in *planning.Period) *gql.PlantingPeriod {
	if in == nil {
		return nil
	}
	out := &gql.PlantingPeriod{Precision: gql.PlantingPeriodPrecision(in.Precision), Year: in.Year}
	if in.Dates != nil {
		out.Dates = &gql.PlantingDateRange{From: scalars.Date(in.Dates.From), To: scalars.Date(in.Dates.To)}
	}
	if in.Months != nil {
		out.Months = &gql.PlantingMonthRange{From: in.Months.From, To: in.Months.To}
	}
	if in.Season != nil {
		out.Season = &gql.PlantingSeasonPeriod{Season: gql.PlantingSeason(in.Season.Season), Year: in.Season.Year}
	}
	return out
}

// Domain inputs use int32; reject out-of-range GraphQL Int values before any
// narrowing conversion. Zero is an invalid sentinel checked by the domain.
func plantingPositiveInt(value int) int32 {
	if value < 1 || value > 2147483647 {
		return 0
	}
	return int32(value)
}
func plantingString[T ~string](in *T) *string {
	if in == nil {
		return nil
	}
	v := string(*in)
	return &v
}
func plantingQuantity(in *int) *int32 {
	if in == nil {
		return nil
	}
	v := plantingPositiveInt(*in)
	return &v
}
func plantingBudget(in *gql.PlantingBudgetInput) *planning.Budget {
	if in == nil {
		return nil
	}
	return &planning.Budget{MinorUnits: string(in.MinorUnits), Currency: in.Currency}
}
func plantingCreateIntent(in gql.CreatePlantingPlanInput) planning.Intent {
	return planning.Intent{
		Name: in.Name, ScientificName: in.ScientificName, Cultivar: in.Cultivar,
		Category: string(in.Category), DesiredQuantity: plantingQuantity(in.DesiredQuantity),
		Method: plantingString(in.Method), IntendedLocationID: in.IntendedLocationID,
		IntendedPlacement: plantingString(in.IntendedPlacement), TargetPeriod: plantingPeriodIn(in.TargetPeriod),
		Budget: plantingBudget(in.Budget), Notes: in.Notes,
	}
}
func plantingUpdateIntent(in gql.UpdatePlantingPlanInput) planning.Intent {
	return planning.Intent{
		Name: in.Name, ScientificName: in.ScientificName, Cultivar: in.Cultivar,
		Category: string(in.Category), DesiredQuantity: plantingQuantity(in.DesiredQuantity),
		Method: plantingString(in.Method), IntendedLocationID: in.IntendedLocationID,
		IntendedPlacement: plantingString(in.IntendedPlacement), TargetPeriod: plantingPeriodIn(in.TargetPeriod),
		Budget: plantingBudget(in.Budget), Notes: in.Notes,
	}
}
func (q queryResolver) PlantingPlans(ctx context.Context, first *int, after *string, filter *gql.PlantingPlanFilter, order *gql.PlantingPlanOrder) (*gql.PlantingPlanConnection, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	if q.r.Planning == nil {
		return nil, capabilityUnavailable("planning")
	}
	in := planning.Query{First: 25, After: after, Order: planning.OrderNameAsc}
	if first != nil {
		if *first < 1 || *first > 100 {
			return nil, invalidFilterError()
		}
		in.First = int32(*first)
	}
	if order != nil {
		in.Order = string(*order)
	}
	if filter != nil {
		if filter.Query != nil {
			in.Search = *filter.Query
		}
		if filter.States != nil {
			in.States = make([]string, len(filter.States))
			for i, v := range filter.States {
				in.States[i] = string(v)
			}
		}
		if filter.Method != nil {
			v := string(*filter.Method)
			in.Method = &v
		}
		in.LocationID = filter.LocationID
	}
	page, err := q.r.Planning.List(ctx, planningActor(p), in)
	if errors.Is(err, planning.ErrInvalidQuery) {
		return nil, invalidFilterError()
	}
	if errors.Is(err, planning.ErrInvalidCursor) {
		return nil, invalidCursorError()
	}
	if err != nil {
		return nil, internalError(ctx, err)
	}
	out := &gql.PlantingPlanConnection{Items: []*gql.PlantingPlan{}, PageInfo: &gql.PageInfo{HasNextPage: page.HasNext, EndCursor: page.EndCursor}}
	locations := map[string]*gql.Location{}
	for _, plan := range page.Plans {
		item, err := q.plantingPlanOut(ctx, p, plan, locations)
		if err != nil {
			return nil, internalError(ctx, err)
		}
		out.Items = append(out.Items, item)
	}
	return out, nil
}
func (q queryResolver) PlantingPlan(ctx context.Context, id string) (*gql.PlantingPlan, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	if q.r.Planning == nil {
		return nil, capabilityUnavailable("planning")
	}
	plan, err := q.r.Planning.Get(ctx, planningActor(p), id)
	if err != nil {
		return nil, internalError(ctx, err)
	}
	if plan == nil {
		return nil, nil
	}
	out, err := q.plantingPlanOut(ctx, p, *plan, map[string]*gql.Location{})
	if err != nil {
		return nil, internalError(ctx, err)
	}
	return out, nil
}
func (q queryResolver) plantingPlanOut(ctx context.Context, p httpx.Principal, plan planning.Plan, locations map[string]*gql.Location) (*gql.PlantingPlan, error) {
	row := plan.Row
	out := &gql.PlantingPlan{ID: uuidString(row.ID), Name: row.Name, Category: gql.PlantCategory(row.Category), TargetPeriod: plantingPeriodOut(planning.PeriodFromRow(row)), State: gql.PlantingPlanState(row.State), Revision: int(row.Revision), GuidanceGeneration: int(row.GuidanceGeneration), Actions: []*gql.PlantingAction{}, CancelledAt: dtPtrOut(row.CancelledAt), CompletedAt: dtPtrOut(row.CompletedAt), CreatedAt: dtOut(row.CreatedAt), UpdatedAt: dtOut(row.UpdatedAt)}
	if row.ScientificName.Valid {
		out.ScientificName = &row.ScientificName.String
	}
	if row.Cultivar.Valid {
		out.Cultivar = &row.Cultivar.String
	}
	if row.Notes.Valid {
		out.Notes = &row.Notes.String
	}
	if row.DesiredQuantity.Valid {
		v := int(row.DesiredQuantity.Int32)
		out.DesiredQuantity = &v
	}
	if row.Method.Valid {
		v := gql.PlantingMethod(row.Method.PlantingMethod)
		out.Method = &v
	}
	if row.IntendedPlacement.Valid {
		v := gql.ContextPlacement(row.IntendedPlacement.ContextPlacement)
		out.IntendedPlacement = &v
	}
	if row.BudgetMinor.Valid {
		out.Budget = &gql.Money{MinorUnits: scalars.MinorUnits(money.Format(row.BudgetMinor.Int64)), Currency: row.BudgetCurrency.String}
	}
	if row.IntendedLocationID.Valid {
		loc, err := q.locationChain(ctx, actor(p), row.IntendedLocationID, locations, map[string]bool{})
		if err != nil {
			return nil, err
		}
		out.IntendedLocation = loc
	}
	for _, a := range plan.Actions {
		out.Actions = append(out.Actions, &gql.PlantingAction{ID: uuidString(a.ID), Kind: gql.PlantingActionKind(a.Kind), Title: a.Title, ScheduledOn: *dateOut(a.ScheduledOn), ReminderOn: dateOut(a.ReminderOn), ScheduleRevision: int(a.ScheduleRevision), Notes: textPtr(a.Notes), State: gql.PlantingActionState(a.State), CompletedOn: dateOut(a.CompletedOn), CompletedAt: dtPtrOut(a.CompletedAt), DismissedAt: dtPtrOut(a.DismissedAt), CreatedAt: dtOut(a.CreatedAt), UpdatedAt: dtOut(a.UpdatedAt)})
	}
	var err error
	out.LatestGuidance, err = plantingGuidanceOut(plan.LatestGuidance)
	if err != nil {
		return nil, err
	}
	out.AcceptedGuidance, err = plantingGuidanceOut(plan.AcceptedGuidance)
	if err != nil {
		return nil, err
	}
	if f := plan.Fulfillment; f != nil {
		out.Fulfillment = &gql.PlantingFulfillment{ID: uuidString(f.ID), Mode: gql.PlantingGardenLinkMode(f.Mode), LinkedAt: dtOut(f.LinkedAt)}
		if f.ActionID.Valid {
			v := uuidString(f.ActionID)
			out.Fulfillment.ActionID = &v
		}
		if f.PlantID.Valid {
			out.Fulfillment.Plant, err = q.Plant(ctx, uuidString(f.PlantID))
			if err != nil {
				return nil, err
			}
		}
	}
	return out, nil
}
func (m mutationResolver) planningCommand(ctx context.Context, key *string, run func(planning.Actor) (planning.Result, []planning.Problem, error)) (*gql.PlantingPlanPayload, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	if m.r.Planning == nil {
		return nil, capabilityUnavailable("planning")
	}
	res, problems, err := run(planningActor(p))
	if err != nil {
		m.r.Log.ErrorContext(ctx, "planning command failed", "err", err)
		return nil, internalError(ctx, err)
	}
	out := &gql.PlantingPlanPayload{ClientMutationID: key, Problems: []*gql.MutationProblem{}}
	for _, problem := range problems {
		mp := mutationProblem(gql.ProblemCode(problem.Code), problem.Field, problem.Message)
		if problem.CurrentRevision != nil {
			v := int(*problem.CurrentRevision)
			mp.CurrentRevision = &v
		}
		out.Problems = append(out.Problems, mp)
	}
	if len(problems) > 0 {
		return out, nil
	}
	out.Plan, err = (queryResolver{m.r}).PlantingPlan(ctx, res.ID)
	if err != nil {
		return nil, err
	}
	if out.Plan == nil {
		out.Problems = append(out.Problems, mutationProblem(gql.ProblemCodeResourceGone, "input.id", "This plan was removed."))
	}
	return out, nil
}
func (m mutationResolver) CreatePlantingPlan(ctx context.Context, in gql.CreatePlantingPlanInput, key *string) (*gql.PlantingPlanPayload, error) {
	intent := plantingCreateIntent(in)
	return m.planningCommand(ctx, key, func(a planning.Actor) (planning.Result, []planning.Problem, error) {
		return m.r.Planning.Create(ctx, a, planning.CreateInput{Intent: intent, State: string(in.State)}, key)
	})
}
func (m mutationResolver) UpdatePlantingPlan(ctx context.Context, in gql.UpdatePlantingPlanInput, key *string) (*gql.PlantingPlanPayload, error) {
	intent := plantingUpdateIntent(in)
	return m.planningCommand(ctx, key, func(a planning.Actor) (planning.Result, []planning.Problem, error) {
		return m.r.Planning.Update(ctx, a, planning.UpdateInput{ID: in.ID, ExpectedRevision: plantingPositiveInt(in.ExpectedRevision), Intent: intent}, key)
	})
}
func (m mutationResolver) ChangePlantingPlanState(ctx context.Context, in gql.ChangePlantingPlanStateInput, key *string) (*gql.PlantingPlanPayload, error) {
	return m.planningCommand(ctx, key, func(a planning.Actor) (planning.Result, []planning.Problem, error) {
		return m.r.Planning.ChangeState(ctx, a, planning.StateInput{ID: in.ID, ExpectedRevision: plantingPositiveInt(in.ExpectedRevision), State: string(in.State)}, key)
	})
}
func (m mutationResolver) CancelPlantingPlan(ctx context.Context, in gql.CancelPlantingPlanInput, key *string) (*gql.PlantingPlanPayload, error) {
	return m.planningCommand(ctx, key, func(a planning.Actor) (planning.Result, []planning.Problem, error) {
		return m.r.Planning.Cancel(ctx, a, planning.RevisionInput{ID: in.ID, ExpectedRevision: plantingPositiveInt(in.ExpectedRevision)}, key)
	})
}
func (m mutationResolver) RestorePlantingPlan(ctx context.Context, in gql.RestorePlantingPlanInput, key *string) (*gql.PlantingPlanPayload, error) {
	return m.planningCommand(ctx, key, func(a planning.Actor) (planning.Result, []planning.Problem, error) {
		return m.r.Planning.Restore(ctx, a, planning.RevisionInput{ID: in.ID, ExpectedRevision: plantingPositiveInt(in.ExpectedRevision)}, key)
	})
}
func actionInput(planID, id string, revision int, kind gql.PlantingActionKind, title string, day scalars.Date, reminder *scalars.Date, notes *string) planning.ActionInput {
	out := planning.ActionInput{PlanID: planID, ID: id, ExpectedRevision: plantingPositiveInt(revision), Kind: string(kind), Title: title, ScheduledOn: string(day), Notes: notes}
	if reminder != nil {
		v := string(*reminder)
		out.ReminderOn = &v
	}
	return out
}
func (m mutationResolver) CreatePlantingAction(ctx context.Context, in gql.CreatePlantingActionInput, key *string) (*gql.PlantingPlanPayload, error) {
	input := actionInput(in.PlanID, "", in.ExpectedRevision, in.Kind, in.Title, in.ScheduledOn, in.ReminderOn, in.Notes)
	return m.planningCommand(ctx, key, func(a planning.Actor) (planning.Result, []planning.Problem, error) {
		return m.r.Planning.CreateAction(ctx, a, input, key)
	})
}
func (m mutationResolver) UpdatePlantingAction(ctx context.Context, in gql.UpdatePlantingActionInput, key *string) (*gql.PlantingPlanPayload, error) {
	input := actionInput(in.PlanID, in.ID, in.ExpectedRevision, in.Kind, in.Title, in.ScheduledOn, in.ReminderOn, in.Notes)
	return m.planningCommand(ctx, key, func(a planning.Actor) (planning.Result, []planning.Problem, error) {
		return m.r.Planning.UpdateAction(ctx, a, input, key)
	})
}
func (m mutationResolver) DeletePlantingAction(ctx context.Context, in gql.DeletePlantingActionInput, key *string) (*gql.PlantingPlanPayload, error) {
	return m.planningCommand(ctx, key, func(a planning.Actor) (planning.Result, []planning.Problem, error) {
		return m.r.Planning.DeleteAction(ctx, a, planning.DeleteActionInput{PlanID: in.PlanID, ID: in.ID, ExpectedRevision: plantingPositiveInt(in.ExpectedRevision)}, key)
	})
}

func (m mutationResolver) RefreshPlantingGuidance(ctx context.Context, in gql.RefreshPlantingGuidanceInput, key *string) (*gql.PlantingPlanPayload, error) {
	return m.planningCommand(ctx, key, func(a planning.Actor) (planning.Result, []planning.Problem, error) {
		return m.r.Planning.RefreshGuidance(ctx, a, planning.RevisionInput{ID: in.ID, ExpectedRevision: plantingPositiveInt(in.ExpectedRevision)}, key)
	})
}
