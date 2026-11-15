package graphql

import (
	"context"
	"errors"

	"canopy/server/internal/planning"
	"canopy/server/internal/transport/graphql/gql"
	"canopy/server/internal/transport/graphql/scalars"
)

func plantingScheduleAssumptionsIn(in *gql.PlantingScheduleAssumptionsInput) planning.ScheduleAssumptions {
	out := planning.ScheduleAssumptions{Anchors: []planning.ScheduleAnchor{}}
	if in == nil {
		return out
	}
	out.Hemisphere = plantingString(in.Hemisphere)
	for _, anchor := range in.Anchors {
		out.Anchors = append(out.Anchors, planning.ScheduleAnchor{Kind: string(anchor.Kind), On: string(anchor.On)})
	}
	return out
}
func plantingScheduleAssumptionsOut(in planning.ScheduleAssumptions) *gql.PlantingScheduleAssumptions {
	out := &gql.PlantingScheduleAssumptions{Anchors: []*gql.PlantingScheduleAnchor{}}
	if in.Hemisphere != nil {
		v := gql.PlantingHemisphere(*in.Hemisphere)
		out.Hemisphere = &v
	}
	for _, anchor := range in.Anchors {
		out.Anchors = append(out.Anchors, &gql.PlantingScheduleAnchor{Kind: gql.PlantingTimingAnchor(anchor.Kind), On: scalars.Date(anchor.On)})
	}
	return out
}
func plantingScheduleSelectionOut(in *planning.ScheduleSelection) *gql.PlantingScheduleSelection {
	if in == nil {
		return nil
	}
	return &gql.PlantingScheduleSelection{Version: in.Version, FactID: in.FactID, WindowIndex: in.WindowIndex, Year: in.Year, Assumptions: plantingScheduleAssumptionsOut(in.Assumptions), SupportedPeriod: plantingPeriodOut(in.SupportedPeriod), ConfirmedAt: scalars.DateTime(in.ConfirmedAt)}
}
func plantingMonthRangeOut(in *planning.MonthRange) *gql.PlantingMonthRange {
	if in == nil {
		return nil
	}
	return &gql.PlantingMonthRange{From: in.From, To: in.To}
}
func (q queryResolver) PlantingSchedule(ctx context.Context, planID string, input gql.PlantingScheduleInput, first *int, after *string) (*gql.PlantingScheduleConnection, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	if q.r.Planning == nil {
		return nil, capabilityUnavailable("planning")
	}
	pageSize := 25
	if first != nil {
		pageSize = *first
	}
	in := planning.ScheduleQuery{PlanID: planID, Range: planning.MonthRange{From: input.FromMonth, To: input.ToMonth}, Assumptions: plantingScheduleAssumptionsIn(input.Assumptions), First: pageSize, After: after}
	page, err := q.r.Planning.Schedule(ctx, planningActor(p), in)
	if errors.Is(err, planning.ErrInvalidSchedule) {
		return nil, invalidFilterError()
	}
	if errors.Is(err, planning.ErrInvalidCursor) {
		return nil, invalidCursorError()
	}
	if err != nil {
		return nil, internalError(ctx, err)
	}
	if page == nil {
		return nil, nil
	}
	out := &gql.PlantingScheduleConnection{PlanID: page.PlanID, Revision: int(page.Revision), GuidanceGeneration: int(page.Generation), Timezone: page.Timezone, Today: scalars.Date(page.Today), TargetPeriod: plantingPeriodOut(page.TargetPeriod), TargetStatus: gql.PlantingPeriodStatus(page.TargetStatus), TargetDisplayMonths: plantingMonthRangeOut(page.TargetDisplayMonths), TargetVisible: page.TargetVisible, AcceptedSchedule: plantingScheduleSelectionOut(page.Selection), SourceSnapshotID: page.SourceSnapshotID, GuidanceState: gql.KnowledgeState(page.GuidanceState), GuidanceCurrent: page.GuidanceCurrent, Message: page.Message, Assumptions: plantingScheduleAssumptionsOut(page.Assumptions), Items: []*gql.PlantingProjectedWindow{}, PageInfo: &gql.PageInfo{HasNextPage: page.HasNext, EndCursor: page.EndCursor}}
	if page.TargetHemisphere != nil {
		v := gql.PlantingHemisphere(*page.TargetHemisphere)
		out.TargetHemisphere = &v
	}
	for _, w := range page.Windows {
		out.Items = append(out.Items, &gql.PlantingProjectedWindow{ID: w.ID, FactID: w.FactID, WindowIndex: w.WindowIndex, Year: w.Year, Phase: gql.PlantingPhaseKind(w.Phase), Label: w.Label, Resolution: gql.PlantingScheduleResolution(w.Resolution), Message: w.Message, Status: gql.PlantingPeriodStatus(w.Status), Period: plantingPeriodOut(w.Period), DisplayMonths: plantingMonthRangeOut(w.DisplayMonths), ContinuesBefore: w.ContinuesBefore, ContinuesAfter: w.ContinuesAfter, Estimated: w.Estimated, FirstHarvestUnknown: w.FirstHarvestUnknown, Selectable: w.Selectable})
	}
	return out, nil
}
func (m mutationResolver) ConfirmPlantingSchedule(ctx context.Context, input gql.ConfirmPlantingScheduleInput, key *string) (*gql.PlantingPlanPayload, error) {
	in := planning.ConfirmScheduleInput{PlanID: input.PlanID, SnapshotID: input.SnapshotID, ExpectedRevision: plantingPositiveInt(input.ExpectedRevision), Generation: plantingPositiveInt(input.Generation), FactID: input.FactID, WindowIndex: input.WindowIndex, Year: input.Year, Assumptions: plantingScheduleAssumptionsIn(input.Assumptions), TargetPeriod: plantingPeriodIn(input.TargetPeriod)}
	return m.planningCommand(ctx, key, func(a planning.Actor) (planning.Result, []planning.Problem, error) {
		return m.r.Planning.ConfirmSchedule(ctx, a, in, key)
	})
}
