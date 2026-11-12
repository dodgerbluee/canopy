package insights

import (
	"bytes"
	"canopy/server/internal/media/processor"
	platformmedia "canopy/server/internal/platform/media"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	dbgen "canopy/server/db/generated"
	"github.com/jackc/pgx/v5/pgxpool"
)

const receiptVisionJSON = `{"seller":"Example Nursery","currency":"USD","purchasedOn":"2026-10-15","lines":[{"description":"Fig","amountMinor":"4999"},{"description":"Alma fig","amountMinor":"6500"},{"description":"Blackberry","amountMinor":"2999"},{"description":"Pomegranate","amountMinor":"4999"}],"totalMinor":"21106","taxMinor":"1609","shippingMinor":"","needsReconciliation":true}`

func receiptRouter(t *testing.T, cfg RouterConfig, store *fakeStore) (*Router, world, context.Context, *pgxpool.Pool) {
	t.Helper()
	ctx, pool := scratch(t)
	w := seedWorld(t, ctx, pool)
	if store == nil {
		store = &fakeStore{objs: map[string][]byte{}}
	}
	r, err := BuildRouter(pool, store, &fakeSB{script: func(_ string, argv []string) (processor.Output, error) {
		if len(argv) > 0 && argv[0] == "vipsheader" {
			return processor.Output{Stdout: []byte("1\n")}, nil
		}
		return processor.Output{}, errors.New("optional OCR unavailable")
	}}, cfg)
	if err != nil {
		t.Fatal(err)
	}
	return r, w, ctx, pool
}
func receiptVisionConfig(url string) RouterConfig {
	return RouterConfig{VisionBaseURL: url, VisionAPIKey: "test-key", VisionModel: "receipt-vision-model", VisionTimeout: 5 * time.Second, VisionMaxRespBytes: 64 << 10, VisionMaxImageB: 2 << 20, VisionLocal: true, OCRLangs: "eng", OCRMaxPages: 3}
}
func receiptVisionPort(t *testing.T, r *Router) *ReceiptVision {
	t.Helper()
	for _, p := range r.Ports {
		if v, ok := p.(*ReceiptVision); ok {
			return v
		}
	}
	t.Fatal("vision configured but receipt port missing")
	return nil
}

func TestReceiptVisionImageWireAndProvenance(t *testing.T) {
	fake := &fakeOpenAI{h: func(int) (int, string) { return 200, chatBody(receiptVisionJSON) }}
	srv := httptest.NewServer(fake)
	defer srv.Close()
	st := &fakeStore{objs: map[string][]byte{"source.png": png1x1, "normalized.jpg": []byte("full resolution receipt"), "preview.jpg": []byte("tiny preview")}}
	r, w, ctx, pool := receiptRouter(t, receiptVisionConfig(srv.URL), st)
	receiptVisionPort(t, r)
	asset := seedAdapterAsset(t, ctx, pool, w.WorkspaceID, "RECEIPT", "READY", "image/png", "source.png")
	_, err := pool.Exec(ctx, `INSERT INTO asset_variants(asset_id,name,storage_key,byte_count) VALUES($1,'original','normalized.jpg',23),($1,'display','preview.jpg',12)`, asset)
	if err != nil {
		t.Fatal(err)
	}
	out, err := r.Analyze(ctx, AnalyzeInput{Capability: CapReceiptExtractor, WorkspaceID: uuidString(w.WorkspaceID), ReceiptAssetID: uuidString(asset), PromptVersion: "receipt-test/v1", RepairOf: "invalid previous output"})
	if err != nil {
		t.Fatal(err)
	}
	if fake.hits != 1 || fake.path != "/v1/chat/completions" || fake.auth != "Bearer test-key" || fake.last.Model != "receipt-vision-model" {
		t.Fatal("receipt did not use configured image chat provider")
	}
	content := fake.last.Messages[1].Content.([]interface{})
	var dataURL string
	for _, part := range content {
		p := part.(map[string]interface{})
		if p["type"] == "image_url" {
			img := p["image_url"].(map[string]interface{})
			dataURL = img["url"].(string)
		}
	}
	if dataURL != "data:image/jpeg;base64,"+base64.StdEncoding.EncodeToString(st.objs["normalized.jpg"]) {
		t.Fatal("receipt must send full-resolution normalized image, never thumbnail/display or OCR text alone")
	}
	var got ReceiptResult
	if err := json.Unmarshal(out.Raw, &got); err != nil {
		t.Fatal(err)
	}
	if got.TotalMinor != "21106" || len(got.Lines) != 4 || !got.NeedsReconciliation {
		t.Fatalf("receipt draft: %+v", got)
	}
	if out.Provider != "openai-compatible" || out.Model != "srv-vision-1" || out.UsageCostMicroUSD != nil {
		t.Fatal("actual provider provenance and unknown cost must survive")
	}
	if r.Capabilities(ctx)[CapReceiptExtractor].ExternalData {
		t.Fatal("explicit deployment-local model incorrectly marked external")
	}
	var purchases int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM purchases").Scan(&purchases); err != nil || purchases != 0 {
		t.Fatal("extraction must not create a purchase")
	}

}

