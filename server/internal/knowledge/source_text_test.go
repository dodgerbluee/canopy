package knowledge

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestSourceBlocksKeepWeatherOutOfHarvestTiming(t *testing.T) {
	html := `<article><h2>Climate limitations</h2><p>Spring freeze and high summer temperatures limit production</p><h2>Harvest</h2><p>late March and early April</p><h2>Irrigation</h2><p>Check moisture as the soil dries</p><p>` + strings.Repeat("Regional plant conditions matter. ", 12) + `</p></article>`
	fetch := &Fetcher{HTTP: &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		body := html
		if r.URL.Path == "/robots.txt" {
			body = "User-agent: *\nAllow: /"
		}
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body))}, nil
	})}}
	source := Registry()[0]
	text, err := fetch.Read(context.Background(), source)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "\nHarvest\nlate March and early April\nIrrigation\n") {
		t.Fatalf("lost source structure: %q", text)
	}
	drafts := []DraftFact{
		{Kind: "HARVEST", Label: "Harvest window", Evidence: "late March and early April", Timing: "late March and early April", Summary: "Harvest occurs in late March and early April depending on conditions."},
		{Kind: "WATER_CHECK", Label: "Moisture checks", Evidence: "Check moisture as the soil dries", Summary: "Check moisture as the soil dries, adapting to conditions."},
	}
	facts, err := ValidateFacts(source, text, drafts, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(facts[0].Months) != "[3 4]" || len(facts[1].Months) != 0 {
		t.Fatalf("unrelated rows leaked into timing: %+v", facts)
	}
	if gaps := uncoveredHarvest(harvestPassages(text), drafts, source, text); len(gaps) != 0 {
		t.Fatal("heading-based harvest not covered", gaps)
	}
	if len(harvestPassages(text)) != 1 {
		t.Fatal("weather warnings must not become harvest requirements", harvestPassages(text))
	}
}
