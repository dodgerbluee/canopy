package knowledge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type Place struct{ Name, Country, Region, Latitude, Longitude, Timezone string }

// Search is an explicit owner action. No IP lookup, background geolocation or
// timezone-to-climate inference occurs; the selected garden location is saved separately.
func (s *Service) Places(ctx context.Context, search string) ([]Place, error) {
	search = strings.TrimSpace(search)
	if len(search) < 3 || len(search) > 120 {
		return nil, invalid("search", "Enter at least three characters of a city or postal code.")
	}
	address := "https://geocoding-api.open-meteo.com/v1/search?" + url.Values{"name": {search}, "count": {"8"}, "language": {"en"}, "format": {"json"}}.Encode()
	req, err := http.NewRequestWithContext(ctx, "GET", address, nil)
	if err != nil {
		return nil, err
	}
	client := s.GeocodingHTTP
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	res, err := client.Do(req)
	if err != nil {
		return nil, errors.New("location search could not be reached; enter the garden location manually")
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return nil, errors.New("location search is unavailable; enter the garden location manually")
	}
	body, err := io.ReadAll(io.LimitReader(res.Body, (1<<20)+1))
	if err != nil || len(body) > 1<<20 {
		return nil, errors.New("location response exceeded its limit")
	}
	var response struct {
		Results []struct {
			Name, Timezone      string
			Country             string `json:"country_code"`
			Region              string `json:"admin1"`
			Latitude, Longitude float64
		} `json:"results"`
	}
	if json.Unmarshal(body, &response) != nil {
		return nil, errors.New("location search returned an invalid response")
	}
	out := []Place{}
	for _, p := range response.Results {
		if len(out) == 8 {
			break
		}
		if len(p.Country) != 2 || p.Name == "" || math.IsNaN(p.Latitude) || math.IsInf(p.Latitude, 0) || math.IsNaN(p.Longitude) || math.IsInf(p.Longitude, 0) || p.Latitude < -90 || p.Latitude > 90 || p.Longitude < -180 || p.Longitude > 180 {
			continue
		}
		if _, err = time.LoadLocation(p.Timezone); err != nil {
			continue
		}
		out = append(out, Place{Name: p.Name, Country: p.Country, Region: p.Region, Timezone: p.Timezone, Latitude: fmt.Sprintf("%.6f", p.Latitude), Longitude: fmt.Sprintf("%.6f", p.Longitude)})
	}
	return out, nil
}
