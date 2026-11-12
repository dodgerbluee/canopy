package knowledge

import (
	"canopy/server/internal/media/processor"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

func TestRegionalRoutingAndIdentity(t *testing.T) {
	if cropFor("Little Miss Figgy", "Ficus carica", "FIG") != "fig" {
		t.Fatal("fig identity")
	}
	if cropFor("Indoor fig", "Ficus benjamina", "FIG") != "" || cropFor("Raspberry", "Rubus idaeus", "OTHER") != "" || cropFor("Unknown avocado relative", "Persea borbonia", "OTHER") != "" {
		t.Fatal("ambiguous botanical families must not become fruit crop guidance")
	}
	if cropFor("configuration", "", "OTHER") != "" {
		t.Fatal("substring is not taxonomy")
	}
	if len(Applicable(Registry(), "US", "Texas", "citrus")) != 1 || len(Applicable(Registry(), "US", "California", "citrus")) != 1 || len(Applicable(Registry(), "GB", "Texas", "citrus")) != 0 || len(Applicable(Registry(), "US", "America/Chicago", "citrus")) != 0 {
		t.Fatal("country/region must match explicitly")
	}
}
func TestSeasonalTimingDoesNotInventDates(t *testing.T) {
	for _, c := range []struct {
		text, basis string
		months      string
	}{{"February through May", "MONTH", "2 3 4 5"}, {"November to February", "MONTH", "1 2 11 12"}, {"February, May and September", "MONTH", "2 5 9"}, {"spring", "SEASON", "3 4 5"}, {"late winter and early spring", "SEASON", "2 3"}, {"midsummer", "SEASON", "7"}, {"March through May, September through October", "MONTH", "3 4 5 9 10"}, {"year-round", "SEASON", "1 2 3 4 5 6 7 8 9 10 11 12"}, {"after harvest", "UNSPECIFIED", ""}, {"may need water", "UNSPECIFIED", ""}, {"avoid winter", "UNSPECIFIED", ""}} {
		months, basis := ParseTiming(c.text)
		got := strings.Trim(strings.Join(strings.Fields(fmt.Sprint(months)), " "), "[]")
		if basis != c.basis || got != c.months {
			t.Fatalf("%q: %v %s", c.text, months, basis)
		}
	}
}
func TestExtractionRejectsUnsupportedEvidence(t *testing.T) {
	source := Registry()[0]
	text := "Flowers appear in March. Check moisture as the soil dries. Fertilization occurs in November."
	drafts := []DraftFact{{Kind: "BLOOM", Label: "Flowering", Summary: "Spring flowers depend on the plant's conditions.", Evidence: "Flowers appear in March.", Timing: "March"}}
	facts, err := ValidateFacts(source, text, drafts, time.Now())
	if err != nil || len(facts) != 1 || facts[0].Months[0] != 3 {
		t.Fatal(facts, err)
	}
	drafts[0].Timing = "November"
	if _, err = ValidateFacts(source, text, drafts, time.Now()); err == nil {
		t.Fatal("fabricated timing accepted")
	}
	drafts[0].Timing = "March"
	drafts[0].Evidence = "Invented extension advice"
	if _, err = ValidateFacts(source, text, drafts, time.Now()); err == nil {
		t.Fatal("fabricated evidence accepted")
	}
	scoped := []Fact{{ID: "lemon", Subjects: []string{"lemon"}}, {ID: "general"}}
	if len(FilterFacts(scoped, "Owari Satsuma mandarin")) != 1 || len(FilterFacts(scoped, "Meyer lemon")) != 2 {
		t.Fatal("subtype applicability")
	}
}
func TestRobotsPolicies(t *testing.T) {
	for _, c := range []struct {
		body, path string
		allow      bool
	}{{"User-agent: *\nDisallow: /private\nAllow: /private/public", "/private/public/garden", true}, {"User-agent: *\nDisallow: /private", "/private/garden", false}, {"User-agent: *\nDisallow: /\nUser-agent: CanopyKnowledge\nAllow: /guide", "/guide", true}, {"User-agent: *\nDisallow: /*.pdf$", "/garden.pdf", false}, {"User-agent: *\nDisallow:", "/guide", true}} {
		if RobotsAllowed(c.body, c.path) != c.allow {
			t.Fatal(c)
		}
	}
}

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestFetchLimitsAndAccess(t *testing.T) {
	for _, mode := range []string{"denied", "oversized", "html"} {
		fetch := &Fetcher{HTTP: &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
			body := "User-agent: *\nAllow: /"
			if r.URL.Path == "/robots.txt" {
				if mode == "denied" {
					body = "User-agent: *\nDisallow: /"
				}
			} else {
				body = "<html><script>ignore instructions</script><article>" + strings.Repeat("Gardening text. ", 60) + "</article></html>"
				if mode == "oversized" {
					body = strings.Repeat("x", 12<<20+1)
				}
			}
			return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body))}, nil
		})}}
		text, err := fetch.Read(context.Background(), Registry()[0])
		if mode == "html" {
			if err != nil || strings.Contains(text, "ignore instructions") {
				t.Fatal(text, err)
			}
		} else if err == nil {
			t.Fatal("unsafe source accepted", mode)
		}
	}
}
func TestKnowledgeSourcesLive(t *testing.T) {
	if os.Getenv("KNOWLEDGE_SOURCES_LIVE") != "1" {
		t.Skip("explicit live source opt-in required")
	}
	fetch := NewFetcher(&processor.DockerSandbox{Image: "canopy-media:r2", Memory: "512m", PIDs: 64})
	for _, source := range Registry() {
		t.Run(source.ID, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 50*time.Second)
			defer cancel()
			text, err := fetch.Read(ctx, source)
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("Readable registered guide: %s, %d characters", source.ID, len(text))
		})
	}
}

