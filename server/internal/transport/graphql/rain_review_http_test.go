package graphql

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"canopy/server/internal/care/weather"
	"canopy/server/internal/journal"
	"canopy/server/internal/platform"
	"github.com/google/uuid"
)

const rainReviewQuery = `query{todayOverview{rainReview{status areaLabel checkedAt proposals{id day estimatedAmountMm checkedAt}}}}`

func rainReviewData(t *testing.T, e *testEnv) map[string]any {
	t.Helper()
	r := e.gql(rainReviewQuery, nil)
	if len(r.Errors) > 0 {
		t.Fatal(r.Raw)
	}
	return dataOf(t, r, "todayOverview")["rainReview"].(map[string]any)
}
func configureRain(t *testing.T, e *testEnv) {
	t.Helper()
	if _, err := e.pool.Exec(context.Background(), `UPDATE workspace SET latitude=32.78,longitude=-96.8,timezone='America/Chicago',location_label='Test garden' WHERE id=$1`, e.wsID); err != nil {
		t.Fatal(err)
	}
}
func rainFixture(t *testing.T, now time.Time, hook func()) (*weather.Client, *atomic.Int32) {
	t.Helper()
	calls := &atomic.Int32{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if hook != nil {
			hook()
		}
		stamps := []int64{}
		rain, showers := []float64{}, []float64{}
		start := now.UTC().Truncate(time.Hour).Add(-72 * time.Hour)
		for i := 0; i < 96; i++ {
			stamps = append(stamps, start.Add(time.Duration(i)*time.Hour).Unix())
			rain = append(rain, 0.5)
			showers = append(showers, 0.1)
		}
		json.NewEncoder(w).Encode(map[string]any{"hourly_units": map[string]string{"time": "unixtime", "rain": "mm", "showers": "mm"}, "hourly": map[string]any{"time": stamps, "rain": rain, "showers": showers}})
	}))
	t.Cleanup(srv.Close)
	return weather.New(srv.URL, time.Second), calls
}
func runRainFixture(t *testing.T, e *testEnv, now time.Time) *atomic.Int32 {
	t.Helper()
	provider, calls := rainFixture(t, now, nil)
	if err := journal.New(e.pool).RunRainCheck(context.Background(), provider, slog.New(slog.DiscardHandler), now); err != nil {
		t.Fatal(err)
	}
	return calls
}