func TestReceiptVisionRejectsWrongTargetsBeforeDispatch(t *testing.T) {
	fake := &fakeOpenAI{h: func(int) (int, string) { return 200, chatBody(receiptVisionJSON) }}
	srv := httptest.NewServer(fake)
	defer srv.Close()
	st := &fakeStore{objs: map[string][]byte{"image.png": png1x1, "photo.png": png1x1, "pending.png": png1x1}}
	r, w, ctx, pool := receiptRouter(t, receiptVisionConfig(srv.URL), st)
	receipt := seedAdapterAsset(t, ctx, pool, w.WorkspaceID, "RECEIPT", "READY", "image/png", "image.png")
	photo := seedAdapterAsset(t, ctx, pool, w.WorkspaceID, "PHOTO", "READY", "image/png", "photo.png")
	pending := seedAdapterAsset(t, ctx, pool, w.WorkspaceID, "RECEIPT", "PROCESSING", "image/png", "pending.png")
	other := seedWorld(t, ctx, pool)
	for _, tc := range []struct {
		name, workspace, asset string
		cap                    Capability
	}{
		{"wrong purpose", uuidString(w.WorkspaceID), uuidString(photo), CapReceiptExtractor},
		{"not ready", uuidString(w.WorkspaceID), uuidString(pending), CapReceiptExtractor},
		{"other workspace", uuidString(other.WorkspaceID), uuidString(receipt), CapReceiptExtractor},
		{"bad id", uuidString(w.WorkspaceID), "invalid", CapReceiptExtractor},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := r.Analyze(ctx, AnalyzeInput{Capability: tc.cap, WorkspaceID: tc.workspace, ReceiptAssetID: tc.asset})
			if err == nil {
				t.Fatal("invalid target accepted")
			}
		})
	}
	if fake.hits != 0 {
		t.Fatal("invalid receipt bytes were dispatched")
	}
}

func TestReceiptVisionBoundsAndFailures(t *testing.T) {
	for _, tc := range []struct {
		name      string
		status    int
		body      string
		max       int64
		wantClass ErrorClass
	}{
		{"unauthorized", 401, "private provider text", 2 << 20, ClassPermanent},
		{"retryable", 503, "private provider text", 2 << 20, ClassRetryable},
		{"bad envelope", 200, "{}", 2 << 20, ClassInvalidOutput},
		{"bad receipt schema", 200, chatBody(`{"seller":"x","lines":[],"totalMinor":"invented","needsReconciliation":true}`), 2 << 20, ClassInvalidOutput},
		{"image bound", 200, chatBody(receiptVisionJSON), 2, ClassPermanent},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fake := &fakeOpenAI{h: func(int) (int, string) { return tc.status, tc.body }}
			srv := httptest.NewServer(fake)
			defer srv.Close()
			cfg := receiptVisionConfig(srv.URL)
			cfg.VisionMaxImageB = tc.max
			r, w, ctx, pool := receiptRouter(t, cfg, &fakeStore{objs: map[string][]byte{"image.png": png1x1, "photo.png": png1x1, "pending.png": png1x1}})
			asset := seedAdapterAsset(t, ctx, pool, w.WorkspaceID, "RECEIPT", "READY", "image/png", "image.png")
			_, err := r.Analyze(ctx, AnalyzeInput{Capability: CapReceiptExtractor, WorkspaceID: uuidString(w.WorkspaceID), ReceiptAssetID: uuidString(asset)})
			var pe *ProviderError
			if !errors.As(err, &pe) || pe.Class != tc.wantClass {
				t.Fatalf("wrong failure class: %v", err)
			}
			if strings.Contains(err.Error(), "private provider text") {
				t.Fatal("provider body leaked")
			}
			if tc.name == "image bound" && fake.hits != 0 {
				t.Fatal("oversize image dispatched")
			}
		})
	}
}

