package weather

// T26 acceptance: the Open-Meteo mapping and provenance against RECORDED
// fixtures (server/testdata/weather) replayed over httptest — default
// tests never touch the network. Covers full mapping, honest nulls,
// provider failure, malformed/misaligned payloads, the timeout guard,
// coordinate refusal, and the snapshot window. A tiny opt-in live smoke
// against the real public API runs only when CANOPY_WEATHER_SMOKE=1.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "..", "testdata", "weather", name))
	if err != nil {
		t.Fatalf("fixture %s: %v", name, err)
	}
	return b
}

func stubServer(t *testing.T, status int, body []byte) *httptest.Server {
	t.Helper()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
		w.Write(body)
	}))
	t.Cleanup(s.Close)
	return s
}

func f64p(v float64) *float64 { return &v }

func TestFetchDailySunnyMapping(t *testing.T) {
	c := New(stubServer(t, 200, fixture(t, "openmeteo-sunny.json")).URL, 5*time.Second)
	d, err := c.FetchDaily(context.Background(), 34.0522, -118.2437)
	if err != nil {
		t.Fatal(err)
	}
	if d.Day != "2026-11-02" || d.OffsetSeconds != -28800 {
		t.Fatalf("day/offset = %s/%d", d.Day, d.OffsetSeconds)
	}
	if *d.HighTempC != 21.4 || *d.LowTempC != 9.8 || *d.RainfallMm != 0.0 || *d.Et0Mm != 3.1 {
		t.Fatalf("values mis-mapped: %+v", d)
	}
	from, until, err := d.Window()
	if err != nil {
		t.Fatal(err)
	}
	// provider-local civil day boundaries, resolved through the
	// response's own utc_offset_seconds
	if from.Format(time.RFC3339) != "2026-11-02T00:00:00-08:00" ||
		!until.Equal(from.AddDate(0, 0, 1)) {
		t.Fatalf("window = %s..%s", from, until)
	}
	if Provider != "open-meteo" || License == "" {
		t.Fatal("provenance constants missing")
	}
}

func TestFetchDailyRainFixture(t *testing.T) {
	c := New(stubServer(t, 200, fixture(t, "openmeteo-rain.json")).URL, 5*time.Second)
	d, err := c.FetchDaily(context.Background(), 34.0522, -118.2437)
	if err != nil {
		t.Fatal(err)
	}
	if *d.RainfallMm != 18.4 {
		t.Fatalf("rainfall = %v", *d.RainfallMm)
	}
}

func TestFetchDailyHonestNulls(t *testing.T) {
	c := New(stubServer(t, 200, fixture(t, "openmeteo-partial.json")).URL, 5*time.Second)
	d, err := c.FetchDaily(context.Background(), 48.8553, 2.3444)
	if err != nil {
		t.Fatal(err)
	}
	if d.LowTempC != nil || d.RainfallMm != nil {
		t.Fatal("absent provider members must stay nil, never invented")
	}
	if d.HighTempC == nil || *d.HighTempC != 12.9 || d.Et0Mm == nil {
		t.Fatalf("present members lost: %+v", d)
	}
	from, _, err := d.Window()
	if err != nil || from.Format(time.RFC3339) != "2026-11-02T00:00:00+01:00" {
		t.Fatalf("window = %s, err %v", from, err)
	}
}

func TestFetchDailyProviderFailure(t *testing.T) {
	c := New(stubServer(t, 500, []byte(`{"error":true,"reason":"upstream down"}`)).URL, 5*time.Second)
	if _, err := c.FetchDaily(context.Background(), 1, 1); err == nil {
		t.Fatal("provider 5xx must surface as an error (worker skips the location)")
	}
}

func TestFetchDailyMalformed(t *testing.T) {
	cases := map[string]string{
		"no daily series":  `{"utc_offset_seconds": 0}`,
		"missing offset":   `{"daily": {"time": ["2026-11-02"]}}`,
		"misaligned array": `{"utc_offset_seconds": 0, "daily": {"time": ["2026-11-02","2026-11-03"], "temperature_2m_max": [1.0]}}`,
		"bad day":          `{"utc_offset_seconds": 0, "daily": {"time": ["not-a-date"]}}`,
		"negative rain":    `{"utc_offset_seconds": 0, "daily": {"time": ["2026-11-02"], "precipitation_sum": [-1]}}`,
		"truncated json":   `{"daily": {"time": [`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			c := New(stubServer(t, 200, []byte(body)).URL, 5*time.Second)
			if _, err := c.FetchDaily(context.Background(), 1, 1); err == nil {
				t.Fatalf("malformed payload (%s) accepted", name)
			}
		})
	}
}

func TestFetchDailyTimeout(t *testing.T) {
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(2 * time.Second)
		w.Write([]byte(`{}`))
	}))
	t.Cleanup(slow.Close)
	c := New(slow.URL, 50*time.Millisecond)
	start := time.Now()
	if _, err := c.FetchDaily(context.Background(), 1, 1); err == nil {
		t.Fatal("hung provider must time out")
	}
	if time.Since(start) > time.Second {
		t.Fatal("timeout did not bound the request")
	}
}

func TestFetchDailyRefusesBadCoordinates(t *testing.T) {
	c := New("http://127.0.0.1:1", time.Second) // never dialed: refused first
	for _, lat := range []float64{-91, 91} {
		if _, err := c.FetchDaily(context.Background(), lat, 0); err == nil {
			t.Fatalf("latitude %g accepted", lat)
		}
	}
	if _, err := c.FetchDaily(context.Background(), 0, 181); err == nil {
		t.Fatal("longitude 181 accepted")
	}
}

// TestOpenMeteoLiveSmoke is the packet's "tiny opt-in public API smoke":
// it exercises the real endpoint with the production request shape, but
// only when explicitly enabled. Default test runs stay network-free.
func TestOpenMeteoLiveSmoke(t *testing.T) {
	if os.Getenv("CANOPY_WEATHER_SMOKE") != "1" {
		t.Skip("set CANOPY_WEATHER_SMOKE=1 to hit the real Open-Meteo endpoint")
	}
	c := New(DefaultBaseURL, 10*time.Second)
	d, err := c.FetchDaily(context.Background(), 34.0522, -118.2437)
	if err != nil {
		t.Fatal(err)
	}
	if d.Day == "" || d.HighTempC == nil {
		t.Fatalf("live smoke got no usable values: %+v", d)
	}
	t.Logf("live open-meteo: day=%s high=%.1fC rain=%v", d.Day, *d.HighTempC, d.RainfallMm)
}
