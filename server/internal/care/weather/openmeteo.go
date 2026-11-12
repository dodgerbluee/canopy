// Package weather is the outbound weather adapter (T26): the only place
// in the server that talks to a third-party forecast API. It maps the
// provider response onto the engine's advisory-only Weather shape and
// carries provenance (provider + license) through to the snapshot row.
//
// Guardrails (03-contracts): weather is advisory evidence only. This
// package never decides that care work is done; it only reports what the
// provider said, with provenance. Locations are user-entered (opt-in) —
// nothing here geolocates.
package weather

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

const (
	// Provider names the snapshot source; stored verbatim on the row.
	Provider = "open-meteo"
	// License is the provider attribution required for stored values
	// (Open-Meteo data is CC-BY 4.0); stored on the row for provenance.
	License = "Open-Meteo.com (CC-BY 4.0)"

	// DefaultBaseURL is the public Open-Meteo endpoint; operator-
	// overridable via CANOPY_WEATHER_BASE_URL (tests point it at a
	// recorded-fixture server; no default test touches the network).
	DefaultBaseURL = "https://api.open-meteo.com"
)

// Daily is one location-day of provider data plus provenance. Nil
// member = the provider did not supply that value (never invented).
type Daily struct {
	Day           string // provider-local civil date, YYYY-MM-DD
	OffsetSeconds int    // provider-resolved UTC offset for that day
	HighTempC     *float64
	LowTempC      *float64
	RainfallMm    *float64
	Et0Mm         *float64
}

// Window is the snapshot validity span: the provider's local civil day,
// start to start. Satisfies the DB chk_weather_window constraint.
func (d Daily) Window() (validFrom, validUntil time.Time, err error) {
	day, err := time.Parse("2006-01-02", d.Day)
	if err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("weather: bad provider day %q", d.Day)
	}
	zone := time.FixedZone("provider", d.OffsetSeconds)
	midnight := time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, zone)
	return midnight, midnight.AddDate(0, 0, 1), nil
}

// Client fetches daily forecast values for one coordinate pair.
type Client struct {
	BaseURL string
	HTTP    *http.Client
}

// New builds a client with a per-request timeout; a hung provider must
// never stall a worker run past its context.
func New(baseURL string, timeout time.Duration) *Client {
	return &Client{BaseURL: baseURL, HTTP: &http.Client{Timeout: timeout}}
}

// FetchDaily retrieves today's daily values for lat/lon (WGS84,
// user-entered). timezone=auto lets the provider resolve the local day
// the values belong to; the offset comes back with the response.
func (c *Client) FetchDaily(ctx context.Context, lat, lon float64) (Daily, error) {
	if lat < -90 || lat > 90 || lon < -180 || lon > 180 {
		return Daily{}, fmt.Errorf("weather: coordinate out of range: %g,%g", lat, lon)
	}
	u := fmt.Sprintf("%s/v1/forecast?latitude=%s&longitude=%s&daily=%s&timezone=auto&forecast_days=1",
		c.BaseURL,
		strconv.FormatFloat(lat, 'f', 6, 64),
		strconv.FormatFloat(lon, 'f', 6, 64),
		"temperature_2m_max,temperature_2m_min,precipitation_sum,et0_fao_evapotranspiration")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return Daily{}, err
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return Daily{}, fmt.Errorf("weather: fetch: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return Daily{}, fmt.Errorf("weather: read body: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return Daily{}, fmt.Errorf("weather: provider status %d", resp.StatusCode)
	}
	return parseDaily(body)
}

// parseDaily decodes and validates the Open-Meteo daily response. Every
// array must align with daily.time; a truncated or malformed payload is
// refused rather than half-mapped.
func parseDaily(body []byte) (Daily, error) {
	var raw struct {
		Latitude  *float64 `json:"latitude"`
		UtcOffset *int     `json:"utc_offset_seconds"`
		Daily     *struct {
			Time   []string   `json:"time"`
			TMax   []*float64 `json:"temperature_2m_max"`
			TMin   []*float64 `json:"temperature_2m_min"`
			Precip []*float64 `json:"precipitation_sum"`
			Et0    []*float64 `json:"et0_fao_evapotranspiration"`
		} `json:"daily"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return Daily{}, fmt.Errorf("weather: decode: %w", err)
	}
	if raw.Daily == nil || len(raw.Daily.Time) == 0 {
		return Daily{}, errors.New("weather: provider returned no daily series")
	}
	if raw.UtcOffset == nil {
		return Daily{}, errors.New("weather: provider omitted utc_offset_seconds")
	}
	d := Daily{Day: raw.Daily.Time[0], OffsetSeconds: *raw.UtcOffset}
	pick := func(arr []*float64) (*float64, error) {
		if len(arr) == 0 {
			return nil, nil // member absent: honest nil, not zero
		}
		if len(arr) != len(raw.Daily.Time) {
			return nil, errors.New("weather: daily arrays misaligned")
		}
		return arr[0], nil
	}
	var err error
	if d.HighTempC, err = pick(raw.Daily.TMax); err != nil {
		return Daily{}, err
	}
	if d.LowTempC, err = pick(raw.Daily.TMin); err != nil {
		return Daily{}, err
	}
	if d.RainfallMm, err = pick(raw.Daily.Precip); err != nil {
		return Daily{}, err
	}
	if d.Et0Mm, err = pick(raw.Daily.Et0); err != nil {
		return Daily{}, err
	}
	if _, _, err := d.Window(); err != nil {
		return Daily{}, err
	}
	if d.RainfallMm != nil && *d.RainfallMm < 0 {
		return Daily{}, fmt.Errorf("weather: negative rainfall %v", *d.RainfallMm)
	}
	return d, nil
}

// QueryURL is exposed for the opt-in live smoke test so it exercises the
// exact production request shape.
func (c *Client) QueryURL(lat, lon float64) string {
	u, err := url.Parse(c.BaseURL)
	if err != nil {
		return ""
	}
	return fmt.Sprintf("%s/v1/forecast?latitude=%s&longitude=%s&daily=%s&timezone=auto&forecast_days=1",
		u.String(),
		strconv.FormatFloat(lat, 'f', 6, 64),
		strconv.FormatFloat(lon, 'f', 6, 64),
		"temperature_2m_max,temperature_2m_min,precipitation_sum,et0_fao_evapotranspiration")
}
