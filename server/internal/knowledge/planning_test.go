package knowledge

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	candb "canopy/server/db"
	dbgen "canopy/server/db/generated"
	"canopy/server/internal/planning"
	"canopy/server/internal/platform"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
)

const plantingEvidence = "Plant nursery fig trees in December through February."
const floweringEvidence = "Fig flowers bloom in March."

type plantingModelStub struct {
	plantingCalls, ownedCalls atomic.Int32
	identity                  string
	extract                   func(context.Context, Source, string) ([]PlantingDraft, error)
}

func (m *plantingModelStub) PlanningIdentity() string { return hash([]string{m.identity}) }
func (m *plantingModelStub) ExtractPlanting(ctx context.Context, s Source, text string) ([]PlantingDraft, error) {
	m.plantingCalls.Add(1)
	if m.extract != nil {
		return m.extract(ctx, s, text)
	}
	return []PlantingDraft{{Phase: "PLANT_OUT", Label: "Planting window", Summary: "Nursery fig trees have a seasonal planting window.", Evidence: plantingEvidence, Timing: "December through February", Methods: []string{"NURSERY_PLANT"}}}, nil
}
func (m *plantingModelStub) Extract(context.Context, Source, string) ([]DraftFact, error) {
	m.ownedCalls.Add(1)
	return []DraftFact{{Kind: "BLOOM", Label: "Flowers", Summary: "Fig flowering is described by the source.", Evidence: floweringEvidence, Timing: "March"}}, nil
}
func (m *plantingModelStub) Answer(context.Context, string) (AnswerDraft, error) {
	return AnswerDraft{}, errors.New("not used")
}

type planningEnv struct {
	s          *Service
	planner    *planning.Service
	actor      planning.Actor
	model      *plantingModelStub
	fetchCalls *atomic.Int32
}

