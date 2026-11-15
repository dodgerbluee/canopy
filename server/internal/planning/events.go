package planning

import (
	"context"
	"time"

	dbgen "canopy/server/db/generated"
	"github.com/jackc/pgx/v5/pgtype"
)

type EventQuery struct {
	From, To string
	First    int32
	After    *string
}
type EventPage struct {
	Items           []dbgen.ListPlantingEventsRow
	Timezone, Today string
	EndCursor       *string
	HasNext         bool
}

// Events projects explicit chosen days only. No source window is rounded to a day.
func (s *Service) Events(ctx context.Context, a Actor, in EventQuery) (EventPage, error) {
	out := EventPage{Items: []dbgen.ListPlantingEventsRow{}}
	if !validDate(in.From) || !validDate(in.To) || in.From > in.To || in.First < 1 || in.First > 100 {
		return out, ErrInvalidQuery
	}
	from, _ := time.Parse(time.DateOnly, in.From)
	to, _ := time.Parse(time.DateOnly, in.To)
	if to.Sub(from) > 61*24*time.Hour {
		return out, ErrInvalidQuery
	}
	scope, _ := requestHash([]any{"planning-events-v1", idString(a.WorkspaceID), in.From, in.To})
	args := dbgen.ListPlantingEventsParams{WorkspaceID: a.WorkspaceID, FromDay: pgtype.Date{Time: from, Valid: true}, ToDay: pgtype.Date{Time: to, Valid: true}, LimitCount: in.First + 1}
	if in.After != nil {
		key, id, err := decodeCursor(*in.After, scope, OrderNameAsc)
		if err != nil || !validDate(key) || key < in.From || key > in.To {
			return out, ErrInvalidCursor
		}
		day, _ := time.Parse(time.DateOnly, key)
		args.AfterDay = pgtype.Date{Time: day, Valid: true}
		args.AfterID = id
	}
	settings, err := s.q().KnowledgeSettings(ctx, a.WorkspaceID)
	if err != nil {
		return out, err
	}
	zone, err := time.LoadLocation(settings.Timezone)
	if err != nil {
		return out, err
	}
	out.Timezone = settings.Timezone
	out.Today = s.Clock.Now().In(zone).Format(time.DateOnly)
	rows, err := s.q().ListPlantingEvents(ctx, args)
	if err != nil {
		return out, err
	}
	out.HasNext = len(rows) > int(in.First)
	if out.HasNext {
		rows = rows[:in.First]
	}
	out.Items = rows
	if len(rows) > 0 {
		last := rows[len(rows)-1]
		c := encodeCursor(scope, last.ScheduledOn.Time.Format(time.DateOnly), idString(last.ID))
		out.EndCursor = &c
	}
	return out, nil
}
