// T28 acceptance at the contract surface: todayOverview is ONE bounded
// composition — due work (fixed window, fixed page size), the six newest
// journal lines, and workspace conditions with the frozen freshness bands.
// Zero-provider mode renders conditions null (never a placeholder); a
// quick completion keeps agenda and journal coherent in the same read.
package graphql

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	dbgen "canopy/server/db/generated"
	"canopy/server/internal/care"
	"canopy/server/internal/care/weather"
	"canopy/server/internal/platform"
)

const overviewSel = `query { todayOverview {
	dueTasks { items { id ruleKey status originalDueDate currentDueDate }
		pageInfo { hasNextPage endCursor } }
	recentEntries { items { __typename
		... on ActivityEntry { id summary activity { id kind } } }
		pageInfo { hasNextPage } }
	conditions { locationName snapshot { id provider freshness rainfallMm license } }
} }`

// overviewDailyWater seeds source -> published daily-water profile -> plant
// -> plan, so a water occurrence is due today. Returns the plant UUID.
func overviewDailyWater(t *testing.T, e *testEnv) pgtype.UUID {
	t.Helper()
	payload, probs := careMut(t, e, "createSource", "CreateSourceInput", map[string]any{
		"kind": "BOOK", "title": "Fig Watering Guide", "publisher": "UC ANR",
		"citation": "UC ANR, Fig Watering Guide", "summary": "Regional fig watering guidance.",
	}, "source { id currentVersion { id } }")
	noProbs(t, "createSource", probs)
	vid := mustID(t, payload["source"].(map[string]any)["currentVersion"].(map[string]any), "id")

	everyday := []string{"MONDAY", "TUESDAY", "WEDNESDAY", "THURSDAY",
		"FRIDAY", "SATURDAY", "SUNDAY"}
	payload, probs = careMut(t, e, "createCareProfileDraft", "CreateCareProfileDraftInput", map[string]any{
		"category": "FIG", "rules": []any{
			map[string]any{"ruleKey": "water", "kind": "ACTION", "activityKind": "WATER",
				"recurrence":       map[string]any{"weeklyDays": map[string]any{"weekdays": everyday}},
				"guidanceText":     "Deep soak; let the top few inches dry between waterings.",
				"sourceVersionIds": []string{vid}},
			// Second daily rule: 2 rules x 8 in-window days = 16 due rows, so
			// the fixed page of 10 must actually cut and report hasNextPage.
			map[string]any{"ruleKey": "observe", "kind": "ACTION", "activityKind": "OBSERVE",
				"recurrence":       map[string]any{"weeklyDays": map[string]any{"weekdays": everyday}},
				"guidanceText":     "Glance at leaf color and posture.",
				"sourceVersionIds": []string{vid}},
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

	payload, probs = careMut(t, e, "createCarePlan", "CreateCarePlanInput", map[string]any{
		"plantId": uuidString(plantID), "profileVersionId": pubVerID, "timezone": "UTC",
	}, "plan { id }")
	noProbs(t, "createCarePlan", probs)
	return plantID
}

func overviewData(t *testing.T, e *testEnv) map[string]any {
	t.Helper()
	r := e.gql(overviewSel, nil)
	if len(r.Errors) > 0 {
		t.Fatalf("todayOverview errors: %s", r.Raw)
	}
	return dataOf(t, r, "todayOverview")
}

func TestTodayOverviewZeroProviderBoundedAndCoherent(t *testing.T) {
	e := newEnv(t, DefaultLimits())
	overviewDailyWater(t, e)

	ov := overviewData(t, e)

	// Zero-provider mode: no location, no snapshot -> conditions null,
	// and the rest of the dashboard is still useful.
	if ov["conditions"] != nil {
		t.Fatalf("conditions must be null without a location, got %v", ov["conditions"])
	}
	due := ov["dueTasks"].(map[string]any)
	items := due["items"].([]any)
	if len(items) != 10 {
		t.Fatalf("dueTasks page size is fixed at 10, got %d", len(items))
	}
	if due["pageInfo"].(map[string]any)["hasNextPage"] != true {
		t.Fatal("two daily rules over the window must have more pages")
	}
	var firstID string
	for _, raw := range items {
		it := raw.(map[string]any)
		if it["ruleKey"] == "water" && it["status"] == "DUE" {
			firstID = mustID(t, it, "id")
			break
		}
	}
	if firstID == "" {
		t.Fatal("no open water occurrence in the due window")
	}
	entries := ov["recentEntries"].(map[string]any)["items"].([]any)
	if len(entries) != 0 {
		t.Fatalf("fresh workspace journal = %v, want empty", entries)
	}

	// Quick action through the frozen mutation, then the SAME read shows
	// both sides coherent: the occurrence leaves the due list and the
	// journal gains the activity the completion created atomically.
	payload, probs := careMut(t, e, "completeOccurrence", "CompleteOccurrenceInput", map[string]any{
		"occurrenceId": firstID, "expectedRevision": 1, "quantity": "2.5", "unit": "L",
	}, "occurrence { id status } activity { id kind }")
	noProbs(t, "completeOccurrence", probs)

	ov = overviewData(t, e)
	for _, raw := range ov["dueTasks"].(map[string]any)["items"].([]any) {
		if mustID(t, raw.(map[string]any), "id") == firstID {
			t.Fatal("completed occurrence must leave the due page")
		}
	}
	entries = ov["recentEntries"].(map[string]any)["items"].([]any)
	if len(entries) != 1 {
		t.Fatalf("journal entries = %d, want 1", len(entries))
	}
	en := entries[0].(map[string]any)
	if en["__typename"] != "ActivityEntry" {
		t.Fatalf("entry typename = %v, want ActivityEntry", en["__typename"])
	}
	if en["summary"] == "" || en["summary"] == nil {
		t.Fatalf("entry summary empty: %v", en)
	}
	act := en["activity"].(map[string]any)
	if act["kind"] != "WATER" {
		t.Fatalf("activity kind = %v, want WATER", act["kind"])
	}
	// The completion created the activity the journal shows — same id the
	// mutation returned.
	if mustID(t, act, "id") != mustID(t, payload["activity"].(map[string]any), "id") {
		t.Fatal("journal activity must be the atomically created activity")
	}
}

func TestTodayOverviewConditionsFreshThenStale(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t, DefaultLimits())
	plantID := overviewDailyWater(t, e)

	// Opt-in coordinates on a real location + plant context there.
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

	cond := overviewData(t, e)["conditions"]
	if cond == nil {
		t.Fatal("conditions must appear once a snapshot exists")
	}
	c := cond.(map[string]any)
	if c["locationName"] != "South yard" {
		t.Fatalf("locationName = %v, want the location's display name", c["locationName"])
	}
	s := c["snapshot"].(map[string]any)
	if s["id"] != uuidString(snap.ID) || s["provider"] != "open-meteo" {
		t.Fatalf("snapshot = %v, want the exact fetched snapshot", s)
	}
	if s["freshness"] != "FRESH" {
		t.Fatalf("freshness = %v, want FRESH for a just-fetched snapshot", s["freshness"])
	}
	if s["rainfallMm"] != "18.4" || s["license"] != weather.License {
		t.Fatalf("snapshot values = %v / %v", s["rainfallMm"], s["license"])
	}

	// Age the snapshot past the 6h FRESH band: the SAME read must say
	// STALE — the dashboard never implies freshness it does not have.
	if _, err := e.pool.Exec(ctx,
		"UPDATE weather_snapshots SET fetched_at = now() - interval '7 hours' WHERE id = $1",
		uuidString(snap.ID)); err != nil {
		t.Fatal(err)
	}
	s = overviewData(t, e)["conditions"].(map[string]any)["snapshot"].(map[string]any)
	if s["freshness"] != "STALE" {
		t.Fatalf("freshness = %v, want STALE after the band lapses", s["freshness"])
	}
}
