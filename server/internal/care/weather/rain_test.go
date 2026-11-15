package weather

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func rainSeries(start time.Time, count int, value func(int) (*float64, *float64)) []byte {
	times := []int64{}
	rain, showers := []*float64{}, []*float64{}
	for i := 0; i < count; i++ {
		times = append(times, start.Add(time.Duration(i)*time.Hour).Unix())
		r, s := value(i)
		rain = append(rain, r)
		showers = append(showers, s)
	}
	b, _ := json.Marshal(map[string]any{"hourly_units": map[string]string{"time": "unixtime", "rain": "mm", "showers": "mm"}, "hourly": map[string]any{"time": times, "rain": rain, "showers": showers}})
	return b
}

func TestRecentRainElapsedIntervalsAndLocalMidnight(t *testing.T) {
	loc, _ := time.LoadLocation("America/Chicago")
	start := time.Date(2026, 11, 15, 5, 0, 0, 0, time.UTC) // local Nov 14, 23:00
	now := start.Add(3*time.Hour + 30*time.Minute)
	body := rainSeries(start, 8, func(i int) (*float64, *float64) { return f64p(1), f64p(0.5) })
	days, err := parseRecentRain(body, loc, now)
	if err != nil {
		t.Fatal(err)
	}
	// 23:00 and 00:00 report the previous day's preceding intervals. Future
	// hours (including a large daily forecast) cannot enter today's estimate.
	if days[0].Day != "2026-11-15" || days[0].AmountMm == nil || *days[0].AmountMm != 3 || days[1].AmountMm == nil || *days[1].AmountMm != 3 || days[2].AmountMm != nil {
		t.Fatalf("incorrect local days: %+v", days)
	}
}

func TestRecentRainDSTAbsoluteHours(t *testing.T) {
	loc, _ := time.LoadLocation("America/Chicago")
	// The 25-hour autumn local day: both repeated 01:00 intervals count once.
	start := time.Date(2026, 11, 1, 6, 0, 0, 0, time.UTC)
	body := rainSeries(start, 25, func(i int) (*float64, *float64) { return f64p(1), f64p(0) })
	days, err := parseRecentRain(body, loc, time.Date(2026, 11, 2, 7, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if days[1].Day != "2026-11-01" || days[1].AmountMm == nil || *days[1].AmountMm != 25 {
		t.Fatalf("DST day lost an interval: %+v", days)
	}
}

func TestRecentRainUnknownIsNotZero(t *testing.T) {
	now := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	body := rainSeries(now.Add(-time.Hour), 3, func(i int) (*float64, *float64) { return nil, f64p(0) })
	if _, err := parseRecentRain(body, time.UTC, now); err == nil {
		t.Fatal("missing precipitation passed as dry weather")
	}
	body = rainSeries(now.Add(-time.Hour), 3, func(i int) (*float64, *float64) { return f64p(0), f64p(0) })
	days, err := parseRecentRain(body, time.UTC, now)
	if err != nil || days[0].AmountMm == nil || *days[0].AmountMm != 0 {
		t.Fatalf("true zero missing: %+v %v", days, err)
	}
}

func TestRecentRainRejectsMalformedOrUnusable(t *testing.T) {
	now := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	for _, body := range []string{
		`{}`, `{"hourly_units":{"time":"unixtime","rain":"inch","showers":"mm"},"hourly":{"time":[1794744000],"rain":[1],"showers":[0]}}`,
		`{"hourly_units":{"time":"unixtime","rain":"mm","showers":"mm"},"hourly":{"time":[1794744000,1794744000],"rain":[1,1],"showers":[0,0]}}`,
		`{"hourly_units":{"time":"unixtime","rain":"mm","showers":"mm"},"hourly":{"time":[1794744000],"rain":[-1],"showers":[0]}}`,
		`{"hourly_units":{"time":"unixtime","rain":"mm","showers":"mm"},"hourly":{"time":[1794744000],"rain":[1],"showers":[]}}`,
	} {
		if _, err := parseRecentRain([]byte(body), time.UTC, now); err == nil {
			t.Fatalf("accepted %s", body)
		}
	}
	if _, err := parseRecentRain(rainSeries(now.Add(time.Hour), 2, func(i int) (*float64, *float64) { return f64p(5), f64p(0) }), time.UTC, now); err == nil {
		t.Fatal("future-only forecast accepted")
	}
}

func TestRecentRainRequestAndFailure(t *testing.T) {
	now := time.Now()
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		q := r.URL.Query()
		if q.Get("hourly") != "rain,showers" || q.Get("timeformat") != "unixtime" || q.Get("past_days") != "3" || q.Get("precipitation_unit") != "mm" {
			t.Errorf("wrong request %s", r.URL.RawQuery)
		}
		w.Write(rainSeries(now.Truncate(time.Hour).Add(-time.Hour), 4, func(i int) (*float64, *float64) { return f64p(1), f64p(0) }))
	}))
	defer srv.Close()
	c := New(srv.URL, time.Second)
	if _, err := c.FetchRecentRain(context.Background(), 32, -96, "America/Chicago", now); err != nil {
		t.Fatal(err)
	}
	if _, err := c.FetchRecentRain(context.Background(), 91, -96, "America/Chicago", now); err == nil {
		t.Fatal("invalid latitude accepted")
	}
	if _, err := c.FetchRecentRain(context.Background(), 32, -96, "unknown/timezone", now); err == nil {
		t.Fatal("invalid zone accepted")
	}
	if calls != 1 {
		t.Fatalf("unexpected provider calls %d", calls)
	}
	c = New(stubServer(t, 503, []byte("down")).URL, time.Second)
	if _, err := c.FetchRecentRain(context.Background(), 32, -96, "UTC", now); err == nil {
		t.Fatal("provider outage hidden")
	}
}
