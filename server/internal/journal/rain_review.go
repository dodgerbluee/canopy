package journal

import (
	"context"
	"strconv"
	"time"

	dbgen "canopy/server/db/generated"
	"github.com/jackc/pgx/v5/pgtype"
)

func floatText(v float64) string { return strconv.FormatFloat(v, 'f', 3, 64) }

type RainProposal struct {
	ID, Day, EstimatedAmountMm string
	CheckedAt                  time.Time
}
type RainReviewState struct {
	Status, AreaLabel string
	CheckedAt         *time.Time
	Proposals         []RainProposal
}

// RainReview never makes a provider request. It reads a bounded three-day cache
// and hides days already recorded manually, as well as old location contexts.
func (s *Service) RainReview(ctx context.Context, wsID pgtype.UUID, now time.Time) (RainReviewState, error) {
	out := RainReviewState{Status: "NOT_CONFIGURED", Proposals: []RainProposal{}}
	ws, err := s.q.GetWorkspaceByID(ctx, wsID)
	if err != nil {
		return out, err
	}
	out.AreaLabel = "Your garden"
	if ws.LocationLabel.Valid && ws.LocationLabel.String != "" {
		out.AreaLabel = ws.LocationLabel.String
	}
	if ws.Latitude == nil || ws.Longitude == nil || !ws.Latitude.Valid || !ws.Longitude.Valid {
		return out, nil
	}
	loc, err := time.LoadLocation(ws.Timezone)
	if err != nil {
		return out, err
	}
	today := now.In(loc)
	from, to := rainDate(today.AddDate(0, 0, -2)), rainDate(today)
	checks, err := s.q.ListCurrentRainChecks(ctx, dbgen.ListCurrentRainChecksParams{WorkspaceID: wsID, FromDay: from, ToDay: to})
	if err != nil {
		return out, err
	}
	out.Status = "WAITING"
	recorded, err := s.q.ListRecordedRainDays(ctx, dbgen.ListRecordedRainDaysParams{WorkspaceID: wsID, FromDay: from, ToDay: to})
	if err != nil {
		return out, err
	}
	known := map[string]bool{}
	for _, d := range recorded {
		known[d.Time.Format(time.DateOnly)] = true
	}
	for _, c := range checks {
		if out.CheckedAt == nil || c.CheckedAt.Time.After(*out.CheckedAt) {
			t := c.CheckedAt.Time
			out.CheckedAt = &t
		}
		if c.CheckedAt.Time.After(now) || now.Sub(c.CheckedAt.Time) > 3*time.Hour {
			continue
		}
		if c.EstimatedMm == nil {
			continue
		}
		mm, err := c.EstimatedMm.Float64Value()
		day := c.EventDate.Time.Format(time.DateOnly)
		if err != nil || !mm.Valid || mm.Float64 < 0.1 || c.Response != "PENDING" || known[day] {
			continue
		}
		out.Proposals = append(out.Proposals, RainProposal{ID: uuidString(c.ID), Day: day, EstimatedAmountMm: numericString(*c.EstimatedMm), CheckedAt: c.CheckedAt.Time})
	}
	if out.CheckedAt != nil {
		out.Status = "READY"
		if out.CheckedAt.After(now) || now.Sub(*out.CheckedAt) > 3*time.Hour {
			out.Status = "STALE"
		}
	}
	return out, nil
}

func (s *Service) DismissRainReview(ctx context.Context, a Actor, id string, key *string) (string, []Problem, error) {
	uid, ok := parseUUID(id)
	if !ok {
		return "", []Problem{problem(CodeValidation, "input.id", "Choose a valid rain review.")}, nil
	}
	return mutate(s, ctx, a, "dismissRainReview", key, id, func(ctx context.Context, q *dbgen.Queries) (string, []Problem, bool) {
		if _, err := q.LockRainWorkspace(ctx, a.WorkspaceID); err != nil {
			return "", []Problem{s.internalProblem(err)}, false
		}
		row, err := q.GetRainReviewForUpdate(ctx, dbgen.GetRainReviewForUpdateParams{ID: uid, WorkspaceID: a.WorkspaceID})
		if isNoRows(err) {
			return "", []Problem{problem(CodeNotFound, "input.id", "This rain review is no longer available.")}, false
		}
		if err != nil {
			return "", []Problem{s.internalProblem(err)}, false
		}
		if row.Response == "CONFIRMED" {
			return "", []Problem{problem(CodeConflict, "input.id", "Rain was already recorded. Review it on the calendar.")}, false
		}
		if row.Response == "DISMISSED" {
			return id, nil, false
		}
		if err = q.RespondToRainReview(ctx, dbgen.RespondToRainReviewParams{ID: uid, WorkspaceID: a.WorkspaceID, Response: "DISMISSED", RespondedAt: pgtype.Timestamptz{Time: s.clock.Now(), Valid: true}}); err != nil {
			return "", []Problem{s.internalProblem(err)}, false
		}
		if err = s.audit(ctx, q, a, "DISMISS", "rain_review", uid, 1, nil); err != nil {
			return "", []Problem{s.internalProblem(err)}, false
		}
		return id, nil, false
	})
}
