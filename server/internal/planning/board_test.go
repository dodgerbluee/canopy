package planning

import (
	"context"
	"errors"
	"testing"
)

func TestBoardComposesScopedRowsAndBindsViewWithoutResearch(t *testing.T) {
	s, a, stub, p, _ := scheduleWorld(t)
	mustCreate(t, s, a, Intent{Name: "Wishlist idea"})
	in := BoardQuery{Plans: Query{First: 1}, Range: MonthRange{"2027-01", "2027-12"}}
	page, err := s.Board(context.Background(), a, in)
	if err != nil || len(page.Rows) != 1 || !page.HasNext || page.EndCursor == nil || !page.Rows[0].Schedule.GuidanceCurrent || page.Rows[0].Plan.Row.ID != p.Row.ID {
		t.Fatal(page, err)
	}
	in.Plans.After = page.EndCursor
	next, err := s.Board(context.Background(), a, in)
	if err != nil || len(next.Rows) != 1 || next.HasNext || next.Rows[0].Schedule.TargetStatus != "UNSCHEDULED" || stub.calls.Load() != 1 {
		t.Fatal(next, err)
	}
	in.Range.To = "2027-11"
	if _, err = s.Board(context.Background(), a, in); !errors.Is(err, ErrInvalidCursor) {
		t.Fatal("cursor accepted changed horizon", err)
	}
	in.Range.To = "2027-12"
	other := workspace(t, s)
	if _, err = s.Board(context.Background(), other, in); !errors.Is(err, ErrInvalidCursor) {
		t.Fatal("private page cursor accepted", err)
	}
	in.Plans.After = nil
	empty, err := s.Board(context.Background(), other, in)
	if err != nil || len(empty.Rows) != 0 || empty.Today == "" {
		t.Fatal(empty, err)
	}
}

func TestScheduleReusesSavedAssumptionsOnlyInTheirCurrentContext(t *testing.T) {
	w := GuidanceWindow{Precision: "RELATIVE", Label: "After frost", Relative: &RelativeWindow{Anchor: "LAST_FROST", FromDays: ptr(0), ToDays: ptr(14)}}
	s, a, stub, _, in := scheduleWorld(t, w)
	in.TargetPeriod = &Period{Precision: DateRangePrecision, Dates: &DateRange{"2027-04-07", "2027-04-07"}}
	in.Assumptions.Anchors = []ScheduleAnchor{{Kind: "LAST_FROST", On: "2027-04-01"}}
	_, ps, err := s.ConfirmSchedule(context.Background(), a, in, nil)
	if err != nil || len(ps) > 0 {
		t.Fatal(ps, err)
	}
	query := scheduleRequest(in.PlanID)
	page, err := s.Schedule(context.Background(), a, query)
	if err != nil || len(page.Windows) != 1 || page.Windows[0].Resolution != "RESOLVED" || len(page.Assumptions.Anchors) != 1 {
		t.Fatal("saved assumption was lost", page, err)
	}
	query.AssumptionsExplicit = true
	page, err = s.Schedule(context.Background(), a, query)
	if err != nil || page.Windows[0].Resolution != "NEEDS_ANCHOR" || len(page.Assumptions.Anchors) != 0 {
		t.Fatal("explicit empty assumptions did not override", page, err)
	}
	query.AssumptionsExplicit = false
	stub.policy = "different growing region"
	page, err = s.Schedule(context.Background(), a, query)
	if err != nil || page.GuidanceCurrent || len(page.Assumptions.Anchors) != 0 || page.TargetPeriod.Dates.From != "2027-04-07" {
		t.Fatal("old context reused assumptions or lost chosen day", page, err)
	}
}