// Explicit live test: provider connection is supplied in memory by the local
// verification runner. The receipt source stays outside repository evidence.
func TestReceiptVisionActualReceipt(t *testing.T) {
	file := os.Getenv("CANOPY_TEST_RECEIPT_FILE")
	if file == "" {
		t.Skip("CANOPY_TEST_RECEIPT_FILE not set")
	}
	pixels, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	cfg := receiptVisionConfig(os.Getenv("CANOPY_TEST_VISION_BASE"))
	cfg.VisionAPIKey = os.Getenv("CANOPY_TEST_VISION_KEY")
	cfg.VisionModel = os.Getenv("CANOPY_TEST_VISION_MODEL")
	cfg.VisionTimeout = 150 * time.Second
	cfg.VisionReasoningEffort = "none"
	cfg.VisionMaxImageB = 15 << 20
	r, w, ctx, pool := receiptRouter(t, cfg, &fakeStore{objs: map[string][]byte{"actual-receipt.png": pixels}})
	receiptVisionPort(t, r).Vision.SB = &processor.DockerSandbox{Image: "canopy-media:r2", Memory: "512m", PIDs: 64}
	s := newSvc(pool, defaultLimits(), r)
	s.AttemptTimeout = 150 * time.Second
	asset := seedAdapterAsset(t, ctx, pool, w.WorkspaceID, "RECEIPT", "READY", "image/png", "actual-receipt.png")
	run, probs, err := s.Request(ctx, w.Actor, RequestInput{Kind: "RECEIPT_EXTRACTION", ReceiptAssetID: uuidString(asset)}, nil)
	noProbs(t, probs, err)
	if err := s.ProcessRun(ctx, jobFor(run.ID)); err != nil {
		t.Fatal(err)
	}
	done := getRun(t, ctx, pool, run.ID)
	if done.State != dbgen.AnalysisRunStateSUCCEEDED {
		t.Fatalf("actual receipt state %s, failure %v", done.State, done.FailureCode)
	}
	var got ReceiptResult
	if err := json.Unmarshal(done.Result, &got); err != nil {
		t.Fatal(err)
	}
	if got.TotalMinor != "21106" || got.TaxMinor != "1609" || len(got.Lines) != 4 || !got.NeedsReconciliation {
		t.Fatalf("actual receipt extraction incomplete: total=%s tax=%s lines=%d review=%v", got.TotalMinor, got.TaxMinor, len(got.Lines), got.NeedsReconciliation)
	}
	expected := []string{"4999", "6500", "2999", "4999"}
	for i, line := range got.Lines {
		if line.AmountMinor != expected[i] {
			t.Fatalf("line %d amount %s expected %s", i, line.AmountMinor, expected[i])
		}
	}
	assertActualReceiptDetails(t, got)
	t.Logf("Actual receipt: date and cultivar names verified; four item amounts and tax reconcile to21106; model=%s; owner review remains required", done.Model.String)
	var purchases int
	_ = pool.QueryRow(ctx, "SELECT count(*) FROM purchases").Scan(&purchases)
	if purchases != 0 {
		t.Fatal("extraction created purchase")
	}
}

