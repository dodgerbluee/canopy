package planning

import (
	dbgen "canopy/server/db/generated"
	"context"
	"github.com/jackc/pgx/v5/pgtype"
	"sync"
	"testing"
	"time"
)

func TestRemindersOptInGateDedupRescheduleAndHistory(t *testing.T) {
	s, a := scratch(t)
	ctx := context.Background()
	r := mustCreate(t, s, a, Intent{Name: "Future fig"})
	in := ActionInput{PlanID: r.ID, ExpectedRevision: 1, Kind: "PLANT_OUT", Title: "Plant the fig", ScheduledOn: "2027-03-15", ReminderOn: ptr("2027-03-14")}
	r, ps, err := s.CreateAction(ctx, a, in, nil)
	if err != nil || len(ps) > 0 {
		t.Fatal(ps, err)
	}
	s.Clock = fixedClock{time.Date(2027, 3, 14, 13, 59, 0, 0, time.UTC)} // 08:59 CDT, DST day
	if err = s.SweepReminders(ctx); err != nil {
		t.Fatal(err)
	}
	if count(t, s, "notifications") != 0 {
		t.Fatal("early")
	}
	s.Clock = fixedClock{time.Date(2027, 3, 14, 14, 0, 0, 0, time.UTC)}
	var wg sync.WaitGroup
	errs := make(chan error, 10)
	for range 10 {
		wg.Go(func() { errs <- s.SweepReminders(ctx) })
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		if e != nil {
			t.Fatal(e)
		}
	}
	if count(t, s, "notifications") != 1 {
		t.Fatal("duplicate", count(t, s, "notifications"))
	}
	p := mustGet(t, s, a, r.ID)
	action := p.Actions[0]
	// Same revision after restart and read state preservation.
	var nid pgtype.UUID
	if err = s.Pool.QueryRow(ctx, "UPDATE notifications SET read_at=now() RETURNING id").Scan(&nid); err != nil {
		t.Fatal(err)
	}
	if err = s.SweepReminders(ctx); err != nil {
		t.Fatal(err)
	}
	in.ID = idString(action.ID)
	in.ExpectedRevision = r.Revision
	in.ReminderOn = ptr("2027-03-15")
	r, ps, err = s.UpdateAction(ctx, a, in, nil)
	if err != nil || len(ps) > 0 {
		t.Fatal(ps, err)
	}
	if err = s.SweepReminders(ctx); err != nil {
		t.Fatal(err)
	}
	if count(t, s, "notifications") != 1 {
		t.Fatal("future reminder")
	}
	s.Clock = fixedClock{time.Date(2027, 3, 15, 14, 0, 0, 0, time.UTC)}
	if err = s.SweepReminders(ctx); err != nil {
		t.Fatal(err)
	}
	if count(t, s, "notifications") != 2 {
		t.Fatal("new schedule did not notify")
	}
	var read bool
	if err = s.Pool.QueryRow(ctx, "SELECT read_at IS NOT NULL FROM notifications WHERE id=$1", nid).Scan(&read); err != nil || !read {
		t.Fatal("read history", err)
	}
	// Candidate fetched before cancellation cannot deliver afterward.
	candidate := dbgen.PlanningReminderCandidatesRow{WorkspaceID: a.WorkspaceID, PlanID: p.Row.ID, ID: action.ID}
	_, ps, err = s.Cancel(ctx, a, RevisionInput{ID: r.ID, ExpectedRevision: r.Revision}, nil)
	if err != nil || len(ps) > 0 {
		t.Fatal(ps, err)
	}
	if err = s.deliverReminder(ctx, candidate, s.Clock.Now()); err != nil {
		t.Fatal(err)
	}
	if count(t, s, "notifications") != 2 {
		t.Fatal("cancelled reminder")
	}
	if count(t, s, "plants")+count(t, s, "activities") > 0 {
		t.Fatal("reminder created actual work")
	}
}
func TestRemindersTimezoneCatchupAndStaleCandidates(t *testing.T) {
	s, a := scratch(t)
	ctx := context.Background()
	r := mustCreate(t, s, a, Intent{Name: "Seed tray"})
	in := ActionInput{PlanID: r.ID, ExpectedRevision: r.Revision, Kind: "BUY", Title: "Buy supplies", ScheduledOn: "2027-11-08"}
	r, ps, err := s.CreateAction(ctx, a, in, nil)
	if err != nil || len(ps) > 0 {
		t.Fatal(ps, err)
	}
	s.Clock = fixedClock{time.Date(2027, 11, 8, 14, 30, 0, 0, time.UTC)}
	if err = s.SweepReminders(ctx); err != nil {
		t.Fatal(err)
	}
	if count(t, s, "notifications") != 0 {
		t.Fatal("not opted in")
	}
	p := mustGet(t, s, a, r.ID)
	in.ID = idString(p.Actions[0].ID)
	in.ExpectedRevision = r.Revision
	in.ReminderOn = ptr("2027-11-08")
	r, ps, err = s.UpdateAction(ctx, a, in, nil)
	if err != nil || len(ps) > 0 {
		t.Fatal(ps, err)
	}
	if err = s.SweepReminders(ctx); err != nil {
		t.Fatal(err)
	}
	if count(t, s, "notifications") != 0 {
		t.Fatal("09:00 CST gate")
	}
	if _, err = s.Pool.Exec(ctx, "UPDATE workspace SET timezone='America/New_York' WHERE id=$1", a.WorkspaceID); err != nil {
		t.Fatal(err)
	}
	if err = s.SweepReminders(ctx); err != nil {
		t.Fatal(err)
	}
	if count(t, s, "notifications") != 1 {
		t.Fatal("new timezone gate")
	}
	p = mustGet(t, s, a, r.ID)
	if p.Actions[0].ReminderOn.Time.Format(time.DateOnly) != "2027-11-08" {
		t.Fatal("civil day changed")
	}
	// Removing opt-in is revalidated even for a candidate already selected.
	candidate := dbgen.PlanningReminderCandidatesRow{WorkspaceID: a.WorkspaceID, PlanID: p.Row.ID, ID: p.Actions[0].ID}
	in.ExpectedRevision = r.Revision
	in.ReminderOn = nil
	r, ps, err = s.UpdateAction(ctx, a, in, nil)
	if err != nil || len(ps) > 0 {
		t.Fatal(ps, err)
	}
	if err = s.deliverReminder(ctx, candidate, s.Clock.Now()); err != nil {
		t.Fatal(err)
	}
	if count(t, s, "notifications") != 1 {
		t.Fatal("removed opt in")
	}
	in.ExpectedRevision = r.Revision
	in.ReminderOn = ptr("2027-11-08")
	_, ps, err = s.UpdateAction(ctx, a, in, nil)
	if err != nil || len(ps) > 0 {
		t.Fatal(ps, err)
	}
	s.Clock = fixedClock{time.Date(2027, 11, 16, 16, 0, 0, 0, time.UTC)}
	if err = s.SweepReminders(ctx); err != nil {
		t.Fatal(err)
	}
	if count(t, s, "notifications") != 1 {
		t.Fatal("unbounded catchup")
	}
}
