package main

import (
	"bytes"
	"context"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestEndpointAndCredentialBoundary(t *testing.T) {
	for _, endpoint := range []string{"http://example.com/mcp", "https://user:secret@example.com/mcp", "https://example.com/mcp?token=secret", "file:///mcp", "https://example.com/anything"} {
		if _, err := agentClient(endpoint, "fixture-token"); err == nil {
			t.Fatal("accepted unsafe endpoint", endpoint)
		}
	}
	for _, endpoint := range []string{"http://127.0.0.1:8080/mcp", "http://localhost:8080/mcp", "https://example.com/mcp"} {
		if _, err := agentClient(endpoint, "fixture-token"); err != nil {
			t.Fatal(err)
		}
	}
	redirected := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Error("redirect target received credentials") }))
	defer redirected.Close()
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, redirected.URL, http.StatusFound) }))
	defer origin.Close()
	client, _ := agentClient(origin.URL+"/mcp", "fixture-token")
	if _, err := client.Get(origin.URL + "/mcp"); err == nil {
		t.Fatal("redirect followed")
	}
	if _, err := client.Get(redirected.URL + "/mcp"); err == nil {
		t.Fatal("credential sent to another origin")
	}
}

func TestCLIHelperProcess(t *testing.T) {
	if os.Getenv("CANOPY_MCP_TEST_HELPER") != "1" {
		return
	}
	if err := run(context.Background(), []string{"--url", os.Getenv("CANOPY_MCP_TEST_ENDPOINT")}); err != nil {
		os.Exit(2)
	}
	os.Exit(0)
}
func TestActualStdioProxyDiscoveryImagesAndRevocation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	remote := sdk.NewServer(&sdk.Implementation{Name: "fixture", Version: "1"}, nil)
	remote.AddTool(&sdk.Tool{Name: "photo_get", InputSchema: map[string]any{"type": "object", "properties": map[string]any{}}}, func(context.Context, *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
		return &sdk.CallToolResult{StructuredContent: map[string]any{"status": "ok"}, Content: []sdk.Content{&sdk.ImageContent{Data: []byte("fixture-image"), MIMEType: "image/jpeg"}}}, nil
	})
	stream := sdk.NewStreamableHTTPHandler(func(*http.Request) *sdk.Server { return remote }, &sdk.StreamableHTTPOptions{Stateless: true})
	var revoked atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer fixture-token" || revoked.Load() {
			http.Error(w, "revoked", 401)
			return
		}
		stream.ServeHTTP(w, r)
	}))
	defer server.Close()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestCLIHelperProcess$")
	cmd.Env = append(os.Environ(), "CANOPY_MCP_TEST_HELPER=1", "CANOPY_MCP_TEST_ENDPOINT="+server.URL+"/mcp", "CANOPY_AGENT_TOKEN=fixture-token")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	reader, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	writer, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = writer.Close()
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		if strings.Contains(stderr.String(), "fixture-token") {
			t.Error("token leaked into stderr")
		}
	}()
	session, err := sdk.NewClient(&sdk.Implementation{Name: "test-agent", Version: "1"}, nil).Connect(ctx, &sdk.IOTransport{Reader: reader, Writer: writer}, nil)
	if err != nil {
		t.Fatal("stdio handshake", err)
	}
	defer session.Close()
	tools, err := session.ListTools(ctx, nil)
	if err != nil || len(tools.Tools) != 1 {
		t.Fatal("tool discovery", err)
	}
	result, err := session.CallTool(ctx, &sdk.CallToolParams{Name: "photo_get", Arguments: map[string]any{}})
	if err != nil || result.IsError || len(result.Content) != 1 {
		t.Fatal("forwarded image", err)
	}
	img, ok := result.Content[0].(*sdk.ImageContent)
	if !ok || string(img.Data) != "fixture-image" {
		t.Fatal("image lost in proxy")
	}
	// Wait for the successful HTTP response to finish before changing authority.
	revoked.Store(true)
	if _, err = session.CallTool(ctx, &sdk.CallToolParams{Name: "photo_get", Arguments: map[string]any{}}); err == nil {
		t.Fatal("revoked connection call succeeded")
	}
}
