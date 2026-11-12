package graphql

import (
	"context"
	"net/http"
	"os"
	"os/exec"
	"syscall"
	"testing"
	"time"
)

func TestAutomaticOriginalCaptureDateHTTP(t *testing.T) {
	m := newMediaEnv(t, 0)
	ctx := context.Background()
	plant := createKnowledgePlant(t, m, "Capture fixture")
	b, err := os.ReadFile("../../media/photodate/testdata/android.jpg")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.pool.Exec(ctx, `UPDATE workspace SET timezone='America/Chicago' WHERE id=$1`, m.wsID); err != nil {
		t.Fatal(err)
	}
	asset, _ := readyAsset(t, m, "PHOTO", "image/jpeg", "4127.jpg", map[string][]byte{"original": b})
	photo := attachPhotoMut(t, m, plant, asset, map[string]any{"dateSource": "UPLOAD_FALLBACK", "preferCaptureDate": true, "timezone": "Asia/Tokyo", "viewpoint": "WHOLE"})
	if photo["dateSource"] != "EXIF" || photo["takenAt"] != "2026-10-31T05:30:39Z" {
		t.Fatal("original capture time lost", photo)
	}
	var occurred time.Time
	if err = m.pool.QueryRow(ctx, `SELECT occurred_at FROM journal_entries WHERE source_id=$1`, photo["id"]).Scan(&occurred); err != nil || occurred.UTC().Format(time.RFC3339Nano) != "2026-10-31T05:30:39.125Z" {
		t.Fatal("journal date", err, occurred)
	}
	page := dataOf(t, m.gql(calendarQuery, map[string]any{"from": "2026-10-01", "to": "2026-10-31", "first": 100}), "calendarEvents")
	found := false
	for _, v := range page["items"].([]any) {
		item := v.(map[string]any)
		if item["entityId"] == photo["id"] {
			found = true
			if item["day"] != "2026-10-31" {
				t.Fatal("calendar date", item)
			}
		}
	}
	if !found {
		t.Fatal("capture missing from historical calendar", page)
	}
	// Explicit manual dates still win even if auto resolution is requested.
	asset, _ = readyAsset(t, m, "PHOTO", "image/jpeg", "original.jpg", map[string][]byte{"original": b})
	photo = attachPhotoMut(t, m, plant, asset, map[string]any{"dateSource": "USER", "takenAt": "2026-09-03T12:00:00Z", "preferCaptureDate": true, "viewpoint": "WHOLE"})
	if photo["dateSource"] != "USER" || photo["takenAt"] != "2026-09-03T12:00:00Z" {
		t.Fatal("manual date overwritten")
	}
	asset, _ = readyAsset(t, m, "PHOTO", "image/jpeg", "stripped.jpg", map[string][]byte{"original": jpegBytes(100)})
	photo = attachPhotoMut(t, m, plant, asset, map[string]any{"dateSource": "EXIF", "preferCaptureDate": true, "viewpoint": "WHOLE"})
	if photo["dateSource"] != "UPLOAD_FALLBACK" {
		t.Fatal("missing metadata falsely labelled capture", photo)
	}
}

func TestCaptureThroughCurrentMediaWorker(t *testing.T) {
	binary := os.Getenv("CANOPY_TEST_WORKER_BINARY")
	if binary == "" {
		t.Skip("explicit current worker binary required")
	}
	storageRoot := t.TempDir()
	m := newMediaEnvStorage(t, 0, nil, storageRoot)
	ctx := context.Background()
	plant := createKnowledgePlant(t, m, "Queued capture fixture")
	bytes, err := os.ReadFile("../../media/photodate/testdata/android.jpg")
	if err != nil {
		t.Fatal(err)
	}
	asset := m.reserve(t, "PHOTO", "android-gallery.jpg", len(bytes), nil)
	id := asset["id"].(string)
	status, _ := m.put(t, id, bytes, m.cookie, m.csrf)
	if status != http.StatusOK {
		t.Fatal("original upload failed", status)
	}
	finalized := m.gql(finalizeMutGQL, map[string]any{"input": map[string]any{"assetId": id, "sha256": shaHex(bytes)}})
	if len(finalized.Errors) > 0 || len(finalized.Data["finalizeUpload"].(map[string]any)["problems"].([]any)) > 0 {
		t.Fatal("finalize failed", finalized.Raw)
	}
	cmd := exec.Command(binary)
	cmd.Env = append(os.Environ(), "DATABASE_URL="+m.pool.Config().ConnString(), "CANOPY_ENV=test", "CANOPY_LOG_LEVEL=warn", "CANOPY_STORAGE_ROOT="+storageRoot, "CANOPY_AI_CREDENTIALS_KEY_FILE=", "CANOPY_MEDIA_IMAGE=canopy-media:r2", "CANOPY_AI_BASE_URL=", "CANOPY_VISION_BASE_URL=", "CANOPY_KNOWLEDGE_BASE_URL=")
	log, err := os.CreateTemp(t.TempDir(), "worker-log")
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	cmd.Stdout = log
	cmd.Stderr = log
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	t.Cleanup(func() {
		_ = cmd.Process.Signal(syscall.SIGTERM)
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			_ = cmd.Process.Kill()
			<-done
		}
	})
	deadline := time.Now().Add(90 * time.Second)
	for time.Now().Before(deadline) {
		var state string
		if err = m.pool.QueryRow(ctx, `SELECT state FROM assets WHERE id=$1`, id).Scan(&state); err != nil {
			t.Fatal(err)
		}
		if state == "FAILED" {
			t.Fatal("processor failed")
		}
		if state == "READY" {
			photo := attachPhotoMut(t, m, plant, id, map[string]any{"dateSource": "UPLOAD_FALLBACK", "preferCaptureDate": true, "viewpoint": "WHOLE", "timezone": "America/Chicago"})
			if photo["dateSource"] != "EXIF" || photo["takenAt"] != "2026-10-31T05:30:39Z" {
				t.Fatal("capture date lost after worker processing", photo)
			}
			var originalKey string
			if err = m.pool.QueryRow(ctx, `SELECT storage_key FROM asset_variants WHERE asset_id=$1 AND name='display'`, id).Scan(&originalKey); err != nil {
				t.Fatal(err)
			}
			display, err := m.store.Open(originalKey)
			if err != nil {
				t.Fatal(err)
			}
			display.Close()
			var jobs int
			if err = m.pool.QueryRow(ctx, `SELECT count(*) FROM river_job WHERE state='completed' AND kind LIKE '%media%'`).Scan(&jobs); err != nil || jobs < 1 {
				t.Fatal("no completed media job", err)
			}
			t.Log("HTTP original upload → durable queue → current media worker → READY display bytes → original EXIF capture date → attached photo. Completed media jobs:", jobs)
			return
		}
		select {
		case err := <-done:
			done <- err
			t.Fatal("worker exited", err)
		default:
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatal("current media worker timed out")
}
