package graphql

import (
	"canopy/server/internal/journal"
	"canopy/server/internal/transport/graphql/gql"
	"canopy/server/internal/transport/graphql/scalars"
	"context"
	"errors"
	"github.com/vektah/gqlparser/v2/gqlerror"
	"time"
)

func (q queryResolver) CalendarEvents(ctx context.Context, from, to scalars.Date, first *int, after *string) (*gql.CalendarEventConnection, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	if q.r.Journal == nil {
		return nil, capabilityUnavailable("calendar")
	}
	// Validate owner input here; infrastructure errors remain safe internal errors.
	start, e1 := time.Parse(time.DateOnly, string(from))
	end, e2 := time.Parse(time.DateOnly, string(to))
	if e1 != nil || e2 != nil || end.Before(start) || end.Sub(start) > 61*24*time.Hour {
		return nil, gqlerror.Errorf("Choose a calendar range of 1 to 62 days.")
	}
	n := int32(100)
	if first != nil {
		if *first < 1 || *first > 100 {
			return nil, gqlerror.Errorf("first must be between 1 and 100")
		}
		n = int32(*first)
	}
	page, err := q.r.Journal.Calendar(ctx, p.WorkspaceID, string(from), string(to), n, after)
	if errors.Is(err, journal.ErrInvalidCursor) {
		return nil, gqlerror.Errorf("Invalid calendar cursor. Reload this month.")
	}
	if err != nil {
		return nil, internalError(ctx, err)
	}
	out := &gql.CalendarEventConnection{Items: make([]*gql.CalendarEvent, 0, len(page.Items)), PageInfo: &gql.PageInfo{HasNextPage: page.HasNext}}
	if page.EndCursor != "" {
		out.PageInfo.EndCursor = &page.EndCursor
	}
	for _, v := range page.Items {
		e := &gql.CalendarEvent{ID: v.ID, EntityID: v.EntityID, PlantID: v.PlantID, Day: scalars.Date(v.Day), Kind: gql.CalendarEventKind(v.Kind), Title: v.Title, Note: v.Note}
		if v.ActivityKind != nil {
			k := gql.ActivityKind(*v.ActivityKind)
			e.ActivityKind = &k
		}
		if v.AmountMm != nil {
			a := scalars.Decimal(*v.AmountMm)
			e.AmountMm = &a
		}
		if v.Revision != nil {
			r := int(*v.Revision)
			e.Revision = &r
		}
		out.Items = append(out.Items, e)
	}
	return out, nil
}
func (m mutationResolver) RecordRain(ctx context.Context, in gql.RecordRainInput, c *string) (*gql.RecordRainPayload, error) {
	if err := m.requireJournal(); err != nil {
		return nil, err
	}
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	v, ps, err := m.r.Journal.RecordRain(ctx, jActor(p), journal.RainInput{Day: string(in.Day), AmountMm: decPtr(in.AmountMm), Note: in.Note}, c)
	if err != nil {
		return nil, internalError(ctx, err)
	}
	out := &gql.RecordRainPayload{Problems: mapJournalProblems(ps), ClientMutationID: c}
	if len(ps) == 0 {
		out.Rain = &gql.RainEvent{ID: v.ID, Day: scalars.Date(v.Day), Note: v.Note, Revision: int(v.Revision)}
		if v.AmountMm != nil {
			a := scalars.Decimal(*v.AmountMm)
			out.Rain.AmountMm = &a
		}
	}
	return out, nil
}
func (m mutationResolver) VoidRain(ctx context.Context, in gql.VoidRainInput, c *string) (*gql.VoidRainPayload, error) {
	if err := m.requireJournal(); err != nil {
		return nil, err
	}
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	if in.ExpectedRevision < 1 || in.ExpectedRevision > 2147483647 {
		return &gql.VoidRainPayload{Problems: []*gql.MutationProblem{{Code: gql.ProblemCodeValidation, Message: "Invalid rain revision."}}, ClientMutationID: c}, nil
	}
	id, ps, err := m.r.Journal.VoidRain(ctx, jActor(p), in.ID, int32(in.ExpectedRevision), c)
	if err != nil {
		return nil, internalError(ctx, err)
	}
	out := &gql.VoidRainPayload{Problems: mapJournalProblems(ps), ClientMutationID: c}
	if len(ps) == 0 {
		out.RemovedID = &id
	}
	return out, nil
}
