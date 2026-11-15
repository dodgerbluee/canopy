package graphql

import (
	"canopy/server/internal/planning"
	"canopy/server/internal/transport/graphql/gql"
	"canopy/server/internal/transport/graphql/scalars"
	"context"
	"errors"
	"time"
)

func (q queryResolver) PlantingEvents(ctx context.Context, from scalars.Date, to scalars.Date, first *int, after *string) (*gql.PlantingEventConnection, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	if q.r.Planning == nil {
		return nil, capabilityUnavailable("planning")
	}
	n := 25
	if first != nil {
		n = *first
	}
	if n < 1 || n > 100 {
		return nil, invalidFilterError()
	}
	page, err := q.r.Planning.Events(ctx, planningActor(p), planning.EventQuery{From: string(from), To: string(to), First: int32(n), After: after})
	if errors.Is(err, planning.ErrInvalidQuery) {
		return nil, invalidFilterError()
	}
	if errors.Is(err, planning.ErrInvalidCursor) {
		return nil, invalidCursorError()
	}
	if err != nil {
		return nil, internalError(ctx, err)
	}
	out := &gql.PlantingEventConnection{Timezone: page.Timezone, Today: scalars.Date(page.Today), Items: []*gql.PlantingEvent{}, PageInfo: &gql.PageInfo{HasNextPage: page.HasNext, EndCursor: page.EndCursor}}
	for _, r := range page.Items {
		out.Items = append(out.Items, &gql.PlantingEvent{ID: r.ID.String(), PlanID: r.PlanID.String(), PlanName: r.PlanName, PlanRevision: int(r.PlanRevision), Kind: gql.PlantingActionKind(r.Kind), Title: r.Title, Day: scalars.Date(r.ScheduledOn.Time.Format(time.DateOnly)), State: gql.PlantingActionState(r.State)})
	}
	return out, nil
}