func TestReceiptVisionQueuedWorker(t *testing.T) {
	binary := os.Getenv("CANOPY_TEST_WORKER_BINARY")
	file := os.Getenv("CANOPY_TEST_RECEIPT_FILE")
	if binary == "" || file == "" {
		t.Skip("explicit actual receipt and worker binary required")
	}
	pixels, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	ctx, pool := scratch(t)
	w := seedWorld(t, ctx, pool)
	root := t.TempDir()
	store, err := platformmedia.NewFSStore(root)
	if err != nil {
		t.Fatal(err)
	}
	key := "e670fc36-1111-4111-8111-000000000002"
	if _, err := store.Stage(key, bytes.NewReader(pixels), int64(len(pixels))); err != nil {
		t.Fatal(err)
	}
	if err := store.Commit(key); err != nil {
		t.Fatal(err)
	}
	cfg := receiptVisionConfig(os.Getenv("CANOPY_TEST_VISION_BASE"))
	cfg.VisionAPIKey = os.Getenv("CANOPY_TEST_VISION_KEY")
	cfg.VisionModel = os.Getenv("CANOPY_TEST_VISION_MODEL")
	cfg.VisionTimeout = 150 * time.Second
	cfg.VisionReasoningEffort = "none"
	cfg.VisionMaxImageB = 15 << 20
	provider, err := BuildRouter(pool, store, &processor.DockerSandbox{Image: "canopy-media:r2", Memory: "512m", PIDs: 64}, cfg)
	if err != nil {
		t.Fatal(err)
	}
	svc := newSvc(pool, defaultLimits(), provider)
	asset := seedAdapterAsset(t, ctx, pool, w.WorkspaceID, "RECEIPT", "READY", "image/png", key)
	run, probs, err := svc.Request(ctx, w.Actor, RequestInput{Kind: "RECEIPT_EXTRACTION", ReceiptAssetID: uuidString(asset)}, nil)
	noProbs(t, probs, err)
	logFile, err := os.Create(filepath.Join(root, "worker.log"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = logFile.Close() })
	cmd := exec.Command(binary)
	cmd.Env = append(os.Environ(), "DATABASE_URL="+pool.Config().ConnString(), "CANOPY_ENV=test", "CANOPY_STORAGE_ROOT="+root, "CANOPY_LOG_LEVEL=warn", "CANOPY_AI_BASE_URL=", "CANOPY_AI_API_KEY=", "CANOPY_VISION_BASE_URL="+cfg.VisionBaseURL, "CANOPY_VISION_API_KEY="+cfg.VisionAPIKey, "CANOPY_VISION_MODEL="+cfg.VisionModel, "CANOPY_VISION_TIMEOUT=150s", "CANOPY_VISION_LOCAL=1", "CANOPY_VISION_REASONING_EFFORT=none")
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	finished := make(chan error, 1)
	go func() { finished <- cmd.Wait() }()
	t.Cleanup(func() {
		_ = cmd.Process.Signal(os.Interrupt)
		select {
		case <-finished:
		case <-time.After(10 * time.Second):
			_ = cmd.Process.Kill()
			<-finished
		}
	})
	deadline := time.Now().Add(150 * time.Second)
	for time.Now().Before(deadline) {
		current := getRun(t, ctx, pool, run.ID)
		switch current.State {
		case dbgen.AnalysisRunStateSUCCEEDED:
			var result ReceiptResult
			if err := json.Unmarshal(current.Result, &result); err != nil {
				t.Fatal(err)
			}
			if result.TotalMinor != "21106" || result.TaxMinor != "1609" || len(result.Lines) != 4 || !result.NeedsReconciliation {
				t.Fatalf("queued receipt incomplete: total=%s tax=%s lines=%d", result.TotalMinor, result.TaxMinor, len(result.Lines))
			}
			assertActualReceiptDetails(t, result)
			if current.Provider.String != "openai-compatible" || current.Model.String == "" {
				t.Fatal("queued receipt lost vision provenance")
			}
			t.Logf("Queued actual receipt vision succeeded: model=%s, four items, total21106, tax1609, review required", current.Model.String)
			return
		case dbgen.AnalysisRunStateSTALE, dbgen.AnalysisRunStateFAILED, dbgen.AnalysisRunStateCANCELLED:
			t.Fatalf("queued vision receipt ended %s, attempts=%d, failure=%v", current.State, current.Attempts, current.FailureCode)
		}
		select {
		case err := <-finished:
			finished <- err
			t.Fatalf("worker exited %v", err)
		case <-time.After(200 * time.Millisecond):
		}
	}
	t.Fatal("actual receipt vision worker timed out")
}

