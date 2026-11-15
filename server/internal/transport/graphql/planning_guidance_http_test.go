package graphql

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"canopy/server/internal/knowledge"
	"canopy/server/internal/platform"
	httpx "canopy/server/internal/transport/http"
)

type planningHTTPModel struct{ calls atomic.Int32 }

func (m *planningHTTPModel) PlanningIdentity() string { return strings.Repeat("a", 64) }
func (m *planningHTTPModel) ExtractPlanting(_ context.Context, s knowledge.Source, text string) ([]knowledge.PlantingDraft, error) {
	m.calls.Add(1)
	if strings.Contains(text, "private-plan-secret") {
		return nil, errors.New("private data reached extraction")
	}
	return []knowledge.PlantingDraft{{Phase: "PLANT_OUT", Label: "Planting", Summary: "Nursery fig trees have a planting window.", Evidence: "Plant nursery fig trees in December through February.", Timing: "December through February", Methods: []string{"NURSERY_PLANT"}}}, nil
}
func (*planningHTTPModel) Extract(context.Context, knowledge.Source, string) ([]knowledge.DraftFact, error) {
	return nil, errors.New("not used")
}
func (*planningHTTPModel) Answer(context.Context, string) (knowledge.AnswerDraft, error) {
	return knowledge.AnswerDraft{}, errors.New("not used")
}

type planningSourceTransport func(*http.Request) (*http.Response, error)

