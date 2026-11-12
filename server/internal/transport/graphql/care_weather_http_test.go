// T26 acceptance at the contract surface: the agenda's typed reason
// carries a weatherSnapshot evidence entry pointing at the EXACT
// snapshot the advice was computed from — provenance included, freshness
// computed live against the engine bands. Provider traffic is the
// recorded-fixture stub; default tests never touch the network.
package graphql

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	dbgen "canopy/server/db/generated"
	"canopy/server/internal/care"
	"canopy/server/internal/care/weather"
	"canopy/server/internal/platform"
)

// weatherFixtureStub replays a recorded provider response over HTTP.
func weatherFixtureStub(t *testing.T, name string, status int) (*httptest.Server, *int) {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("..", "..", "..", "testdata", "weather", name))
	if err != nil {
		t.Fatalf("fixture: %v", err)
	}
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(status)
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	return srv, &calls
}

func TestCareAgendaWeatherEvidence(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t, DefaultLimits())

	payload, probs := careMut(t, e, "createSource", "CreateSourceInput", map[string]any{
		"kind": "EXTENSION_PUBLICATION", "title": "Fig Watering Guide",
		"publisher": "UC ANR", "citation": "UC ANR, Fig Watering Guide",
		"summary": "Regional fig watering guidance.",
	}, "source { id currentVersion { id } }")
	noProbs(t, "createSource", probs)
	vid := mustID(t, payload["source"].(map[string]any)["currentVersion"].(map[string]any), "id")

	everyday := []string{"MONDAY", "TUESDAY", "WEDNESDAY", "THURSDAY",
		"FRIDAY", "SATURDAY", "SUNDAY"}
	payload, probs = careMut(t, e, "createCareProfileDraft", "CreateCareProfileDraftInput", map[string]any{
		"category": "FIG", "rules": []any{
			map[string]any{"ruleKey": "water", "kind": "ACTION", "activityKind": "WATER",
				"recurrence":         map[string]any{"weeklyDays": map[string]any{"weekdays": everyday}},
				"weatherSensitivity": "RAINFALL",
				"guidanceText":       "Deep soak; let the top few inches dry between waterings.",
				"sourceVersionIds":   []string{vid}},
		},
	}, "profile { id revision currentVersion { id } }")
	noProbs(t, "createCareProfileDraft", probs)
	prof := payload["profile"].(map[string]any)
	payload, probs = careMut(t, e, "publishCareProfile", "PublishCareProfileInput", map[string]any{
		"profileId": mustID(t, prof, "id"), "expectedRevision": int(prof["revision"].(float64)),
		"reviewerLabel": "owner", "reviewedOn": "2026-11-02",
	}, "profile { currentVersion { id } }")
	noProbs(t, "publishCareProfile", probs)
	pubVerID := mustID(t, payload["profile"].(map[string]any)["currentVersion"].(map[string]any), "id")

	payload, probs = careMut(t, e, "createPlant", "CreatePlantInput", map[string]any{
		"name": "Mission fig", "type": "INDIVIDUAL", "category": "FIG",
	}, "plant { id }")
	noProbs(t, "createPlant", probs)
	var plantID pgtype.UUID
	if err := plantID.Scan(mustID(t, payload["plant"].(map[string]any), "id")); err != nil {
		t.Fatal(err)
	}

	// Owner-entered coordinates (opt-in) + current context at that location.
	ids := platform.UUIDv4{}
	loc, err := e.q.InsertLocation(ctx, dbgen.InsertLocationParams{
		ID: ids.New(), WorkspaceID: e.wsID, Name: "South yard",
		Situation: dbgen.LocationSituationOUTDOOR,
	})
	if err != nil {
		t.Fatal(err)
	}
	var lat, lon pgtype.Numeric
	if err := lat.Scan("34.0522"); err != nil {
		t.Fatal(err)
	}
	if err := lon.Scan("-118.2437"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.q.UpdateLocation(ctx, dbgen.UpdateLocationParams{
		ID: loc.ID, WorkspaceID: e.wsID, ExpectedRevision: loc.Revision,
		Latitude: &lat, Longitude: &lon,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.q.InsertPlantContext(ctx, dbgen.InsertPlantContextParams{
		ID: ids.New(), WorkspaceID: e.wsID, PlantID: plantID,
		EffectiveFrom: pgtype.Timestamptz{Time: time.Now().Add(-24 * time.Hour), Valid: true},
		LocationID:    loc.ID, Placement: dbgen.ContextPlacementGROUND,
	}); err != nil {
		t.Fatal(err)
	}

	// Worker pass over the recorded fixture, then the plan: materialized
	// occurrences must reference the snapshot that exists at compute time.
	stub, n := weatherFixtureStub(t, "openmeteo-rain.json", 200)
	svc := care.New(e.pool, slog.New(slog.DiscardHandler))
	svc.Weather = weather.New(stub.URL, 5*time.Second)
	if err := svc.RunWeatherFetch(ctx, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if *n != 1 {
		t.Fatalf("fetches = %d, want 1", *n)
	}
	snap, err := e.q.LatestWeatherSnapshot(ctx, dbgen.LatestWeatherSnapshotParams{
		WorkspaceID: e.wsID, LocationID: loc.ID})
	if err != nil {
		t.Fatal(err)
	}

	payload, probs = careMut(t, e, "createCarePlan", "CreateCarePlanInput", map[string]any{
		"plantId": uuidString(plantID), "profileVersionId": pubVerID, "timezone": "UTC",
	}, "plan { id }")
	noProbs(t, "createCarePlan", probs)

	r := e.gql(`query($from: LocalDate, $to: LocalDate, $first: Int) {
		careAgenda(from: $from, to: $to, first: $first) { items {
			id ruleKey status reason { text evidence {
				note
				weatherSnapshot { id locationId provider fetchedAt validFrom validUntil
					highTempC lowTempC rainfallMm et0Mm license freshness }
			} missingFacts } } } }`, agendaWindow())
	if len(r.Errors) > 0 {
		t.Fatalf("careAgenda errors: %s", r.Raw)
	}
	items := dataOf(t, r, "careAgenda")["items"].([]any)
	if len(items) == 0 {
		t.Fatal("agenda empty")
	}
	var reason map[string]any
	for _, raw := range items {
		it := raw.(map[string]any)
		if it["ruleKey"] == "water" && it["status"] == "DUE" {
			reason = it["reason"].(map[string]any)
			break
		}
	}
	if reason == nil {
		t.Fatal("no open water occurrence")
	}
	ev := reason["evidence"].([]any)
	if len(ev) == 0 {
		t.Fatalf("evidence empty: %v", reason)
	}
	ws := ev[0].(map[string]any)["weatherSnapshot"]
	if ws == nil {
		t.Fatalf("first evidence entry must be the weather snapshot, got %v", ev[0])
	}
	s := ws.(map[string]any)
	if s["id"] != uuidString(snap.ID) || s["locationId"] != uuidString(loc.ID) {
		t.Fatalf("evidence must reference the EXACT snapshot %s, got %v", uuidString(snap.ID), s)
	}
	if s["provider"] != "open-meteo" || s["license"] != weather.License {
		t.Fatalf("provenance: %v / %v", s["provider"], s["license"])
	}
	if s["freshness"] != "FRESH" {
		t.Fatalf("freshness = %v, want FRESH for a just-fetched snapshot", s["freshness"])
	}
	if s["rainfallMm"] != "18.4" {
		t.Fatalf("rainfallMm = %v, want 18.4", s["rainfallMm"])
	}
	// The snapshot fed the advice itself (engine FRESH band).
	found := false
	for _, entry := range ev {
		if note, ok := entry.(map[string]any)["note"].(string); ok && note == "reported rainfall 18.4mm" {
			found = true
		}
	}
	if !found {
		t.Fatalf("evidence notes = %v, want the fresh snapshot's rainfall note", ev)
	}
}
