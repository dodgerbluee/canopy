package knowledge

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"canopy/server/internal/aiconnection"
)

// Explicit opt-in. Only public publications are sent; configuration is supplied
// through the environment and no private garden database is used.
func TestPlantingSourcesAndConfiguredLocalModelLive(t *testing.T) {
	if os.Getenv("KNOWLEDGE_PLANNING_LIVE") != "1" {
		t.Skip("explicit local-model/source opt-in required")
	}
	config := aiconnection.Config{Protocol: aiconnection.OpenAI, Endpoint: os.Getenv("CANOPY_PLANNING_LIVE_BASE_URL"), Model: os.Getenv("CANOPY_PLANNING_LIVE_MODEL"), APIKey: os.Getenv("CANOPY_PLANNING_LIVE_API_KEY"), ReasoningEffort: os.Getenv("CANOPY_PLANNING_LIVE_REASONING"), TokenLimitField: "MAX_TOKENS", DeploymentLocal: true}
	model := NewModel(config.Origin(), config.APIKey, config.Model, config.ReasoningEffort)
	if model == nil {
		t.Fatal("local model configuration missing")
	}
	model.HTTP = (&aiconnection.Manager{}).Client(config)
	for _, source := range Registry() {
		if source.ID != "tamu-tomato" && source.ID != "osu-tomato" && source.ID != "rhs-strawberry" {
			continue
		}
		t.Run(source.ID, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
			defer cancel()
			var replay struct {
				Text   string          `json:"text"`
				Drafts []PlantingDraft `json:"drafts"`
			}
			path := ""
			if dir := os.Getenv("CANOPY_PLANNING_LIVE_RECORD_DIR"); dir != "" {
				path = filepath.Join(dir, source.ID+".json")
			}
			var err error
			if os.Getenv("CANOPY_PLANNING_LIVE_REPLAY") == "1" {
				raw, e := os.ReadFile(path)
				if e != nil {
					t.Fatal(e)
				}
				if e = json.Unmarshal(raw, &replay); e != nil {
					t.Fatal(e)
				}
			} else {
				replay.Text, err = NewFetcher(nil).Read(ctx, source)
				if err != nil {
					t.Fatal("primary publication could not be read", err)
				}
				replay.Drafts, err = model.ExtractPlanting(ctx, source, planningSourceText(source, replay.Text))
				if err != nil {
					t.Fatal("local extraction failed", err)
				}
				if path != "" {
					raw, e := json.Marshal(replay)
					if e != nil {
						t.Fatal(e)
					}
					if e = os.WriteFile(path, raw, 0o600); e != nil {
						t.Fatal(e)
					}
				}
			}
			text, drafts := planningSourceText(source, replay.Text), replay.Drafts

			facts, err := usablePlantingSource(source, text, drafts, time.Now().UTC(), model.PlanningIdentity())
			if err != nil {
				for _, draft := range drafts {
					_, validation := validatePlantingSource(source, text, []PlantingDraft{draft}, time.Now().UTC(), model.PlanningIdentity())
					t.Logf("draft phase=%s validation=%v", draft.Phase, validation)
				}
				t.Fatal("no supported extraction", err)
			}
			timed := 0
			planting := 0
			for _, fact := range facts {
				for _, window := range fact.Fact.Windows {
					if window.Precision != "UNKNOWN" {
						timed++
					}
				}
				switch fact.Fact.Phase {
				case "START_INDOORS", "DIRECT_SOW", "TRANSPLANT", "PLANT_OUT":
					planting++
				}
			}
			t.Logf("primary source=%s bytes=%d drafts=%d verifiedFacts=%d timedWindows=%d plantingFacts=%d", source.ID, len(text), len(drafts), len(facts), timed, planting)
			if planting == 0 || timed == 0 {
				t.Fatal("expected actual source-supported planting guidance")
			}
		})
	}
}