func planningDatabase(t *testing.T) *planningEnv {
	t.Helper()
	ctx := context.Background()
	dsn := os.Getenv("PGTEST_DSN")
	if dsn == "" {
		t.Skip("PGTEST_DSN not set")
	}
	admin, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	schema := fmt.Sprintf("planning_knowledge_%d", time.Now().UnixNano())
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		c, e := pgx.Connect(context.Background(), dsn)
		if e == nil {
			_, _ = c.Exec(context.Background(), "DROP SCHEMA IF EXISTS "+schema+" CASCADE")
			_ = c.Close(context.Background())
		}
	})
	if _, err = admin.Exec(ctx, "SET search_path TO "+schema); err != nil {
		t.Fatal(err)
	}
	up, err := candb.AllUp()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = admin.Exec(ctx, up); err != nil {
		t.Fatal(err)
	}
	_ = admin.Close(ctx)
	config, err := pgxpool.ParseConfig(dsn + "&search_path=" + schema)
	if err != nil {
		t.Fatal(err)
	}
	config.MaxConns = 4
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	s := New(pool)
	s.Sources = []Source{plantingFixtureSource()}
	model := &plantingModelStub{identity: "fixture-model"}
	s.Model = model
	calls := &atomic.Int32{}
	s.Fetch = &Fetcher{HTTP: &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		calls.Add(1)
		body := "User-agent: *\nAllow: /"
		if r.URL.Path != "/robots.txt" {
			body = "<article><p>" + plantingEvidence + "</p><p>" + floweringEvidence + "</p><p>" + strings.Repeat("Synthetic fixture prose only; not growing advice. ", 20) + "</p></article>"
		}
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body))}, nil
	})}}
	planner := planning.New(pool, slog.New(slog.DiscardHandler))
	planner.Guidance = PlanningProvider{Service: s}
	e := &planningEnv{s: s, planner: planner, model: model, fetchCalls: calls}
	e.actor = e.workspace(t)
	return e
}
func (e *planningEnv) workspace(t *testing.T) planning.Actor {
	t.Helper()
	ctx := context.Background()
	ids := platform.UUIDv4{}
	q := e.s.Q
	owner, err := q.InsertOwner(ctx, dbgen.InsertOwnerParams{ID: ids.New(), PasswordHash: "disposable"})
	if err != nil {
		t.Fatal(err)
	}
	ws, err := q.InsertWorkspace(ctx, dbgen.InsertWorkspaceParams{ID: ids.New(), OwnerID: owner.ID, DisplayName: "Fixture garden", Timezone: "America/Chicago", DefaultCurrency: "USD"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = q.SaveKnowledgeSettings(ctx, dbgen.SaveKnowledgeSettingsParams{WorkspaceID: ws.ID, Country: "US", Region: "Texas", Place: "Fixture place", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	return planning.Actor{WorkspaceID: ws.ID, OwnerID: owner.ID, SessionID: ids.New()}
}
func (e *planningEnv) idea(t *testing.T, a planning.Actor, name string) string {
	t.Helper()
	method, placement, notes := "NURSERY_PLANT", "GROUND", "private-plan-secret"
	r, ps, err := e.planner.Create(context.Background(), a, planning.CreateInput{Intent: planning.Intent{Name: name, Category: "FIG", Method: &method, IntendedPlacement: &placement, Notes: &notes}}, nil)
	if err != nil || len(ps) > 0 {
		t.Fatal(ps, err)
	}
	return r.ID
}
func (e *planningEnv) plan(t *testing.T, a planning.Actor, id string) *planning.Plan {
	t.Helper()
	p, err := e.planner.Get(context.Background(), a, id)
	if err != nil || p == nil {
		t.Fatal(p, err)
	}
	return p
}
func (e *planningEnv) usage(t *testing.T, a planning.Actor) int {
	t.Helper()
	var n int
	if err := e.s.Pool.QueryRow(context.Background(), `SELECT COALESCE(sum(requests),0)::int FROM planning_provider_usage WHERE workspace_id=$1`, a.WorkspaceID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}
func TestPlantingProviderPublicCacheWorkspaceContextAndOwnedCompatibility(t *testing.T) {
	e := planningDatabase(t)
	ctx := context.Background()
	first := e.idea(t, e.actor, "Private fig idea one")
	if err := e.planner.SweepGuidance(ctx); err != nil {
		t.Fatal(err)
	}
	p := e.plan(t, e.actor, first)
	if p.LatestGuidance == nil || p.LatestGuidance.State != "READY" || e.model.plantingCalls.Load() != 1 || e.usage(t, e.actor) != 1 {
		t.Fatal("future research failed", p.LatestGuidance)
	}
	other := e.workspace(t)
	second := e.idea(t, other, "Private fig idea two")
	if err := e.planner.SweepGuidance(ctx); err != nil {
		t.Fatal(err)
	}
	p2 := e.plan(t, other, second)
	if p2.LatestGuidance.State != "READY" || p.LatestGuidance.ContextSha256 == p2.LatestGuidance.ContextSha256 || e.model.plantingCalls.Load() != 1 || e.usage(t, other) != 0 {
		t.Fatal("public cache/private context were mixed")
	}
	if e.fetchCalls.Load() != 2 {
		t.Fatal("publication was fetched more than once", e.fetchCalls.Load())
	}
	doc, err := e.s.Q.GetPublication(ctx, plantingFixtureSource().URL)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(doc.SourceText, "Private fig") || strings.Contains(string(doc.PlanningFacts), "private-plan-secret") {
		t.Fatal("private context entered the public cache")
	}
	var plants int
	if err = e.s.Pool.QueryRow(ctx, `SELECT count(*) FROM plants`).Scan(&plants); err != nil || plants != 0 {
		t.Fatal("research created placeholder plants")
	}
	// The original owned-plant adapter still returns its existing seasonal kinds,
	// while reusing the same fetched publication body.
	owned, err := e.s.Q.InsertPlant(ctx, dbgen.InsertPlantParams{ID: platform.UUIDv4{}.New(), WorkspaceID: e.actor.WorkspaceID, Name: "Actual fig", Category: dbgen.PlantCategoryFIG, Type: dbgen.PlantTypeINDIVIDUAL})
	if err != nil {
		t.Fatal(err)
	}
	rows, err := e.s.Q.PendingKnowledge(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		if row.PlantID == owned.ID {
			claim, err := e.s.Q.ClaimKnowledge(ctx, dbgen.ClaimKnowledgeParams{PlantID: row.PlantID, WorkspaceID: row.WorkspaceID, Generation: row.Generation})
			if err != nil {
				t.Fatal(err)
			}
			if err = e.s.Enrich(ctx, claim); err != nil {
				t.Fatal(err)
			}
		}
	}
	garden, err := e.s.Garden(ctx, e.actor.WorkspaceID, 2027)
	if err != nil || len(garden.Plants) != 1 || garden.Plants[0].State != "READY" || len(garden.Plants[0].Facts) != 1 || garden.Plants[0].Facts[0].Kind != "BLOOM" || e.model.ownedCalls.Load() != 1 || e.fetchCalls.Load() != 2 {
		t.Fatal("owned knowledge regressed", garden, err)
	}
}
func TestPlantingProviderFailedAttemptsCountAndQuotaIsWorkspaceScoped(t *testing.T) {
	e := planningDatabase(t)
	ctx := context.Background()
	subject := planning.GuidanceSubject{Name: "Fig", Category: "FIG", Method: "NURSERY_PLANT", Placement: "GROUND"}
	provider := PlanningProvider{Service: e.s}
	e.model.extract = func(context.Context, Source, string) ([]PlantingDraft, error) {
		return nil, errors.New("private-provider-key-secret")
	}
	c, err := provider.Context(ctx, e.actor.WorkspaceID, subject)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 9; i++ {
		r, err := provider.Research(ctx, e.actor.WorkspaceID, subject, c)
		if err != nil || r.State != "FAILED" || strings.Contains(r.Message, "private-provider-key") {
			t.Fatal(r, err)
		}
		if i == 8 && !strings.Contains(r.Message, "hourly") {
			t.Fatal("quota did not bind", r)
		}
	}
	if e.model.plantingCalls.Load() != 8 || e.usage(t, e.actor) != 8 {
		t.Fatal("failed attempts escaped quota", e.model.plantingCalls.Load())
	}
	other := e.workspace(t)
	if n, err := e.s.Q.ReservePlanningProviderRequest(ctx, other.WorkspaceID); err != nil || n != 1 || e.usage(t, other) != 1 {
		t.Fatal("one workspace blocked another", err)
	}
}
func TestPlantingProviderAtomicQuotaConcurrent(t *testing.T) {
	e := planningDatabase(t)
	ctx := context.Background()
	success := atomic.Int32{}
	errorsCh := make(chan error, 20)
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			n, err := e.s.Q.ReservePlanningProviderRequest(ctx, e.actor.WorkspaceID)
			if n == 1 {
				success.Add(1)
			}
			errorsCh <- err
		}()
	}
	wg.Wait()
	close(errorsCh)
	for err := range errorsCh {
		if err != nil {
			t.Fatal(err)
		}
	}
	if success.Load() != 8 || e.usage(t, e.actor) != 8 {
		t.Fatal("quota raced", success.Load())
	}
}
func TestPlantingProviderContextAndApplicabilityStates(t *testing.T) {
	e := planningDatabase(t)
	ctx := context.Background()
	provider := PlanningProvider{Service: e.s}
	subject := planning.GuidanceSubject{Name: "Fig", Category: "FIG", Method: "NURSERY_PLANT", Placement: "GROUND"}
	c, err := provider.Context(ctx, e.actor.WorkspaceID, subject)
	if err != nil || c.State != "PENDING" {
		t.Fatal(c, err)
	}
	noMethod := subject
	noMethod.Method = ""
	mc, err := provider.Context(ctx, e.actor.WorkspaceID, noMethod)
	if err != nil {
		t.Fatal(err)
	}
	result, err := provider.Research(ctx, e.actor.WorkspaceID, noMethod, mc)
	if err != nil || result.State != "UNSUPPORTED" {
		t.Fatal("method-specific advice leaked", result, err)
	}
	unknown := subject
	unknown.ScientificName = "Ficus benjamina"
	uc, err := provider.Context(ctx, e.actor.WorkspaceID, unknown)
	if err != nil || uc.State != "UNSUPPORTED" {
		t.Fatal("wrong fig taxonomy", uc, err)
	}
	if _, err = e.s.Pool.Exec(ctx, `UPDATE garden_knowledge_settings SET enabled=false,revision=revision+1 WHERE workspace_id=$1`, e.actor.WorkspaceID); err != nil {
		t.Fatal(err)
	}
	disabled, err := provider.Context(ctx, e.actor.WorkspaceID, subject)
	if err != nil || disabled.State != "DISABLED" {
		t.Fatal(disabled, err)
	}
	if _, err = provider.Research(ctx, e.actor.WorkspaceID, subject, c); !errors.Is(err, planning.ErrGuidanceContextChanged) {
		t.Fatal("stale context accepted", err)
	}
	if _, err = e.s.Pool.Exec(ctx, `UPDATE garden_knowledge_settings SET enabled=true,country='',region='',revision=revision+1 WHERE workspace_id=$1`, e.actor.WorkspaceID); err != nil {
		t.Fatal(err)
	}
	missing, err := provider.Context(ctx, e.actor.WorkspaceID, subject)
	if err != nil || missing.State != "NEEDS_LOCATION" {
		t.Fatal("timezone supplied a climate", missing, err)
	}
}
func TestPlantingGuidanceRunsAsActualRiverJob(t *testing.T) {
	e := planningDatabase(t)
	ctx := context.Background()
	if err := platform.MigrateRiver(ctx, e.s.Pool); err != nil {
		t.Fatal(err)
	}
	workers := river.NewWorkers()
	river.AddWorker(workers, &planning.GuidanceWorker{Service: e.planner})
	client, err := platform.NewRiverClient(e.s.Pool, workers)
	if err != nil {
		t.Fatal(err)
	}
	id := e.idea(t, e.actor, "Queued fig")
	inserted, err := client.Insert(ctx, planning.GuidanceSweepArgs{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = client.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer func() {
		stop, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := client.Stop(stop); err != nil {
			t.Error(err)
		}
	}()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		var state string
		if err = e.s.Pool.QueryRow(ctx, `SELECT state::text FROM river_job WHERE id=$1`, inserted.Job.ID).Scan(&state); err != nil {
			t.Fatal(err)
		}
		if state == "completed" {
			p := e.plan(t, e.actor, id)
			if p.LatestGuidance.State != "READY" {
				t.Fatal("job completed without guidance")
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("actual queued guidance job did not complete")
}
