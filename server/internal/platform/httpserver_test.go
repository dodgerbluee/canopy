package platform

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestHealthAndReadinessDifferentiated(t *testing.T) {
	log := NewLogger(Config{LogLevel: "error"})
	mux := NewMux(func(ctx context.Context) error { return errors.New("db down") }, log)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("liveness while dependency down: got %d", resp.StatusCode)
	}
	resp, err = http.Get(srv.URL + "/readyz")
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 503 {
		t.Fatalf("readiness with failing dep: got %d", resp.StatusCode)
	}
	var env struct {
		Problems []Problem `json:"problems"`
	}
	body, _ := io.ReadAll(resp.Body)
	if err := json.Unmarshal(body, &env); err != nil || env.Problems[0].Code != "NOT_READY" {
		t.Fatalf("bad readiness envelope: %s", body)
	}

	mux2 := NewMux(func(ctx context.Context) error { return nil }, log)
	srv2 := httptest.NewServer(mux2)
	defer srv2.Close()
	resp, _ = http.Get(srv2.URL + "/readyz")
	if resp.StatusCode != 200 {
		t.Fatalf("readiness with healthy dep: got %d", resp.StatusCode)
	}
}

func TestNotFoundIsSafeEnvelope(t *testing.T) {
	log := NewLogger(Config{LogLevel: "error"})
	srv := httptest.NewServer(NewMux(func(context.Context) error { return nil }, log))
	defer srv.Close()
	resp, err := http.Post(srv.URL+"/graphql", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 404 {
		t.Fatalf("got %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	var env struct {
		Problems []Problem `json:"problems"`
	}
	if err := json.Unmarshal(body, &env); err != nil || env.Problems[0].Code != "NOT_FOUND" {
		t.Fatalf("bad envelope: %s", body)
	}
}

func TestGracefulShutdownIsBounded(t *testing.T) {
	log := NewLogger(Config{LogLevel: "error"})
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := NewServer(Config{LogLevel: "error"}, NewMux(func(context.Context) error { return nil }, log), log)
	srv.ShutdownTimeout = 300 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- srv.Run(ctx, func() error { return srv.Serve(ln) }) }()

	resp, err := http.Get("http://" + ln.Addr().String() + "/healthz")
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("server not serving: %v", err)
	}
	resp.Body.Close()

	cancel()
	start := time.Now()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("clean shutdown should return nil, got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("shutdown not bounded")
	}
	if time.Since(start) > time.Second {
		t.Fatal("shutdown took too long")
	}
}
