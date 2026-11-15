package graphql

import "testing"

func TestPlanningEventsNamedOperation(t *testing.T) {
	e := newEnv(t, DefaultLimits())
	p := plantingPayload(t, plantingOp(t, e, "CreatePlantingPlan", map[string]any{"input": map[string]any{"name": "Future fig", "targetPeriod": map[string]any{"precision": "YEAR", "year": 2027}}}), "createPlantingPlan")
	vars := map[string]any{"from": "2027-01-01", "to": "2027-02-28", "first": 1}
	page := dataOf(t, plantingOp(t, e, "PlantingEvents", vars), "plantingEvents")
	if len(page["items"].([]any)) != 0 {
		t.Fatal(page)
	}
	plantingPayload(t, plantingOp(t, e, "CreatePlantingAction", map[string]any{"input": map[string]any{"planId": p["id"], "expectedRevision": p["revision"], "kind": "PREPARE", "title": "Prepare container", "scheduledOn": "2027-02-10"}}), "createPlantingAction")
	page = dataOf(t, plantingOp(t, e, "PlantingEvents", vars), "plantingEvents")
	items := page["items"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["planId"] != p["id"] || page["timezone"] == nil {
		t.Fatal(page)
	}
	vars["to"] = "2027-03-04"
	r := plantingOp(t, e, "PlantingEvents", vars)
	if len(r.Errors) == 0 {
		t.Fatal("unbounded range", r.Raw)
	}
}