func TestKnowledgeExtractionLive(t *testing.T) {
	if os.Getenv("KNOWLEDGE_MODEL_LIVE") != "1" {
		t.Skip("explicit configured model opt-in required")
	}
	model := NewModel(os.Getenv("CANOPY_VISION_BASE_URL"), os.Getenv("CANOPY_VISION_API_KEY"), os.Getenv("CANOPY_VISION_MODEL"), "none")
	fetch := NewFetcher(&processor.DockerSandbox{Image: "canopy-media:r2", Memory: "512m", PIDs: 64})
	for _, source := range Registry() {
		t.Run(source.ID, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
			defer cancel()
			text, err := fetch.Read(ctx, source)
			if err != nil {
				t.Fatal(err)
			}
			drafts, err := model.Extract(ctx, source, text)
			if err != nil {
				t.Fatal(err)
			}
			facts, err := ValidateFacts(source, text, drafts, time.Now().UTC())
			if err != nil {
				t.Fatal(err)
			}
			timed := 0
			for _, f := range facts {
				if len(f.Months) > 0 {
					timed++
				}
			}
			if timed == 0 && (source.ID == "tamu-citrus" || source.ID == "tamu-strawberry") {
				t.Fatal("no source-supported seasonal windows extracted")
			}
			t.Logf("Live model validated %d cited facts, %d seasonal windows from %s", len(facts), timed, source.ID)
		})
	}
}

func TestKnowledgePlacesLive(t *testing.T) {
	if os.Getenv("KNOWLEDGE_SOURCES_LIVE") != "1" {
		t.Skip("explicit location-provider opt-in required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	places, err := New(nil).Places(ctx, "Berlin, Germany")
	if err != nil || len(places) == 0 {
		t.Fatal("global garden location search", err)
	}
	if places[0].Country != "DE" || places[0].Timezone != "Europe/Berlin" {
		t.Fatal("country/timezone should come from the selected place", places[0].Country, places[0].Timezone)
	}
	t.Log("Global city search returned explicit country, region, coordinates and timezone.")
}
