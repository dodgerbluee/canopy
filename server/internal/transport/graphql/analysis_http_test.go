// T29 acceptance over the real stack: the analysis GraphQL surface
// (providerCapabilities / requestAnalysis / cancelAnalysis / analysisRun)
// against an isolated disposable schema, with honest no-provider mode
// and a scripted provider seam. Suggestion surfaces went live with T33.
package graphql

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	candb "canopy/server/db"
	dbgen "canopy/server/db/generated"
	"canopy/server/internal/identity"
	"canopy/server/internal/insights"
	"canopy/server/internal/platform"
	httpx "canopy/server/internal/transport/http"
)

type analysisEnv struct {
	*testEnv
	svc *insights.Service
}

func newAnalysisEnv(t *testing.T, provider insights.Port) *analysisEnv {
	t.Helper()
	ctx := context.Background()
	dsn := os.Getenv("PGTEST_DSN")
	if dsn == "" {
		t.Skip("PGTEST_DSN not set")
	}
	admin, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	schema := fmt.Sprintf("t29gql_%d", time.Now().UnixNano())
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	up, err := candb.AllUp()
	if err != nil {
		t.Fatalf("migrations: %v", err)
	}
	if _, err := admin.Exec(ctx, "SET search_path TO "+schema); err != nil {
		t.Fatalf("search_path: %v", err)
	}
	if _, err := admin.Exec(ctx, up); err != nil {
		t.Fatalf("apply migration: %v", err)
	}
	admin.Close(ctx)

	pool, err := pgxpool.New(ctx, dsn+"&search_path="+schema)
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	t.Cleanup(func() {
		pool.Close()
		if a, err := pgx.Connect(ctx, dsn); err == nil {
			_, _ = a.Exec(ctx, "DROP SCHEMA IF EXISTS "+schema+" CASCADE")
			a.Close(ctx)
		}
	})
	if err := platform.MigrateRiver(ctx, pool); err != nil {
		t.Fatalf("river migrate: %v", err)
	}

	q := dbgen.New(pool)
	log := slog.New(slog.DiscardHandler)
	cfg := identity.DefaultSessionConfig(false, false)
	sm := identity.NewSessionManager(q, platform.UUIDv4{}, platform.SystemClock{}, cfg)
	hasher := identity.NewPasswordHasher(identity.Argon2Params{Memory: 64, Iterations: 1, Parallelism: 1})
	owners := identity.NewOwnerService(q, sm, hasher, platform.UUIDv4{}, log)
	owner, ws, err := owners.Bootstrap(ctx, identity.BootstrapInput{Password: "t08-passphrase"})
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}

	queue, err := platform.NewInsertOnlyRiverClient(pool)
	if err != nil {
		t.Fatalf("river client: %v", err)
	}
	svc := insights.New(pool, log, insights.Limits{
		DailyRunLimit: 10, ConcurrentRunLimit: 1, MonthCeilingMicroUSD: 0, AttemptCostMicroUSD: 4000,
	}, provider)
	svc.Queue = queue
	svc.PromptVersion = "p1"
	svc.SchemaVersion = "r1"
	svc.AttemptTimeout = 2 * time.Second
	svc.MaxResultBytes = 64 << 10

	mux := http.NewServeMux()
	auth := httpx.NewAuth(owners, sm, identity.NewLoginLimiter(10), cfg, log)
	auth.Register(mux)
	root := NewResolverFromPool(pool, log)
	root.Insights = svc
	mux.Handle("POST /api/graphql", auth.RequirePrincipal(NewHandler(root, DefaultLimits(), log).ServeHTTP))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	e := &testEnv{t: t, srv: srv, pool: pool, q: q, ownerID: owner.ID, wsID: ws.ID}
	e.login()
	return &analysisEnv{testEnv: e, svc: svc}
}

