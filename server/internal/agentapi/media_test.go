package agentapi_test

import (
	"bytes"
	"canopy/server/internal/agentaccess"
	httpx "canopy/server/internal/transport/http"
	mcptransport "canopy/server/internal/transport/mcp"
	"context"
	"encoding/json"
	"github.com/google/uuid"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"syscall"
	"testing"
	"time"
)

func TestAgentOriginalUploadThroughWorker(t *testing.T) {
	binary := os.Getenv("CANOPY_TEST_WORKER_BINARY")
	if binary == "" {
		t.Skip("current worker binary required")
	}
	api, m, ws, plant, root := fixtureWithStorage(t)
	ctx := context.Background()
	issued, err := m.Save(ctx, agentaccess.ID(ws), agentaccess.SaveInput{ID: uuid.NewString(), Name: "Media fixture", Scopes: []string{"garden:read", "media:write", "photos:read"}, PlantIDs: []string{plant}, ExpiresAt: time.Now().Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	principal, _ := m.Verify(ctx, issued.Token)
	data, err := os.ReadFile("../media/photodate/testdata/android.jpg")
	if err != nil {
		t.Fatal(err)
	}
	reserved := api.Call(ctx, principal, "upload_prepare", map[string]any{"operation_id": "reserve-one", "input": map[string]any{"purpose": "PHOTO", "filename": "android-original.jpg", "claimed_bytes": len(data)}})
	if len(reserved.Problems) > 0 {
		t.Fatal(reserved)
	}
	id := reserved.Data.(map[string]any)["asset"].(map[string]any)["id"].(string)
	auth := mcptransport.New(m, api, nil)
	uploads := httpx.NewAgentUploads(api, httpx.NewUploads(m.Pool, api.Store, slog.New(slog.DiscardHandler)))
	mux := http.NewServeMux()
	mux.HandleFunc("PUT /agent/uploads/{assetID}", func(w http.ResponseWriter, r *http.Request) {
		req, ok := auth.Authenticate(w, r)
		if ok {
			uploads.ServeHTTP(w, req)
		}
	})
	server := httptest.NewServer(mux)
	defer server.Close()
	u, _ := url.Parse(server.URL)
	auth.Hosts[u.Host] = true
	request, _ := http.NewRequest("PUT", server.URL+"/agent/uploads/"+id, bytes.NewReader(data))
	request.Header.Set("Authorization", "Bearer "+issued.Token)
	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	receiptBytes, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatal(response.StatusCode, string(receiptBytes))
	}
	var receipt struct {
		SHA string `json:"sha256"`
	}
	_ = json.Unmarshal(receiptBytes, &receipt)
	finalized := api.Call(ctx, principal, "upload_finalize", map[string]any{"operation_id": "finalize-one", "input": map[string]any{"asset_id": id, "sha256": receipt.SHA}})
	if len(finalized.Problems) > 0 {
		t.Fatal(finalized)
	}
	cmd := exec.Command(binary)
	cmd.Env = append(os.Environ(), "DATABASE_URL="+m.Pool.Config().ConnString(), "CANOPY_ENV=test", "CANOPY_LOG_LEVEL=warn", "CANOPY_STORAGE_ROOT="+root, "CANOPY_AI_CREDENTIALS_KEY_FILE=", "CANOPY_MEDIA_IMAGE=canopy-media:r2", "CANOPY_AI_BASE_URL=", "CANOPY_VISION_BASE_URL=", "CANOPY_KNOWLEDGE_BASE_URL=")
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
	ready := false
	for deadline := time.Now().Add(90 * time.Second); time.Now().Before(deadline); {
		var state string
		if err = m.Pool.QueryRow(ctx, `SELECT state FROM assets WHERE id=$1`, id).Scan(&state); err != nil {
			t.Fatal(err)
		}
		if state == "READY" {
			ready = true
			break
		}
		if state == "FAILED" {
			t.Fatal("processor failed")
		}
		time.Sleep(200 * time.Millisecond)
	}
	if !ready {
		t.Fatal("worker did not finish")
	}
	attached := api.Call(ctx, principal, "photo_attach", map[string]any{"operation_id": "attach-one", "input": map[string]any{"plant_id": plant, "asset_id": id}})
	if len(attached.Problems) > 0 {
		t.Fatal(attached)
	}
	photo := attached.Data.(map[string]any)["photo"].(map[string]any)
	if photo["date_source"] != "EXIF" || photo["taken_at"] != "2026-10-31T05:30:39Z" {
		t.Fatal("original capture metadata lost", photo)
	}
	preview := api.Call(ctx, principal, "photo_get", map[string]any{"photo_id": photo["id"]})
	if len(preview.Problems) > 0 || len(preview.Image) == 0 {
		t.Fatal("preview unavailable", preview)
	}
	t.Log("Bearer upload → existing validation → queued worker → original capture-date attach → private sanitized preview passed.")
}
