package graphql

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"canopy/server/internal/platform"
	"canopy/server/internal/transport/graphql/gql"
	httpx "canopy/server/internal/transport/http"
)

// Exercise the accepted client operations, not simplified copies. The request
// selects its operationName from the complete typed document.
func plantingOp(t *testing.T, e *testEnv, name string, vars map[string]any) gqlResp {
	t.Helper()
	source, err := os.ReadFile("../../../../contracts/operations/planning.graphql")
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(map[string]any{"query": string(source), "operationName": name, "variables": vars})
	if err != nil {
		t.Fatal(err)
	}
	response := e.raw("POST", "/api/graphql", string(data), "application/json", e.cookie, e.csrf)
	raw := readAll(t, response)
	var out gqlResp
	if err = json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatal(err)
	}
	out.Status = response.StatusCode
	out.Raw = raw
	return out
}
func plantingPayload(t *testing.T, r gqlResp, field string) map[string]any {
	t.Helper()
	if ps := payloadProblems(t, r, field); len(ps) != 0 {
		t.Fatalf("unexpected problems: %s", r.Raw)
	}
	return dataOf(t, r, field)["plan"].(map[string]any)
}
func TestPlanningFrozenOperationsOverHTTP(t *testing.T) {
	e := newEnv(t, DefaultLimits())
	plan := plantingPayload(t, plantingOp(t, e, "CreatePlantingPlan", map[string]any{"input": map[string]any{"name": " Future fig "}, "clientMutationId": "idea"}), "createPlantingPlan")
	id := mustID(t, plan, "id")
	for _, field := range []string{"budget", "desiredQuantity", "method", "targetPeriod", "scientificName", "intendedLocation", "fulfillment"} {
		if plan[field] != nil {
			t.Fatalf("unknown %s became %v", field, plan[field])
		}
	}
	if plan["name"] != "Future fig" || plan["state"] != "IDEA" || plan["revision"] != float64(1) {
		t.Fatal(plan)
	}
	update := map[string]any{"id": id, "expectedRevision": 1, "name": "Future fig", "category": "FIG", "targetPeriod": map[string]any{"precision": "YEAR", "year": 2027}, "budget": map[string]any{"minorUnits": "0", "currency": "USD"}}
	plan = plantingPayload(t, plantingOp(t, e, "UpdatePlantingPlan", map[string]any{"input": update}), "updatePlantingPlan")
	target := plan["targetPeriod"].(map[string]any)
	if target["year"] != float64(2027) || target["dates"] != nil || target["months"] != nil {
		t.Fatal("coarse date changed", target)
	}
	action := map[string]any{"planId": id, "expectedRevision": plan["revision"], "kind": "PLANT_OUT", "title": "Plant after review", "scheduledOn": "2027-03-14", "reminderOn": "2027-03-13"}
	plan = plantingPayload(t, plantingOp(t, e, "CreatePlantingAction", map[string]any{"input": action, "clientMutationId": "action"}), "createPlantingAction")
	actions := plan["actions"].([]any)
	a := actions[0].(map[string]any)
	aid := mustID(t, a, "id")
	if a["scheduledOn"] != "2027-03-14" || a["reminderOn"] != "2027-03-13" || a["completedOn"] != nil || plan["state"] != "IDEA" {
		t.Fatal(plan)
	}
	action["id"] = aid
	action["expectedRevision"] = plan["revision"]
	action["title"] = "Review the site"
	plan = plantingPayload(t, plantingOp(t, e, "UpdatePlantingAction", map[string]any{"input": action}), "updatePlantingAction")
	if plan["actions"].([]any)[0].(map[string]any)["scheduleRevision"] != float64(1) {
		t.Fatal("title bumped schedule")
	}
	plan = plantingPayload(t, plantingOp(t, e, "ChangePlantingPlanState", map[string]any{"input": map[string]any{"id": id, "expectedRevision": plan["revision"], "state": "IN_PROGRESS"}}), "changePlantingPlanState")
	plan = plantingPayload(t, plantingOp(t, e, "CancelPlantingPlan", map[string]any{"input": map[string]any{"id": id, "expectedRevision": plan["revision"]}}), "cancelPlantingPlan")
	if plan["state"] != "CANCELLED" || plan["cancelledAt"] == nil {
		t.Fatal(plan)
	}
	plan = plantingPayload(t, plantingOp(t, e, "RestorePlantingPlan", map[string]any{"input": map[string]any{"id": id, "expectedRevision": plan["revision"]}}), "restorePlantingPlan")
	if plan["state"] != "IN_PROGRESS" || plan["cancelledAt"] != nil {
		t.Fatal(plan)
	}
	blocked := plantingOp(t, e, "ChangePlantingPlanState", map[string]any{"input": map[string]any{"id": id, "expectedRevision": plan["revision"], "state": "COMPLETED"}})
	wantProblemCode(t, payloadProblems(t, blocked, "changePlantingPlanState"), "VALIDATION")
	plan = plantingPayload(t, plantingOp(t, e, "DeletePlantingAction", map[string]any{"input": map[string]any{"planId": id, "id": aid, "expectedRevision": plan["revision"]}}), "deletePlantingAction")
	plan = plantingPayload(t, plantingOp(t, e, "ChangePlantingPlanState", map[string]any{"input": map[string]any{"id": id, "expectedRevision": plan["revision"], "state": "COMPLETED"}}), "changePlantingPlanState")
	if plan["completedAt"] == nil {
		t.Fatal("no completion time")
	}
	detail := dataOf(t, plantingOp(t, e, "PlantingPlanDetail", map[string]any{"id": id}), "plantingPlan")
	if detail["latestGuidance"] != nil || detail["acceptedGuidance"] != nil {
		t.Fatal("invented knowledge")
	}
	active := dataOf(t, plantingOp(t, e, "PlantingPlans", nil), "plantingPlans")["items"].([]any)
	if len(active) != 0 {
		t.Fatal("completed plan in active defaults")
	}
	completed := dataOf(t, plantingOp(t, e, "PlantingPlans", map[string]any{"filter": map[string]any{"states": []string{"COMPLETED"}}}), "plantingPlans")["items"].([]any)
	if len(completed) != 1 {
		t.Fatal("completed filter missing plan")
	}
	var plants, purchases, activities int
	if err := e.pool.QueryRow(context.Background(), `SELECT (SELECT count(*) FROM plants),(SELECT count(*) FROM purchases),(SELECT count(*) FROM activities)`).Scan(&plants, &purchases, &activities); err != nil || plants+purchases+activities != 0 {
		t.Fatal("planner wrote actual garden records", err)
	}
}
func TestPlanningHTTPValidationReplayAndIsolation(t *testing.T) {
	e := newEnv(t, DefaultLimits())
	first := map[string]any{"name": "Berry idea"}
	plan := plantingPayload(t, plantingOp(t, e, "CreatePlantingPlan", map[string]any{"input": first, "clientMutationId": "once"}), "createPlantingPlan")
	id := mustID(t, plan, "id")
	replay := plantingPayload(t, plantingOp(t, e, "CreatePlantingPlan", map[string]any{"input": first, "clientMutationId": "once"}), "createPlantingPlan")
	if replay["id"] != id {
		t.Fatal("HTTP replay duplicated plan")
	}
	conflict := plantingOp(t, e, "CreatePlantingPlan", map[string]any{"input": map[string]any{"name": "Other idea"}, "clientMutationId": "once"})
	wantProblemCode(t, payloadProblems(t, conflict, "createPlantingPlan"), "IDEMPOTENCY_CONFLICT")
	for _, in := range []map[string]any{
		{"name": " ", "state": "IDEA"}, {"name": "Invalid", "state": "COMPLETED"},
		{"name": "Invalid", "desiredQuantity": 4294967297},
		{"name": "Invalid", "targetPeriod": map[string]any{"precision": "YEAR", "year": 2027, "months": map[string]any{"from": "2027-01", "to": "2027-02"}}},
		{"name": "Invalid", "budget": map[string]any{"minorUnits": "1000000000000001", "currency": "USD"}},
	} {
		r := plantingOp(t, e, "CreatePlantingPlan", map[string]any{"input": in})
		wantProblemCode(t, payloadProblems(t, r, "createPlantingPlan"), "VALIDATION")
	}
	for _, revision := range []int{0, 2, 4294967297} {
		r := plantingOp(t, e, "UpdatePlantingPlan", map[string]any{"input": map[string]any{"id": id, "expectedRevision": revision, "name": "Changed"}})
		ps := payloadProblems(t, r, "updatePlantingPlan")
		code := "VALIDATION"
		if revision == 2 {
			code = "CONFLICT"
		}
		wantProblemCode(t, ps, code)
	}
	for _, first := range []int{0, -1, 101} {
		r := e.gql(`query($first:Int){plantingPlans(first:$first){items{id}}}`, map[string]any{"first": first})
		if len(r.Errors) == 0 {
			t.Fatal("invalid page size", r.Raw)
		}
	}
	empty := dataOf(t, plantingOp(t, e, "PlantingPlans", map[string]any{"filter": map[string]any{"states": []string{}}}), "plantingPlans")["items"].([]any)
	if len(empty) != 0 {
		t.Fatal("empty state filter defaulted")
	}
	r := plantingOp(t, e, "CreatePlantingAction", map[string]any{"input": map[string]any{"planId": id, "expectedRevision": 1, "kind": "BUY", "title": "Bad day", "scheduledOn": "2027-02-29"}})
	if len(r.Errors) == 0 {
		t.Fatal("invalid scalar date accepted", r.Raw)
	}
	// Missing/foreign IDs return the same null shape; query result never exposes a
	// different workspace's existence.
	otherID := platform.UUIDv4{}.New()
	if _, err := e.pool.Exec(context.Background(), `INSERT INTO owner(id,password_hash) VALUES($1,'fixture')`, otherID); err != nil {
		t.Fatal(err)
	}
	otherWS := platform.UUIDv4{}.New()
	foreignPlan := platform.UUIDv4{}.New()
	if _, err := e.pool.Exec(context.Background(), `INSERT INTO workspace(id,owner_id,display_name,timezone,default_currency) VALUES($1,$2,'Other','UTC','USD')`, otherWS, otherID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.pool.Exec(context.Background(), `INSERT INTO planting_plans(id,workspace_id,name) VALUES($1,$2,'Private')`, foreignPlan, otherWS); err != nil {
		t.Fatal(err)
	}
	for _, uid := range []string{uuidString(foreignPlan), uuidString(platform.UUIDv4{}.New()), "bad-id"} {
		r := plantingOp(t, e, "PlantingPlanDetail", map[string]any{"id": uid})
		if len(r.Errors) > 0 || r.Data["plantingPlan"] != nil {
			t.Fatal("enumerable id", r.Raw)
		}
	}
	r = plantingOp(t, e, "UpdatePlantingPlan", map[string]any{"input": map[string]any{"id": uuidString(foreignPlan), "expectedRevision": 1, "name": "Attack"}})
	wantProblemCode(t, payloadProblems(t, r, "updatePlantingPlan"), "NOT_FOUND")
	for _, planID := range []string{"bad-id", uuidString(platform.UUIDv4{}.New())} {
		r := plantingOp(t, e, "CreatePlantingAction", map[string]any{"input": map[string]any{"planId": planID, "expectedRevision": 1, "kind": "BUY", "title": "Buy later", "scheduledOn": "2027-01-01"}})
		ps := payloadProblems(t, r, "createPlantingAction")
		if len(ps) != 1 || ps[0]["field"] != "input.planId" {
			t.Fatal("parent error bound to wrong form field", r.Raw)
		}
	}
	unauth := e.gqlAs(nil, "", `query{plantingPlans{items{id}}}`, nil)
	if unauth.Status != http.StatusUnauthorized {
		t.Fatal("unauthenticated read", unauth.Raw)
	}
	// Regular garden members can plan; these are not administrator settings.
	log := slog.New(slog.DiscardHandler)
	h := NewHandler(NewResolverFromPool(e.pool, log), DefaultLimits(), log)
	body := []byte(`{"query":"mutation{createPlantingPlan(input:{name:\"Member idea\"}){plan{id} problems{code}}}"}`)
	request := httptest.NewRequest("POST", "/api/graphql", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request = request.WithContext(httpx.WithPrincipal(request.Context(), httpx.Principal{WorkspaceID: e.wsID, OwnerID: e.ownerID, Role: "MEMBER"}))
	recorder := httptest.NewRecorder()
	h.ServeHTTP(recorder, request)
	if recorder.Code != 200 || strings.Contains(recorder.Body.String(), `"errors"`) || strings.Contains(recorder.Body.String(), `FORBIDDEN`) {
		t.Fatal("member planning rejected", recorder.Body.String())
	}
}
func TestPlanningGuidanceDatesAndAcceptedGenerationOverHTTP(t *testing.T) {
	e := newEnv(t, DefaultLimits())
	plan := plantingPayload(t, plantingOp(t, e, "CreatePlantingPlan", map[string]any{"input": map[string]any{"name": "Future fig", "targetPeriod": map[string]any{"precision": "YEAR", "year": 2027}}}), "createPlantingPlan")
	id := mustID(t, plan, "id")
	source, err := os.ReadFile("../../../../contracts/fixtures/api/planning-annual-guidance.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Data struct {
			Plan struct {
				Latest struct {
					Facts json.RawMessage `json:"facts"`
				} `json:"latestGuidance"`
			} `json:"plantingPlan"`
		} `json:"data"`
	}
	if err = json.Unmarshal(source, &fixture); err != nil {
		t.Fatal(err)
	}
	snapshot := platform.UUIDv4{}.New()
	if _, err = e.pool.Exec(context.Background(), `INSERT INTO planting_guidance_snapshots(id,workspace_id,plan_id,generation,context_sha256,state,facts,retrieved_at) VALUES($1,$2,$3,1,repeat('a',64),'READY',$4,'2026-11-14T12:00:00Z')`, snapshot, e.wsID, id, fixture.Data.Plan.Latest.Facts); err != nil {
		t.Fatal(err)
	}
	if _, err = e.pool.Exec(context.Background(), `UPDATE planting_plans SET accepted_guidance_id=$1,guidance_generation=2 WHERE id=$2`, snapshot, id); err != nil {
		t.Fatal(err)
	}
	detail := dataOf(t, plantingOp(t, e, "PlantingPlanDetail", map[string]any{"id": id}), "plantingPlan")
	if detail["latestGuidance"] != nil || detail["guidanceGeneration"] != float64(2) {
		t.Fatal("used stale latest guidance", detail)
	}
	accepted := detail["acceptedGuidance"].(map[string]any)
	fact := accepted["facts"].([]any)[0].(map[string]any)
	if fact["retrievedAt"] != "2026-11-14T12:00:00Z" {
		t.Fatal("source time did not round trip", fact)
	}
	annual := fact["windows"].([]any)[0].(map[string]any)["annual"].(map[string]any)
	if annual["months"].(map[string]any)["from"] != float64(12) || annual["months"].(map[string]any)["to"] != float64(2) {
		t.Fatal("year-free cross-year window lost", annual)
	}
}
func TestPlanningGuidanceShapeBounds(t *testing.T) {
	good := &gql.PlantingAnnualPeriod{Precision: gql.PlantingAnnualPrecisionDateRange, Dates: &gql.PlantingAnnualDateRange{From: &gql.PlantingMonthDay{Month: 2, Day: 29}, To: &gql.PlantingMonthDay{Month: 3, Day: 1}}}
	if !validPlantingAnnual(good) {
		t.Fatal("annual leap day must remain representable")
	}
	good.Dates.From.Day = 30
	if validPlantingAnnual(good) {
		t.Fatal("invalid annual day")
	}
	if validPlantingAnnual(&gql.PlantingAnnualPeriod{Precision: gql.PlantingAnnualPrecisionMonthRange, Months: &gql.PlantingAnnualMonthRange{From: 0, To: 12}}) {
		t.Fatal("invalid annual month")
	}
}
