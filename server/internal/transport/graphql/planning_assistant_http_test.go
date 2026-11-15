package graphql

import "testing"

func TestPlanningAssistantNamedManualOperationNeverWrites(t *testing.T) {
	e := newEnv(t, DefaultLimits())
	p := plantingPayload(t, plantingOp(t, e, "CreatePlantingPlan", map[string]any{"input": map[string]any{"name": "Manual idea"}}), "createPlantingPlan")
	result := dataOf(t, plantingOp(t, e, "AskPlantingPlanner", map[string]any{"question": "When can I start?", "planIds": []any{p["id"]}, "year": 2027}), "askPlantingPlanner")
	if result["isAI"] != false || result["note"] != nil || len(result["sources"].([]any)) != 0 {
		t.Fatal(result)
	}
	for _, query := range []string{`mutation{a:askPlantingPlanner(question:"One?",planIds:["00000000-0000-0000-0000-000000000000"],year:2027){answer} b:askGarden(question:"Two?",year:2027){answer}}`, `mutation{...Questions} fragment Questions on Mutation{a:askPlantingPlanner(question:"One?",planIds:["00000000-0000-0000-0000-000000000000"],year:2027){answer} b:askPlantingPlanner(question:"Two?",planIds:["00000000-0000-0000-0000-000000000000"],year:2027){answer}}`} {
		r := e.gql(query, nil)
		if len(r.Errors) == 0 {
			t.Fatal("multiple model calls accepted", r.Raw)
		}
	}
}
