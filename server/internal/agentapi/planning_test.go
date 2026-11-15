package agentapi_test

import (
	"canopy/server/internal/agentaccess"
	"canopy/server/internal/agentapi"
	"canopy/server/internal/agentapi/catalog"
	"canopy/server/internal/collection"
	mcptransport "canopy/server/internal/transport/mcp"
	"context"
	"encoding/json"
	"github.com/google/uuid"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func plannerGrant(t *testing.T, m *agentaccess.Manager, ws string, scopes ...string) agentaccess.Principal {
	t.Helper()
	issued, err := m.Save(context.Background(), agentaccess.ID(ws), agentaccess.SaveInput{ID: uuid.NewString(), Name: "Planner fixture", Scopes: scopes, ExpiresAt: time.Now().Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	p, err := m.Verify(context.Background(), issued.Token)
	if err != nil {
		t.Fatal(err)
	}
	return p
}
func approvePlanner(t *testing.T, api *agentapi.API, m *agentaccess.Manager, p agentaccess.Principal, tool, op string, input map[string]any) agentapi.Result {
	t.Helper()
	args := map[string]any{"operation_id": op, "input": input}
	proposal := api.Call(context.Background(), p, tool, args)
	if proposal.Status != "proposed" || len(proposal.Problems) > 0 {
		t.Fatal(proposal)
	}
	actions, err := m.Actions(context.Background(), agentaccess.ID(p.Grant.WorkspaceID), p.Grant.ID)
	if err != nil {
		t.Fatal(err)
	}
	var id string
	for _, a := range actions {
		if a.OperationID == op {
			id = a.ID
		}
	}
	if id == "" {
		t.Fatal("missing proposal")
	}
	if _, err = m.Decide(context.Background(), agentaccess.ID(p.Grant.WorkspaceID), id, true); err != nil {
		t.Fatal(err)
	}
	applied := api.Call(context.Background(), p, tool, args)
	if applied.Status != "applied" {
		t.Fatal(applied)
	}
	return applied
}
func TestPlannerMCPReviewScopePrecisionAndLinkedPlantPrivacy(t *testing.T) {
	api, m, ws, plant := fixture(t)
	ctx := context.Background()
	p := plannerGrant(t, m, ws, "planning:read", "planning:write", "plants:write")
	created := approvePlanner(t, api, m, p, "planting_plan_create", "one-plan", map[string]any{"name": "Future patch", "category": "OTHER", "target_period": map[string]any{"precision": "SEASON", "season": map[string]any{"season": "WINTER", "year": 2027}}, "budget": map[string]any{"minor_units": "15000", "currency": "USD"}})
	data := created.Data.(map[string]any)
	plan := data["plan"].(map[string]any)
	id := plan["id"].(string)
	read := api.Call(ctx, p, "planting_plan_get", map[string]any{"id": id})
	if len(read.Problems) > 0 {
		t.Fatal(read)
	}
	raw, _ := json.Marshal(read.Data)
	if !strings.Contains(string(raw), `"precision":"SEASON"`) || !strings.Contains(string(raw), `"year":2027`) || strings.Contains(string(raw), `"from":"2027-01-01"`) {
		t.Fatal(string(raw))
	}
	noPlants := plannerGrant(t, m, ws, "planning:read", "planning:write")
	if result := api.Call(ctx, noPlants, "planting_add_to_garden", map[string]any{"operation_id": "denied", "input": map[string]any{"plan_id": id, "expected_revision": 1, "existing_plant_id": plant}}); len(result.Problems) == 0 || result.Problems[0].Code != "FORBIDDEN" {
		t.Fatal(result)
	}
	// Linking needs collection-write, while reading the plan need not reveal actual plant details.
	linked := approvePlanner(t, api, m, p, "planting_add_to_garden", "link-existing", map[string]any{"plan_id": id, "expected_revision": 1, "existing_plant_id": plant})
	raw, _ = json.Marshal(linked.Data)
	if strings.Contains(string(raw), "Allowed lemon") || strings.Contains(string(raw), "acquired_on") {
		t.Fatal("actual detail leaked", string(raw))
	}
	var links int
	if err := m.Pool.QueryRow(ctx, "SELECT count(*) FROM planting_fulfillments WHERE plan_id=$1", id).Scan(&links); err != nil || links != 1 {
		t.Fatal(links, err)
	}
	read = api.Call(ctx, p, "planting_plan_get", map[string]any{"id": id})
	raw, _ = json.Marshal(read.Data)
	if strings.Contains(string(raw), "Allowed lemon") {
		t.Fatal("read leaked actual name")
	}
	// A real price requires finance authority in addition to planting/collection authority.
	paid := api.Call(ctx, p, "planting_add_to_garden", map[string]any{"operation_id": "no-purchase-authority", "input": map[string]any{"plan_id": id, "expected_revision": 2, "new_plant": map[string]any{"name": "Paid tree", "type": "INDIVIDUAL", "category": "OTHER", "initial_purchase": map[string]any{"currency": "USD", "total_minor": "100"}}}})
	if len(paid.Problems) == 0 {
		t.Fatal("purchase permission bypass")
	}
	for _, tool := range catalog.Tools() {
		if strings.HasPrefix(tool.Scope, "planning:") {
			restricted := p
			restricted.Grant.PlantIDs = []string{plant}
			if agentapi.Allowed(restricted, tool) {
				t.Fatal("restricted planning discovery", tool.Name)
			}
		}
	}
	if _, err := m.Save(ctx, agentaccess.ID(ws), agentaccess.SaveInput{ID: uuid.NewString(), Name: "Restricted planner", Scopes: []string{"planning:read"}, PlantIDs: []string{plant}, ExpiresAt: time.Now().Add(time.Hour)}); err == nil {
		t.Fatal("restricted grant accepted")
	}
	if _, err := m.ChangeCredential(ctx, agentaccess.ID(ws), agentaccess.ID(p.Grant.ID), 1, true); err != nil {
		t.Fatal(err)
	}
	if result := api.Call(ctx, p, "planting_plan_get", map[string]any{"id": id}); len(result.Problems) == 0 {
		t.Fatal("revoked planning access")
	}
}

func TestPlannerActualMCPDiscoveryAndBoundedReads(t *testing.T) {
	api, m, ws, _ := fixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	issued, err := m.Save(ctx, agentaccess.ID(ws), agentaccess.SaveInput{ID: uuid.NewString(), Name: "Planning SDK", Scopes: []string{"planning:read", "planning:write", "plants:write"}, ExpiresAt: time.Now().Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	handler := mcptransport.New(m, api, nil)
	server := httptest.NewServer(handler)
	defer server.Close()
	u, _ := url.Parse(server.URL)
	handler.Hosts[u.Host] = true
	client := sdk.NewClient(&sdk.Implementation{Name: "Planning SDK", Version: "1"}, nil)
	session, err := client.Connect(ctx, &sdk.StreamableClientTransport{Endpoint: server.URL, HTTPClient: &http.Client{Transport: bearer{issued.Token}}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	tools, err := session.ListTools(ctx, &sdk.ListToolsParams{})
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, tool := range tools.Tools {
		seen[tool.Name] = true
	}
	if !seen["planting_plans_list"] || !seen["planting_add_to_garden"] || seen["plants_list"] || seen["purchase_get"] {
		t.Fatal("planner SDK discovery", seen)
	}
	for _, call := range []struct {
		name string
		args map[string]any
	}{{"planting_plans_list", map[string]any{}}, {"planting_board", map[string]any{"input": map[string]any{"from_month": "2027-01", "to_month": "2027-12"}}}, {"planting_calendar_events", map[string]any{"from": "2027-01-01", "to": "2027-01-31"}}} {
		r, err := session.CallTool(ctx, &sdk.CallToolParams{Name: call.name, Arguments: call.args})
		if err != nil || r.IsError {
			t.Fatal(call.name, err, r)
		}
	}
	if _, err = m.ChangeCredential(ctx, agentaccess.ID(ws), agentaccess.ID(issued.Grant.ID), 1, true); err != nil {
		t.Fatal(err)
	}
	r, err := session.CallTool(ctx, &sdk.CallToolParams{Name: "planting_plans_list", Arguments: map[string]any{}})
	if err == nil && !r.IsError {
		t.Fatal("revoked SDK connection remained active")
	}
}

func TestPlannerMCPPurgeRedactsCachedResponseAndFencesLateCacheWrites(t *testing.T) {
	api, m, ws, _ := fixture(t)
	ctx := context.Background()
	p := plannerGrant(t, m, ws, "planning:read", "planning:write", "plants:write", "garden:read")
	created := approvePlanner(t, api, m, p, "planting_plan_create", "privacy-plan", map[string]any{"name": "Independent future intent"})
	plan := created.Data.(map[string]any)["plan"].(map[string]any)
	id := plan["id"].(string)
	input := map[string]any{"plan_id": id, "expected_revision": 1, "new_plant": map[string]any{"name": "Erase this actual plant name", "type": "INDIVIDUAL", "category": "OTHER"}}
	linked := approvePlanner(t, api, m, p, "planting_add_to_garden", "privacy-promotion", input)
	actual := linked.Data.(map[string]any)["plan"].(map[string]any)["fulfillment"].(map[string]any)["plant"].(map[string]any)
	plantID := actual["id"].(string)
	// A pending proposal containing the actual id must also lose its copied input.
	pending := api.Call(ctx, p, "planting_add_to_garden", map[string]any{"operation_id": "pending-related", "input": map[string]any{"plan_id": id, "expected_revision": 2, "existing_plant_id": plantID}})
	if pending.Status != "proposed" {
		t.Fatal(pending)
	}
	c := collection.New(m.Pool, nil, nil)
	_, ps, err := c.PurgePlant(ctx, collection.Actor{WorkspaceID: agentaccess.ID(ws), OwnerID: agentaccess.ID(p.OwnerID)}, plantID, 1, "PERMANENTLY_DELETE_PLANT", nil)
	if err != nil || len(ps) > 0 {
		t.Fatal(ps, err)
	}
	replay := api.Call(ctx, p, "planting_add_to_garden", map[string]any{"operation_id": "privacy-promotion", "input": input})
	if len(replay.Problems) == 0 || replay.Problems[0].Code != "RESOURCE_GONE" {
		t.Fatal("purged replay", replay)
	}
	var retained string
	if err = m.Pool.QueryRow(ctx, `SELECT coalesce(string_agg(input::text||resource::text||response::text,''),'') FROM agent_operations WHERE workspace_id=$1`, ws).Scan(&retained); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(retained, "Erase this actual plant name") || strings.Contains(retained, plantID) {
		t.Fatal("retained actual details in operation")
	}
	if err = m.Pool.QueryRow(ctx, `SELECT coalesce(string_agg(input::text||before_snapshot::text,''),'') FROM agent_actions WHERE workspace_id=$1`, ws).Scan(&retained); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(retained, "Erase this actual plant name") || strings.Contains(retained, plantID) {
		t.Fatal("retained actual details in review")
	}
	var state string
	if err = m.Pool.QueryRow(ctx, `SELECT state FROM agent_actions WHERE operation_id='pending-related'`).Scan(&state); err != nil || state != "REVOKED" {
		t.Fatal(state, err)
	}
	tag, err := m.Pool.Exec(ctx, `UPDATE agent_operations SET response=$3 WHERE grant_id=$1 AND operation_id=$2 AND state='APPLIED' AND resource->>'gone' IS DISTINCT FROM 'true'`, p.Grant.ID, "privacy-promotion", []byte(`{"stale":"private response"}`))
	if err != nil || tag.RowsAffected() != 0 {
		t.Fatal("late response recreated erased data", err)
	}
}