func TestReceiptVisionPDFUsesExistingOCRPath(t *testing.T) {
	fake := &fakeOpenAI{h: func(int) (int, string) { return 200, chatBody(receiptVisionJSON) }}
	srv := httptest.NewServer(fake)
	defer srv.Close()
	ctx, pool := scratch(t)
	w := seedWorld(t, ctx, pool)
	st := &fakeStore{objs: map[string][]byte{"receipt.pdf": []byte("%PDF synthetic")}}
	sb := &fakeSB{script: func(dir string, argv []string) (processor.Output, error) {
		if argv[0] == "pdftoppm" {
			return processor.Output{}, os.WriteFile(filepath.Join(dir, "page-1.png"), []byte("png"), 0o600)
		}
		return processor.Output{Stdout: []byte("ACME\nTOTAL 12.34 USD\n")}, nil
	}}
	router, err := BuildRouter(pool, st, sb, receiptVisionConfig(srv.URL))
	if err != nil {
		t.Fatal(err)
	}
	asset := seedAdapterAsset(t, ctx, pool, w.WorkspaceID, "RECEIPT", "READY", "application/pdf", "receipt.pdf")
	out, err := router.Analyze(ctx, AnalyzeInput{Capability: CapReceiptExtractor, WorkspaceID: uuidString(w.WorkspaceID), ReceiptAssetID: uuidString(asset)})
	if err != nil {
		t.Fatal(err)
	}
	var draft ReceiptResult
	_ = json.Unmarshal(out.Raw, &draft)
	if fake.hits != 0 || out.Provider != "local-ocr" || draft.TotalMinor != "1234" {
		t.Fatal("PDF receipt lost the existing OCR path")
	}
}

func TestReceiptVisionCapabilityDisclosure(t *testing.T) {
	for _, tc := range []struct {
		name                  string
		local, text, external bool
	}{
		{"remote default", false, false, true}, {"deployment local", true, false, false}, {"remote PDF structurer", true, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := receiptVisionConfig("http://localhost:9999")
			cfg.VisionLocal = tc.local
			if tc.text {
				cfg.AIBaseURL = "http://localhost:9998"
				cfg.AIModel = "text-model"
				cfg.AITimeout = time.Second
				cfg.AIMaxResponseBytes = 64 << 10
			}
			router, _, ctx, _ := receiptRouter(t, cfg, nil)
			cap := router.Capabilities(ctx)[CapReceiptExtractor]
			if !cap.Available || cap.Model != "receipt-vision-model" || cap.ExternalData != tc.external {
				t.Fatalf("receipt disclosure: %+v", cap)
			}
		})
	}
}

func assertActualReceiptDetails(t *testing.T, r ReceiptResult) {
	t.Helper()
	t.Logf("Receipt printed date: %s", r.PurchasedOn)
	if !strings.Contains(strings.ToLower(r.Seller), "covington") {
		t.Error("receipt seller lost")
	}
	if r.Currency != "" || r.ShippingMinor != "" {
		t.Error("unprinted currency/shipping must remain unknown")
	}
	if r.PurchasedOn != "2026-10-15" {
		t.Errorf("printed date: got %q expected 2026-10-15", r.PurchasedOn)
	}
	if len(r.Lines) != 4 {
		t.Fatalf("expected four lines, got %d", len(r.Lines))
	}
	words := [][]string{{"little miss figgy"}, {"alma"}, {"blackberry", "arapaho"}, {"pomegranate", "wonderful"}}
	expectedAmounts := []string{"4999", "6500", "2999", "4999"}
	for i, line := range r.Lines {
		if line.AmountMinor != expectedAmounts[i] {
			t.Errorf("line %d amount %q expected %q", i, line.AmountMinor, expectedAmounts[i])
		}
		t.Logf("Receipt line %d: %s; amount %s", i, line.Description, line.AmountMinor)
		d := strings.ToLower(line.Description)
		for _, word := range words[i] {
			if !strings.Contains(d, word) {
				t.Errorf("line %d missing %q: %s", i, word, line.Description)
			}
		}
		for _, bad := range []string{"fizzy", "fic ", "fi ", "cat.", "cat a", "514", "526", "507", "poi egranate", "blu ckberry"} {
			if strings.Contains(d, bad) {
				t.Errorf("line %d includes OCR artifact %q: %s", i, bad, line.Description)
			}
		}
	}
}