func (e *analysisEnv) seedPlant(t *testing.T) string {
	t.Helper()
	plant, err := e.q.InsertPlant(context.Background(), dbgen.InsertPlantParams{
		ID: platform.UUIDv4{}.New(), WorkspaceID: e.wsID, Name: "Monstera",
		Type: dbgen.PlantTypeINDIVIDUAL, Category: dbgen.PlantCategoryOTHER,
	})
	if err != nil {
		t.Fatalf("plant: %v", err)
	}
	return uuidString(plant.ID)
}

// ---- fake provider seam ------------------------------------------------------

type httpFakePort struct {
	caps map[insights.Capability]insights.CapabilityStatus
}

func (f *httpFakePort) Capabilities(context.Context) map[insights.Capability]insights.CapabilityStatus {
	return f.caps
}

func (f *httpFakePort) Analyze(_ context.Context, in insights.AnalyzeInput) (insights.AnalyzeOutput, error) {
	return insights.AnalyzeOutput{
		Raw: json.RawMessage(`{"visibleObservations":[],"hypotheses":[],"missingInformation":[],` +
			`"followUpChecks":[],"citations":[]}`),
		Provider: "fakeprov", Model: "fake-model",
	}, nil
}

func textAvailablePort() *httpFakePort {
	return &httpFakePort{caps: map[insights.Capability]insights.CapabilityStatus{
		insights.CapTextAnalyzer: {Available: true, Model: "fake-model", ExternalData: true},
	}}
}

// ---- operations (mirror the frozen operations document) -----------------------

const capsQueryGQL = `query ProviderCapabilities {
  providerCapabilities {
    text { available model externalData unavailableReason }
    vision { available model externalData unavailableReason }
    identification { available model externalData unavailableReason }
    receiptExtraction { available model externalData unavailableReason }
    budget {
      dailyRunsUsed dailyRunLimit concurrentRunLimit monthUtc
      monthCeilingMicroUsd monthReservedMicroUsd monthSpentMicroUsd
    }
  }
}`

const runFieldsGQL = `id revision kind state attempts failureCode
  costReservedMicroUsd costActualMicroUsd
  evidence { inputHash missingFacts }
  suggestions { id }`

const requestMutGQL = `mutation($input: RequestAnalysisInput!, $cmid: String) {
  requestAnalysis(input: $input, clientMutationId: $cmid) {
    run { ` + runFieldsGQL + ` }
    problems { code field message currentRevision }
    clientMutationId
  }
}`

const cancelMutGQL = `mutation($input: CancelAnalysisInput!, $cmid: String) {
  cancelAnalysis(input: $input, clientMutationId: $cmid) {
    run { ` + runFieldsGQL + ` }
    problems { code field message currentRevision }
    clientMutationId
  }
}`

const statusQueryGQL = `query AnalysisRunStatus($runId: ID!) {
  analysisRun(id: $runId) { ` + runFieldsGQL + ` }
}`

func problemsOf(t *testing.T, payload map[string]any) []map[string]any {
	t.Helper()
	raw, ok := payload["problems"].([]any)
	if !ok {
		t.Fatalf("no problems array: %v", payload)
	}
	var out []map[string]any
	for _, p := range raw {
		out = append(out, p.(map[string]any))
	}
	return out
}

// ---- no-provider honesty -------------------------------------------------------

func TestProviderCapabilitiesNoProvider(t *testing.T) {
	e := newAnalysisEnv(t, insights.NoProvider{})
	data := dataOf(t, e.gql(capsQueryGQL, nil), "providerCapabilities")
	for _, name := range []string{"text", "vision", "identification", "receiptExtraction"} {
		cap := data[name].(map[string]any)
		if cap["available"] != false {
			t.Fatalf("%s must be unavailable: %v", name, cap)
		}
		if cap["model"] != nil {
			t.Fatalf("%s must not name a model: %v", name, cap["model"])
		}
		if reason, ok := cap["unavailableReason"].(string); !ok || reason == "" {
			t.Fatalf("%s must give an owner-language reason: %v", name, cap["unavailableReason"])
		}
	}
	b := data["budget"].(map[string]any)
	if b["dailyRunLimit"] != float64(10) || b["concurrentRunLimit"] != float64(1) {
		t.Fatalf("budget limits: %v", b)
	}
	if b["monthCeilingMicroUsd"] != nil {
		t.Fatalf("ceiling must be null when unset: %v", b["monthCeilingMicroUsd"])
	}
}

