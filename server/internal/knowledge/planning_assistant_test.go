package knowledge

import (
	"canopy/server/internal/aiconnection"
	"canopy/server/internal/planning"
	"canopy/server/internal/platform"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
)

func TestPlanningAssistantConfiguredProtocolsAndSeparateOwnership(t *testing.T) {
	for _, protocol := range []string{aiconnection.OpenAI, aiconnection.Anthropic, aiconnection.Gemini} {
		t.Run(protocol, func(t *testing.T) {
			e := planningDatabase(t)
			ctx := context.Background()
			other := e.workspace(t)
			for _, row := range []struct{ ws, name string }{{e.actor.WorkspaceID.String(), "Current lemon fixture"}, {other.WorkspaceID.String(), "Hidden owned fig"}} {
				if _, err := e.s.Pool.Exec(ctx, "INSERT INTO plants(id,workspace_id,name,type,category) VALUES($1,$2,$3,'INDIVIDUAL','OTHER')", platform.UUIDv4{}.New(), row.ws, row.name); err != nil {
					t.Fatal(err)
				}
			}
			manager, err := aiconnection.New(e.s.Pool, filepath.Join(t.TempDir(), "key"), nil)
			if err != nil {
				t.Fatal(err)
			}
			calls := 0
			manager.Transport = roundTrip(func(r *http.Request) (*http.Response, error) {
				calls++
				raw, _ := io.ReadAll(r.Body)
				body := string(raw)
				if !strings.Contains(body, "Selected private future plan") || !strings.Contains(body, "Current lemon fixture") || strings.Contains(body, "Hidden owned fig") || strings.Contains(body, "fixture-provider-secret") {
					t.Error("ownership/secret boundary failed")
				}
				switch protocol {
				case aiconnection.OpenAI:
					if !strings.Contains(body, "max_completion_tokens") {
						t.Error("OpenAI token binding")
					}
				case aiconnection.Anthropic:
					if r.URL.Path != "/v1/messages" {
						t.Error(r.URL.Path)
					}
				case aiconnection.Gemini:
					if !strings.Contains(r.URL.Path, "fixture-model:generateContent") {
						t.Error(r.URL.Path)
					}
				}
				content := `{"answer":"Review the supported planting window.","planRefs":["p1"],"sourceRefs":["p1f1"],"notePlanRef":"p1","proposedNote":"Review this cited window before choosing a day."}`
				var envelope any
				switch protocol {
				case aiconnection.OpenAI:
					envelope = map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": content}}}}
				case aiconnection.Anthropic:
					envelope = map[string]any{"content": []any{map[string]any{"type": "text", "text": content}}}
				case aiconnection.Gemini:
					envelope = map[string]any{"candidates": []any{map[string]any{"content": map[string]any{"parts": []any{map[string]any{"text": content}}}}}}
				}
				response, _ := json.Marshal(envelope)
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(string(response)))}, nil
			})
			key := "fixture-provider-secret"
			connection, err := manager.Save(ctx, e.actor.WorkspaceID, aiconnection.SaveInput{ID: platform.UUIDv4{}.New().String(), Name: "Fixture", Protocol: protocol, Endpoint: "https://provider.example.invalid", Model: "fixture-model", APIKey: &key, ExternalConsent: true, TokenLimitField: "MAX_COMPLETION_TOKENS"}, nil)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = manager.SaveBindings(ctx, e.actor.WorkspaceID, []aiconnection.BindingInput{{Feature: aiconnection.Garden, Mode: "CONNECTION", ConnectionID: connection.ID}}, nil); err != nil {
				t.Fatal(err)
			}
			e.s.UseConnections(manager)
			out, err := (PlanningAssistant{Service: e.s}).AnswerPlanning(ctx, e.actor.WorkspaceID, planning.AssistantContext{Question: "What next?", Year: 2027, ProposedPlans: []planning.AssistantPlan{{Ref: "p1", Name: "Selected private future plan"}}})
			if err != nil || out.NotePlanRef != "p1" || calls != 1 {
				t.Fatal(out, err, calls)
			}
		})
	}
}
func TestPlanningAssistantUsesSharedChatLimitAndStrictDraftShape(t *testing.T) {
	e := planningDatabase(t)
	for range 12 {
		if err := e.s.reserveChat(e.actor.WorkspaceID); err != nil {
			t.Fatal(err)
		}
	}
	if err := e.s.reserveChat(e.actor.WorkspaceID); err == nil {
		t.Fatal("unbounded chat")
	}
	var draft planning.AssistantDraft
	if err := json.Unmarshal([]byte(`{"answer":"Hi","createPlant":{"name":"invented"}}`), &draft); err == nil {
		t.Fatal("unknown model command field accepted")
	}
}
