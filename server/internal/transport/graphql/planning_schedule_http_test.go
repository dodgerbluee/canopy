package graphql

import (
	"context"
	"net/http"
	"testing"
)

func TestPlanningScheduleFrozenOperationsAndConfirmationOverHTTP(t *testing.T) {
	e := newEnv(t, DefaultLimits())
	root, model := planningHTTPRoot(t, e)
	ctx := context.Background()
	settings := map[string]any{"country": "US", "region": "Texas", "place": "Fixture garden", "timezone": "America/Chicago", "enabled": true, "expectedRevision": 0}
	r := e.gql(`mutation($input:SaveGardenKnowledgeSettingsInput!){saveGardenKnowledgeSettings(input:$input){settings{revision} problems{code}}}`, map[string]any{"input": settings})
	if ps := payloadProblems(t, r, "saveGardenKnowledgeSettings"); len(ps) != 0 {
		t.Fatal(ps)
	}
	plan := plantingPayload(t, plantingOp(t, e, "CreatePlantingPlan", map[string]any{"input": map[string]any{"name": "Future fig", "category": "FIG", "method": "NURSERY_PLANT"}}), "createPlantingPlan")
	id := plan["id"].(string)
	if err := root.Planning.SweepGuidance(ctx); err != nil {
		t.Fatal(err)
	}
	vars := map[string]any{"planId": id, "input": map[string]any{"fromMonth": "2027-01", "toMonth": "2027-12"}, "first": 1}
	r = plantingOp(t, e, "PlantingSchedule", vars)
	page := dataOf(t, r, "plantingSchedule")
	if page["guidanceCurrent"] != true || page["targetStatus"] != "UNSCHEDULED" {
		t.Fatal(r.Raw)
	}
	items := page["items"].([]any)
	window := items[0].(map[string]any)
	if window["year"] != float64(2026) || window["resolution"] != "RESOLVED" || window["continuesBefore"] != true || window["selectable"] != true {
		t.Fatal(window)
	}
	period := window["period"].(map[string]any)
	if period["precision"] != "MONTH_RANGE" || period["dates"] != nil || period["months"].(map[string]any)["from"] != "2026-12" {
		t.Fatal(period)
	}
	pageInfo := page["pageInfo"].(map[string]any)
	if pageInfo["hasNextPage"] != true || pageInfo["endCursor"] == nil {
		t.Fatal(pageInfo)
	}
	vars["after"] = pageInfo["endCursor"]
	second := dataOf(t, plantingOp(t, e, "PlantingSchedule", vars), "plantingSchedule")
	if second["items"].([]any)[0].(map[string]any)["year"] != float64(2027) || second["pageInfo"].(map[string]any)["hasNextPage"] != false {
		t.Fatal(second)
	}
	input := map[string]any{"planId": id, "expectedRevision": page["revision"], "snapshotId": page["sourceSnapshotId"], "generation": page["guidanceGeneration"], "factId": window["factId"], "windowIndex": window["windowIndex"], "year": window["year"], "targetPeriod": map[string]any{"precision": "DATE_RANGE", "dates": map[string]any{"from": "2027-01-17", "to": "2027-01-17"}}}
	confirmed := plantingPayload(t, plantingOp(t, e, "ConfirmPlantingSchedule", map[string]any{"input": input, "clientMutationId": "confirm-HTTP"}), "confirmPlantingSchedule")
	choice := confirmed["acceptedSchedule"].(map[string]any)
	if choice["supportedPeriod"].(map[string]any)["precision"] != "MONTH_RANGE" || choice["assumptions"].(map[string]any)["hemisphere"] != "NORTHERN" || confirmed["acceptedGuidance"] == nil || model.calls.Load() != 1 {
		t.Fatal(confirmed)
	}
	if confirmed["targetPeriod"].(map[string]any)["dates"].(map[string]any)["from"] != "2027-01-17" || confirmed["state"] != "IDEA" {
		t.Fatal(confirmed)
	}
	// Exact replay still succeeds with its original expectedRevision.
	replay := plantingPayload(t, plantingOp(t, e, "ConfirmPlantingSchedule", map[string]any{"input": input, "clientMutationId": "confirm-HTTP"}), "confirmPlantingSchedule")
	if replay["revision"] != confirmed["revision"] {
		t.Fatal(replay)
	}
	bad := plantingOp(t, e, "ConfirmPlantingSchedule", map[string]any{"input": input})
	wantProblemCode(t, payloadProblems(t, bad, "confirmPlantingSchedule"), "CONFLICT")
	// An edit invalidates old pagination; reads do not trigger another model call.
	r = plantingOp(t, e, "PlantingSchedule", vars)
	if len(r.Errors) == 0 || model.calls.Load() != 1 {
		t.Fatal(r.Raw)
	}
	delete(vars, "after")
	r = plantingOp(t, e, "PlantingSchedule", vars)
	page = dataOf(t, r, "plantingSchedule")
	if page["acceptedSchedule"] == nil || page["targetPeriod"].(map[string]any)["dates"].(map[string]any)["from"] != "2027-01-17" {
		t.Fatal(r.Raw)
	}
}
func TestPlanningScheduleHTTPManualPathsAndInputBounds(t *testing.T) {
	e := newEnv(t, DefaultLimits())
	plan := plantingPayload(t, plantingOp(t, e, "CreatePlantingPlan", map[string]any{"input": map[string]any{"name": "Manual idea", "targetPeriod": map[string]any{"precision": "YEAR", "year": 2027}}}), "createPlantingPlan")
	id := plan["id"].(string)
	input := map[string]any{"fromMonth": "2027-01", "toMonth": "2027-12"}
	vars := map[string]any{"planId": id, "input": input}
	page := dataOf(t, plantingOp(t, e, "PlantingSchedule", vars), "plantingSchedule")
	if page["guidanceState"] != "DISABLED" || len(page["items"].([]any)) != 0 || page["targetPeriod"].(map[string]any)["year"] != float64(2027) {
		t.Fatal(page)
	}
	for _, bad := range []map[string]any{
		{"planId": id, "input": map[string]any{"fromMonth": "2027-01", "toMonth": "2028-01"}},
		{"planId": id, "input": input, "first": 0},
		{"planId": id, "input": input, "first": 101},
		{"planId": id, "input": input, "first": 4294967297},
		{"planId": id, "input": map[string]any{"fromMonth": "2027-01", "toMonth": "2027-12", "assumptions": map[string]any{"anchors": []any{map[string]any{"kind": "SOWING", "on": "2027-02-30"}}}}},
	} {
		r := plantingOp(t, e, "PlantingSchedule", bad)
		if len(r.Errors) == 0 {
			t.Fatal("bad schedule input accepted", r.Raw)
		}
	}
	r := plantingOp(t, e, "PlantingSchedule", map[string]any{"planId": "00000000-0000-4000-8000-000000000001", "input": input})
	if len(r.Errors) != 0 || r.Data["plantingSchedule"] != nil {
		t.Fatal("absent id was enumerable", r.Raw)
	}
	// The standard auth middleware still protects this additive read operation.
	e.cookie = nil
	r = plantingOp(t, e, "PlantingSchedule", vars)
	if r.Status != http.StatusUnauthorized && len(r.Errors) == 0 {
		t.Fatal("unauthenticated schedule readable", r.Raw)
	}
}
