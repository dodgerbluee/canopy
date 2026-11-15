package planning

import (
	"context"
	"testing"
	"time"
)

func TestEventsOnlyExplicitDatesAndScopedPages(t *testing.T) {
	s, a := scratch(t)
	ctx := context.Background()
	other := workspace(t, s)
	r := mustCreate(t, s, a, Intent{Name: "Coarse fig", TargetPeriod: &Period{Precision: YearPrecision, Year: ptr(2027)}})
	p, err := s.Events(ctx, a, EventQuery{From: "2027-01-01", To: "2027-02-28", First: 1})
	if err != nil || len(p.Items) != 0 {
		t.Fatal(p, err)
	}
	for _, day := range []string{"2027-02-01", "2027-02-01", "2027-02-28"} {
		var ps []Problem
		r, ps, err = s.CreateAction(ctx, a, ActionInput{PlanID: r.ID, ExpectedRevision: r.Revision, Kind: "PREPARE", Title: "Prepare", ScheduledOn: day}, nil)
		if err != nil || len(ps) > 0 {
			t.Fatal(ps, err)
		}
	}
	q := EventQuery{From: "2027-01-01", To: "2027-02-28", First: 1}
	seen := map[string]bool{}
	for {
		p, err = s.Events(ctx, a, q)
		if err != nil || len(p.Items) != 1 {
			t.Fatal(p, err)
		}
		id := idString(p.Items[0].ID)
		if seen[id] {
			t.Fatal("duplicate")
		}
		seen[id] = true
		if !p.HasNext {
			break
		}
		q.After = p.EndCursor
		if _, err = s.Events(ctx, other, q); err != ErrInvalidCursor {
			t.Fatal("cross scope", err)
		}
		bad := q
		bad.To = "2027-03-01"
		if _, err = s.Events(ctx, a, bad); err != ErrInvalidCursor {
			t.Fatal("range cursor", err)
		}
	}
	if len(seen) != 3 {
		t.Fatal(seen)
	}
	for _, bad := range []EventQuery{{From: "2027-02-30", To: "2027-03-01", First: 1}, {From: "2027-01-01", To: "2027-03-04", First: 1}, {From: "2027-02-01", To: "2027-01-01", First: 1}, {From: "2027-01-01", To: "2027-01-01", First: 101}} {
		if _, err = s.Events(ctx, a, bad); err != ErrInvalidQuery {
			t.Fatal(bad, err)
		}
	}
	_, ps, err := s.Cancel(ctx, a, RevisionInput{ID: r.ID, ExpectedRevision: r.Revision}, nil)
	if err != nil || len(ps) > 0 {
		t.Fatal(ps, err)
	}
	p, err = s.Events(ctx, a, EventQuery{From: "2027-01-01", To: "2027-02-28", First: 100})
	if err != nil || len(p.Items) > 0 {
		t.Fatal("cancelled markers", p, err)
	}
	if count(t, s, "plants")+count(t, s, "activities")+count(t, s, "journal_entries") > 0 {
		t.Fatal("created actual history")
	}
	s.Clock = fixedClock{time.Date(2027, 1, 2, 3, 0, 0, 0, time.UTC)}
	p, err = s.Events(ctx, a, EventQuery{From: "2027-01-01", To: "2027-01-01", First: 1})
	if err != nil || p.Today != "2027-01-01" {
		t.Fatal(p, err)
	}
}
