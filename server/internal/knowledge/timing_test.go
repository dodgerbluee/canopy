package knowledge

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestCompleteCompoundTiming(t *testing.T) {
	for _, c := range []struct{ text, months, basis string }{
		{"September for early varieties continuing through October for later varieties", "[9 10]", "MONTH"},
		{"late spring and late summer through fall", "[5 8 9 10 11]", "SEASON"},
		{"March and late summer through fall", "[3 8 9 10 11]", "SEASON"},
		{"late winter through early spring", "[2 3]", "SEASON"},
		{"early winter through early spring", "[1 2 3 12]", "SEASON"},
		{"February, May and September", "[2 5 9]", "MONTH"},
		{"after harvest in September", "[]", "UNSPECIFIED"},
		{"before March", "[]", "UNSPECIFIED"},
	} {
		t.Run(c.text, func(t *testing.T) {
			months, basis := ParseTiming(c.text)
			if fmt.Sprint(months) != c.months || basis != c.basis {
				t.Fatalf("got %v %s, want %s %s", months, basis, c.months, c.basis)
			}
		})
	}
}

func TestMixedTimingDoesNotShiftExplicitMonthsWithHemisphere(t *testing.T) {
	months, basis := parseTimingForHemisphere("March and summer", "SOUTHERN")
	if fmt.Sprint(months) != "[1 2 3 12]" || basis != "SEASON" {
		t.Fatal(months, basis)
	}
	months, basis = parseTimingForHemisphere("September through October", "SOUTHERN")
	if fmt.Sprint(months) != "[9 10]" || basis != "MONTH" {
		t.Fatal(months, basis)
	}
	months, basis = parseTimingForHemisphere("summer", "")
	if len(months) != 0 || basis != "UNSPECIFIED" {
		t.Fatal(months, basis)
	}
}

func TestTimingCompletenessValidation(t *testing.T) {
	cases := []struct {
		name, text, evidence, timing, summary string
		valid                                 bool
	}{
		{"pomegranate loses October", "Early varieties mature in September, continuing through October for later varieties.", "Early varieties mature in September, continuing through October for later varieties.", "September", "Early varieties mature in September; later ones continue through October.", false},
		{"pomegranate partial quote and summary", "Early varieties mature in September, continuing through October for later varieties.", "Early varieties mature in September", "September", "Early varieties mature in September.", false},
		{"complete pomegranate", "Early varieties mature in September, continuing through October for later varieties.", "Early varieties mature in September, continuing through October for later varieties.", "September, continuing through October for later varieties", "Early varieties mature in September; later ones continue through October.", true},
		{"fig loses second crop", "The first crop ripens in late spring; the main crop ripens in late summer through fall.", "The first crop ripens in late spring; the main crop ripens in late summer through fall.", "late spring", "First crop in late spring; main crop in late summer through fall.", false},
		{"complete fig", "The first crop ripens in late spring; the main crop ripens in late summer through fall.", "The first crop ripens in late spring; the main crop ripens in late summer through fall.", "late spring; the main crop ripens in late summer through fall", "First crop in late spring; main crop in late summer through fall.", true},
		{"separate sentence not widened", "Fruit matures in September. Fertilize in February, May and September.", "Fruit matures in September.", "September", "Fruit matures in September depending on conditions.", true},
		{"mixed actions cannot supply a narrow window", "Fertilize in March and harvest in September.", "Fertilize in March", "March", "Fertilize in March depending on conditions.", false},
		{"mixed actions cannot widen one phase", "Fertilize in March and harvest in September.", "Fertilize in March and harvest in September.", "March and harvest in September", "Harvest in September depending on conditions.", false},
		{"unverified summary month", "Fruit matures in September.", "Fruit matures in September.", "September", "Fruit matures in September through November.", false},
		{"season paraphrase", "Flowers appear in March.", "Flowers appear in March.", "March", "Spring flowers depend on the plant's conditions.", true},
		{"omitted timing triggers repair", "Fruit matures in September through October.", "Fruit matures in September through October.", "", "Fruit matures in September through October.", false},
		{"negative source cannot become positive window", "Do not fertilize in winter.", "fertilize in winter.", "winter", "Fertilize in winter depending on conditions.", false},
		{"relative source cannot supply isolated month", "Prune after harvest in September.", "Prune after harvest in September.", "September", "Prune after harvest in September.", false},
		{"relative source can remain untimed", "Prune after harvest in September.", "Prune after harvest in September.", "", "Prune after harvest in September.", true},
		{"relative formula does not cancel separate explicit dates", "Fruit ripens six months after bloom. Early varieties mature in September through October.", "Fruit ripens six months after bloom. Early varieties mature in September through October.", "September through October", "Early varieties mature in September through October depending on conditions.", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := ValidateFacts(Registry()[0], c.text, []DraftFact{{Kind: "HARVEST", Label: "Fruit maturity", Summary: c.summary, Evidence: c.evidence, Timing: c.timing}}, time.Now())
			if (err == nil) != c.valid {
				t.Fatalf("valid=%v, err=%v", c.valid, err)
			}
		})
	}
}

