package knowledge

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"testing"
	"time"
)

func TestHarvestTimingCoverageLive(t *testing.T) {
	if os.Getenv("KNOWLEDGE_MODEL_LIVE") != "1" {
		t.Skip("explicit model/source opt-in required")
	}
	for _, source := range Registry() {
		var want []int
		switch source.ID {
		case "tamu-pomegranate":
			want = []int{9, 10}
		case "tamu-strawberry":
			want = []int{3, 4}
		case "tamu-fig":
			want = []int{5, 8, 9, 10, 11}
		default:
			continue
		}
		t.Run(source.ID, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
			defer cancel()
			text, err := NewFetcher(nil).Read(ctx, source)
			if err != nil {
				t.Fatal(err)
			}
			for _, passage := range harvestPassages(text) {
				t.Logf("Source audit timing concepts: %v", missingTiming(passage, ""))
			}
			model := NewModel(os.Getenv("CANOPY_VISION_BASE_URL"), os.Getenv("CANOPY_VISION_API_KEY"), os.Getenv("CANOPY_VISION_MODEL"), "none")
			model.HTTP.Transport = roundTrip(func(r *http.Request) (*http.Response, error) {
				response, err := http.DefaultTransport.RoundTrip(r)
				if err != nil {
					return nil, err
				}
				b, err := io.ReadAll(io.LimitReader(response.Body, (1<<20)+1))
				response.Body.Close()
				if err != nil {
					return nil, err
				}
				response.Body = io.NopCloser(bytes.NewReader(b))
				var envelope struct {
					Choices []struct{ Message struct{ Content string } }
				}
				if json.Unmarshal(b, &envelope) == nil && len(envelope.Choices) == 1 {
					var out struct{ Facts []DraftFact }
					if json.Unmarshal([]byte(envelope.Choices[0].Message.Content), &out) == nil {
						for _, d := range out.Facts {
							_, validation := ValidateFacts(source, text, []DraftFact{d}, time.Now())
							t.Logf("Draft validation: kind=%s label=%s evidenceChars=%d timingChars=%d result=%v", d.Kind, d.Label, len(d.Evidence), len(d.Timing), validation)
						}
					}
				}
				return response, nil
			})
			drafts, err := model.Extract(ctx, source, text)
			if err != nil {
				t.Fatal(err)
			}
			facts, err := ValidateFacts(source, text, drafts, time.Now().UTC())
			if err != nil {
				t.Fatal(err)
			}
			months := map[int]bool{}
			for _, f := range facts {
				if f.Kind == "HARVEST" {
					for _, m := range f.Months {
						months[m] = true
					}
					t.Logf("Validated harvest: %s, months=%v, basis=%s", f.Label, f.Months, f.TimingBasis)
				}
			}
			for _, m := range want {
				if !months[m] {
					t.Errorf("source-supported harvest month %d missing from fresh extraction", m)
				}
			}
		})
	}
}
