package graphql

import (
	"canopy/server/internal/aiconnection"
	"canopy/server/internal/identity"
	"canopy/server/internal/knowledge"
	"canopy/server/internal/platform"
	httpx "canopy/server/internal/transport/http"
	"context"
	"github.com/google/uuid"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func aiTestManager(t *testing.T, m *mediaEnv, defaults map[string]aiconnection.Config) *aiconnection.Manager {
	t.Helper()
	manager, err := aiconnection.New(m.pool, filepath.Join(t.TempDir(), "key"), defaults)
	if err != nil {
		t.Fatal(err)
	}
	return manager
}
func aiServe(t *testing.T, m *mediaEnv, manager *aiconnection.Manager) {
	t.Helper()
	log := slog.New(slog.DiscardHandler)
	cfg := identity.DefaultSessionConfig(false, false)
	sm := identity.NewSessionManager(m.q, platform.UUIDv4{}, platform.SystemClock{}, cfg)
	owners := identity.NewOwnerService(m.q, sm, identity.NewPasswordHasher(identity.Argon2Params{Memory: 64, Iterations: 1, Parallelism: 1}), platform.UUIDv4{}, log)
	auth := httpx.NewAuth(owners, sm, identity.NewLoginLimiter(10), cfg, log)
	mux := http.NewServeMux()
	auth.Register(mux)
	root := NewResolverFromPool(m.pool, log)
	root.AIConnections = manager
	mux.Handle("POST /api/graphql", auth.RequirePrincipal(NewHandler(root, DefaultLimits(), log).ServeHTTP))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	m.srv = srv
	m.login()
}
func fixtureAIInput() aiconnection.SaveInput {
	return aiconnection.SaveInput{ID: uuid.NewString(), Name: "My AI", Protocol: aiconnection.OpenAI, Endpoint: "http://127.0.0.1:1234/v1", Model: "fixture-model", DeploymentLocal: true, SupportsVision: true, ReasoningEffort: "none", TokenLimitField: "MAX_TOKENS"}
}
func TestAIConnectionsStorageIsolationAndRouting(t *testing.T) {
	m := newMediaEnv(t, 0)
	ctx := context.Background()
	in := fixtureAIInput()
	secret := "fixture-private-key"
	in.APIKey = &secret
	manager := aiTestManager(t, m, map[string]aiconnection.Config{aiconnection.Garden: {Protocol: aiconnection.OpenAI, Endpoint: in.Endpoint, Model: "default", APIKey: secret}})
	key := uuid.NewString()
	saved, err := manager.Save(ctx, m.wsID, in, &key)
	if err != nil || !saved.HasAPIKey || saved.Revision != 1 {
		t.Fatal(err, saved)
	}
	replay, err := manager.Save(ctx, m.wsID, in, &key)
	if err != nil || replay.Revision != 1 {
		t.Fatal("replay", err)
	}
	changed := in
	changed.Name = "Other"
	if _, err = manager.Save(ctx, m.wsID, changed, &key); err == nil {
		t.Fatal("idempotency conflict accepted")
	}
	var cipher []byte
	if err = m.pool.QueryRow(ctx, `SELECT credential_ciphertext FROM ai_connections WHERE id=$1`, in.ID).Scan(&cipher); err != nil || strings.Contains(string(cipher), secret) {
		t.Fatal("plaintext credential stored", err)
	}
	var receipts []byte
	if err = m.pool.QueryRow(ctx, `SELECT jsonb_agg(to_jsonb(t)) FROM mutation_receipts t WHERE workspace_id=$1`, m.wsID).Scan(&receipts); err != nil || strings.Contains(string(receipts), secret) {
		t.Fatal("receipt leaked credential", err)
	}
	exported, err := m.q.ExportAIConnectionMetadata(ctx, m.wsID)
	if err != nil || strings.Contains(string(exported[0]), "credential_ciphertext") || strings.Contains(string(exported[0]), secret) {
		t.Fatal("export leak", err)
	}
	other := platform.UUIDv4{}.New()
	if _, err = m.pool.Exec(ctx, `INSERT INTO workspace(id,owner_id,display_name,timezone,default_currency) VALUES($1,$2,'Other','UTC','USD')`, other, m.ownerID); err != nil {
		t.Fatal(err)
	}
	settings, err := manager.Settings(ctx, other)
	if err != nil || len(settings.Connections) != 0 {
		t.Fatal("tenant leak", err)
	}
	if _, err = manager.SaveBindings(ctx, other, []aiconnection.BindingInput{{Feature: aiconnection.Garden, Mode: "CONNECTION", ConnectionID: in.ID}}, nil); err == nil {
		t.Fatal("cross-tenant binding accepted")
	}
	in.ExpectedRevision = 1
	in.APIKey = nil
	updated, err := manager.Save(ctx, m.wsID, in, nil)
	if err != nil || !updated.HasAPIKey || updated.Revision != 2 {
		t.Fatal("keep key", err)
	}
	in.ExpectedRevision = 2
	in.Endpoint = "http://127.0.0.1:9999/v1"
	if _, err = manager.Save(ctx, m.wsID, in, nil); err == nil {
		t.Fatal("key forwarded to changed endpoint")
	}
	in.ClearAPIKey = true
	updated, err = manager.Save(ctx, m.wsID, in, nil)
	if err != nil || updated.HasAPIKey {
		t.Fatal("clear", err)
	}
	_, err = manager.SaveBindings(ctx, m.wsID, []aiconnection.BindingInput{{Feature: aiconnection.Garden, Mode: "CONNECTION", ConnectionID: in.ID}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	selection, err := manager.Resolve(ctx, m.wsID, aiconnection.Garden)
	if err != nil || !selection.Explicit || selection.Config.Endpoint != in.Endpoint {
		t.Fatal("dynamic selection", err)
	}
	if err = manager.Delete(ctx, m.wsID, in.ID, 3, nil); err != nil {
		t.Fatal(err)
	}
	selection, err = manager.Resolve(ctx, m.wsID, aiconnection.Garden)
	if err != nil || !selection.Disabled || selection.Config != nil {
		t.Fatal("delete silently fell back", err)
	}
	// Copying an operator-held key is constrained to that exact endpoint/protocol.
	in = fixtureAIInput()
	feature := aiconnection.Garden
	in.CopyDeploymentKeyFrom = &feature
	if _, err = manager.Save(ctx, m.wsID, in, nil); err != nil {
		t.Fatal("copy deployment", err)
	}
	in = fixtureAIInput()
	in.CopyDeploymentKeyFrom = &feature
	in.Endpoint = "http://127.0.0.1:7777"
	if _, err = manager.Save(ctx, m.wsID, in, nil); err == nil {
		t.Fatal("copy leaked to other endpoint")
	}
}
func TestAIConnectionsHTTPAndRevisionFencedTest(t *testing.T) {
	m := newMediaEnv(t, 0)
	ctx := context.Background()
	manager := aiTestManager(t, m, nil)
	aiServe(t, m, manager)
	in := fixtureAIInput()
	secret := "fixture-private-key"
	input := map[string]any{"id": in.ID, "name": in.Name, "protocol": in.Protocol, "endpoint": in.Endpoint, "model": in.Model, "deploymentLocal": true, "supportsVision": true, "externalConsent": false, "apiKey": secret, "reasoningEffort": "none", "tokenLimitField": "MAX_TOKENS", "expectedRevision": 0}
	key := uuid.NewString()
	payload, ps := runMut(t, m, "saveAIConnection", "SaveAIConnectionInput", input, &key, `connection{id hasAPIKey revision}`)
	mustNoProblems(t, ps, "saveAIConnection")
	if payload["connection"].(map[string]any)["hasAPIKey"] != true {
		t.Fatal(payload)
	}
	page := m.gql(`query{aiConnections{connections{id name endpoint model hasAPIKey} bindings{feature mode revision} deploymentDefaults{model hasAPIKey}}}`, nil)
	if len(page.Errors) != 0 || strings.Contains(page.Raw, secret) {
		t.Fatal("read leak", page.Raw)
	}
	started, release := make(chan struct{}), make(chan struct{})
	manager.Transport = guideTransport(func(*http.Request) (*http.Response, error) {
		close(started)
		<-release
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"choices":[{"message":{"content":"{\"status\":\"ok\"}"}}]}`))}, nil
	})
	revision := 1
	done := make(chan error, 1)
	go func() {
		_, err := manager.Test(ctx, m.wsID, aiconnection.TestInput{ID: in.ID, ExpectedRevision: &revision})
		done <- err
	}()
	<-started
	in.ExpectedRevision = 1
	in.Name = "Changed during test"
	if _, err := manager.Save(ctx, m.wsID, in, nil); err != nil {
		t.Fatal(err)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	settings, err := manager.Settings(ctx, m.wsID)
	if err != nil || settings.Connections[0].LastTestSucceeded != nil {
		t.Fatal("stale test certified edited configuration", err)
	}
	// External data requires explicit connection consent, even after assignment.
	in.ExpectedRevision = 2
	in.DeploymentLocal = false
	in.Endpoint = "https://hosted.example/v1"
	in.ClearAPIKey = true
	if _, err = manager.Save(ctx, m.wsID, in, nil); err != nil {
		t.Fatal(err)
	}
	if _, err = manager.SaveBindings(ctx, m.wsID, []aiconnection.BindingInput{{Feature: aiconnection.Garden, Mode: "CONNECTION", ConnectionID: in.ID}}, nil); err != nil {
		t.Fatal(err)
	}
	if _, err = manager.Resolve(ctx, m.wsID, aiconnection.Garden); err == nil {
		t.Fatal("hosted connection used without consent")
	}
	for _, query := range []string{`mutation{a:testAIConnection(input:{id:"00000000-0000-0000-0000-000000000001",expectedRevision:1}){result{succeeded}} b:testAIConnection(input:{id:"00000000-0000-0000-0000-000000000001",expectedRevision:1}){result{succeeded}}}`, `mutation{...Tests} fragment Tests on Mutation{a:testAIConnection(input:{id:"00000000-0000-0000-0000-000000000001",expectedRevision:1}){result{succeeded}} b:testAIConnection(input:{id:"00000000-0000-0000-0000-000000000001",expectedRevision:1}){result{succeeded}}}`} {
		r := m.gql(query, nil)
		if len(r.Errors) != 1 || !strings.Contains(r.Raw, "one AI connection test") {
			t.Fatal("unbounded synchronous tests", r.Raw)
		}
	}
}

// Native vendors are covered by deterministic protocol fixtures. This opt-in
// checks the user's managed compatible model without persisting its credential.
func TestAIConnectionLiveStrata(t *testing.T) {
	if os.Getenv("KNOWLEDGE_MODEL_LIVE") != "1" {
		t.Skip("live model opt-in required")
	}
	m := newMediaEnv(t, 0)
	manager := aiTestManager(t, m, nil)
	in := fixtureAIInput()
	in.Endpoint = os.Getenv("CANOPY_VISION_BASE_URL")
	in.Model = os.Getenv("CANOPY_VISION_MODEL")
	secret := os.Getenv("CANOPY_VISION_API_KEY")
	in.APIKey = &secret
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	saved, err := manager.Save(ctx, m.wsID, in, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, vision := range []bool{false, true} {
		result, err := manager.Test(ctx, m.wsID, aiconnection.TestInput{ID: in.ID, ExpectedRevision: &saved.Revision, TestVision: vision})
		if err != nil || !result.Succeeded {
			t.Fatal("live synthetic test", err, result.Message)
		}
		t.Logf("Managed model accepted synthetic %s request.", map[bool]string{false: "text", true: "image"}[vision])
	}
}

func TestAIConnectionThroughSavedWorker(t *testing.T) {
	binary := os.Getenv("CANOPY_TEST_WORKER_BINARY")
	if binary == "" || os.Getenv("KNOWLEDGE_MODEL_LIVE") != "1" {
		t.Skip("explicit worker/model opt-in required")
	}
	m := newMediaEnv(t, 0)
	keyFile := filepath.Join(t.TempDir(), "shared-ai-key")
	manager, err := aiconnection.New(m.pool, keyFile, nil)
	if err != nil {
		t.Fatal(err)
	}
	in := fixtureAIInput()
	in.Endpoint = os.Getenv("CANOPY_VISION_BASE_URL")
	in.Model = os.Getenv("CANOPY_VISION_MODEL")
	secret := os.Getenv("CANOPY_VISION_API_KEY")
	in.APIKey = &secret
	if _, err = manager.Save(context.Background(), m.wsID, in, nil); err != nil {
		t.Fatal(err)
	}
	if _, err = manager.SaveBindings(context.Background(), m.wsID, []aiconnection.BindingInput{{Feature: aiconnection.Garden, Mode: "CONNECTION", ConnectionID: in.ID}}, nil); err != nil {
		t.Fatal(err)
	}
	knowledgeSettings(t, m, 0, "US", "Texas")
	createKnowledgePlant(t, m, "Meyer lemon")
	createKnowledgePlant(t, m, "Second Meyer lemon")
	cmd := exec.Command(binary)
	cmd.Env = append(os.Environ(), "DATABASE_URL="+m.pool.Config().ConnString(), "CANOPY_ENV=test", "CANOPY_LOG_LEVEL=warn", "CANOPY_STORAGE_ROOT="+t.TempDir(), "CANOPY_MEDIA_IMAGE=canopy-media:r2", "CANOPY_AI_BASE_URL=", "CANOPY_AI_API_KEY=", "CANOPY_VISION_BASE_URL=", "CANOPY_VISION_API_KEY=", "CANOPY_KNOWLEDGE_BASE_URL=", "CANOPY_KNOWLEDGE_API_KEY=", "CANOPY_AI_CREDENTIALS_KEY_FILE="+keyFile)
	log, err := os.CreateTemp(t.TempDir(), "worker-log")
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	cmd.Stdout = log
	cmd.Stderr = log
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	t.Cleanup(func() {
		_ = cmd.Process.Signal(syscall.SIGTERM)
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			_ = cmd.Process.Kill()
			<-done
		}
	})
	deadline := time.Now().Add(4 * time.Minute)
	for time.Now().Before(deadline) {
		var ready, jobs int
		err = m.pool.QueryRow(context.Background(), `SELECT (SELECT count(*) FROM plant_knowledge WHERE workspace_id=$1 AND state='READY'),(SELECT count(*) FROM river_job WHERE kind='knowledge.enrich' AND state='completed')`, m.wsID).Scan(&ready, &jobs)
		if err != nil {
			t.Fatal(err)
		}
		if ready == 2 && jobs > 0 {
			var profiles int
			if err = m.pool.QueryRow(context.Background(), `SELECT count(*) FROM knowledge_profiles WHERE workspace_id=$1`, m.wsID).Scan(&profiles); err != nil || profiles != 1 {
				t.Fatal("profile reuse", err, profiles)
			}
			s := knowledge.New(m.pool)
			s.UseConnections(manager)
			answer, citations, err := s.Ask(context.Background(), m.wsID, "What do the sources say about flowering for my Meyer lemons? Distinguish expected timing from actual observations.", 2026)
			if err != nil || len(answer.Answer) == 0 || len(citations) == 0 {
				t.Fatal("live grounded garden answer", err)
			}
			t.Logf("Configured model returned a validated garden answer with %d supporting fact references.", len(citations))
			t.Log("Saved encrypted workspace connection → worker with deployment AI URLs/keys cleared → queued public-source enrichment → 2 READY plants, 1 reusable profile; saved-connection garden chat passed.")
			return
		}
		select {
		case err := <-done:
			done <- err
			t.Fatal("worker exited early", err)
		default:
		}
		time.Sleep(500 * time.Millisecond)
	}
	rows, _ := m.pool.Query(context.Background(), `SELECT state,message FROM plant_knowledge WHERE workspace_id=$1`, m.wsID)
	defer rows.Close()
	for rows.Next() {
		var state, message string
		_ = rows.Scan(&state, &message)
		t.Log(state, message)
	}
	t.Fatal("queued knowledge did not finish")
}
