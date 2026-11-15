package planning

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"time"
)

type BoardQuery struct {
	Plans               Query
	Range               MonthRange
	Assumptions         ScheduleAssumptions
	AssumptionsExplicit bool
}
type BoardRow struct {
	Plan     Plan
	Schedule *SchedulePage
}
type BoardPage struct {
	Rows            []BoardRow
	Timezone, Today string
	EndCursor       *string
	HasNext         bool
}
type boardCursor struct {
	Scope string `json:"scope"`
	After string `json:"after"`
}
type boardClock struct{ now time.Time }

func (c boardClock) Now() time.Time { return c.now }

func (s *Service) Board(ctx context.Context, a Actor, in BoardQuery) (BoardPage, error) {
	if validateScheduleRange(in.Range) != nil || validateScheduleAssumptions(in.Assumptions) != nil {
		return BoardPage{}, ErrInvalidSchedule
	}
	assumptions := normalizedScheduleAssumptions(in.Assumptions)
	scope, _ := requestHash([]any{ScheduleVersion, idString(a.WorkspaceID), in.Range, assumptions, in.AssumptionsExplicit})
	query := in.Plans
	if query.After != nil {
		after, err := decodeBoardCursor(*query.After, scope)
		if err != nil {
			return BoardPage{}, err
		}
		query.After = &after
	}
	page, err := s.List(ctx, a, query)
	if err != nil {
		return BoardPage{}, err
	}
	settings, err := s.q().KnowledgeSettings(ctx, a.WorkspaceID)
	if err != nil {
		return BoardPage{}, err
	}
	zone, err := time.LoadLocation(settings.Timezone)
	if err != nil {
		return BoardPage{}, err
	}
	reader := *s
	reader.Clock = boardClock{s.Clock.Now()}
	out := BoardPage{Rows: []BoardRow{}, Timezone: settings.Timezone, Today: reader.Clock.Now().In(zone).Format(time.DateOnly), HasNext: page.HasNext}
	for _, plan := range page.Plans {
		schedule, err := reader.schedulePlan(ctx, a, plan, ScheduleQuery{PlanID: idString(plan.Row.ID), Range: in.Range, Assumptions: assumptions, AssumptionsExplicit: in.AssumptionsExplicit, First: 25})
		if err != nil {
			return BoardPage{}, err
		}
		out.Rows = append(out.Rows, BoardRow{Plan: plan, Schedule: schedule})
	}
	if page.EndCursor != nil {
		raw, _ := json.Marshal(boardCursor{Scope: scope, After: *page.EndCursor})
		value := base64.RawURLEncoding.EncodeToString(raw)
		out.EndCursor = &value
	}
	return out, nil
}
func decodeBoardCursor(raw, scope string) (string, error) {
	if len(raw) > 16384 {
		return "", ErrInvalidCursor
	}
	data, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return "", ErrInvalidCursor
	}
	var cursor boardCursor
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err = dec.Decode(&cursor); err != nil || cursor.Scope != scope || cursor.After == "" {
		return "", ErrInvalidCursor
	}
	if err = dec.Decode(new(any)); err != io.EOF {
		return "", ErrInvalidCursor
	}
	return cursor.After, nil
}
