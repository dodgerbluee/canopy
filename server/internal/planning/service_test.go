package planning

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	candb "canopy/server/db"
	dbgen "canopy/server/db/generated"
	"canopy/server/internal/platform"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

func scratch(t *testing.T) (*Service, Actor) {
	t.Helper()
	ctx := context.Background()
	dsn := os.Getenv("PGTEST_DSN")
	if dsn == "" {
		t.Skip("PGTEST_DSN not set")
	}
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	schema := fmt.Sprintf("plan02_%d", time.Now().UnixNano())
	if _, err = conn.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		c, e := pgx.Connect(context.Background(), dsn)
		if e == nil {
			_, _ = c.Exec(context.Background(), "DROP SCHEMA IF EXISTS "+schema+" CASCADE")
			_ = c.Close(context.Background())
		}
	})
	if _, err = conn.Exec(ctx, "SET search_path TO "+schema); err != nil {
		t.Fatal(err)
	}
	up, err := candb.AllUp()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = conn.Exec(ctx, up); err != nil {
		t.Fatal(err)
	}
	_ = conn.Close(ctx)
	pool, err := pgxpool.New(ctx, dsn+"&search_path="+schema)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	s := New(pool, slog.New(slog.DiscardHandler))
	return s, workspace(t, s)
}
func workspace(t *testing.T, s *Service) Actor {
	t.Helper()
	ctx := context.Background()
	q := s.q()
	id := platform.UUIDv4{}
	owner, err := q.InsertOwner(ctx, dbgen.InsertOwnerParams{ID: id.New(), PasswordHash: "disposable"})
	if err != nil {
		t.Fatal(err)
	}
	ws, err := q.InsertWorkspace(ctx, dbgen.InsertWorkspaceParams{ID: id.New(), OwnerID: owner.ID, DisplayName: "Planning fixture", Timezone: "America/Chicago", DefaultCurrency: "USD"})
	if err != nil {
		t.Fatal(err)
	}
	return Actor{WorkspaceID: ws.ID, OwnerID: owner.ID, SessionID: id.New()}
}
func mustCreate(t *testing.T, s *Service, a Actor, in Intent) Result {
	t.Helper()
	res, ps, err := s.Create(context.Background(), a, CreateInput{Intent: in}, nil)
	if err != nil || len(ps) > 0 {
		t.Fatalf("create: %v %v", ps, err)
	}
	return res
}
func mustGet(t *testing.T, s *Service, a Actor, id string) *Plan {
	t.Helper()
	p, err := s.Get(context.Background(), a, id)
	if err != nil || p == nil {
		t.Fatalf("get: %v %v", p, err)
	}
	return p
}
func assertCode(t *testing.T, ps []Problem, err error, code string) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range ps {
		if p.Code == code {
			return
		}
	}
	t.Fatalf("missing %s in %+v", code, ps)
}
func count(t *testing.T, s *Service, table string) int {
	t.Helper()
	var n int
	if err := s.Pool.QueryRow(context.Background(), "SELECT count(*) FROM "+table).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestCreateReplaceAndIsolation(t *testing.T) {
	s, a := scratch(t)
	ctx := context.Background()
	other := workspace(t, s)
	loc, err := s.q().InsertLocation(ctx, dbgen.InsertLocationParams{ID: s.IDs.New(), WorkspaceID: other.WorkspaceID, Name: "Other location", Situation: dbgen.LocationSituationOUTDOOR})
	if err != nil {
		t.Fatal(err)
	}
	_, ps, err := s.Create(ctx, a, CreateInput{Intent: Intent{Name: "Wrong location", IntendedLocationID: ptr(idString(loc.ID))}}, ptr("invalid"))
	assertCode(t, ps, err, "VALIDATION")
	if count(t, s, "planting_plans") != 0 || count(t, s, "audit_revisions") != 0 || count(t, s, "mutation_receipts") != 0 {
		t.Fatal("invalid save wrote data")
	}
	r := mustCreate(t, s, a, Intent{Name: " Another fig ", ScientificName: ptr("Ficus carica"), Category: "FIG", DesiredQuantity: ptr(int32(2)), TargetPeriod: &Period{Precision: SeasonPrecision, Season: &SeasonPeriod{Season: Winter, Year: 2027}}, Budget: &Budget{MinorUnits: "1000000000000000", Currency: "USD"}, Notes: ptr("Room for a container")})
	p := mustGet(t, s, a, r.ID)
	if p.Row.Name != "Another fig" || p.Row.TargetFrom.Valid || p.Row.Method.Valid || p.Row.BudgetMinor.Int64 != 1000000000000000 {
		t.Fatal("incorrect values", p.Row)
	}
	if x, e := s.Get(ctx, other, r.ID); e != nil || x != nil {
		t.Fatal("cross-workspace read", x, e)
	}
	_, ps, err = s.Update(ctx, other, UpdateInput{ID: r.ID, ExpectedRevision: 1, Intent: Intent{Name: "Attack"}}, nil)
	assertCode(t, ps, err, "NOT_FOUND")
	_, ps, err = s.Update(ctx, a, UpdateInput{ID: r.ID, ExpectedRevision: 2, Intent: Intent{Name: "Stale"}}, nil)
	assertCode(t, ps, err, "CONFLICT")
	if ps[0].CurrentRevision == nil || *ps[0].CurrentRevision != 1 {
		t.Fatal("no current revision")
	}
	changed, ps, err := s.Update(ctx, a, UpdateInput{ID: r.ID, ExpectedRevision: 1, Intent: Intent{Name: "Minimal idea"}}, nil)
	if err != nil || len(ps) > 0 || changed.Revision != 2 {
		t.Fatal(changed, ps, err)
	}
	p = mustGet(t, s, a, r.ID)
	if p.Row.BudgetMinor.Valid || p.Row.DesiredQuantity.Valid || p.Row.ScientificName.Valid || p.Row.Notes.Valid || p.Row.TargetPrecision.Valid || p.Row.GuidanceGeneration != 2 {
		t.Fatal("full replacement retained old fields", p.Row)
	}
	for _, table := range []string{"plants", "purchases", "activities", "care_plans", "planting_fulfillments"} {
		if count(t, s, table) != 0 {
			t.Fatalf("planning created actual %s", table)
		}
	}
	if count(t, s, "audit_revisions") != 2 {
		t.Fatal("audit count")
	}
}
func TestIdempotencyConcurrentReplayExpiryAndGone(t *testing.T) {
	s, a := scratch(t)
	ctx := context.Background()
	key := "same-request"
	in := CreateInput{Intent: Intent{Name: "Blueberry"}}
	type response struct {
		res Result
		ps  []Problem
		err error
	}
	out := make(chan response, 8)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); <-start; r, p, e := s.Create(ctx, a, in, &key); out <- response{r, p, e} }()
	}
	close(start)
	wg.Wait()
	close(out)
	var id string
	for v := range out {
		if v.err != nil || len(v.ps) > 0 {
			t.Fatal(v.ps, v.err)
		}
		if id != "" && v.res.ID != id {
			t.Fatal("duplicate create")
		}
		id = v.res.ID
	}
	if count(t, s, "planting_plans") != 1 || count(t, s, "audit_revisions") != 1 || count(t, s, "mutation_receipts") != 1 {
		t.Fatal("replay duplicated writes")
	}
	_, ps, err := s.Create(ctx, a, CreateInput{Intent: Intent{Name: "Different"}}, &key)
	assertCode(t, ps, err, "IDEMPOTENCY_CONFLICT")
	if _, err = s.Pool.Exec(ctx, `UPDATE mutation_receipts SET gone=true WHERE workspace_id=$1`, a.WorkspaceID); err != nil {
		t.Fatal(err)
	}
	_, ps, err = s.Create(ctx, a, in, &key)
	assertCode(t, ps, err, "RESOURCE_GONE")
	if _, err = s.Pool.Exec(ctx, `UPDATE mutation_receipts SET expires_at=now()-interval '1 second' WHERE workspace_id=$1`, a.WorkspaceID); err != nil {
		t.Fatal(err)
	}
	r, ps, err := s.Create(ctx, a, CreateInput{Intent: Intent{Name: "After expiry"}}, &key)
	if err != nil || len(ps) > 0 || r.ID == id {
		t.Fatal("expired reuse", r, ps, err)
	}
	if count(t, s, "planting_plans") != 2 || count(t, s, "mutation_receipts") != 1 {
		t.Fatal("expired receipt not replaced")
	}
}
func TestActionsAggregateRevisionSchedulesAndLifecycle(t *testing.T) {
	s, a := scratch(t)
	ctx := context.Background()
	r := mustCreate(t, s, a, Intent{Name: "Winter tree"})
	in := ActionInput{PlanID: r.ID, ExpectedRevision: 1, Kind: "PLANT_OUT", Title: " Plant outside ", ScheduledOn: "2027-02-01", ReminderOn: ptr("2027-01-30")}
	r, ps, err := s.CreateAction(ctx, a, in, ptr("action-create"))
	if err != nil || len(ps) > 0 || r.Revision != 2 {
		t.Fatal(r, ps, err)
	}
	p := mustGet(t, s, a, r.ID)
	action := p.Actions[0]
	in.ID = idString(action.ID)
	in.ExpectedRevision = 2
	in.Title = "Updated title"
	if action.ScheduleRevision != 1 || action.State != "OPEN" || p.Row.State != "IDEA" {
		t.Fatal("action implied progress/completion")
	}
	r, ps, err = s.UpdateAction(ctx, a, in, nil)
	if err != nil || len(ps) > 0 {
		t.Fatal(ps, err)
	}
	p = mustGet(t, s, a, r.ID)
	if p.Actions[0].ScheduleRevision != 1 {
		t.Fatal("title edit invalidated reminder")
	}
	in.ExpectedRevision = r.Revision
	in.ScheduledOn = "2027-02-02"
	r, ps, err = s.UpdateAction(ctx, a, in, nil)
	if err != nil || len(ps) > 0 {
		t.Fatal(ps, err)
	}
	if mustGet(t, s, a, r.ID).Actions[0].ScheduleRevision != 2 {
		t.Fatal("schedule edit did not invalidate reminder")
	}
	r, ps, err = s.Cancel(ctx, a, RevisionInput{ID: r.ID, ExpectedRevision: r.Revision}, nil)
	if err != nil || len(ps) > 0 {
		t.Fatal(ps, err)
	}
	p = mustGet(t, s, a, r.ID)
	if p.Row.ResumeState.PlantingPlanState != "IDEA" || p.Row.CompletedAt.Valid || p.Actions[0].State != "OPEN" || p.Actions[0].ScheduleRevision != 3 {
		t.Fatal("cancel lost intent")
	}
	_, ps, err = s.UpdateAction(ctx, a, ActionInput{PlanID: r.ID, ID: in.ID, ExpectedRevision: r.Revision}, nil)
	assertCode(t, ps, err, "VALIDATION")
	r, ps, err = s.Restore(ctx, a, RevisionInput{ID: r.ID, ExpectedRevision: r.Revision}, nil)
	if err != nil || len(ps) > 0 {
		t.Fatal(ps, err)
	}
	if mustGet(t, s, a, r.ID).Actions[0].ScheduleRevision != 4 {
		t.Fatal("restore did not invalidate reminder")
	}
	r, ps, err = s.ChangeState(ctx, a, StateInput{ID: r.ID, ExpectedRevision: r.Revision, State: "IN_PROGRESS"}, nil)
	if err != nil || len(ps) > 0 {
		t.Fatal(ps, err)
	}
	_, ps, err = s.ChangeState(ctx, a, StateInput{ID: r.ID, ExpectedRevision: r.Revision, State: "COMPLETED"}, nil)
	assertCode(t, ps, err, "VALIDATION")
	r, ps, err = s.DeleteAction(ctx, a, DeleteActionInput{PlanID: r.ID, ID: in.ID, ExpectedRevision: r.Revision}, ptr("remove"))
	if err != nil || len(ps) > 0 {
		t.Fatal(ps, err)
	}
	r, ps, err = s.ChangeState(ctx, a, StateInput{ID: r.ID, ExpectedRevision: r.Revision, State: "COMPLETED"}, nil)
	if err != nil || len(ps) > 0 {
		t.Fatal(ps, err)
	}
	p = mustGet(t, s, a, r.ID)
	if !p.Row.CompletedAt.Valid || len(p.Actions) != 0 {
		t.Fatal("completion metadata")
	}
	_, ps, err = s.Cancel(ctx, a, RevisionInput{ID: r.ID, ExpectedRevision: r.Revision}, nil)
	assertCode(t, ps, err, "VALIDATION")
	_, ps, err = s.Update(ctx, a, UpdateInput{ID: r.ID, ExpectedRevision: r.Revision, Intent: Intent{Name: "Edit final"}}, nil)
	assertCode(t, ps, err, "VALIDATION")
	before := count(t, s, "audit_revisions")
	again, ps, err := s.ChangeState(ctx, a, StateInput{ID: r.ID, ExpectedRevision: r.Revision, State: "COMPLETED"}, nil)
	if err != nil || len(ps) > 0 || again.Revision != r.Revision || count(t, s, "audit_revisions") != before {
		t.Fatal("same-state command wrote")
	}
}
func TestActionScopeLimitsAndConcurrentEdits(t *testing.T) {
	s, a := scratch(t)
	ctx := context.Background()
	one := mustCreate(t, s, a, Intent{Name: "One"})
	two := mustCreate(t, s, a, Intent{Name: "Two"})
	type response struct {
		r Result
		p []Problem
		e error
	}
	ch := make(chan response, 2)
	start := make(chan struct{})
	for i := 0; i < 2; i++ {
		go func() {
			<-start
			r, p, e := s.CreateAction(ctx, a, ActionInput{PlanID: one.ID, ExpectedRevision: 1, Kind: "BUY", Title: "Buy", ScheduledOn: "2027-01-01"}, nil)
			ch <- response{r, p, e}
		}()
	}
	close(start)
	success, conflict := 0, 0
	for i := 0; i < 2; i++ {
		v := <-ch
		if v.e != nil {
			t.Fatal(v.e)
		}
		if len(v.p) == 0 {
			success++
		} else if v.p[0].Code == "CONFLICT" {
			conflict++
		} else {
			t.Fatal(v.p)
		}
	}
	if success != 1 || conflict != 1 {
		t.Fatal("lost concurrent write", success, conflict)
	}
	p := mustGet(t, s, a, one.ID)
	action := p.Actions[0]
	_, ps, err := s.DeleteAction(ctx, a, DeleteActionInput{PlanID: two.ID, ID: idString(action.ID), ExpectedRevision: 1}, nil)
	assertCode(t, ps, err, "NOT_FOUND")
	if mustGet(t, s, a, two.ID).Row.Revision != 1 {
		t.Fatal("wrong parent modified")
	}
	if _, err = s.Pool.Exec(ctx, `UPDATE planting_actions SET state='DISMISSED',dismissed_at=now() WHERE id=$1`, action.ID); err != nil {
		t.Fatal(err)
	}
	_, ps, err = s.DeleteAction(ctx, a, DeleteActionInput{PlanID: one.ID, ID: idString(action.ID), ExpectedRevision: 2}, nil)
	assertCode(t, ps, err, "VALIDATION")
	if _, err = s.Pool.Exec(ctx, `INSERT INTO planting_actions(id,workspace_id,plan_id,kind,title,scheduled_on) SELECT gen_random_uuid(),$1,$2,'BUY','Fixture','2027-01-01' FROM generate_series(1,99)`, a.WorkspaceID, mustID(one.ID)); err != nil {
		t.Fatal(err)
	}
	_, ps, err = s.CreateAction(ctx, a, ActionInput{PlanID: one.ID, ExpectedRevision: 2, Kind: "BUY", Title: "Over limit", ScheduledOn: "2027-01-01"}, nil)
	assertCode(t, ps, err, "VALIDATION")
	if count(t, s, "planting_actions") != 100 || mustGet(t, s, a, one.ID).Row.Revision != 2 {
		t.Fatal("over-limit write")
	}
}
func mustID(raw string) pgtype.UUID { id, _ := parseID(raw); return id }
func TestAuditFailureRollsBackEntireAction(t *testing.T) {
	s, a := scratch(t)
	ctx := context.Background()
	r := mustCreate(t, s, a, Intent{Name: "Rollback"})
	_, err := s.Pool.Exec(ctx, `CREATE FUNCTION reject_planning_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.aggregate_type='planting_plan' THEN RAISE EXCEPTION 'fixture failure'; END IF; RETURN NEW; END $$; CREATE TRIGGER reject_planning_audit BEFORE INSERT ON audit_revisions FOR EACH ROW EXECUTE FUNCTION reject_planning_audit()`)
	if err != nil {
		t.Fatal(err)
	}
	_, ps, err := s.CreateAction(ctx, a, ActionInput{PlanID: r.ID, ExpectedRevision: 1, Kind: "BUY", Title: "Must roll back", ScheduledOn: "2027-01-01"}, ptr("rollback"))
	if err == nil || len(ps) > 0 {
		t.Fatal("storage failure must be an error", ps, err)
	}
	if count(t, s, "planting_actions") != 0 || count(t, s, "mutation_receipts") != 0 || count(t, s, "audit_revisions") != 1 || mustGet(t, s, a, r.ID).Row.Revision != 1 {
		t.Fatal("partial failed transaction")
	}
}
func TestPaginationFiltersBindingsAndLiteralSearch(t *testing.T) {
	s, a := scratch(t)
	ctx := context.Background()
	other := workspace(t, s)
	for _, name := range []string{"Alpha", "alpha", "Fig | yellow", "100% berry", "under_score", "冬青"} {
		mustCreate(t, s, a, Intent{Name: name, Cultivar: ptr("Test cultivar")})
	}
	mustCreate(t, s, other, Intent{Name: "Other workspace"})
	var cursor *string
	seen := map[string]bool{}
	for {
		page, err := s.List(ctx, a, Query{First: 2, After: cursor})
		if err != nil {
			t.Fatal(err)
		}
		for _, p := range page.Plans {
			if seen[idString(p.Row.ID)] {
				t.Fatal("repeated row")
			}
			seen[idString(p.Row.ID)] = true
		}
		cursor = page.EndCursor
		if !page.HasNext {
			break
		}
	}
	if len(seen) != 6 || cursor == nil {
		t.Fatal("pagination lost data")
	}
	for _, in := range []Query{{First: 2, After: cursor, Search: "new"}, {First: 2, After: cursor, Order: OrderUpdatedDesc}, {First: 0}, {First: 101}, {First: 2, Search: strings.Repeat("a", 201)}, {First: 2, After: ptr("broken")}} {
		if _, err := s.List(ctx, a, in); err == nil {
			t.Fatal("invalid query/cursor accepted", in)
		}
	}
	if _, err := s.List(ctx, other, Query{First: 2, After: cursor}); err != ErrInvalidCursor {
		t.Fatal("cross-workspace cursor", err)
	}
	for _, search := range []string{"%", "_", "  |  "} {
		p, err := s.List(ctx, a, Query{First: 25, Search: search})
		if err != nil || len(p.Plans) != 1 {
			t.Fatal("search treated literal as wildcard", search, p, err)
		}
	}
	empty, err := s.List(ctx, a, Query{First: 25, States: []string{}})
	if err != nil || len(empty.Plans) != 0 {
		t.Fatal("empty states defaulted")
	}
	for _, order := range []string{OrderNameAsc, OrderUpdatedDesc} {
		page, err := s.List(ctx, a, Query{First: 1, Order: order})
		if err != nil || !page.HasNext {
			t.Fatal(page, err)
		}
		if _, err = s.List(ctx, a, Query{First: 4, Order: order, After: page.EndCursor}); err != nil {
			t.Fatal("cursor bound to size", err)
		}
	}
}