func TestRequestAnalysisNoProvider(t *testing.T) {
	e := newAnalysisEnv(t, insights.NoProvider{})
	plantID := e.seedPlant(t)
	r := e.gql(requestMutGQL, map[string]any{"input": map[string]any{
		"kind": "TEXT", "plantId": plantID,
	}})
	payload := dataOf(t, r, "requestAnalysis")
	if payload["run"] != nil {
		t.Fatalf("no-provider mode must not return a run: %v", payload["run"])
	}
	ps := problemsOf(t, payload)
	if len(ps) != 1 || ps[0]["code"] != "CAPABILITY_UNAVAILABLE" {
		t.Fatalf("problems: %v", ps)
	}
}

func TestAnalysisRunUnknownIdIsNull(t *testing.T) {
	e := newAnalysisEnv(t, insights.NoProvider{})
	r := e.gql(statusQueryGQL, map[string]any{"runId": "11111111-1111-1111-1111-111111111111"})
	if r.Status != http.StatusOK || len(r.Errors) != 0 {
		t.Fatalf("unknown run must be a clean null: %s", r.Raw)
	}
	if r.Data["analysisRun"] != nil {
		t.Fatalf("unknown run must be null, not an error: %v", r.Data["analysisRun"])
	}
}

// ---- suggestion surfaces go live with T33 -------------------------------------

