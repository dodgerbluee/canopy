package knowledge

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"canopy/server/internal/aiconnection"
	"canopy/server/internal/platform"
)

func TestPlantingResearchUsesConfiguredProviderProtocolsAndOnlyPublicPrompts(t *testing.T) {
	for _, protocol := range []string{aiconnection.OpenAI, aiconnection.Anthropic, aiconnection.Gemini} {
		t.Run(protocol, func(t *testing.T) {
			e := planningDatabase(t)
			ctx := context.Background()
			manager, err := aiconnection.New(e.s.Pool, filepath.Join(t.TempDir(), "fixture-key"), nil)
			if err != nil {
				t.Fatal(err)
			}
			calls := atomic.Int32{}
			manager.Transport = roundTrip(func(r *http.Request) (*http.Response, error) {
				calls.Add(1)
				raw, err := io.ReadAll(r.Body)
				if err != nil {
					return nil, err
				}
				if strings.Contains(string(raw), "private-plan-secret") || strings.Contains(string(raw), "Private future fig") {
					t.Error("private planning data entered provider prompt")
				}
				var request map[string]any
				if err = json.Unmarshal(raw, &request); err != nil {
					return nil, err
				}
				switch protocol {
				case aiconnection.OpenAI:
					if request["model"] != "fixture-model" || request["max_completion_tokens"] != float64(5000) {
						t.Error("configured OpenAI token strategy not used")
					}
				case aiconnection.Anthropic:
					if r.URL.Path != "/v1/messages" || request["max_tokens"] != float64(5000) {
						t.Error("Anthropic transport not used")
					}
				case aiconnection.Gemini:
					if !strings.Contains(r.URL.Path, "fixture-model:generateContent") {
						t.Error("Gemini transport not used")
					}
				}
				content, _ := json.Marshal(map[string]any{"facts": []PlantingDraft{{Phase: "PLANT_OUT", Label: "Planting", Summary: "Nursery trees have a planting window.", Evidence: plantingEvidence, Timing: "December through February", Methods: []string{"NURSERY_PLANT"}}}})
				var envelope any
				switch protocol {
				case aiconnection.OpenAI:
					envelope = map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": string(content)}}}}
				case aiconnection.Anthropic:
					envelope = map[string]any{"content": []any{map[string]any{"type": "text", "text": string(content)}}}
				case aiconnection.Gemini:
					envelope = map[string]any{"candidates": []any{map[string]any{"content": map[string]any{"parts": []any{map[string]any{"text": string(content)}}}}}}
				}
				response, _ := json.Marshal(envelope)
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(string(response)))}, nil
			})
			key := "fixture-provider-secret"
			connection, err := manager.Save(ctx, e.actor.WorkspaceID, aiconnection.SaveInput{ID: platform.UUIDv4{}.New().String(), Name: "Planting fixture", Protocol: protocol, Endpoint: "https://provider.example.invalid", Model: "fixture-model", APIKey: &key, ExternalConsent: true, TokenLimitField: "MAX_COMPLETION_TOKENS"}, nil)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = manager.SaveBindings(ctx, e.actor.WorkspaceID, []aiconnection.BindingInput{{Feature: aiconnection.Garden, Mode: "CONNECTION", ConnectionID: connection.ID}}, nil); err != nil {
				t.Fatal(err)
			}
			e.s.UseConnections(manager)
			id := e.idea(t, e.actor, "Private future fig")
			if err = e.planner.SweepGuidance(ctx); err != nil {
				t.Fatal(err)
			}
			if p := e.plan(t, e.actor, id); p.LatestGuidance.State != "READY" || calls.Load() != 1 {
				t.Fatal("configured provider did not produce guidance", p.LatestGuidance.State, calls.Load())
			}
		})
	}
}
