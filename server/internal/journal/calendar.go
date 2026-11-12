package journal

import (
	"context"
	"encoding/base64"
	"fmt"
	"math/big"
	"strings"
	"time"

	dbgen "canopy/server/db/generated"
	"github.com/jackc/pgx/v5/pgtype"
)

// Rain is area context, deliberately independent of activities and care completions.
type RainInput struct {
	Day      string
	AmountMm *string
	Note     *string
}
type RainView struct {
	ID       string
	Day      string
	AmountMm *string
	Note     *string
	Revision int32
}

func rainView(r dbgen.RainEvent) RainView {
	v := RainView{ID: uuidString(r.ID), Day: r.EventDate.Time.Format(time.DateOnly), Revision: r.Revision}
	if r.AmountMm != nil {
		n := numericString(*r.AmountMm)
		v.AmountMm = &n
	}
	if r.Note.Valid {
		v.Note = &r.Note.String
	}
	return v
}
func (s *Service) RecordRain(ctx context.Context, a Actor, in RainInput, key *string) (RainView, []Problem, error) {
	day, err := time.Parse(time.DateOnly, in.Day)
	if err != nil || day.Year() < 1 {
		return RainView{}, []Problem{problem(CodeValidation, "input.day", "Enter a valid date.")}, nil
	}
	if in.Note != nil && len([]rune(*in.Note)) > 2000 {
		return RainView{}, []Problem{problem(CodeValidation, "input.note", "Keep notes within 2000 characters.")}, nil
	}
	var amount *pgtype.Numeric
	if in.AmountMm != nil {
		n, ok := parseDecimal(*in.AmountMm)
		rat, rok := new(big.Rat).SetString(*in.AmountMm)
		if !ok || !rok || rat.Sign() < 0 || rat.Cmp(big.NewRat(10000, 1)) > 0 || n.Exp < -6 {
			return RainView{}, []Problem{problem(CodeValidation, "input.amountMm", "Enter a depth between 0 and 10000 mm with at most six decimal places, or leave it blank.")}, nil
		}
		amount = &n
	}
	return mutate(s, ctx, a, "recordRain", key, in, func(ctx context.Context, q *dbgen.Queries) (RainView, []Problem, bool) {
		row, err := q.InsertRain(ctx, dbgen.InsertRainParams{ID: s.uuid.New(), WorkspaceID: a.WorkspaceID, EventDate: pgtype.Date{Time: day, Valid: true}, AmountMm: amount, Note: nullText(in.Note)})
		if err != nil {
			return RainView{}, []Problem{s.internalProblem(err)}, false
		}
		if err = s.audit(ctx, q, a, "CREATE", "rain", row.ID, row.Revision, nil); err != nil {
			return RainView{}, []Problem{s.internalProblem(err)}, false
		}
		return rainView(row), nil, false
	})
}
func (s *Service) VoidRain(ctx context.Context, a Actor, id string, revision int32, key *string) (string, []Problem, error) {
	uid, ok := parseUUID(id)
	if !ok {
		return "", []Problem{problem(CodeValidation, "input.id", "Enter a valid rain ID.")}, nil
	}
	input := struct {
		ID       string
		Revision int32
	}{id, revision}
	return mutate(s, ctx, a, "voidRain", key, input, func(ctx context.Context, q *dbgen.Queries) (string, []Problem, bool) {
		cur, err := q.GetRainForUpdate(ctx, dbgen.GetRainForUpdateParams{ID: uid, WorkspaceID: a.WorkspaceID})
		if isNoRows(err) {
			return "", []Problem{problem(CodeNotFound, "input.id", "Rain record not found.")}, false
		}
		if err != nil {
			return "", []Problem{s.internalProblem(err)}, false
		}
		if cur.Revision != revision {
			return "", []Problem{problemWithRevision(problem(CodeConflict, "input.expectedRevision", "Rain record changed. Reload before removing it."), cur.Revision)}, false
		}
		if cur.VoidedAt.Valid {
			return id, nil, false
		}
		row, err := q.VoidRain(ctx, dbgen.VoidRainParams{ID: uid, WorkspaceID: a.WorkspaceID, ExpectedRevision: revision})
		if err != nil {
			return "", []Problem{s.internalProblem(err)}, false
		}
		if err = s.audit(ctx, q, a, "VOID", "rain", uid, row.Revision, rainView(cur)); err != nil {
			return "", []Problem{s.internalProblem(err)}, false
		}
		return id, nil, false
	})
}

type CalendarEvent struct {
	ID, EntityID, Day, Kind, Title        string
	PlantID, ActivityKind, AmountMm, Note *string
	Revision                              *int32
}
type CalendarPage struct {
	Items     []CalendarEvent
	HasNext   bool
	EndCursor string
}

// Calendar cursors bind the workspace, range and timezone. Photos are selected
// using taken_at in SQL. Unknown/upload-fallback dates never imply a capture date.
func (s *Service) Calendar(ctx context.Context, ws pgtype.UUID, from, to string, first int32, after *string) (CalendarPage, error) {
	start, e1 := time.Parse(time.DateOnly, from)
	end, e2 := time.Parse(time.DateOnly, to)
	if e1 != nil || e2 != nil || start.Year() < 1 || end.Before(start) || end.Sub(start) > 61*24*time.Hour {
		return CalendarPage{}, fmt.Errorf("calendar range must contain 1 to 62 valid days")
	}
	workspace, err := s.q.GetWorkspaceByID(ctx, ws)
	if err != nil {
		return CalendarPage{}, err
	}
	if _, err = time.LoadLocation(workspace.Timezone); err != nil {
		return CalendarPage{}, err
	}
	sig := requestHash([]string{uuidString(ws), from, to, workspace.Timezone})
	params := dbgen.ListCalendarEventsParams{WorkspaceID: ws, Timezone: workspace.Timezone, FromDay: pgtype.Date{Time: start, Valid: true}, ToDay: pgtype.Date{Time: end, Valid: true}, LimitCount: clampFirst(first) + 1}
	if after != nil {
		raw, err := base64.RawURLEncoding.DecodeString(*after)
		parts := strings.SplitN(string(raw), "|", 2)
		if err != nil || len(parts) != 2 || parts[0] != sig || len(parts[1]) > 150 {
			return CalendarPage{}, ErrInvalidCursor
		}
		params.AfterKey = pgtype.Text{String: parts[1], Valid: true}
	}
	rows, err := s.q.ListCalendarEvents(ctx, params)
	if err != nil {
		return CalendarPage{}, err
	}
	page := CalendarPage{Items: make([]CalendarEvent, 0, len(rows)), HasNext: len(rows) > int(clampFirst(first))}
	if page.HasNext {
		rows = rows[:clampFirst(first)]
	}
	for _, r := range rows {
		v := CalendarEvent{ID: r.EventKey, EntityID: uuidString(r.EntityID), Day: r.Day.Time.Format(time.DateOnly), Kind: r.Kind, Title: r.Title}
		if r.PlantID.Valid {
			p := uuidString(r.PlantID)
			v.PlantID = &p
		}
		if r.ActivityKind != "" {
			v.ActivityKind = &r.ActivityKind
		}
		if r.AmountMm != nil {
			n := numericString(*r.AmountMm)
			v.AmountMm = &n
		}
		if r.Note.Valid {
			v.Note = &r.Note.String
		}
		if r.Revision.Valid {
			v.Revision = &r.Revision.Int32
		}
		page.Items = append(page.Items, v)
		page.EndCursor = base64.RawURLEncoding.EncodeToString([]byte(sig + "|" + v.Day + ":" + r.EventKey))
	}
	return page, nil
}
