package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

type uploadFixture struct {
	t     *testing.T
	calls map[string][]map[string]any
	asset string
}

func (f uploadFixture) CallTool(_ context.Context, p *sdk.CallToolParams) (*sdk.CallToolResult, error) {
	args := p.Arguments.(map[string]any)
	f.calls[p.Name] = append(f.calls[p.Name], args)
	var data map[string]any
	switch p.Name {
	case "upload_prepare":
		data = map[string]any{"asset": map[string]any{"id": f.asset}}
	case "upload_finalize":
		data = map[string]any{"asset": map[string]any{"id": f.asset}}
	case "upload_status":
		data = map[string]any{"state": "READY"}
	case "photo_attach":
		data = map[string]any{"photo": map[string]any{"id": "fixture-photo"}}
	default:
		f.t.Fatalf("unexpected tool %s", p.Name)
	}
	return &sdk.CallToolResult{StructuredContent: map[string]any{"status": "ok", "data": data}}, nil
}
func TestUploadPreservesBytesCaptureMetadataAndStableOperation(t *testing.T) {
	original, err := os.ReadFile("../../internal/media/photodate/testdata/android.jpg")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "original.jpg")
	if err = os.WriteFile(path, original, 0600); err != nil {
		t.Fatal(err)
	}
	asset := "11111111-1111-4111-8111-111111111111"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "PUT" || r.URL.Path != "/agent/uploads/"+asset || r.Header.Get("Authorization") != "Bearer fixture-token" {
			t.Error("unexpected byte transfer request")
			w.WriteHeader(400)
			return
		}
		body, err := io.ReadAll(r.Body)
		if err != nil || !bytes.Equal(body, original) {
			t.Error("original camera bytes changed")
		}
		sum := sha256.Sum256(body)
		_ = json.NewEncoder(w).Encode(map[string]string{"sha256": hex.EncodeToString(sum[:])})
	}))
	defer server.Close()
	client, err := agentClient(server.URL+"/mcp", "fixture-token")
	if err != nil {
		t.Fatal(err)
	}
	fixture := uploadFixture{t, map[string][]map[string]any{}, asset}
	for range 2 {
		if _, err = uploadPhoto(context.Background(), fixture, client, server.URL+"/mcp", path, "22222222-2222-4222-8222-222222222222", "stable-camera-file", "America/Chicago"); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"upload_prepare", "upload_finalize", "photo_attach"} {
		calls := fixture.calls[name]
		if len(calls) != 2 || calls[0]["operation_id"] != calls[1]["operation_id"] {
			t.Fatal("retry changed operation identity", name)
		}
	}
	input := fixture.calls["photo_attach"][0]["input"].(map[string]any)
	if input["timezone"] != "America/Chicago" || input["taken_at"] != nil || input["date_source"] != nil {
		t.Fatal("client fabricated a capture date", input)
	}
}
