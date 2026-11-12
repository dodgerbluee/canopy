package graphql

import (
	"bytes"
	"canopy/server/internal/agentaccess"
	httpx "canopy/server/internal/transport/http"
	"context"
	"encoding/json"
	"github.com/google/uuid"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestOwnerAgentManagementReturnsCredentialOnlyOnIssuance(t *testing.T) {
	e := newMediaEnv(t, 0)
	root := NewResolverFromPool(e.pool, slog.New(slog.DiscardHandler))
	root.AgentAccess = agentaccess.New(e.pool)
	root.AgentEnabled = true
	root.AgentEndpoint = "http://localhost:8080/mcp"
	handler := NewHandler(root, DefaultLimits(), root.Log)
	call := func(query string, variables map[string]any, authenticated bool) map[string]any {
		t.Helper()
		body, _ := json.Marshal(map[string]any{"query": query, "variables": variables})
		req := httptest.NewRequest("POST", "http://localhost/api/graphql", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		if authenticated {
			req = req.WithContext(httpx.WithPrincipal(req.Context(), httpx.Principal{WorkspaceID: e.wsID, OwnerID: e.ownerID}))
		}
		out := httptest.NewRecorder()
		handler.ServeHTTP(out, req)
		var result map[string]any
		if err := json.Unmarshal(out.Body.Bytes(), &result); err != nil {
			t.Fatal("invalid management response", err)
		}
		return result
	}
	query := `query {agentSettings{enabled endpoint scopeOptions grants{id name scopes revision} actions{id state before input}}}`
	if result := call(query, nil, false); result["errors"] == nil {
		t.Fatal("management query accepted without owner identity")
	}
	id := uuid.NewString()
	input := map[string]any{"id": id, "name": "Owner test", "scopes": []string{"garden:read"}, "plantIds": []string{}, "expiresAt": time.Now().Add(time.Hour).UTC().Format(time.RFC3339), "expectedRevision": 0}
	mutation := `mutation($input:SaveAgentGrantInput!){saveAgentGrant(input:$input){grant{id name revision} token problems{code message}}}`
	response := call(mutation, map[string]any{"input": input}, true)
	data, ok := response["data"].(map[string]any)
	if !ok {
		t.Fatal("owner management mutation failed")
	}
	payload := data["saveAgentGrant"].(map[string]any)
	token, _ := payload["token"].(string)
	if !strings.HasPrefix(token, "canopy_agent.") {
		t.Fatal("one-time credential missing")
	}
	if _, err := root.AgentAccess.Verify(context.Background(), token); err != nil {
		t.Fatal("issued credential unavailable")
	}
	settings := call(query, nil, true)
	raw, _ := json.Marshal(settings)
	if bytes.Contains(raw, []byte(token)) || bytes.Contains(raw, []byte("verifier")) {
		t.Fatal("read settings exposed credential material")
	}
	input["expectedRevision"] = 1
	input["name"] = "Updated owner test"
	response = call(mutation, map[string]any{"input": input}, true)
	payload = response["data"].(map[string]any)["saveAgentGrant"].(map[string]any)
	if payload["token"] != nil {
		t.Fatal("editing permissions returned an existing secret")
	}
}