func TestAcceptedGuidanceSurvivesContextChangesButDetachesChangedPeriod(t *testing.T) {
	s, a := scratch(t)
	ctx := context.Background()
	period := &Period{Precision: YearPrecision, Year: ptr(2027)}
	r := mustCreate(t, s, a, Intent{Name: "Future fig", Category: "FIG", TargetPeriod: period})
	snapshot := s.IDs.New()
	if _, err := s.Pool.Exec(ctx, `INSERT INTO planting_guidance_snapshots(id,workspace_id,plan_id,generation,context_sha256,state,facts,retrieved_at) VALUES($1,$2,$3,1,repeat('a',64),'READY','[{"id":"fixture"}]',now())`, snapshot, a.WorkspaceID, mustID(r.ID)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Pool.Exec(ctx, `UPDATE planting_plans SET accepted_guidance_id=$1 WHERE id=$2`, snapshot, mustID(r.ID)); err != nil {
		t.Fatal(err)
	}
	p := mustGet(t, s, a, r.ID)
	if p.LatestGuidance == nil || p.AcceptedGuidance == nil {
		t.Fatal("guidance read")
	}
	r, ps, err := s.Update(ctx, a, UpdateInput{ID: r.ID, ExpectedRevision: 1, Intent: Intent{Name: "Renamed fig", Category: "FIG", TargetPeriod: period}}, nil)
	if err != nil || len(ps) > 0 {
		t.Fatal(ps, err)
	}
	p = mustGet(t, s, a, r.ID)
	if p.Row.GuidanceGeneration != 2 || p.LatestGuidance != nil || p.AcceptedGuidance == nil || p.AcceptedGuidance.ID != snapshot || p.Row.TargetYear.Int32 != 2027 {
		t.Fatal("context refresh overwrote dates or reused old latest")
	}
	r, ps, err = s.Update(ctx, a, UpdateInput{ID: r.ID, ExpectedRevision: r.Revision, Intent: Intent{Name: "Renamed fig", Category: "FIG", TargetPeriod: period, Budget: &Budget{MinorUnits: "0", Currency: "USD"}}}, nil)
	if err != nil || len(ps) > 0 {
		t.Fatal(ps, err)
	}
	if mustGet(t, s, a, r.ID).Row.GuidanceGeneration != 2 {
		t.Fatal("budget-only edit invalidated planting context")
	}
	_, ps, err = s.Update(ctx, a, UpdateInput{ID: r.ID, ExpectedRevision: r.Revision, Intent: Intent{Name: "Renamed fig", Category: "FIG", TargetPeriod: &Period{Precision: MonthRangePrecision, Months: &MonthRange{From: "2027-12", To: "2028-02"}}}}, nil)
	if err != nil || len(ps) > 0 {
		t.Fatal(ps, err)
	}
	p = mustGet(t, s, a, r.ID)
	if p.AcceptedGuidance != nil || p.Row.AcceptedGuidanceID.Valid || count(t, s, "planting_guidance_snapshots") != 1 {
		t.Fatal("period change destroyed history or retained acceptance")
	}
}

type fixedClock struct{ now time.Time }

func (c fixedClock) Now() time.Time { return c.now }
func TestCompletionUsesInjectedClockAndActionReplayDoesNotDuplicate(t *testing.T) {
	s, a := scratch(t)
	ctx := context.Background()
	r := mustCreate(t, s, a, Intent{Name: "Deterministic plan"})
	input := ActionInput{PlanID: r.ID, ExpectedRevision: 1, Kind: "BUY", Title: "Review acquisition", ScheduledOn: "2027-01-01"}
	r, ps, err := s.CreateAction(ctx, a, input, ptr("replay-action"))
	if err != nil || len(ps) > 0 {
		t.Fatal(ps, err)
	}
	replay, ps, err := s.CreateAction(ctx, a, input, ptr("replay-action"))
	if err != nil || len(ps) > 0 || replay != r {
		t.Fatal("action replay changed result", replay, ps, err)
	}
	p := mustGet(t, s, a, r.ID)
	if len(p.Actions) != 1 || p.Row.Revision != 2 || count(t, s, "audit_revisions") != 2 {
		t.Fatal("action replay wrote twice")
	}
	r, ps, err = s.DeleteAction(ctx, a, DeleteActionInput{PlanID: r.ID, ID: idString(p.Actions[0].ID), ExpectedRevision: 2}, nil)
	if err != nil || len(ps) > 0 {
		t.Fatal(ps, err)
	}
	s.Clock = fixedClock{now: time.Date(2026, 11, 14, 12, 30, 0, 0, time.UTC)}
	r, ps, err = s.ChangeState(ctx, a, StateInput{ID: r.ID, ExpectedRevision: r.Revision, State: "IN_PROGRESS"}, nil)
	if err != nil || len(ps) > 0 {
		t.Fatal(ps, err)
	}
	r, ps, err = s.ChangeState(ctx, a, StateInput{ID: r.ID, ExpectedRevision: r.Revision, State: "COMPLETED"}, nil)
	if err != nil || len(ps) > 0 {
		t.Fatal(ps, err)
	}
	if !mustGet(t, s, a, r.ID).Row.CompletedAt.Time.Equal(s.Clock.Now()) {
		t.Fatal("completion ignored injected clock")
	}
}
func TestMethodLocationAndStateFilters(t *testing.T) {
	s, a := scratch(t)
	ctx := context.Background()
	location, err := s.q().InsertLocation(ctx, dbgen.InsertLocationParams{ID: s.IDs.New(), WorkspaceID: a.WorkspaceID, Name: "Bed", Situation: dbgen.LocationSituationOUTDOOR})
	if err != nil {
		t.Fatal(err)
	}
	r := mustCreate(t, s, a, Intent{Name: "Chosen variety", Cultivar: ptr("Heritage"), Method: ptr("DIRECT_SOW"), IntendedLocationID: ptr(idString(location.ID))})
	mustCreate(t, s, a, Intent{Name: "Unscheduled other idea"})
	for _, query := range []Query{{First: 25, Method: ptr("DIRECT_SOW")}, {First: 25, LocationID: ptr(idString(location.ID))}, {First: 25, Search: "HERITAGE"}} {
		page, err := s.List(ctx, a, query)
		if err != nil || len(page.Plans) != 1 || idString(page.Plans[0].Row.ID) != r.ID {
			t.Fatal("filter mismatch", query, page, err)
		}
	}
	cancelled, ps, err := s.Cancel(ctx, a, RevisionInput{ID: r.ID, ExpectedRevision: 1}, nil)
	if err != nil || len(ps) > 0 {
		t.Fatal(ps, err)
	}
	page, err := s.List(ctx, a, Query{First: 25, States: []string{"CANCELLED", "CANCELLED"}})
	if err != nil || len(page.Plans) != 1 || page.Plans[0].Row.Revision != cancelled.Revision {
		t.Fatal("state filter mismatch", page, err)
	}
}
