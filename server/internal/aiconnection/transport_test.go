package aiconnection

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

type roundtrip func(*http.Request) (*http.Response, error)

func (f roundtrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestProviderProtocols(t *testing.T) {
	for _, protocol := range []string{OpenAI, Anthropic, Gemini} {
		t.Run(protocol, func(t *testing.T) {
			c := Config{Protocol: protocol, Endpoint: "https://provider.example", Model: "fixture-model", APIKey: "fixture-secret", SupportsVision: true, TokenLimitField: "MAX_COMPLETION_TOKENS"}
			m := &Manager{Transport: roundtrip(func(r *http.Request) (*http.Response, error) {
				raw, _ := io.ReadAll(r.Body)
				var body map[string]any
				if err := json.Unmarshal(raw, &body); err != nil {
					t.Fatal(err)
				}
				if strings.Contains(string(raw), c.APIKey) || r.Header.Get("Cookie") != "" {
					t.Fatal("credential placed in content or cookie forwarded")
				}
				var reply string
				switch protocol {
				case OpenAI:
					if r.URL.Path != "/v1/chat/completions" || r.Header.Get("Authorization") != "Bearer fixture-secret" || body["max_tokens"] != nil || body["temperature"] != nil || body["max_completion_tokens"] != float64(512) {
						t.Fatal("OpenAI request shape")
					}
					reply = `{"choices":[{"message":{"content":"{\"status\":\"ok\"}"}}]}`
				case Anthropic:
					if r.URL.Path != "/v1/messages" || r.Header.Get("x-api-key") != c.APIKey || r.Header.Get("anthropic-version") != "2026-10-21" || body["system"] == nil || body["temperature"] != nil || !strings.Contains(string(raw), `"media_type":"image/png"`) {
						t.Fatal("Anthropic request shape")
					}
					reply = `{"content":[{"type":"text","text":"{\"status\":\"ok\"}"}],"stop_reason":"end_turn"}`
				case Gemini:
					if r.URL.Path != "/v1beta/models/fixture-model:generateContent" || r.Header.Get("x-goog-api-key") != c.APIKey || body["systemInstruction"] == nil || !strings.Contains(string(raw), `"inlineData"`) {
						t.Fatal("Gemini request shape")
					}
					reply = `{"candidates":[{"content":{"parts":[{"thought":true,"text":"ignore reasoning"},{"text":"{\"status\":\"ok\"}"}]},"finishReason":"STOP"}]}`
				}
				return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(reply))}, nil
			})}
			ok, msg := m.Probe(context.Background(), c, true)
			if !ok {
				t.Fatal(msg)
			}
		})
	}
}
func TestAPIBasePaths(t *testing.T) {
	for _, tc := range []struct{ base, want string }{{"https://x", "https://x/v1/messages"}, {"https://x/v1", "https://x/v1/messages"}, {"https://x/proxy/v1/", "https://x/proxy/v1/messages"}} {
		if got := apiPath(tc.base, "/v1/messages", "/messages"); got != tc.want {
			t.Fatal(got)
		}
	}
}
func TestTransportRedactsErrorsAndDoesNotRedirect(t *testing.T) {
	requests := 0
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.Header().Set("Location", "http://127.0.0.1:1/leak")
		w.WriteHeader(307)
		_, _ = w.Write([]byte("fixture-secret"))
	}))
	defer s.Close()
	c := Config{Protocol: OpenAI, Endpoint: s.URL, Model: "fixture", APIKey: "fixture-secret", DeploymentLocal: true}
	m := &Manager{}
	ok, msg := m.Probe(context.Background(), c, false)
	if ok || strings.Contains(msg, c.APIKey) || requests != 1 {
		t.Fatal(ok, msg, requests)
	}
	m.Transport = roundtrip(func(*http.Request) (*http.Response, error) { return nil, errors.New(c.APIKey) })
	ok, msg = m.Probe(context.Background(), c, false)
	if ok || strings.Contains(msg, c.APIKey) {
		t.Fatal("transport error leaked")
	}
}
func TestLocalAddressPolicyAndEndpointValidation(t *testing.T) {
	c := Config{Protocol: OpenAI, Endpoint: "https://169.254.169.254", Model: "x", DeploymentLocal: true}
	ok, _ := (&Manager{}).Probe(context.Background(), c, false)
	if ok {
		t.Fatal("metadata address allowed")
	}
	c.Endpoint = "https://8.8.8.8"
	ok, _ = (&Manager{}).Probe(context.Background(), c, false)
	if ok {
		t.Fatal("public address marked local")
	}
	for _, endpoint := range []string{"https://user:secret@example.com", "https://example.com/?key=x", "http://example.com", "https://example.com/#key"} {
		_, err := normalize(SaveInput{Name: "test", Protocol: OpenAI, Endpoint: endpoint, Model: "model", TokenLimitField: "MAX_TOKENS"})
		if err == nil {
			t.Fatal("accepted", endpoint)
		}
	}
	_, data, _ := strings.Cut(testImage, ",")
	b, err := base64.StdEncoding.DecodeString(data)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = png.Decode(strings.NewReader(string(b))); err != nil {
		t.Fatal("sample image invalid", err)
	}
}
func TestVaultConcurrentPersistenceAndIdentity(t *testing.T) {
	path := filepath.Join(t.TempDir(), "private", "key")
	vaults := make(chan *Vault, 8)
	errs := make(chan error, 8)
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			v, err := LoadVault(path)
			if err != nil {
				errs <- err
			} else {
				vaults <- v
			}
		}()
	}
	wg.Wait()
	close(vaults)
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	first := <-vaults
	sealed, err := first.seal("fixture-key", "workspace|connection")
	if err != nil || strings.Contains(string(sealed), "fixture-key") {
		t.Fatal(err)
	}
	for v := range vaults {
		plain, err := v.open(sealed, "workspace|connection")
		if err != nil || plain != "fixture-key" {
			t.Fatal("concurrent key mismatch")
		}
		if _, err = v.open(sealed, "other|connection"); err == nil {
			t.Fatal("cross-identity decrypt")
		}
	}
	if err = os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err = LoadVault(path); err == nil {
		t.Fatal("public keyfile accepted")
	}
}
