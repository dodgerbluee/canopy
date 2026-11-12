package insights

import (
	dbgen "canopy/server/db/generated"
	"canopy/server/internal/aiconnection"
	"context"
	"encoding/json"
	"github.com/jackc/pgx/v5/pgxpool"
	"os"
	"strings"
	"testing"
	"time"
)

func TestHistoryPromptClosedEvidenceAndRepairDiagnostic(t *testing.T) {
	snap := Snapshot{Schema: SnapSchema, Kind: "SYNTHESIS", Plant: SnapPlant{ID: "plant-context"}, Photos: []SnapPhoto{{ID: "photo-evidence", AssetID: "asset-context"}}, Activities: []SnapActivity{{ID: "activity-evidence"}}}
	raw, _ := json.Marshal(snap)
	bad := `{"visibleObservations":[{"text":"Meyer lemon","evidenceIds":["plant-context"]}],"hypotheses":[],"missingInformation":[],"followUpChecks":[],"citations":[]}`
	messages, err := historyMessages(AnalyzeInput{Snapshot: raw, PromptVersion: "test", RepairOf: bad})
	if err != nil {
		t.Fatal(err)
	}
	system := messages[0].Content.(string)
	user := messages[1].Content.(string)
	if !strings.Contains(system, `["activity-evidence","photo-evidence"]`) || !strings.Contains(system, "plant.id, workspace.id, photo.assetId") || !strings.Contains(system, "metadata only") {
		t.Fatal("missing closed evidence or metadata boundaries")
	}
	if !strings.Contains(user, "not in supplied evidence") || !strings.Contains(user, "plant-context") {
		t.Fatal("repair lacks concrete validation feedback")
	}
	if _, err := ValidateResult(dbgen.AnalysisKindSYNTHESIS, &snap, []byte(bad), 1<<20); err == nil {
		t.Fatal("invalid plant citation was accepted")
	}
}

// Opt-in read-only replay through the owner's selected connection.
func TestLiveHistoryReplay(t *testing.T) {
	runID := os.Getenv("CANOPY_LIVE_HISTORY_RUN")
	if runID == "" {
		t.Skip("live history replay is opt-in")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, os.Getenv("PGTEST_DSN"))
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	id, err := parseUUID(runID)
	if err != nil {
		t.Fatal(err)
	}
	var run dbgen.AnalysisRun
	err = pool.QueryRow(ctx, `SELECT workspace_id,input_snapshot FROM analysis_runs WHERE id=$1`, id).Scan(&run.WorkspaceID, &run.InputSnapshot)
	if err != nil {
		t.Fatal(err)
	}
	manager, err := aiconnection.New(pool, os.Getenv("CANOPY_LIVE_AI_KEY_FILE"), nil)
	if err != nil {
		t.Fatal("could not load managed AI connection")
	}
	router := &ConnectionRouter{Manager: manager, Fallback: NoProvider{}, Q: dbgen.New(pool)}
	in := AnalyzeInput{RunID: runID, WorkspaceID: uuidString(run.WorkspaceID), Snapshot: run.InputSnapshot, Capability: CapTextAnalyzer, PromptVersion: "history-evidence-v2"}
	snap, err := ParseSnapshot(in.Snapshot)
	if err != nil {
		t.Fatal(err)
	}
	out, err := router.Analyze(ctx, in)
	if err != nil {
		t.Fatal("live model request failed")
	}
	canonical, validation := ValidateResult(dbgen.AnalysisKindSYNTHESIS, snap, out.Raw, 1<<20)
	if validation != nil {
		t.Log("First response required bounded repair:", validation)
		in.RepairOf = string(out.Raw)
		out, err = router.Analyze(ctx, in)
		if err != nil {
			t.Fatal("live model repair failed")
		}
		canonical, validation = ValidateResult(dbgen.AnalysisKindSYNTHESIS, snap, out.Raw, 1<<20)
	}
	if validation != nil {
		t.Fatal("live result rejected:", validation)
	}
	var result ObsResult
	_ = json.Unmarshal(canonical, &result)
	t.Logf("Validated live history: %d observations, %d hypotheses, %d missing-context items; provider=%s model=%s; no database mutations", len(result.VisibleObservations), len(result.Hypotheses), len(result.MissingInformation), out.Provider, out.Model)
}
