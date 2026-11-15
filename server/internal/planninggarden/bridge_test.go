package planninggarden

import (
	candb "canopy/server/db"
	dbgen "canopy/server/db/generated"
	"canopy/server/internal/collection"
	"canopy/server/internal/collection/commands"
	"canopy/server/internal/planning"
	"canopy/server/internal/platform"
	"context"
	"fmt"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"log/slog"
	"os"
	"sync"
	"testing"
	"time"
)

func ptr[T any](x T) *T { return &x }

type clock struct{ now time.Time }

func (c clock) Now() time.Time { return c.now }
func day(s string) *time.Time  { d, _ := time.Parse(time.DateOnly, s); return &d }
func setup(t *testing.T) (*planning.Service, *collection.Service, planning.Actor) {
	t.Helper()
	dsn := os.Getenv("PGTEST_DSN")
	if dsn == "" {
		t.Skip("PGTEST_DSN not set")
	}
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	schema := fmt.Sprintf("plan07_%d", time.Now().UnixNano())
	if _, err = conn.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		c, e := pgx.Connect(context.Background(), dsn)
		if e == nil {
			_, _ = c.Exec(context.Background(), "DROP SCHEMA "+schema+" CASCADE")
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
	cfg, err := pgxpool.ParseConfig(dsn + "&search_path=" + schema)
	if err != nil {
		t.Fatal(err)
	}
	cfg.MaxConns = 2
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	log := slog.New(slog.DiscardHandler)
	s := planning.New(pool, log)
	s.Clock = clock{time.Date(2026, 11, 15, 18, 0, 0, 0, time.UTC)}
	c := collection.New(pool, log, nil)
	s.Garden = New(c)
	return s, c, workspace(t, s)
}
func workspace(t *testing.T, s *planning.Service) planning.Actor {
	t.Helper()
	ctx := context.Background()
	q := dbgen.New(s.Pool)
	ids := platform.UUIDv4{}
	o, err := q.InsertOwner(ctx, dbgen.InsertOwnerParams{ID: ids.New(), PasswordHash: "disposable"})
	if err != nil {
		t.Fatal(err)
	}
	w, err := q.InsertWorkspace(ctx, dbgen.InsertWorkspaceParams{ID: ids.New(), OwnerID: o.ID, DisplayName: "Planner", Timezone: "America/Chicago", DefaultCurrency: "USD"})
	if err != nil {
		t.Fatal(err)
	}
	return planning.Actor{WorkspaceID: w.ID, OwnerID: o.ID, SessionID: ids.New()}
}
func create(t *testing.T, s *planning.Service, a planning.Actor) planning.Result {
	t.Helper()
	r, ps, err := s.Create(context.Background(), a, planning.CreateInput{Intent: planning.Intent{Name: "Future fig", Category: "FIG", DesiredQuantity: ptr(int32(3)), IntendedPlacement: ptr("GROUND"), TargetPeriod: &planning.Period{Precision: planning.YearPrecision, Year: ptr(2027)}, Budget: &planning.Budget{MinorUnits: "15000", Currency: "USD"}}}, nil)
	check(t, ps, err)
	return r
}
func get(t *testing.T, s *planning.Service, a planning.Actor, id string) *planning.Plan {
	t.Helper()
	p, err := s.Get(context.Background(), a, id)
	if err != nil || p == nil {
		t.Fatal(p, err)
	}
	return p
}
func check(t *testing.T, ps []planning.Problem, err error) {
	t.Helper()
	if err != nil || len(ps) > 0 {
		t.Fatal(ps, err)
	}
}
func code(t *testing.T, ps []planning.Problem, err error, want string) {
	t.Helper()
	if err != nil || len(ps) == 0 || ps[0].Code != want {
		t.Fatal("want", want, ps, err)
	}
}
func count(t *testing.T, s *planning.Service, table string) int {
	t.Helper()
	var n int
	if err := s.Pool.QueryRow(context.Background(), "SELECT count(*) FROM "+table).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}
func actual() *commands.CreatePlantInput {
	return &commands.CreatePlantInput{Name: "My actual fig", Type: "INDIVIDUAL", Category: "FIG", InitialContext: &commands.ContextInput{Placement: "CONTAINER"}, Acquisition: &commands.AcquisitionInput{PricingState: "UNKNOWN", AcquiredOn: day("2026-10-25")}}
}

func TestAcquireEarlyThenPlantLaterWithoutFinishingPlan(t *testing.T) {
	s, _, a := setup(t)
	ctx := context.Background()
	r := create(t, s, a)
	r, ps, err := s.CreateAction(ctx, a, planning.ActionInput{PlanID: r.ID, ExpectedRevision: r.Revision, Kind: "PLANT_OUT", Title: "Plant outside", ScheduledOn: "2027-02-01", ReminderOn: ptr("2027-01-31")}, nil)
	check(t, ps, err)
	p := get(t, s, a, r.ID)
	action := p.Actions[0]
	in := planning.AddToGardenInput{PlanID: r.ID, ExpectedRevision: r.Revision, NewPlant: actual()}
	added, ps, err := s.AddToGarden(ctx, a, in, ptr("acquire"))
	check(t, ps, err)
	replay, ps, err := s.AddToGarden(ctx, a, in, ptr("acquire"))
	check(t, ps, err)
	if replay != added {
		t.Fatal("lost response replay", replay, added)
	}
	p = get(t, s, a, r.ID)
	if p.Row.State != "IN_PROGRESS" || p.Actions[0].State != "OPEN" || p.Fulfillment == nil {
		t.Fatal("ownership closed future work", p)
	}
	q := dbgen.New(s.Pool)
	plant, err := q.GetPlant(ctx, dbgen.GetPlantParams{WorkspaceID: a.WorkspaceID, ID: p.Fulfillment.PlantID})
	if err != nil {
		t.Fatal(err)
	}
	if plant.Count.Valid || plant.PlantedOn.Valid || plant.AcquiredOn.Time.Format(time.DateOnly) != "2026-10-25" || count(t, s, "purchases") != 0 {
		t.Fatal("intent leaked into actual", plant)
	}
	var placement string
	if err = s.Pool.QueryRow(ctx, "SELECT placement::text FROM plant_contexts WHERE plant_id=$1", plant.ID).Scan(&placement); err != nil || placement != "CONTAINER" {
		t.Fatal(placement, err)
	}
	s.Clock = clock{time.Date(2027, 2, 1, 18, 0, 0, 0, time.UTC)}
	_, ps, err = s.ChangeState(ctx, a, planning.StateInput{ID: r.ID, ExpectedRevision: added.Revision, State: "COMPLETED"}, nil)
	code(t, ps, err, "VALIDATION")
	completed, ps, err := s.CompleteAction(ctx, a, planning.CompleteActionInput{PlanID: r.ID, ID: action.ID.String(), ExpectedRevision: added.Revision, CompletedOn: "2027-01-31", ExpectedPlantRevision: &plant.Revision, ActualContext: &commands.ContextInput{Placement: "GROUND"}}, ptr("plant-later"))
	check(t, ps, err)
	p = get(t, s, a, r.ID)
	if p.Actions[0].CompletedOn.Time.Format(time.DateOnly) != "2027-01-31" || p.Actions[0].ScheduledOn.Time.Format(time.DateOnly) != "2027-02-01" || p.Row.State != "IN_PROGRESS" {
		t.Fatal(p)
	}
	plant, err = q.GetPlant(ctx, dbgen.GetPlantParams{WorkspaceID: a.WorkspaceID, ID: plant.ID})
	if err != nil || plant.PlantedOn.Time.Format(time.DateOnly) != "2027-01-31" || count(t, s, "plant_contexts") != 2 || count(t, s, "plants") != 1 {
		t.Fatal(plant, err)
	}
	_, ps, err = s.ChangeState(ctx, a, planning.StateInput{ID: r.ID, ExpectedRevision: completed.Revision, State: "COMPLETED"}, nil)
	check(t, ps, err)
}
func TestConversionConcurrentKeysRollbackAndRealPrice(t *testing.T) {
	s, _, a := setup(t)
	ctx := context.Background()
	r := create(t, s, a)
	in := planning.AddToGardenInput{PlanID: r.ID, ExpectedRevision: 1, NewPlant: actual()}
	var wg sync.WaitGroup
	success := make(chan planning.Result, 12)
	fail := make(chan []planning.Problem, 12)
	errs := make(chan error, 12)
	for i := range 12 {
		wg.Go(func() {
			out, ps, err := s.AddToGarden(ctx, a, in, ptr(fmt.Sprintf("client-%d", i)))
			if err != nil {
				errs <- err
			} else if len(ps) > 0 {
				fail <- ps
			} else {
				success <- out
			}
		})
	}
	wg.Wait()
	close(success)
	close(fail)
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	if len(success) != 1 || len(fail) != 11 || count(t, s, "plants") != 1 || count(t, s, "planting_fulfillments") != 1 {
		t.Fatal("duplicate conversion", len(success), len(fail))
	}
	for ps := range fail {
		code(t, ps, nil, "CONFLICT")
	}
	r = create(t, s, a)
	in.PlanID = r.ID
	in.NewPlant = actual()
	in.NewPlant.Acquisition.PricingState = "KNOWN"
	in.NewPlant.InitialPurchase = &commands.InitialPurchaseInput{Currency: "USD", Total: 1000, Tax: 82, PurchasedOn: day("2026-10-25")}
	// Fail the final planning audit, after plant/acquisition/purchase have been written.
	_, err := s.Pool.Exec(ctx, `CREATE FUNCTION reject_conversion() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.action='addPlantingToGarden' THEN RAISE EXCEPTION 'audit unavailable'; END IF; RETURN NEW; END $$; CREATE TRIGGER reject_conversion BEFORE INSERT ON audit_revisions FOR EACH ROW EXECUTE FUNCTION reject_conversion()`)
	if err != nil {
		t.Fatal(err)
	}
	before := count(t, s, "audit_revisions")
	_, _, err = s.AddToGarden(ctx, a, in, ptr("rollback"))
	if err == nil {
		t.Fatal("audit failure ignored")
	}
	if count(t, s, "plants") != 1 || count(t, s, "purchases") != 0 || count(t, s, "acquisitions") != 1 || count(t, s, "planting_fulfillments") != 1 || count(t, s, "audit_revisions") != before {
		t.Fatal("partial atomic transaction")
	}
	if _, err = s.Pool.Exec(ctx, "DROP TRIGGER reject_conversion ON audit_revisions"); err != nil {
		t.Fatal(err)
	}
	_, ps, err := s.AddToGarden(ctx, a, in, ptr("rollback"))
	check(t, ps, err)
	var total int64
	if err = s.Pool.QueryRow(ctx, "SELECT total_minor FROM purchases").Scan(&total); err != nil || total != 1000 || count(t, s, "purchase_allocations") != 1 {
		t.Fatal("real purchase", total, err)
	}
}
func TestLinkIsolationFutureDatesAndPurgeReplay(t *testing.T) {
	s, c, a := setup(t)
	ctx := context.Background()
	r := create(t, s, a)
	other := workspace(t, s)
	oa := collection.Actor{WorkspaceID: other.WorkspaceID, OwnerID: other.OwnerID, SessionID: other.SessionID}
	foreign, psC, err := c.CreatePlant(ctx, oa, *actual(), nil)
	if err != nil || len(psC) > 0 {
		t.Fatal(psC, err)
	}
	_, ps, err := s.AddToGarden(ctx, a, planning.AddToGardenInput{PlanID: r.ID, ExpectedRevision: 1, ExistingPlantID: &foreign.ID}, nil)
	code(t, ps, err, "NOT_FOUND")
	draft := actual()
	draft.PlantedOn = day("2027-02-01")
	_, ps, err = s.AddToGarden(ctx, a, planning.AddToGardenInput{PlanID: r.ID, ExpectedRevision: 1, NewPlant: draft}, nil)
	code(t, ps, err, "VALIDATION")
	draft = actual()
	future := time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)
	draft.InitialContext.EffectiveFrom = &future
	_, ps, err = s.AddToGarden(ctx, a, planning.AddToGardenInput{PlanID: r.ID, ExpectedRevision: 1, NewPlant: draft}, nil)
	code(t, ps, err, "VALIDATION")
	in := planning.AddToGardenInput{PlanID: r.ID, ExpectedRevision: 1, NewPlant: actual()}
	added, ps, err := s.AddToGarden(ctx, a, in, ptr("private-result"))
	check(t, ps, err)
	ca := collection.Actor{WorkspaceID: a.WorkspaceID, OwnerID: a.OwnerID, SessionID: a.SessionID}
	_, psC, err = c.PurgePlant(ctx, ca, added.PlantID, 1, "PERMANENTLY_DELETE_PLANT", ptr("purge"))
	if err != nil || len(psC) > 0 {
		t.Fatal(psC, err)
	}
	_, ps, err = s.AddToGarden(ctx, a, in, ptr("private-result"))
	code(t, ps, err, "RESOURCE_GONE")
	in.ExpectedRevision = added.Revision
	_, ps, err = s.AddToGarden(ctx, a, in, ptr("another-attempt"))
	code(t, ps, err, "RESOURCE_GONE")
	p := get(t, s, a, r.ID)
	if p.Fulfillment == nil || p.Fulfillment.PlantID.Valid {
		t.Fatal("lost purge marker")
	}
	// Linking an existing living plant does not create another plant or touch it.
	existing, psC, err := c.CreatePlant(ctx, ca, *actual(), nil)
	if err != nil || len(psC) > 0 {
		t.Fatal(psC, err)
	}
	r = create(t, s, a)
	before := count(t, s, "plants")
	_, ps, err = s.AddToGarden(ctx, a, planning.AddToGardenInput{PlanID: r.ID, ExpectedRevision: 1, ExistingPlantID: &existing.ID}, nil)
	check(t, ps, err)
	if count(t, s, "plants") != before {
		t.Fatal("link created another plant")
	}
}
func TestSowingPromotionAndSuppliesAreDifferentActions(t *testing.T) {
	s, _, a := setup(t)
	ctx := context.Background()
	r := create(t, s, a)
	r, ps, err := s.CreateAction(ctx, a, planning.ActionInput{PlanID: r.ID, ExpectedRevision: 1, Kind: "BUY", Title: "Buy seeds", ScheduledOn: "2026-11-10"}, nil)
	check(t, ps, err)
	p := get(t, s, a, r.ID)
	done, ps, err := s.CompleteAction(ctx, a, planning.CompleteActionInput{PlanID: r.ID, ID: p.Actions[0].ID.String(), ExpectedRevision: r.Revision, CompletedOn: "2026-11-10"}, nil)
	check(t, ps, err)
	if count(t, s, "plants") != 0 {
		t.Fatal("seed purchase created a plant")
	}
	r, ps, err = s.CreateAction(ctx, a, planning.ActionInput{PlanID: r.ID, ExpectedRevision: done.Revision, Kind: "DIRECT_SOW", Title: "Sow batch", ScheduledOn: "2026-11-12"}, nil)
	check(t, ps, err)
	p = get(t, s, a, r.ID)
	action := p.Actions[1]
	_, ps, err = s.CompleteAction(ctx, a, planning.CompleteActionInput{PlanID: r.ID, ID: action.ID.String(), ExpectedRevision: r.Revision, CompletedOn: "2026-11-12"}, nil)
	code(t, ps, err, "VALIDATION")
	draft := actual()
	draft.Type = "GROUP"
	draft.Count = nil
	added, ps, err := s.AddToGarden(ctx, a, planning.AddToGardenInput{PlanID: r.ID, ExpectedRevision: r.Revision, NewPlant: draft, ActionID: ptr(action.ID.String()), CompletedOn: ptr("2026-11-12")}, ptr("sow"))
	check(t, ps, err)
	p = get(t, s, a, r.ID)
	plantID := p.Fulfillment.PlantID
	var on string
	var n *int32
	if err = s.Pool.QueryRow(ctx, "SELECT planted_on::text,count FROM plants WHERE id=$1", plantID).Scan(&on, &n); err != nil || on != "2026-11-12" || n != nil {
		t.Fatal(on, n, err)
	}
	if p.Actions[1].State != "DONE" || p.Fulfillment.ActionID != action.ID || added.Revision != r.Revision+1 {
		t.Fatal(p)
	}
	// Dismissal closes a remaining action without creating garden work.
	r, ps, err = s.CreateAction(ctx, a, planning.ActionInput{PlanID: r.ID, ExpectedRevision: added.Revision, Kind: "PREPARE", Title: "Unneeded", ScheduledOn: "2026-11-13"}, nil)
	check(t, ps, err)
	p = get(t, s, a, r.ID)
	_, ps, err = s.DismissAction(ctx, a, planning.DeleteActionInput{PlanID: r.ID, ID: p.Actions[2].ID.String(), ExpectedRevision: r.Revision}, nil)
	check(t, ps, err)
	if count(t, s, "activities") != 0 || count(t, s, "planting_fulfillments") != 1 {
		t.Fatal("planning action invented activity")
	}
}