func TestModelRepairsIncompleteTimingOrKeepsItUntimed(t *testing.T) {
	for _, repair := range []bool{false, true} {
		t.Run(fmt.Sprint(repair), func(t *testing.T) {
			text := "Early varieties mature in September, continuing through October for later varieties."
			calls := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				var request struct {
					Messages []struct {
						Content string `json:"content"`
					} `json:"messages"`
				}
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Error(err)
				}
				if calls == 2 && !strings.Contains(request.Messages[0].Content, "incomplete timing") {
					t.Error("repair did not receive completeness feedback")
				}
				timing := "September"
				if calls == 2 && repair {
					timing = "September, continuing through October for later varieties"
				}
				body, _ := json.Marshal(map[string]any{"facts": []map[string]any{{"kind": "HARVEST", "label": "Ripening window", "summary": "Early varieties mature in September; later ones continue through October.", "evidence": text, "timing": timing, "timingUnverified": true}}})
				_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": string(body)}}}})
			}))
			defer srv.Close()
			drafts, err := NewModel(srv.URL, "", "fake", "").Extract(context.Background(), Registry()[0], text)
			if err != nil {
				t.Fatal(err)
			}
			facts, err := ValidateFacts(Registry()[0], text, drafts, time.Now())
			if err != nil || calls != 2 || len(facts) != 1 {
				t.Fatalf("facts=%v calls=%d err=%v", facts, calls, err)
			}
			if repair {
				if fmt.Sprint(facts[0].Months) != "[9 10]" || facts[0].TimingText == "" {
					t.Fatalf("lost complete timing: %+v", facts[0])
				}
			} else if len(facts[0].Months) != 0 || facts[0].TimingBasis != "UNSPECIFIED" {
				t.Fatalf("partial timing reached calendar: %+v", facts[0])
			}
			if facts[0].ExtractionVersion != ExtractionVersion {
				t.Fatal("missing extraction provenance")
			}
		})
	}
}

func TestModelRepairDoesNotDiscardValidatedFacts(t *testing.T) {
	text := "Fruit matures in September through October. Remove unwanted shoots regularly."
	good := DraftFact{Kind: "HARVEST", Label: "Maturity window", Summary: "Fruit matures in September through October depending on conditions.", Evidence: "Fruit matures in September through October.", Timing: "September through October"}
	bad := DraftFact{Kind: "PRUNE", Label: "Pruning", Summary: "Remove unwanted shoots regularly.", Evidence: "An invented supporting sentence."}
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		drafts := []DraftFact{good, bad}
		if calls == 2 {
			var request struct{ Messages []struct{ Content string } }
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Error(err)
			}
			var payload struct{ PreviousDraft []DraftFact }
			if err := json.Unmarshal([]byte(request.Messages[1].Content), &payload); err != nil {
				t.Error(err)
			}
			if len(payload.PreviousDraft) != 1 || payload.PreviousDraft[0].Kind != "PRUNE" {
				t.Error("repair should receive only invalid facts")
			}
			bad.Evidence = "Remove unwanted shoots regularly."
			duplicate := good
			duplicate.Label = "Repeated maturity window"
			duplicate.Evidence = strings.TrimSuffix(good.Evidence, ".")
			drafts = []DraftFact{bad, duplicate}
		}
		body, _ := json.Marshal(map[string]any{"facts": drafts})
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": string(body)}}}})
	}))
	defer srv.Close()
	drafts, err := NewModel(srv.URL, "", "fake", "").Extract(context.Background(), Registry()[0], text)
	if err != nil {
		t.Fatal(err)
	}
	facts, err := ValidateFacts(Registry()[0], text, drafts, time.Now())
	if err != nil || calls != 2 || len(facts) != 2 {
		t.Fatalf("facts=%v calls=%d err=%v", facts, calls, err)
	}
	if facts[0].Kind != "HARVEST" || fmt.Sprint(facts[0].Months) != "[9 10]" {
		t.Fatalf("repair lost validated harvest: %+v", facts)
	}
}

func TestModelAuditsAnEntirelyOmittedCropWindow(t *testing.T) {
	for _, repair := range []bool{false, true} {
		t.Run(fmt.Sprint(repair), func(t *testing.T) {
			first := "The first crop ripens in late spring."
			second := "The main crop ripens in late summer through fall."
			text := first + " " + second + " Irrigate in February."
			firstDraft := DraftFact{Kind: "HARVEST", Label: "First crop", Summary: "The first crop ripens in late spring depending on conditions.", Evidence: first, Timing: "late spring"}
			secondDraft := DraftFact{Kind: "HARVEST", Label: "Main crop", Summary: "The main crop ripens in late summer through fall depending on conditions.", Evidence: second, Timing: "late summer through fall"}
			calls := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				drafts := []DraftFact{firstDraft}
				if calls == 2 {
					var request struct{ Messages []struct{ Content string } }
					_ = json.NewDecoder(r.Body).Decode(&request)
					var payload struct{ UncoveredHarvestPassages []string }
					_ = json.Unmarshal([]byte(request.Messages[1].Content), &payload)
					if len(payload.UncoveredHarvestPassages) != 1 || !strings.Contains(payload.UncoveredHarvestPassages[0], "main crop") {
						t.Error("omitted source window not sent for repair")
					}
					drafts = []DraftFact{}
					if repair {
						drafts = []DraftFact{secondDraft}
					}
				}
				body, _ := json.Marshal(map[string]any{"facts": drafts})
				_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": string(body)}}}})
			}))
			defer srv.Close()
			drafts, err := NewModel(srv.URL, "", "fake", "").Extract(context.Background(), Registry()[0], text)
			if err != nil {
				t.Fatal(err)
			}
			facts, err := ValidateFacts(Registry()[0], text, drafts, time.Now())
			if err != nil || calls != 2 {
				t.Fatal("bounded coverage repair", err, calls)
			}
			if repair {
				if len(facts) != 2 || fmt.Sprint(facts[1].Months) != "[8 9 10 11]" {
					t.Fatalf("second crop missing: %+v", facts)
				}
			} else if len(facts) != 1 || len(facts[0].Months) != 0 {
				t.Fatalf("partial harvest calendar should stay untimed: %+v", facts)
			}
		})
	}
}