func TestGardenRainWorkerUsesWorkspaceWithoutLocationsOrCare(t *testing.T) {
	e := newEnv(t, DefaultLimits())
	ctx := context.Background()
	svc := journal.New(e.pool)
	now := time.Now()
	provider, calls := rainFixture(t, now, nil)
	if err := svc.RunRainCheck(ctx, provider, slog.New(slog.DiscardHandler), now); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 0 || rainReviewData(t, e)["status"] != "NOT_CONFIGURED" {
		t.Fatal("unconfigured garden sent outside")
	}
	configureRain(t, e)
	if rainReviewData(t, e)["status"] != "WAITING" {
		t.Fatal("first check must be waiting")
	}
	for _, at := range []time.Time{now, now.Add(30 * time.Minute)} {
		if err := svc.RunRainCheck(ctx, provider, slog.New(slog.DiscardHandler), at); err != nil {
			t.Fatal(err)
		}
	}
	if calls.Load() != 1 {
		t.Fatal("one-hour cache missed", calls.Load())
	}
	review := rainReviewData(t, e)
	if review["status"] != "READY" || review["checkedAt"] == nil || len(review["proposals"].([]any)) == 0 {
		t.Fatal(review)
	}
	var actual, places, plans int
	if err := e.pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM rain_events),(SELECT count(*) FROM locations),(SELECT count(*) FROM care_plans)`).Scan(&actual, &places, &plans); err != nil || actual != 0 || places != 0 || plans != 0 {
		t.Fatal("weather must work without care, and never record rain", actual, places, plans, err)
	}
	if _, err := e.pool.Exec(ctx, `UPDATE rain_reviews SET checked_at=now()-interval '4 hours'`); err != nil {
		t.Fatal(err)
	}
	review = rainReviewData(t, e)
	if review["status"] != "STALE" || len(review["proposals"].([]any)) != 0 {
		t.Fatal("stale estimate presented as actionable", review)
	}
	failed := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(503) }))
	defer failed.Close()
	if err := svc.RunRainCheck(ctx, weather.New(failed.URL, time.Second), slog.New(slog.DiscardHandler), now); err != nil {
		t.Fatal(err)
	}
	if rainReviewData(t, e)["status"] != "STALE" {
		t.Fatal("provider failure hid stale state")
	}
}

func TestRainReviewHTTPConfirmRetryDismissAndCalendar(t *testing.T) {
	e := newEnv(t, DefaultLimits())
	configureRain(t, e)
	runRainFixture(t, e, time.Now())
	review := rainReviewData(t, e)
	p := review["proposals"].([]any)[0].(map[string]any)
	input := map[string]any{"day": p["day"], "rainReviewId": p["id"]}
	var first string
	for i := 0; i < 2; i++ {
		payload, ps := careMut(t, e, "recordRain", "RecordRainInput", input, `rain{id day amountMm}`)
		noProbs(t, "confirm", ps)
		r := payload["rain"].(map[string]any)
		if r["amountMm"] != nil {
			t.Fatal("model estimate was silently saved as measured depth", r)
		}
		if first == "" {
			first = r["id"].(string)
		} else if first != r["id"] {
			t.Fatal("confirmation duplicated rain")
		}
	}
	page := dataOf(t, e.gql(calendarQuery, map[string]any{"from": p["day"], "to": p["day"], "first": 100}), "calendarEvents")
	if len(page["items"].([]any)) != 1 || page["items"].([]any)[0].(map[string]any)["kind"] != "RAIN" {
		t.Fatal("confirmed rain missing from calendar", page)
	}
	var activities, occurrences int
	if err := e.pool.QueryRow(context.Background(), `SELECT (SELECT count(*) FROM activities),(SELECT count(*) FROM care_occurrences WHERE status='COMPLETED')`).Scan(&activities, &occurrences); err != nil || activities != 0 || occurrences != 0 {
		t.Fatal("rain changed care", activities, occurrences, err)
	}
	remaining := rainReviewData(t, e)["proposals"].([]any)
	if len(remaining) == 0 {
		t.Fatal("fixture must leave another rain day")
	}
	dismiss := remaining[0].(map[string]any)
	for i := 0; i < 2; i++ {
		_, ps := careMut(t, e, "dismissRainReview", "DismissRainReviewInput", map[string]any{"id": dismiss["id"]}, "dismissedId")
		noProbs(t, "dismiss", ps)
	}
	if _, err := e.pool.Exec(context.Background(), `UPDATE rain_reviews SET checked_at=now()-interval '2 hours' WHERE workspace_id=$1`, e.wsID); err != nil {
		t.Fatal(err)
	}
	runRainFixture(t, e, time.Now())
	for _, v := range rainReviewData(t, e)["proposals"].([]any) {
		if v.(map[string]any)["id"] == dismiss["id"] {
			t.Fatal("dismissed day reappeared")
		}
	}
}

func TestRainReviewConcurrentConfirmAndManualDayDedup(t *testing.T) {
	e := newEnv(t, DefaultLimits())
	configureRain(t, e)
	runRainFixture(t, e, time.Now())
	svc := journal.New(e.pool)
	a := journal.Actor{WorkspaceID: e.wsID, OwnerID: e.ownerID, SessionID: platform.UUIDv4{}.New()}
	proposals := rainReviewData(t, e)["proposals"].([]any)
	p := proposals[0].(map[string]any)
	id, day := p["id"].(string), p["day"].(string)
	type result struct {
		id  string
		ps  []journal.Problem
		err error
	}
	results := make(chan result, 8)
	for i := 0; i < 8; i++ {
		go func(i int) {
			key := fmt.Sprintf("concurrent-rain-%d", i)
			r, ps, err := svc.RecordRain(context.Background(), a, journal.RainInput{Day: day, RainReviewID: &id}, &key)
			results <- result{r.ID, ps, err}
		}(i)
	}
	first := ""
	for i := 0; i < 8; i++ {
		r := <-results
		if r.err != nil || len(r.ps) > 0 {
			t.Fatal(r)
		}
		if first == "" {
			first = r.id
		} else if first != r.id {
			t.Fatal("concurrent confirmations duplicated rain")
		}
	}
	p = proposals[1].(map[string]any)
	id, day = p["id"].(string), p["day"].(string)
	manual, ps, err := svc.RecordRain(context.Background(), a, journal.RainInput{Day: day}, nil)
	if err != nil || len(ps) > 0 {
		t.Fatal(ps, err)
	}
	r, ps, err := svc.RecordRain(context.Background(), a, journal.RainInput{Day: day, RainReviewID: &id}, nil)
	if err != nil || len(ps) > 0 || r.ID != manual.ID {
		t.Fatal("manual record duplicated", r, ps, err)
	}
}

func TestRainReviewLocationFenceIsolationAndUnknownID(t *testing.T) {
	e := newEnv(t, DefaultLimits())
	configureRain(t, e)
	runRainFixture(t, e, time.Now())
	p := rainReviewData(t, e)["proposals"].([]any)[0].(map[string]any)
	foreign := uuid.NewString()
	foreignOwner := uuid.NewString()
	if _, err := e.pool.Exec(context.Background(), `INSERT INTO owner(id,password_hash) VALUES($1,'fixture')`, foreignOwner); err != nil {
		t.Fatal(err)
	}
	if _, err := e.pool.Exec(context.Background(), `INSERT INTO workspace(id,owner_id,display_name,timezone,default_currency,latitude,longitude) VALUES($1,$2,'Other','UTC','USD',32,-96)`, foreign, foreignOwner); err != nil {
		t.Fatal(err)
	}
	foreignReview := uuid.NewString()
	if _, err := e.pool.Exec(context.Background(), `INSERT INTO rain_reviews(id,workspace_id,event_date,latitude,longitude,timezone,estimated_mm,checked_at) VALUES($1,$2,current_date,32,-96,'UTC',2,now())`, foreignReview, foreign); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{foreignReview, uuid.NewString()} {
		_, ps := careMut(t, e, "dismissRainReview", "DismissRainReviewInput", map[string]any{"id": id}, "dismissedId")
		wantProblemCode(t, ps, "NOT_FOUND")
	}
	if _, err := e.pool.Exec(context.Background(), `UPDATE workspace SET latitude=NULL,longitude=NULL WHERE id=$1`, e.wsID); err != nil {
		t.Fatal(err)
	}
	if rainReviewData(t, e)["status"] != "NOT_CONFIGURED" {
		t.Fatal("removed coordinates still monitored")
	}
	_, ps := careMut(t, e, "recordRain", "RecordRainInput", map[string]any{"day": p["day"], "rainReviewId": p["id"]}, "rain{id}")
	wantProblemCode(t, ps, "NOT_FOUND")
	configureRain(t, e)
	if _, err := e.pool.Exec(context.Background(), `DELETE FROM rain_reviews WHERE workspace_id=$1`, e.wsID); err != nil {
		t.Fatal(err)
	}
	provider, _ := rainFixture(t, time.Now(), func() {
		if _, err := e.pool.Exec(context.Background(), `UPDATE workspace SET longitude=-97 WHERE id=$1`, e.wsID); err != nil {
			t.Error(err)
		}
	})
	if err := journal.New(e.pool).RunRainCheck(context.Background(), provider, slog.New(slog.DiscardHandler), time.Now()); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := e.pool.QueryRow(context.Background(), `SELECT count(*) FROM rain_reviews WHERE workspace_id=$1`, e.wsID).Scan(&count); err != nil || count != 0 {
		t.Fatal("late weather crossed moved-location fence", count, err)
	}
}
