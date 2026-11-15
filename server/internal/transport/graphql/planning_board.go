package graphql

import (
	"context"
	"errors"

	"canopy/server/internal/planning"
	"canopy/server/internal/transport/graphql/gql"
	"canopy/server/internal/transport/graphql/scalars"
)

func (q queryResolver) PlantingBoard(ctx context.Context, input gql.PlantingScheduleInput, first *int, after *string, filter *gql.PlantingPlanFilter) (*gql.PlantingBoardConnection, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	if q.r.Planning == nil {
		return nil, capabilityUnavailable("planning")
	}
	query := planning.Query{First: 25, After: after, Order: planning.OrderNameAsc}
	if first != nil {
		if *first < 1 || *first > 100 {
			return nil, invalidFilterError()
		}
		query.First = int32(*first)
	}
	if filter != nil {
		if filter.Query != nil {
			query.Search = *filter.Query
		}
		if filter.States != nil {
			query.States = make([]string, len(filter.States))
			for i, v := range filter.States {
				query.States[i] = string(v)
			}
		}
		query.Method = plantingString(filter.Method)
		query.LocationID = filter.LocationID
	}
	page, err := q.r.Planning.Board(ctx, planningActor(p), planning.BoardQuery{Plans: query, Range: planning.MonthRange{From: input.FromMonth, To: input.ToMonth}, Assumptions: plantingScheduleAssumptionsIn(input.Assumptions), AssumptionsExplicit: input.Assumptions != nil})
	if errors.Is(err, planning.ErrInvalidQuery) || errors.Is(err, planning.ErrInvalidSchedule) {
		return nil, invalidFilterError()
	}
	if errors.Is(err, planning.ErrInvalidCursor) {
		return nil, invalidCursorError()
	}
	if err != nil {
		return nil, internalError(ctx, err)
	}
	out := &gql.PlantingBoardConnection{Timezone: page.Timezone, Today: scalars.Date(page.Today), Items: []*gql.PlantingBoardRow{}, PageInfo: &gql.PageInfo{HasNextPage: page.HasNext, EndCursor: page.EndCursor}}
	locations := map[string]*gql.Location{}
	for _, row := range page.Rows {
		plan, err := q.plantingPlanOut(ctx, p, row.Plan, locations)
		if err != nil {
			return nil, internalError(ctx, err)
		}
		out.Items = append(out.Items, &gql.PlantingBoardRow{Plan: plan, Schedule: plantingSchedulePageOut(row.Schedule)})
	}
	return out, nil
}
