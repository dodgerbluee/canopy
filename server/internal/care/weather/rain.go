package weather

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// RainDay is modelled liquid precipitation for elapsed intervals, never a gauge
// observation. Nil means no usable interval, not a dry day.
type RainDay struct {
	Day      string
	AmountMm *float64
}

// FetchRecentRain requests liquid rain and showers (not snow) in absolute UTC
// time. Grouping in the workspace's IANA zone keeps DST and local days correct.
func (c *Client) FetchRecentRain(ctx context.Context, lat, lon float64, zone string, now time.Time) ([]RainDay, error) {
	if math.IsNaN(lat) || math.IsNaN(lon) || lat < -90 || lat > 90 || lon < -180 || lon > 180 {
		return nil, fmt.Errorf("weather: invalid coordinates")
	}
	loc, err := time.LoadLocation(zone)
	if err != nil {
		return nil, fmt.Errorf("weather: invalid timezone")
	}
	values := url.Values{
		"latitude":  {strconv.FormatFloat(lat, 'f', 6, 64)},
		"longitude": {strconv.FormatFloat(lon, 'f', 6, 64)},
		"hourly":    {"rain,showers"}, "precipitation_unit": {"mm"},
		"timeformat": {"unixtime"}, "timezone": {"GMT"},
		"past_days": {"3"}, "forecast_days": {"1"},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(c.BaseURL, "/")+"/v1/forecast?"+values.Encode(), nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("weather: rain fetch failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("weather: rain provider status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, (1<<20)+1))
	if err != nil || len(body) > 1<<20 {
		return nil, fmt.Errorf("weather: invalid rain response size")
	}
	return parseRecentRain(body, loc, now)
}

func parseRecentRain(body []byte, loc *time.Location, now time.Time) ([]RainDay, error) {
	var raw struct {
		Units struct {
			Time    string `json:"time"`
			Rain    string `json:"rain"`
			Showers string `json:"showers"`
		} `json:"hourly_units"`
		Hourly struct {
			Time    []int64    `json:"time"`
			Rain    []*float64 `json:"rain"`
			Showers []*float64 `json:"showers"`
		} `json:"hourly"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("weather: malformed rain response")
	}
	n := len(raw.Hourly.Time)
	if n == 0 || n > 120 || len(raw.Hourly.Rain) != n || len(raw.Hourly.Showers) != n || raw.Units.Time != "unixtime" || raw.Units.Rain != "mm" || raw.Units.Showers != "mm" {
		return nil, fmt.Errorf("weather: invalid rain series or units")
	}
	days := make([]RainDay, 3)
	sums := map[string]float64{}
	seen := map[string]bool{}
	for i := range days {
		days[i].Day = now.In(loc).AddDate(0, 0, -i).Format(time.DateOnly)
	}
	for i, stamp := range raw.Hourly.Time {
		if stamp <= 0 || (i > 0 && stamp-raw.Hourly.Time[i-1] != 3600) {
			return nil, fmt.Errorf("weather: invalid hourly timestamps")
		}
		rain, showers := raw.Hourly.Rain[i], raw.Hourly.Showers[i]
		for _, v := range []*float64{rain, showers} {
			if v != nil && (math.IsNaN(*v) || math.IsInf(*v, 0) || *v < 0 || *v > 1000) {
				return nil, fmt.Errorf("weather: invalid rain value")
			}
		}
		end := time.Unix(stamp, 0)
		if end.After(now) || rain == nil || showers == nil {
			continue
		}
		// Values sum the preceding hour. Split across civil midnight, including
		// zones whose offset has half/quarter hours. A midnight endpoint still
		// belongs to the day that just ended. These remain model estimates.
		for start := end.Add(-time.Hour); start.Before(end); {
			local := start.In(loc)
			boundary := time.Date(local.Year(), local.Month(), local.Day()+1, 0, 0, 0, 0, loc)
			if boundary.After(end) {
				boundary = end
			}
			if !boundary.After(start) {
				return nil, fmt.Errorf("weather: invalid civil day boundary")
			}
			day := local.Format(time.DateOnly)
			sums[day] += (*rain + *showers) * boundary.Sub(start).Hours()
			seen[day] = true
			start = boundary
		}
	}
	usable := false
	for i := range days {
		if seen[days[i].Day] {
			value := math.Round(sums[days[i].Day]*1000) / 1000
			if value > 10000 {
				return nil, fmt.Errorf("weather: implausible rain total")
			}
			days[i].AmountMm = &value
			usable = true
		}
	}
	if !usable {
		return nil, fmt.Errorf("weather: no elapsed rain intervals")
	}
	return days, nil
}
