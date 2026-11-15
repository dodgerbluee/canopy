package graphql

import "testing"

func TestPlanningBoardFrozenHTTPIncludesManualIdeas(t *testing.T) {
	e := newEnv(t, DefaultLimits())
	plantingPayload(t, plantingOp(t, e, "CreatePlantingPlan", map[string]any{"input": map[string]any{"name": "Manual future fig"}}), "createPlantingPlan")
	vars := map[string]any{"input": map[string]any{"fromMonth": "2027-01", "toMonth": "2027-12"}}
	page := dataOf(t, plantingOp(t, e, "PlantingBoard", vars), "plantingBoard")
	items := page["items"].([]any)
	if len(items) != 1 || page["timezone"] == nil || page["today"] == nil {
		t.Fatal(page)
	}
	row := items[0].(map[string]any)
	if row["plan"].(map[string]any)["name"] != "Manual future fig" || row["schedule"].(map[string]any)["guidanceState"] != "DISABLED" {
		t.Fatal(row)
	}
	vars["filter"] = map[string]any{"states": []string{}}
	if len(dataOf(t, plantingOp(t, e, "PlantingBoard", vars), "plantingBoard")["items"].([]any)) != 0 {
		t.Fatal("empty states lost")
	}
}