func TestSuggestionSurfacesLive(t *testing.T) {
	e := newAnalysisEnv(t, textAvailablePort())
	plantID := e.seedPlant(t)

	// The list resolves as an empty connection before any advice exists.
	r := e.gql(`query { suggestions(first: 5) { items { id } pageInfo { hasNextPage } } }`, nil)
	if len(r.Errors) != 0 {
		t.Fatalf("suggestions must resolve after T33: %s", r.Raw)
	}
	conn := r.Data["suggestions"].(map[string]any)
	if items := conn["items"].([]any); len(items) != 0 {
		t.Fatalf("expected empty connection, got %d", len(items))
	}

	// A seeded PROPOSED row maps through the frozen Suggestion shape.
	sugg, err := e.q.InsertSuggestion(context.Background(), dbgen.InsertSuggestionParams{
		ID: platform.UUIDv4{}.New(), WorkspaceID: e.wsID,
		PlantID: mustUUID(plantID), Kind: dbgen.SuggestionKindCARE,
		Severity: dbgen.SuggestionSeverityMEDIUM, Action: "Check the saucer for standing water.",
		Observation:  pgtype.Text{String: "Leaf edges are pale.", Valid: true},
		Hypothesis:   pgtype.Text{String: "Watering may be inconsistent.", Valid: true},
		EvidenceIds:  []byte(`["11111111-1111-1111-1111-111111111111"]`),
		MissingFacts: []byte(`["soil moisture reading"]`),
		ExpiresAt:    pgtype.Timestamptz{Time: time.Now().Add(24 * time.Hour), Valid: true},
	})
	if err != nil {
		t.Fatalf("seed suggestion: %v", err)
	}
	r = e.gql(`query($plantId: ID!) {
		suggestions(filter: { plantId: $plantId }, first: 5) {
			items { id revision kind severity status action observation hypothesis
				evidenceIds missingFacts needsReview expiresAt }
			pageInfo { hasNextPage }
		} }`, map[string]any{"plantId": plantID})
	if len(r.Errors) != 0 {
		t.Fatalf("suggestions query: %s", r.Raw)
	}
	items := r.Data["suggestions"].(map[string]any)["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("items: %d", len(items))
	}
	got := items[0].(map[string]any)
	if got["id"] != uuidString(sugg.ID) || got["revision"] != float64(1) ||
		got["kind"] != "CARE" || got["severity"] != "MEDIUM" || got["status"] != "PROPOSED" ||
		got["action"] != "Check the saucer for standing water." ||
		got["needsReview"] != false {
		t.Fatalf("mapping: %v", got)
	}
	if ev := got["evidenceIds"].([]any); len(ev) != 1 || ev[0] != "11111111-1111-1111-1111-111111111111" {
		t.Fatalf("evidenceIds: %v", got["evidenceIds"])
	}
	if mf := got["missingFacts"].([]any); len(mf) != 1 || mf[0] != "soil moisture reading" {
		t.Fatalf("missingFacts: %v", got["missingFacts"])
	}

	// Mutations reach the service: unknown ids answer NOT_FOUND as problems,
	// not transport errors.
	r = e.gql(`mutation($input: AcceptSuggestionInput!) {
		acceptSuggestion(input: $input) { problems { code message } } }`,
		map[string]any{"input": map[string]any{"suggestionId": "11111111-1111-1111-1111-111111111111",
			"expectedRevision": 1}})
	if len(r.Errors) != 0 {
		t.Fatalf("acceptSuggestion: %s", r.Raw)
	}
	if p := problemsOf(t, dataOf(t, r, "acceptSuggestion")); len(p) != 1 || p[0]["code"] != "NOT_FOUND" {
		t.Fatalf("accept problems: %v", p)
	}
	r = e.gql(`mutation($input: DismissSuggestionInput!) {
		dismissSuggestion(input: $input) { problems { code } } }`,
		map[string]any{"input": map[string]any{"suggestionId": "11111111-1111-1111-1111-111111111111",
			"expectedRevision": 1}})
	if len(r.Errors) != 0 {
		t.Fatalf("dismissSuggestion: %s", r.Raw)
	}
	if p := problemsOf(t, dataOf(t, r, "dismissSuggestion")); len(p) != 1 || p[0]["code"] != "NOT_FOUND" {
		t.Fatalf("dismiss problems: %v", p)
	}
}

// ---- request -> poll -> cancel over HTTP ------------------------------------------

