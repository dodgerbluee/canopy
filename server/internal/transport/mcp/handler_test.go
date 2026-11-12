package mcptransport

import (
	"net/http/httptest"
	"testing"
)

func TestHTTPAuthenticationBoundary(t *testing.T) {
	h := New(nil, nil, []string{"localhost:8080"})
	for _, tc := range []struct {
		host, origin, cookie string
		status               int
	}{{"localhost:8080", "", "", 401}, {"localhost:8080", "", "browser-session", 401}, {"attacker.example", "", "", 403}, {"localhost:8080", "https://attacker.example", "", 403}} {
		r := httptest.NewRequest("POST", "http://localhost:8080/mcp", nil)
		r.Host = tc.host
		r.Header.Set("Origin", tc.origin)
		r.Header.Set("Cookie", tc.cookie)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != tc.status {
			t.Fatal(w.Code, tc)
		}
		if w.Header().Get("Cache-Control") != "private, no-store" {
			t.Fatal("private cache policy missing")
		}
	}
}
