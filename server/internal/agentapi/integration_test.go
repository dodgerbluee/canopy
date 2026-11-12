package agentapi_test

import (
	candb "canopy/server/db"
	"canopy/server/internal/agentaccess"
	"canopy/server/internal/agentapi"
	"canopy/server/internal/collection"
	"canopy/server/internal/media"
	"canopy/server/internal/platform"
	platformmedia "canopy/server/internal/platform/media"
	"canopy/server/internal/portability"
	gqlx "canopy/server/internal/transport/graphql"
	mcptransport "canopy/server/internal/transport/mcp"
	"context"
	"encoding/json"
	"fmt"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"
)

type bearer struct{ token string }

func (b bearer) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header = r.Header.Clone()
	r.Header.Set("Authorization", "Bearer "+b.token)
	return http.DefaultTransport.RoundTrip(r)
}
func fixture(t *testing.T) (*agentapi.API, *agentaccess.Manager, string, string) {
	a, m, w, p, _ := fixtureWithStorage(t)
	return a, m, w, p
}
func fixtureWithStorage(t *testing.T) (*agentapi.API, *agentaccess.Manager, string, string, string) {
	t.Helper()
	dsn := os.Getenv("PGTEST_DSN")
	if dsn == "" {
		t.Skip("PGTEST_DSN required")
	}
	ctx := context.Background()
	admin, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	schema := fmt.Sprintf("mcp_%d", time.Now().UnixNano())
	_, err = admin.Exec(ctx, "CREATE SCHEMA "+schema)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = admin.Exec(ctx, "SET search_path TO "+schema)
	up, _ := candb.AllUp()
	if _, err = admin.Exec(ctx, up); err != nil {
		t.Fatal(err)
	}
	pool, err := pgxpool.New(ctx, dsn+"&search_path="+schema)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pool.Close(); _, _ = admin.Exec(ctx, "DROP SCHEMA "+schema+" CASCADE"); admin.Close(ctx) })
	owner, ws := uuid.NewString(), uuid.NewString()
	_, _ = pool.Exec(ctx, `INSERT INTO owner(id,password_hash) VALUES($1,'fixture')`, owner)
	_, err = pool.Exec(ctx, `INSERT INTO workspace(id,owner_id,display_name,timezone,default_currency) VALUES($1,$2,'Fixture garden','America/Chicago','USD')`, ws, owner)
	if err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.DiscardHandler)
	root := gqlx.NewResolverFromPool(pool, log)
	storageRoot := t.TempDir()
	store, err := platformmedia.NewFSStore(storageRoot)
	if err != nil {
		t.Fatal(err)
	}
	root.Media = media.New(pool, log, store, nil, 0)
	if err = platform.MigrateRiver(ctx, pool); err != nil {
		t.Fatal(err)
	}
	queue, err := platform.NewInsertOnlyRiverClient(pool)
	if err != nil {
		t.Fatal(err)
	}
	root.Media.Queue = queue
	root.Port = portability.New(pool, log, storageRoot, queue)
	m := agentaccess.New(pool)
	api, err := agentapi.New(m, gqlx.NewAgentExecutor(root), store)
	if err != nil {
		t.Fatal(err)
	}
	actor := collection.Actor{WorkspaceID: agentaccess.ID(ws), OwnerID: agentaccess.ID(owner)}
	one, ps, err := root.Coll.CreatePlant(ctx, actor, collection.CreatePlantInput{Name: "Allowed lemon", Type: "INDIVIDUAL", Category: "CITRUS"}, nil)
	if err != nil || len(ps) > 0 {
		t.Fatal(err, ps)
	}
	_, ps, err = root.Coll.CreatePlant(ctx, actor, collection.CreatePlantInput{Name: "Hidden fig", Type: "INDIVIDUAL", Category: "FIG"}, nil)
	if err != nil || len(ps) > 0 {
		t.Fatal(err, ps)
	}
	return api, m, ws, one.ID, storageRoot
}
func TestActualMCPScopedDiscoveryAndGardenReads(t *testing.T) {
	api, m, ws, plant := fixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	issued, err := m.Save(ctx, agentaccess.ID(ws), agentaccess.SaveInput{ID: uuid.NewString(), Name: "SDK fixture", Scopes: []string{"garden:read", "knowledge:read", "photos:read", "care:read"}, PlantIDs: []string{plant}, ExpiresAt: time.Now().Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	handler := mcptransport.New(m, api, nil)
	server := httptest.NewServer(handler)
	defer server.Close()
	u, _ := url.Parse(server.URL)
	handler.Hosts[u.Host] = true
	client := sdk.NewClient(&sdk.Implementation{Name: "SDK fixture", Version: "1"}, nil)
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
	if !seen["plants_list"] || seen["purchase_get"] || seen["log_watered"] || seen["execute_graphql"] {
		t.Fatal("scope-aware discovery incorrect", seen)
	}
	for _, call := range []struct {
		name string
		args map[string]any
	}{{"plants_list", map[string]any{}}, {"plant_get", map[string]any{"id": plant}}, {"garden_overview", map[string]any{}}, {"garden_year", map[string]any{"year": 2026}}, {"care_agenda", map[string]any{}}, {"photos_list", map[string]any{"plant_id": plant}}} {
		t.Run(call.name, func(t *testing.T) {
			result, err := session.CallTool(ctx, &sdk.CallToolParams{Name: call.name, Arguments: call.args})
			if err != nil {
				t.Fatal(err)
			}
			raw, _ := json.Marshal(result.StructuredContent)
			if result.IsError {
				t.Fatal(string(raw))
			}
			if strings.Contains(string(raw), "Hidden fig") || strings.Contains(string(raw), "storage_key") {
				t.Fatal("restricted data escaped")
			}
		})
	}
	if _, err = m.ChangeCredential(ctx, agentaccess.ID(ws), agentaccess.ID(issued.Grant.ID), 1, true); err != nil {
		t.Fatal(err)
	}
	if _, err = session.ListTools(ctx, &sdk.ListToolsParams{}); err == nil {
		t.Fatal("revoked HTTP credential accepted")
	}
}

func TestTrustedLoggingRetriesAndAudit(t *testing.T) {
	api, m, ws, plant := fixture(t)
	ctx := context.Background()
	issued, err := m.Save(ctx, agentaccess.ID(ws), agentaccess.SaveInput{ID: uuid.NewString(), Name: "Logging fixture", Scopes: []string{"garden:read", "logs:write", "rain:write"}, PlantIDs: []string{plant}, ExpiresAt: time.Now().Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	p, err := m.Verify(ctx, issued.Token)
	if err != nil {
		t.Fatal(err)
	}
	input := map[string]any{"operation_id": "watering-one", "input": map[string]any{"plant_id": plant, "occurred_at": "2026-11-06T20:00:00Z", "note": "Explicitly watered"}}
	first := api.Call(ctx, p, "log_watered", input)
	if len(first.Problems) > 0 {
		t.Fatal(first)
	}
	again := api.Call(ctx, p, "log_watered", input)
	if len(again.Problems) > 0 || again.Status != "applied" {
		t.Fatal(again)
	}
	var count int
	if err = m.Pool.QueryRow(ctx, `SELECT count(*) FROM activities WHERE workspace_id=$1`, ws).Scan(&count); err != nil || count != 1 {
		t.Fatal("duplicate activity", count, err)
	}
	var grant, request, op string
	if err = m.Pool.QueryRow(ctx, `SELECT agent_grant_id::text,request_id,agent_operation_id FROM audit_revisions WHERE agent_grant_id=$1`, issued.Grant.ID).Scan(&grant, &request, &op); err != nil || grant != issued.Grant.ID || op != "watering-one" || request == "" {
		t.Fatal("missing agent attribution", err)
	}
	input["input"].(map[string]any)["note"] = "Changed input"
	conflict := api.Call(ctx, p, "log_watered", input)
	if len(conflict.Problems) == 0 || conflict.Problems[0].Code != "IDEMPOTENCY_CONFLICT" {
		t.Fatal("identity conflict", conflict)
	}
	rain := api.Call(ctx, p, "record_rain", map[string]any{"operation_id": "rain-one", "input": map[string]any{"day": "2026-11-06", "amount_mm": "8"}})
	if len(rain.Problems) > 0 {
		t.Fatal(rain)
	}
	if err = m.Pool.QueryRow(ctx, `SELECT count(*) FROM activities WHERE workspace_id=$1`, ws).Scan(&count); err != nil || count != 1 {
		t.Fatal("rain created watering", count, err)
	}
	// fixture() creates and drops a unique mcp_* schema. Explicit qualification
	// plus restoration prevents this test from touching public.system_state.
	var namespace string
	var prior bool
	if err = m.Pool.QueryRow(ctx, `SELECT current_schema()`).Scan(&namespace); err != nil || !strings.HasPrefix(namespace, "mcp_") {
		t.Fatal("not a disposable MCP schema", err)
	}
	table := pgx.Identifier{namespace, "system_state"}.Sanitize()
	if err = m.Pool.QueryRow(ctx, "SELECT maintenance FROM "+table+" WHERE id=true").Scan(&prior); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = m.Pool.Exec(context.Background(), "UPDATE "+table+" SET maintenance=$1 WHERE id=true", prior)
	}()
	if _, err = m.Pool.Exec(ctx, "UPDATE "+table+" SET maintenance=$1 WHERE id=true", true); err != nil {
		t.Fatal(err)
	}
	blocked := api.Call(ctx, p, "log_watered", map[string]any{"operation_id": "during-backup", "input": map[string]any{"plant_id": plant, "occurred_at": "2026-11-06T21:00:00Z"}})
	if len(blocked.Problems) == 0 {
		t.Fatal("maintenance bypass")
	}
}

func TestReviewedChangeAtomicityAndRevocation(t *testing.T) {
	api, m, ws, plant := fixture(t)
	ctx := context.Background()
	issued, err := m.Save(ctx, agentaccess.ID(ws), agentaccess.SaveInput{ID: uuid.NewString(), Name: "Reviewer", Scopes: []string{"garden:read", "plants:write"}, ExpiresAt: time.Now().Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	p, _ := m.Verify(ctx, issued.Token)
	request := map[string]any{"operation_id": "rename-one", "input": map[string]any{"id": plant, "expected_revision": 1, "name": "Reviewed lemon"}}
	proposal := api.Call(ctx, p, "plant_update", request)
	if proposal.Status != "proposed" || len(proposal.Problems) > 0 {
		t.Fatal(proposal)
	}
	var name string
	_ = m.Pool.QueryRow(ctx, `SELECT name FROM plants WHERE id=$1`, plant).Scan(&name)
	if name != "Allowed lemon" {
		t.Fatal("proposal applied itself")
	}
	actions, err := m.Actions(ctx, agentaccess.ID(ws), p.Grant.ID)
	if err != nil || len(actions) != 1 {
		t.Fatal(err, actions)
	}
	if !strings.Contains(string(actions[0].Before), "Allowed lemon") {
		t.Fatal("missing review snapshot")
	}
	if _, err = m.Decide(ctx, agentaccess.ID(ws), actions[0].ID, true); err != nil {
		t.Fatal(err)
	}
	_ = m.Pool.QueryRow(ctx, `SELECT name FROM plants WHERE id=$1`, plant).Scan(&name)
	if name != "Reviewed lemon" {
		t.Fatal("approval not applied")
	}
	var state string
	_ = m.Pool.QueryRow(ctx, `SELECT state FROM agent_actions WHERE id=$1`, actions[0].ID).Scan(&state)
	if state != "APPLIED" {
		t.Fatal("action state not atomic")
	}
	again := api.Call(ctx, p, "plant_update", request)
	if again.Status != "applied" {
		t.Fatal("approved replay", again)
	}
	stale := api.Call(ctx, p, "plant_update", map[string]any{"operation_id": "stale-change", "input": map[string]any{"id": plant, "expected_revision": 1, "name": "Stale overwrite"}})
	if stale.Status != "proposed" {
		t.Fatal(stale)
	}
	actions, _ = m.Actions(ctx, agentaccess.ID(ws), p.Grant.ID)
	if _, err = m.Decide(ctx, agentaccess.ID(ws), actions[0].ID, true); err == nil {
		t.Fatal("stale action applied")
	}
	_, err = m.ChangeCredential(ctx, agentaccess.ID(ws), agentaccess.ID(issued.Grant.ID), 1, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.Decide(ctx, agentaccess.ID(ws), actions[0].ID, true); err == nil {
		t.Fatal("revoked proposal applied")
	}
	_ = m.Pool.QueryRow(ctx, `SELECT name FROM plants WHERE id=$1`, plant).Scan(&name)
	if name != "Reviewed lemon" {
		t.Fatal("failed action leaked a domain change")
	}
}

func TestReviewedExportAndScopedStatus(t *testing.T) {
	api, m, ws, _ := fixture(t)
	ctx := context.Background()
	issued, err := m.Save(ctx, agentaccess.ID(ws), agentaccess.SaveInput{ID: uuid.NewString(), Name: "Exporter", Scopes: []string{"garden:read", "photos:read", "purchases:read", "receipts:read", "exports:request"}, ExpiresAt: time.Now().Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	p, err := m.Verify(ctx, issued.Token)
	if err != nil {
		t.Fatal(err)
	}
	proposal := api.Call(ctx, p, "export_propose", map[string]any{"operation_id": "review-export"})
	if proposal.Status != "proposed" {
		t.Fatal(proposal)
	}
	actions, err := m.Actions(ctx, agentaccess.ID(ws), p.Grant.ID)
	if err != nil || len(actions) != 1 {
		t.Fatal(err, len(actions))
	}
	applied, err := m.Decide(ctx, agentaccess.ID(ws), actions[0].ID, true)
	if err != nil {
		t.Fatal(err, applied)
	}
	var id string
	if err = m.Pool.QueryRow(ctx, `SELECT id::text FROM export_jobs WHERE agent_grant_id=$1`, p.Grant.ID).Scan(&id); err != nil {
		t.Fatal(err)
	}
	status := api.Call(ctx, p, "export_status", map[string]any{"id": id})
	raw, _ := json.Marshal(status)
	if status.Status != "ok" || !strings.Contains(string(raw), "/agent/exports/"+id+"/download") {
		t.Fatal(status)
	}
	if denied := api.Call(ctx, p, "export_status", map[string]any{"id": uuid.NewString()}); denied.Status != "failed" {
		t.Fatal("unknown export returned", denied)
	}
}

func TestFinancialAnalysisCannotBypassPurchaseScope(t *testing.T) {
	api, m, ws, plant := fixture(t)
	ctx := context.Background()
	issued, err := m.Save(ctx, agentaccess.ID(ws), agentaccess.SaveInput{ID: uuid.NewString(), Name: "Read without finances", Scopes: []string{"garden:read", "analysis:run", "suggestions:review"}, ExpiresAt: time.Now().Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	principal, err := m.Verify(ctx, issued.Token)
	if err != nil {
		t.Fatal(err)
	}
	run := uuid.NewString()
	if _, err = m.Pool.Exec(ctx, `INSERT INTO analysis_runs(id,workspace_id,plant_id,kind,capability,input_snapshot,input_hash,semantic_key) VALUES($1,$2,$3,'SYNTHESIS','TEXT_ANALYZER','{}','sha256:'||repeat('a',64),'financial-fixture')`, run, ws, plant); err != nil {
		t.Fatal(err)
	}
	if _, err = m.Pool.Exec(ctx, `INSERT INTO journal_entries(id,workspace_id,plant_id,entry_type,source_id,occurred_at,summary) VALUES($1,$2,$3,'INSIGHT',$4,now(),'private financial advice')`, uuid.NewString(), ws, plant, run); err != nil {
		t.Fatal(err)
	}
	if denied := api.Call(ctx, principal, "analysis_status", map[string]any{"id": run}); denied.Status != "failed" {
		t.Fatal("financial analysis status bypassed purchase scope")
	}
	if denied := api.Call(ctx, principal, "analysis_request", map[string]any{"operation_id": "financial-attempt", "input": map[string]any{"kind": "SYNTHESIS", "plant_id": plant}}); denied.Status != "failed" {
		t.Fatal("financial analysis request bypassed purchase scope")
	}
	for _, call := range []struct {
		name string
		args map[string]any
	}{
		{"journal_list", map[string]any{"plant_id": plant}},
		{"calendar_events", map[string]any{"from": time.Now().Add(-24 * time.Hour).Format(time.DateOnly), "to": time.Now().Add(24 * time.Hour).Format(time.DateOnly)}},
	} {
		result := api.Call(ctx, principal, call.name, call.args)
		raw, _ := json.Marshal(result)
		if result.Status != "ok" {
			t.Fatal(call.name, result)
		}
		if strings.Contains(string(raw), "private financial advice") {
			t.Fatal("financial summary leaked through", call.name)
		}
	}
}

// A trusted proxy reaches a loopback socket with its public Host preserved.
// The explicit Canopy allowlist must remain authoritative for that deployment.
func TestMCPTrustedProxyHostAndLegacyProtocol(t *testing.T) {
	api, m, ws, _ := fixture(t)
	ctx := context.Background()
	issued, err := m.Save(ctx, agentaccess.ID(ws), agentaccess.SaveInput{ID: uuid.NewString(), Name: "Proxy fixture", Scopes: []string{"garden:read"}, ExpiresAt: time.Now().Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	handler := mcptransport.New(m, api, []string{"garden.example.test"})
	server := httptest.NewServer(handler)
	defer server.Close()
	client := server.Client()
	for _, host := range []string{"garden.example.test", "attacker.example.test"} {
		request, err := http.NewRequest("POST", server.URL+"/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`))
		if err != nil {
			t.Fatal(err)
		}
		request.Host = host
		request.Header.Set("Authorization", "Bearer "+issued.Token)
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Accept", "application/json, text/event-stream")
		request.Header.Set("MCP-Protocol-Version", "2025-11-25")
		response, err := client.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		expected := 200
		if host != "garden.example.test" {
			expected = 403
		}
		if response.StatusCode != expected {
			t.Fatal("proxy host boundary", host, response.StatusCode)
		}
	}
}
