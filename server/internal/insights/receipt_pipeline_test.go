package insights

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	dbgen "canopy/server/db/generated"
	"canopy/server/internal/media/processor"
	platformmedia "canopy/server/internal/platform/media"
)

// Run with CANOPY_TEST_WORKER_BINARY pointing to the actual executable.
// This crosses the API/worker process boundary, catching an outdated worker
// that invalidates a current receipt snapshot before calling OCR. All records
// and storage live in the harness's disposable schema and temporary directory.
func TestReceiptPipelineThroughCurrentWorker(t *testing.T) {
	binary := os.Getenv("CANOPY_TEST_WORKER_BINARY")
	if binary == "" {
		t.Skip("CANOPY_TEST_WORKER_BINARY not set")
	}
	if out, err := exec.Command("docker", "image", "inspect", "canopy-media:r2").CombinedOutput(); err != nil {
		t.Fatalf("real OCR image required: %v %s", err, out)
	}
	ctx, pool := scratch(t)
	w := seedWorld(t, ctx, pool)
	root := t.TempDir()
	store, err := platformmedia.NewFSStore(root)
	if err != nil {
		t.Fatal(err)
	}
	key := "f331232e-1111-4111-8111-000000000001"
	pixels, err := os.ReadFile(filepath.Join("..", "..", "..", "tests", "ai-eval", "fixtures", "receipt-clean.png"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Stage(key, bytes.NewReader(pixels), int64(len(pixels))); err != nil {
		t.Fatal(err)
	}
	if err := store.Commit(key); err != nil {
		t.Fatal(err)
	}
	asset := seedAdapterAsset(t, ctx, pool, w.WorkspaceID, "RECEIPT", "READY", "image/png", key)
	ocr := &ReceiptOCR{Q: dbgen.New(pool), Store: store, Langs: "eng", MaxPages: 3, SB: &processor.DockerSandbox{Image: "canopy-media:r2", Memory: "512m", PIDs: 64}}
	svc := newSvc(pool, defaultLimits(), &Router{Ports: []Port{ocr}})
	run, probs, err := svc.Request(ctx, w.Actor, RequestInput{Kind: "RECEIPT_EXTRACTION", ReceiptAssetID: uuidString(asset)}, nil)
	noProbs(t, probs, err)
	snap, err := ParseSnapshot(run.InputSnapshot)
	if err != nil {
		t.Fatal(err)
	}
	if snap.Schema != SnapSchema || snap.Plant.ID != "" {
		t.Fatalf("receipt snapshot shape: schema=%q plant=%q", snap.Schema, snap.Plant.ID)
	}
	cmd := exec.Command(binary)
	cmd.Env = append(os.Environ(), "DATABASE_URL="+pool.Config().ConnString(), "CANOPY_ENV=test", "CANOPY_LOG_LEVEL=warn", "CANOPY_STORAGE_ROOT="+root, "CANOPY_MEDIA_IMAGE=canopy-media:r2", "CANOPY_AI_BASE_URL=", "CANOPY_AI_API_KEY=", "CANOPY_VISION_BASE_URL=", "CANOPY_VISION_API_KEY=", "CANOPY_PLANTNET_API_KEY=")
	workerLog, err := os.CreateTemp(root, "worker-log")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = workerLog.Close() })
	cmd.Stdout = workerLog
	cmd.Stderr = workerLog
	output := func() string { raw, _ := os.ReadFile(workerLog.Name()); return string(raw) }
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	t.Cleanup(func() {
		_ = cmd.Process.Signal(os.Interrupt)
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			_ = cmd.Process.Kill()
			<-done
		}
	})
	deadline := time.Now().Add(45 * time.Second)
	for time.Now().Before(deadline) {
		row := getRun(t, ctx, pool, run.ID)
		switch row.State {
		case dbgen.AnalysisRunStateSUCCEEDED:
			var result ReceiptResult
			if err := json.Unmarshal(row.Result, &result); err != nil {
				t.Fatal(err)
			}
			if result.Currency != "USD" || result.PurchasedOn != "2026-05-14" || row.Attempts != 1 || row.Provider.String != "local-ocr" {
				t.Fatalf("expected real local OCR result: state=%s attempts=%d provider=%s result=%+v", row.State, row.Attempts, row.Provider.String, result)
			}
			t.Logf("current snapshot %s crossed queued API→worker→real Docker OCR: SUCCEEDED, attempts=%d, currency=%s, date=%s", snap.Schema, row.Attempts, result.Currency, result.PurchasedOn)
			var count int
			if err := pool.QueryRow(ctx, "SELECT count(*) FROM purchases").Scan(&count); err != nil {
				t.Fatal(err)
			}
			if count != 0 {
				t.Fatal("receipt extraction created a purchase")
			}
			return
		case dbgen.AnalysisRunStateSTALE, dbgen.AnalysisRunStateFAILED, dbgen.AnalysisRunStateCANCELLED:
			t.Fatalf("receipt run rejected: state=%s attempts=%d failure=%v worker=%s", row.State, row.Attempts, row.FailureCode, output())
		}
		select {
		case err := <-done:
			done <- err
			t.Fatalf("worker exited: %v %s", err, output())
		case <-time.After(100 * time.Millisecond):
		}
	}
	t.Fatalf("worker did not finish queued receipt: %s", output())
}
