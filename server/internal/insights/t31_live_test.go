// Live acceptance (T31): real pair-compare against the configured
// vision provider. Skips unless CANOPY_LIVE_VISION=1 plus the
// CANOPY_VISION_* trio are set. The API key is read from the
// environment only and never printed.
package insights

import (
	"bytes"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"regexp"
	"testing"
	"time"

	dbgen "canopy/server/db/generated"
)

func liveVisionEnv(t *testing.T) (baseURL, apiKey, model string) {
	t.Helper()
	if os.Getenv("CANOPY_LIVE_VISION") != "1" {
		t.Skip("live vision acceptance is opt-in")
	}
	baseURL, apiKey, model = os.Getenv("CANOPY_VISION_BASE_URL"),
		os.Getenv("CANOPY_VISION_API_KEY"), os.Getenv("CANOPY_VISION_MODEL")
	if baseURL == "" || apiKey == "" || model == "" {
		t.Fatal("CANOPY_VISION_BASE_URL/API_KEY/MODEL must all be set for live acceptance")
	}
	return
}

// drawPlant renders a deterministic stylised potted plant; grow>0 adds
// a larger canopy and an extra leaf so a pair reads as later growth.
func drawPlant(t *testing.T, grow int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 400, 500))
	for y := 0; y < 500; y++ {
		for x := 0; x < 400; x++ {
			img.Set(x, y, color.RGBA{240, 240, 235, 255})
		}
	}
	// terracotta pot
	for y := 340; y < 440; y++ {
		for x := 140; x < 260; x++ {
			img.Set(x, y, color.RGBA{190, 100, 60, 255})
		}
	}
	// stem
	for y := 200 - grow*30; y < 345; y++ {
		for x := 196; x < 204; x++ {
			img.Set(x, y, color.RGBA{60, 120, 60, 255})
		}
	}
	// canopy leaves (ellipses approximated)
	leaf := func(cx, cy, rx, ry int, c color.RGBA) {
		for y := cy - ry; y <= cy+ry; y++ {
			for x := cx - rx; x <= cx+rx; x++ {
				dx, dy := float64(x-cx)/float64(rx), float64(y-cy)/float64(ry)
				if dx*dx+dy*dy <= 1 {
					img.Set(x, y, c)
				}
			}
		}
	}
	green := color.RGBA{40, 140, 50, 255}
	leaf(150, 220-grow*20, 55, 35, green)
	leaf(250, 210-grow*20, 55, 35, green)
	leaf(200, 170-grow*30, 50, 32, green)
	if grow > 0 {
		leaf(120, 260, 40, 26, color.RGBA{70, 170, 70, 255}) // new leaf
		leaf(280, 250, 40, 26, color.RGBA{70, 170, 70, 255})
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("png: %v", err)
	}
	return buf.Bytes()
}

// drawMug renders an obviously unrelated object (incompatible pair).
func drawMug(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 400, 500))
	for y := 0; y < 500; y++ {
		for x := 0; x < 400; x++ {
			img.Set(x, y, color.RGBA{250, 248, 240, 255})
		}
	}
	for y := 200; y < 340; y++ {
		for x := 130; x < 250; x++ {
			img.Set(x, y, color.RGBA{70, 90, 140, 255})
		}
	}
	for y := 230; y < 300; y++ {
		for x := 250; x < 300; x++ {
			dx := float64(x-250) / 50
			dy := float64(y-265) / 35
			if dx*dx+dy*dy <= 1 && (x-250)*(x-250)+(y-265)*(y-265) > 900 {
				img.Set(x, y, color.RGBA{70, 90, 140, 255})
			}
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("png: %v", err)
	}
	return buf.Bytes()
}

func runLivePair(t *testing.T, olderPNG, newerPNG []byte) ObsResult {
	t.Helper()
	baseURL, apiKey, model := liveVisionEnv(t)
	ctx, pool := scratch(t)
	w := seedWorld(t, ctx, pool)

	aOld := seedAdapterAsset(t, ctx, pool, w.WorkspaceID, "PHOTO", "READY", "image/png", "ws/old.png")
	aNew := seedAdapterAsset(t, ctx, pool, w.WorkspaceID, "PHOTO", "READY", "image/png", "ws/new.png")
	st := &fakeStore{objs: map[string][]byte{"ws/old.png": olderPNG, "ws/new.png": newerPNG}}
	pOld := seedPhotoAt(t, ctx, pool, w, aOld, time.Now().UTC().Add(-30*24*time.Hour))
	pNew := seedPhotoAt(t, ctx, pool, w, aNew, time.Now().UTC())

	v, err := NewVisionAnalyzer(baseURL, apiKey, model, 150*time.Second, 256<<10, 15<<20)
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	v.Q, v.Store, v.SB = dbgen.New(pool), st, nil
	s := newSvc(pool, defaultLimits(), &Router{Ports: []Port{NoProvider{}, v}})
	s.PromptVersion = "canopy-vision-live/v1"
	s.AttemptTimeout = 150 * time.Second

	run, probs, err := s.Request(ctx, w.Actor, RequestInput{
		Kind: "VISION", PlantID: uuidString(w.PlantID),
		PhotoIDs: []string{uuidString(pOld), uuidString(pNew)},
	}, nil)
	noProbs(t, probs, err)
	if err := s.ProcessRun(ctx, jobFor(run.ID)); err != nil {
		t.Fatalf("process: %v", err)
	}
	done := getRun(t, ctx, pool, run.ID)
	if done.State != dbgen.AnalysisRunStateSUCCEEDED {
		t.Fatalf("live pair-compare state=%s failure=%+v", done.State, done.FailureCode)
	}
	t.Logf("provider=%s model=%s prompt=%s costNULL=%v",
		done.Provider.String, done.Model.String, done.PromptVersion.String,
		!done.CostActualMicroUsd.Valid)
	var res ObsResult
	if err := json.Unmarshal(done.Result, &res); err != nil {
		t.Fatalf("result: %v", err)
	}
	for _, o := range res.VisibleObservations {
		t.Logf("observation: %s (evidence %d)", o.Text, len(o.EvidenceIDs))
	}
	for _, h := range res.Hypotheses {
		t.Logf("hypothesis [%s]: %s", h.Confidence, h.Text)
	}
	for _, m := range res.MissingInformation {
		t.Logf("missing: %s", m)
	}
	return res
}

func TestLiveVisionPairCompare(t *testing.T) {
	res := runLivePair(t, drawPlant(t, 0), drawPlant(t, 1))
	if len(res.VisibleObservations) == 0 {
		t.Fatal("a real pair must yield at least one visible observation")
	}
}

func TestLiveVisionAbstainsOnIncompatiblePair(t *testing.T) {
	res := runLivePair(t, drawPlant(t, 0), drawMug(t))
	// Honest abstention: refusal stated, and no quantitative growth or
	// comparison-rate claims anywhere in the stored result.
	if len(res.MissingInformation) == 0 {
		t.Fatal("abstention must state what prevented comparison")
	}
	quant := regexp.MustCompile(`(?i)\d+\s*(cm|mm|inch|inches|ft|feet|%|percent)|faster|slower|growth rate`)
	texts := append([]string{}, res.MissingInformation...)
	for _, o := range res.VisibleObservations {
		texts = append(texts, o.Text)
	}
	for _, h := range res.Hypotheses {
		texts = append(texts, h.Text)
	}
	for _, s := range texts {
		if quant.MatchString(s) {
			t.Fatalf("incompatible pair produced a quantitative comparison claim: %q", s)
		}
	}
	fmt.Println("abstention verified against live provider")
}
