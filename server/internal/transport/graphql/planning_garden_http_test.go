package graphql

import "testing"

func TestPlanningGardenNamedCommandsOverHTTP(t *testing.T) {
	e := newEnv(t, DefaultLimits())
	p := plantingPayload(t, plantingOp(t, e, "CreatePlantingPlan", map[string]any{"input": map[string]any{"name": "Future patch", "desiredQuantity": 6, "budget": map[string]any{"minorUnits": "15000", "currency": "USD"}}}), "createPlantingPlan")
	p = plantingPayload(t, plantingOp(t, e, "CreatePlantingAction", map[string]any{"input": map[string]any{"planId": p["id"], "expectedRevision": p["revision"], "kind": "PLANT_OUT", "title": "Plant later", "scheduledOn": "2027-03-01"}}), "createPlantingAction")
	action := p["actions"].([]any)[0].(map[string]any)
	in := map[string]any{"planId": p["id"], "expectedRevision": p["revision"], "newPlant": map[string]any{"name": "Actual patch", "type": "GROUP", "category": "STRAWBERRY", "initialContext": map[string]any{"placement": "CONTAINER"}}}
	vars := map[string]any{"input": in, "clientMutationId": "single-conversion"}
	p = plantingPayload(t, plantingOp(t, e, "AddPlantingToGarden", vars), "addPlantingToGarden")
	link := p["fulfillment"].(map[string]any)
	plantID := link["plant"].(map[string]any)["id"]
	replay := plantingPayload(t, plantingOp(t, e, "AddPlantingToGarden", vars), "addPlantingToGarden")
	if replay["fulfillment"].(map[string]any)["id"] != link["id"] || p["state"] != "IN_PROGRESS" || p["actions"].([]any)[0].(map[string]any)["state"] != "OPEN" {
		t.Fatal(p, replay)
	}
	actual := dataOf(t, e.gql(`query($id:ID!){plant(id:$id){id revision count plantedOn currentContext{placement}}}`, map[string]any{"id": plantID}), "plant")
	if actual["count"] != nil || actual["plantedOn"] != nil || actual["currentContext"].(map[string]any)["placement"] != "CONTAINER" {
		t.Fatal(actual)
	}
	complete := map[string]any{"planId": p["id"], "id": action["id"], "expectedRevision": p["revision"], "completedOn": "2026-11-10", "expectedPlantRevision": actual["revision"], "actualContext": map[string]any{"placement": "GROUND"}}
	p = plantingPayload(t, plantingOp(t, e, "CompletePlantingAction", map[string]any{"input": complete}), "completePlantingAction")
	if p["actions"].([]any)[0].(map[string]any)["completedOn"] != "2026-11-10" || p["state"] != "IN_PROGRESS" {
		t.Fatal(p)
	}
	p = plantingPayload(t, plantingOp(t, e, "CreatePlantingAction", map[string]any{"input": map[string]any{"planId": p["id"], "expectedRevision": p["revision"], "kind": "BUY", "title": "Not needed", "scheduledOn": "2027-03-02"}}), "createPlantingAction")
	second := p["actions"].([]any)[1].(map[string]any)
	p = plantingPayload(t, plantingOp(t, e, "DismissPlantingAction", map[string]any{"input": map[string]any{"planId": p["id"], "id": second["id"], "expectedRevision": p["revision"]}}), "dismissPlantingAction")
	if p["actions"].([]any)[1].(map[string]any)["state"] != "DISMISSED" {
		t.Fatal(p)
	}
}
func TestPlanningGardenRejectsCountOverflowBeforeNarrowing(t *testing.T) {
	e := newEnv(t, DefaultLimits())
	p := plantingPayload(t, plantingOp(t, e, "CreatePlantingPlan", map[string]any{"input": map[string]any{"name": "Future fig"}}), "createPlantingPlan")
	r := plantingOp(t, e, "AddPlantingToGarden", map[string]any{"input": map[string]any{"planId": p["id"], "expectedRevision": 1, "newPlant": map[string]any{"name": "Actual fig", "type": "GROUP", "category": "FIG", "count": 4294967297}}})
	wantProblemCode(t, payloadProblems(t, r, "addPlantingToGarden"), "VALIDATION")
}