func TestReceiptEvidenceUsesCropCoordinatesScaleAndBoundedImages(t *testing.T) {
	fake := &fakeOpenAI{h: func(int) (int, string) { return 200, chatBody(receiptVisionJSON) }}
	srv := httptest.NewServer(fake)
	defer srv.Close()
	cfg := receiptVisionConfig(srv.URL)
	cfg.VisionReasoningEffort = "none"
	r, w, ctx, pool := receiptRouter(t, cfg, &fakeStore{objs: map[string][]byte{"receipt.png": png1x1}})
	port := receiptVisionPort(t, r)
	crops := 0
	scales := map[string]bool{}
	port.Vision.SB = &fakeSB{script: func(dir string, argv []string) (processor.Output, error) {
		name := func(s string) string { return filepath.Base(s) }
		if argv[0] == "vipsheader" {
			value := "701"
			if argv[2] == "height" {
				value = "1561"
			}
			return processor.Output{Stdout: []byte(value)}, nil
		}
		if argv[0] == "tesseract" {
			if argv[len(argv)-1] == "tsv" {
				return processor.Output{Stdout: []byte(tsvWord(56, 484, 136, 30, "10/.5/2026"))}, nil
			}
			if strings.Contains(strings.Join(argv, " "), "tessedit_char_whitelist") {
				return processor.Output{Stdout: []byte("10/15/2026")}, nil
			}
			return processor.Output{Stdout: []byte("Example Nursery\nViburnum shrub\nTax 1.00\nVISA 1234")}, nil
		}
		if argv[0] == "vips" {
			if argv[1] == "crop" {
				crops++
				if crops == 1 && strings.Join(argv[4:], " ") != "41 469 166 60" {
					t.Fatalf("crop must be left top width height: %v", argv)
				}
			}
			if argv[1] == "resize" {
				if len(argv) != 7 || argv[5] != "--kernel" || argv[6] != "lanczos3" {
					t.Fatalf("resize must use one scale argument: %v", argv)
				}
				scales[argv[4]] = true
			}
			if err := os.WriteFile(filepath.Join(dir, name(argv[3])), png1x1, 0600); err != nil {
				t.Fatal(err)
			}
			return processor.Output{}, nil
		}
		return processor.Output{}, errors.New("unexpected sandbox command")
	}}
	asset := seedAdapterAsset(t, ctx, pool, w.WorkspaceID, "RECEIPT", "READY", "image/png", "receipt.png")
	out, err := r.Analyze(ctx, AnalyzeInput{Capability: CapReceiptExtractor, WorkspaceID: uuidString(w.WorkspaceID), ReceiptAssetID: uuidString(asset), PromptVersion: "test"})
	if err != nil {
		t.Fatal(err)
	}
	var result ReceiptResult
	if err := json.Unmarshal(out.Raw, &result); err != nil {
		t.Fatal(err)
	}
	if result.PurchasedOn != "2026-10-15" || !(scales["3"] || scales["3.00000000"]) || !scales["4"] || crops != 4 {
		t.Fatalf("evidence date=%q scales=%v crops=%d", result.PurchasedOn, scales, crops)
	}
	if fake.last.ReasoningEffort != "none" {
		t.Fatal("configured reasoning effort missing")
	}
	images, bytesRead := 0, 0
	for _, part := range fake.last.Messages[1].Content.([]interface{}) {
		p := part.(map[string]interface{})
		if p["type"] == "image_url" {
			images++
			u := p["image_url"].(map[string]interface{})["url"].(string)
			encoded := strings.SplitN(u, ",", 2)[1]
			b, e := base64.StdEncoding.DecodeString(encoded)
			if e != nil {
				t.Fatal(e)
			}
			bytesRead += len(b)
		}
	}
	if images != 5 || int64(bytesRead) > cfg.VisionMaxImageB {
		t.Fatalf("image budget images=%d bytes=%d", images, bytesRead)
	}
}