func TestRequestPollCancelFlowOverHTTP(t *testing.T) {
	e := newAnalysisEnv(t, textAvailablePort())
	plantID := e.seedPlant(t)

	r := e.gql(requestMutGQL, map[string]any{"input": map[string]any{"kind": "TEXT", "plantId": plantID},
		"cmid": "cm-http-1"})
	payload := dataOf(t, r, "requestAnalysis")
	run := payload["run"].(map[string]any)
	runID := run["id"].(string)
	if run["state"] != "QUEUED" || run["kind"] != "TEXT" {
		t.Fatalf("run: %v", run)
	}
	if run["costReservedMicroUsd"] != "4000" || run["costActualMicroUsd"] != nil {
		t.Fatalf("costs: %v", run)
	}
	ev := run["evidence"].(map[string]any)
	if h, ok := ev["inputHash"].(string); !ok || len(h) < 8 || h[:7] != "sha256:" {
		t.Fatalf("evidence.inputHash: %v", ev["inputHash"])
	}
	if facts, ok := ev["missingFacts"].([]any); !ok || len(facts) == 0 {
		t.Fatalf("empty workspace must report honest missing facts: %v", ev)
	}
	if payload["clientMutationId"] != "cm-http-1" {
		t.Fatalf("cmid echo: %v", payload["clientMutationId"])
	}

	// same input again: honest conflict carrying the existing run
	r = e.gql(requestMutGQL, map[string]any{"input": map[string]any{"kind": "TEXT", "plantId": plantID}})
	payload = dataOf(t, r, "requestAnalysis")
	ps := problemsOf(t, payload)
	if len(ps) != 1 || ps[0]["code"] != "IDEMPOTENCY_CONFLICT" {
		t.Fatalf("dup problems: %v", ps)
	}
	if dup := payload["run"].(map[string]any); dup["id"] != runID {
		t.Fatalf("conflict must carry the existing run: %v", dup["id"])
	}

	// polling read
	r = e.gql(statusQueryGQL, map[string]any{"runId": runID})
	got := dataOf(t, r, "analysisRun")
	if got["state"] != "QUEUED" || got["revision"] != run["revision"] {
		t.Fatalf("poll: %v", got)
	}

	// cancel before dispatch: refund recorded as actual 0
	rev := int32(run["revision"].(float64))
	r = e.gql(cancelMutGQL, map[string]any{"input": map[string]any{
		"runId": runID, "expectedRevision": rev,
	}})
	payload = dataOf(t, r, "cancelAnalysis")
	canceled := payload["run"].(map[string]any)
	if canceled["state"] != "CANCELLED" || canceled["costActualMicroUsd"] != "0" {
		t.Fatalf("cancel: %v", canceled)
	}

	// stale expectedRevision on the terminal row: CONFLICT + currentRevision
	r = e.gql(cancelMutGQL, map[string]any{"input": map[string]any{
		"runId": runID, "expectedRevision": rev,
	}})
	payload = dataOf(t, r, "cancelAnalysis")
	ps = problemsOf(t, payload)
	if len(ps) != 1 || ps[0]["code"] != "CONFLICT" || ps[0]["currentRevision"] == nil {
		t.Fatalf("re-cancel problems: %v", ps)
	}

	// the queued job must never resurrect the run: run the worker inline
	ctx := context.Background()
	var jobID int64
	if err := e.pool.QueryRow(ctx,
		`SELECT id FROM river_job WHERE kind = 'analysis:run' LIMIT 1`).Scan(&jobID); err != nil {
		t.Fatalf("job: %v", err)
	}
	if err := e.svc.ProcessRun(ctx, &river.Job[insights.RunArgs]{Args: insights.RunArgs{RunID: runID}}); err != nil {
		t.Fatalf("process: %v", err)
	}
	r = e.gql(statusQueryGQL, map[string]any{"runId": runID})
	got = dataOf(t, r, "analysisRun")
	if got["state"] != "CANCELLED" {
		t.Fatalf("cancelled run resurrected by worker: %v", got["state"])
	}
}

// ---- budget view reflects reservations --------------------------------------------

func TestCapabilitiesBudgetViewOverHTTP(t *testing.T) {
	e := newAnalysisEnv(t, textAvailablePort())
	plantID := e.seedPlant(t)
	r := e.gql(requestMutGQL, map[string]any{"input": map[string]any{"kind": "TEXT", "plantId": plantID}})
	if payload := dataOf(t, r, "requestAnalysis"); payload["run"] == nil {
		t.Fatalf("request failed: %v", payload["problems"])
	}
	data := dataOf(t, e.gql(capsQueryGQL, nil), "providerCapabilities")
	b := data["budget"].(map[string]any)
	if b["dailyRunsUsed"] != float64(1) {
		t.Fatalf("dailyRunsUsed: %v", b["dailyRunsUsed"])
	}
	if b["monthReservedMicroUsd"] != "4000" || b["monthSpentMicroUsd"] != "0" {
		t.Fatalf("ledger: %v", b)
	}
	if tz, ok := b["monthUtc"].(string); !ok || len(tz) < 7 {
		t.Fatalf("monthUtc: %v", b["monthUtc"])
	}
	caps := data["text"].(map[string]any)
	if caps["available"] != true || caps["model"] != "fake-model" || caps["externalData"] != true {
		t.Fatalf("text caps: %v", caps)
	}
	if data["vision"].(map[string]any)["available"] != false {
		t.Fatal("vision must stay unavailable with this port")
	}
}