func (f planningSourceTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func planningHTTPRoot(t *testing.T, e *testEnv) (*Resolver, *planningHTTPModel) {
	t.Helper()
	log := slog.New(slog.DiscardHandler)
	root := NewResolverFromPool(e.pool, log)
	model := &planningHTTPModel{}
	root.Knowledge.Model = model
	root.Knowledge.Sources = []knowledge.Source{{ID: "fixture-fig", Crop: "fig", Country: "US", Regions: []string{"Texas"}, Title: "Synthetic planting source", Publisher: "Fixture", Scope: "Synthetic region; not planting advice.", URL: "https://fixture.example.invalid/guide", Hemisphere: "NORTHERN"}}
	root.Knowledge.Fetch = &knowledge.Fetcher{HTTP: &http.Client{Transport: planningSourceTransport(func(r *http.Request) (*http.Response, error) {
		body := "User-agent: *\nAllow: /"
		if r.URL.Path != "/robots.txt" {
			body = "<article><p>Plant nursery fig trees in December through February.</p><p>" + strings.Repeat("Synthetic fixture prose only. ", 30) + "</p></article>"
		}
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body))}, nil
	})}}
	handler := NewHandler(root, DefaultLimits(), log)
	e.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		principal := httpx.Principal{WorkspaceID: e.wsID, OwnerID: e.ownerID, SessionID: platform.UUIDv4{}.New(), Role: "ADMIN"}
		handler.ServeHTTP(w, r.WithContext(httpx.WithPrincipal(r.Context(), principal)))
	}))
	t.Cleanup(e.srv.Close)
	return root, model
}
func TestPlanningGuidanceHTTPRefreshAndSettingsInvalidation(t *testing.T) {
	e := newEnv(t, DefaultLimits())
	root, model := planningHTTPRoot(t, e)
	ctx := context.Background()
	settings := map[string]any{"country": "US", "region": "Texas", "place": "Fixture garden", "timezone": "America/Chicago", "enabled": true, "expectedRevision": 0}
	r := e.gql(`mutation($input:SaveGardenKnowledgeSettingsInput!){saveGardenKnowledgeSettings(input:$input){settings{revision} problems{code}}}`, map[string]any{"input": settings})
	if ps := payloadProblems(t, r, "saveGardenKnowledgeSettings"); len(ps) > 0 {
		t.Fatal(ps)
	}
	target := map[string]any{"precision": "MONTH_RANGE", "months": map[string]any{"from": "2027-12", "to": "2028-02"}}
	p := plantingPayload(t, plantingOp(t, e, "CreatePlantingPlan", map[string]any{"input": map[string]any{"name": "Private future fig", "category": "FIG", "method": "NURSERY_PLANT", "intendedPlacement": "GROUND", "notes": "private-plan-secret", "targetPeriod": target}}), "createPlantingPlan")
	id := mustID(t, p, "id")
	if err := root.Planning.SweepGuidance(ctx); err != nil {
		t.Fatal(err)
	}
	detail := dataOf(t, plantingOp(t, e, "PlantingPlanDetail", map[string]any{"id": id}), "plantingPlan")
	latest := detail["latestGuidance"].(map[string]any)
	if latest["state"] != "READY" || model.calls.Load() != 1 {
		t.Fatal("research not ready", detail)
	}
	if detail["targetPeriod"].(map[string]any)["months"].(map[string]any)["from"] != "2027-12" || detail["revision"] != float64(1) {
		t.Fatal("research changed user dates/revision")
	}
	facts := latest["facts"].([]any)
	window := facts[0].(map[string]any)["windows"].([]any)[0].(map[string]any)
	if window["annual"].(map[string]any)["months"].(map[string]any)["from"] != float64(12) {
		t.Fatal("annual guidance gained a year", window)
	}
	accepted := latest["id"].(string)
	if _, err := e.pool.Exec(ctx, `UPDATE planting_plans SET accepted_guidance_id=$1 WHERE id=$2`, accepted, id); err != nil {
		t.Fatal(err)
	}
	refreshed := plantingPayload(t, plantingOp(t, e, "RefreshPlantingGuidance", map[string]any{"input": map[string]any{"id": id, "expectedRevision": 1}, "clientMutationId": "refresh"}), "refreshPlantingGuidance")
	if refreshed["revision"] != float64(2) || refreshed["acceptedGuidance"].(map[string]any)["id"] != accepted {
		t.Fatal("refresh changed accepted provenance", refreshed)
	}
	again := plantingPayload(t, plantingOp(t, e, "RefreshPlantingGuidance", map[string]any{"input": map[string]any{"id": id, "expectedRevision": 2}}), "refreshPlantingGuidance")
	if again["revision"] != float64(2) || again["guidanceGeneration"] != float64(2) {
		t.Fatal("pending refresh did not deduplicate")
	}
	bad := plantingOp(t, e, "RefreshPlantingGuidance", map[string]any{"input": map[string]any{"id": id, "expectedRevision": 999}, "clientMutationId": "refresh"})
	wantProblemCode(t, payloadProblems(t, bad, "refreshPlantingGuidance"), "IDEMPOTENCY_CONFLICT")
	bad = plantingOp(t, e, "RefreshPlantingGuidance", map[string]any{"input": map[string]any{"id": uuidString(platform.UUIDv4{}.New()), "expectedRevision": 1}})
	wantProblemCode(t, payloadProblems(t, bad, "refreshPlantingGuidance"), "NOT_FOUND")
	settings["region"] = "California"
	settings["expectedRevision"] = 1
	r = e.gql(`mutation($input:SaveGardenKnowledgeSettingsInput!){saveGardenKnowledgeSettings(input:$input){settings{revision} problems{code}}}`, map[string]any{"input": settings})
	if ps := payloadProblems(t, r, "saveGardenKnowledgeSettings"); len(ps) > 0 {
		t.Fatal(ps)
	}
	detail = dataOf(t, plantingOp(t, e, "PlantingPlanDetail", map[string]any{"id": id}), "plantingPlan")
	if detail["latestGuidance"] != nil || detail["acceptedGuidance"].(map[string]any)["id"] != accepted || detail["targetPeriod"].(map[string]any)["months"].(map[string]any)["to"] != "2028-02" {
		t.Fatal("geography change overwrote choices or used stale current data")
	}
	if err := root.Planning.SweepGuidance(ctx); err != nil {
		t.Fatal(err)
	}
	detail = dataOf(t, plantingOp(t, e, "PlantingPlanDetail", map[string]any{"id": id}), "plantingPlan")
	if detail["latestGuidance"].(map[string]any)["state"] != "UNSUPPORTED" || model.calls.Load() != 1 {
		t.Fatal("wrong region reused Texas extraction as applicable")
	}
}
func TestPlanningGuidanceHTTPDisabledRefreshStillPreservesIdea(t *testing.T) {
	e := newEnv(t, DefaultLimits())
	p := plantingPayload(t, plantingOp(t, e, "CreatePlantingPlan", map[string]any{"input": map[string]any{"name": "Manual future plant"}}), "createPlantingPlan")
	refreshed := plantingPayload(t, plantingOp(t, e, "RefreshPlantingGuidance", map[string]any{"input": map[string]any{"id": p["id"], "expectedRevision": 1}}), "refreshPlantingGuidance")
	if refreshed["latestGuidance"].(map[string]any)["state"] != "DISABLED" || refreshed["targetPeriod"] != nil || refreshed["budget"] != nil {
		t.Fatal("disabled research damaged manual plan")
	}
}
