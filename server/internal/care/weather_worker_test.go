package care

// T26 acceptance suite for the weather fetch worker: provider mapping +
// provenance land as immutable snapshot rows, the 6h cache window keeps
// a location from being re-fetched, provider failure skips (care is
// never gated on the forecast API), retention sweeps expired rows, and
// materialized occurrences reference the EXACT snapshot their advice was
// computed from. All provider traffic is a recorded-fixture httptest
// stub — default tests never touch the network.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	dbgen "canopy/server/db/generated"
	"canopy/server/internal/care/weather"
	"canopy/server/internal/platform"
)

// fixtureStub replays a recorded provider response; status lets the
// same helper model provider failure without touching the network.
func fixtureStub(t *testing.T, name string, status int) (*httptest.Server, *int) {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("..", "..", "testdata", "weather", name))
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

func snapCount(t *testing.T, pool *pgxpool.Pool, ws pgtype.UUID) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(),
		"SELECT count(*) FROM weather_snapshots WHERE workspace_id = $1", ws).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func coord(t *testing.T, v float64) *pgtype.Numeric {
	t.Helper()
	f := v
	n := numericOf(&f)
	if n == nil {
		t.Fatalf("numeric for %v", v)
	}
	return n
}

func TestWeatherFetchCacheFailureRetention(t *testing.T) {
	ctx, pool, a := scratch(t)
	s := svc(pool)
	srv, calls := fixtureStub(t, "openmeteo-rain.json", 200)
	s.Weather = weather.New(srv.URL, 5*time.Second)

	ids := platform.UUIDv4{}
	q := dbgen.New(pool)
	loc, err := q.InsertLocation(ctx, dbgen.InsertLocationParams{
		ID: ids.New(), WorkspaceID: a.WorkspaceID, Name: "Patio",
		Situation: dbgen.LocationSituationOUTDOOR,
		Latitude:  coord(t, 34.0522), Longitude: coord(t, -118.2437),
	})
	if err != nil {
		t.Fatalf("location: %v", err)
	}
	// A second location WITHOUT coordinates: never fetched (opt-in).
	if _, err := q.InsertLocation(ctx, dbgen.InsertLocationParams{
		ID: ids.New(), WorkspaceID: a.WorkspaceID, Name: "Unmapped shelf",
		Situation: dbgen.LocationSituationINDOOR,
	}); err != nil {
		t.Fatalf("location2: %v", err)
	}

	now := time.Now().UTC().Truncate(time.Second)
	if err := s.RunWeatherFetch(ctx, now); err != nil {
		t.Fatal(err)
	}
	if *calls != 1 {
		t.Fatalf("fetches = %d, want 1 (only the mapped location is opt-in)", *calls)
	}
	snap, err := q.LatestWeatherSnapshot(ctx, dbgen.LatestWeatherSnapshotParams{
		WorkspaceID: a.WorkspaceID, LocationID: loc.ID})
	if err != nil {
		t.Fatalf("snapshot row: %v", err)
	}
	if snap.Provider != weather.Provider || !snap.License.Valid || snap.License.String != weather.License {
		t.Fatalf("provenance: %q / %q", snap.Provider, snap.License.String)
	}
	if !snap.FetchedAt.Time.Equal(now) {
		t.Fatalf("fetchedAt = %v, want %v", snap.FetchedAt.Time, now)
	}
	// provider-local civil day window from the recorded fixture (-08:00):
	// 2026-11-02T00:00:00-08:00 through the following midnight
	wantFrom, _ := time.Parse(time.RFC3339, "2026-11-02T00:00:00-08:00")
	if !snap.ValidFrom.Time.Equal(wantFrom) ||
		!snap.ValidUntil.Time.Equal(wantFrom.AddDate(0, 0, 1)) {
		t.Fatalf("window = %v..%v", snap.ValidFrom.Time, snap.ValidUntil.Time)
	}
	if rf, _ := snap.RainfallMm.Float64Value(); rf.Float64 != 18.4 {
		t.Fatalf("rainfall = %v, want 18.4", rf)
	}
	if !snap.ExpiresAt.Time.Equal(now.Add(7 * 24 * time.Hour)) {
		t.Fatalf("expiresAt = %v, want fetchedAt+7d", snap.ExpiresAt.Time)
	}

	// Cache: inside the 6h freshness window the location is not re-fetched.
	if err := s.RunWeatherFetch(ctx, now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if *calls != 1 {
		t.Fatalf("cache miss: %d fetches inside the fresh window", *calls)
	}

	// Past the window: one more fetch, one more immutable row.
	if err := s.RunWeatherFetch(ctx, now.Add(7*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if *calls != 2 {
		t.Fatalf("fetches = %d, want 2 after the window", *calls)
	}
	if n := snapCount(t, pool, a.WorkspaceID); n != 2 {
		t.Fatalf("snapshots = %d, want 2 immutable rows", n)
	}

	// Provider failure: the attempt is made, no row lands, the pass
	// succeeds — manual care is never gated on the forecast API.
	srv2, calls2 := fixtureStub(t, "openmeteo-sunny.json", 500)
	s.Weather = weather.New(srv2.URL, 5*time.Second)
	if err := s.RunWeatherFetch(ctx, now.Add(14*time.Hour)); err != nil {
		t.Fatalf("provider failure must not fail the pass: %v", err)
	}
	if *calls2 != 1 {
		t.Fatalf("failure pass should still attempt the fetch")
	}
	if n := snapCount(t, pool, a.WorkspaceID); n != 2 {
		t.Fatalf("snapshots = %d after provider failure, want 2 (no row from a 5xx)", n)
	}

	// Retention sweep: everything past expires_at is gone.
	if err := s.RunWeatherFetch(ctx, now.Add(8*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if n := snapCount(t, pool, a.WorkspaceID); n != 0 {
		t.Fatalf("snapshots = %d after retention sweep, want 0", n)
	}
}

// rainRule keeps requiredFacts empty so Advise reaches the weather notes
// (the waterRule helper abstains on REGION).
func rainRule(vid string) RuleInput {
	return RuleInput{
		RuleKey: "water-fig", Kind: "ACTION", ActivityKind: ptr("WATER"),
		Recurrence:         RecurrenceSpecInput{EveryNDays: &EveryNDaysInput{Days: 14, Anchor: "PLAN_START"}},
		WeatherSensitivity: ptr("RAINFALL"),
		GuidanceText:       "Deep soak; let the top few inches dry between waterings.",
		SourceVersionIDs:   []string{vid},
	}
}

func TestOccurrenceEvidenceReferencesExactSnapshot(t *testing.T) {
	ctx, pool, a := scratch(t)
	s := svc(pool)
	ids := platform.UUIDv4{}
	q := dbgen.New(pool)

	loc, err := q.InsertLocation(ctx, dbgen.InsertLocationParams{
		ID: ids.New(), WorkspaceID: a.WorkspaceID, Name: "Courtyard",
		Situation: dbgen.LocationSituationOUTDOOR,
		Latitude:  coord(t, 34.0522), Longitude: coord(t, -118.2437),
	})
	if err != nil {
		t.Fatalf("location: %v", err)
	}
	plant := mkPlant(t, pool, a)
	if _, err := q.InsertPlantContext(ctx, dbgen.InsertPlantContextParams{
		ID: ids.New(), WorkspaceID: a.WorkspaceID, PlantID: plant.ID,
		EffectiveFrom: pgtype.Timestamptz{Time: time.Now().Add(-24 * time.Hour), Valid: true},
		LocationID:    loc.ID, Placement: dbgen.ContextPlacementGROUND,
	}); err != nil {
		t.Fatalf("context: %v", err)
	}

	vid := publishedVersion(t, s, a, rainRule(mkSource(t, s, a, "Weather evidence source").Versions[0].ID))

	// Snapshot first, then the plan: materialization must consult it.
	srv, _ := fixtureStub(t, "openmeteo-rain.json", 200)
	s.Weather = weather.New(srv.URL, 5*time.Second)
	now := time.Now().UTC()
	if err := s.RunWeatherFetch(ctx, now); err != nil {
		t.Fatal(err)
	}
	snap1, err := q.LatestWeatherSnapshot(ctx, dbgen.LatestWeatherSnapshotParams{
		WorkspaceID: a.WorkspaceID, LocationID: loc.ID})
	if err != nil {
		t.Fatal(err)
	}

	plan := mkPlan(t, s, a, uuidString(plant.ID), vid)
	if err := s.MaterializePlan(ctx, plan.ID); err != nil {
		t.Fatal(err)
	}

	conn, probs, err := s.CareAgenda(ctx, a, nil, nil, nil, 50, nil)
	noProbs(t, probs, err)
	if len(conn.Items) == 0 {
		t.Fatal("agenda empty after materialization")
	}
	it := conn.Items[0]
	// Advisory-only guardrail: heavy rain elevates the check, it never
	// completes or cancels the occurrence.
	if it.Row.Status != dbgen.OccurrenceStatusDUE {
		t.Fatalf("status = %s, want DUE (weather never marks work done)", it.Row.Status)
	}
	// The row references the EXACT snapshot the advice was computed from.
	if !it.Row.WeatherSnapshotID.Valid || uuidString(it.Row.WeatherSnapshotID) != uuidString(snap1.ID) {
		t.Fatalf("row weather ref = %v, want %s", it.Row.WeatherSnapshotID, uuidString(snap1.ID))
	}
	if it.Weather == nil || uuidString(it.Weather.ID) != uuidString(snap1.ID) {
		t.Fatalf("hydrated weather = %v, want snapshot %s", it.Weather, uuidString(snap1.ID))
	}
	// The FRESH snapshot fed the engine: its values appear in the notes.
	if !strings.Contains(strings.Join(it.Row.EvidenceNotes, "|"), "reported rainfall 18.4mm") {
		t.Fatalf("evidence notes = %v, want the fresh snapshot's rainfall value", it.Row.EvidenceNotes)
	}

	// A newer snapshot never rewrites the old occurrence's evidence:
	// the reference stays pinned to the snapshot consulted at compute.
	if err := s.RunWeatherFetch(ctx, now.Add(7*time.Hour)); err != nil {
		t.Fatal(err)
	}
	snap2, err := q.LatestWeatherSnapshot(ctx, dbgen.LatestWeatherSnapshotParams{
		WorkspaceID: a.WorkspaceID, LocationID: loc.ID})
	if err != nil {
		t.Fatal(err)
	}
	if uuidString(snap2.ID) == uuidString(snap1.ID) {
		t.Fatal("expected a second snapshot")
	}
	conn, probs, err = s.CareAgenda(ctx, a, nil, nil, nil, 50, nil)
	noProbs(t, probs, err)
	if uuidString(conn.Items[0].Row.WeatherSnapshotID) != uuidString(snap1.ID) {
		t.Fatal("occurrence evidence must stay pinned to the exact snapshot")
	}
}
