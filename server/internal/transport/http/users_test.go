package httpx

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"canopy/server/internal/identity"
)

func userHTTPEnv(t *testing.T) *testEnv {
	t.Helper()
	e := newTestEnv(t, false)
	e.srv.Close()
	users := identity.NewUsers(e.pool, e.sessions)
	users.Hasher = identity.NewPasswordHasher(identity.Argon2Params{Memory: 64, Iterations: 1, Parallelism: 1})
	oidc, err := identity.NewOIDC(users, filepath.Join(t.TempDir(), "oidc.key"), "http://localhost:7080")
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	auth := NewAuth(e.owners, e.sessions, identity.NewLoginLimiter(100), e.cfg, slog.New(slog.DiscardHandler))
	auth.Register(mux)
	auth.EnableUsers(mux, users, oidc)
	e.srv = httptest.NewServer(mux)
	t.Cleanup(e.srv.Close)
	return e
}
func TestBrowserSetupAndUserHTTPBoundaries(t *testing.T) {
	e := userHTTPEnv(t)
	res := e.call("GET", "/api/auth/options", reqOpts{})
	body := bodyString(t, res)
	if res.StatusCode != 200 || !json.Valid([]byte(body)) {
		t.Fatalf("options: %d %s", res.StatusCode, body)
	}
	setup := `{"username":"gardener","displayName":"Gardener","password":"garden-passphrase","workspaceName":"Trees","timezone":"America/Chicago","currency":"USD"}`
	res = e.call("POST", "/api/auth/setup", reqOpts{body: setup, contentType: "application/json", origin: "https://evil.invalid"})
	if res.StatusCode != 403 {
		t.Fatal("cross-origin setup accepted")
	}
	res.Body.Close()
	res = e.call("POST", "/api/auth/setup", reqOpts{body: setup, contentType: "application/json"})
	if res.StatusCode != 200 {
		t.Fatalf("setup: %s", bodyString(t, res))
	}
	ck := sessionCookie(t, res)
	var sess struct {
		CSRF string        `json:"csrfToken"`
		User identity.User `json:"user"`
	}
	if err := json.NewDecoder(res.Body).Decode(&sess); err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if sess.User.Role != "ADMIN" {
		t.Fatal("first user is not admin")
	}
	res = e.call("POST", "/api/auth/setup", reqOpts{body: setup, contentType: "application/json"})
	if res.StatusCode != 409 {
		t.Fatalf("second setup=%d", res.StatusCode)
	}
	res.Body.Close()
	member := `{"username":"helper","displayName":"Helper","role":"MEMBER","password":"helper-passphrase","disabled":false}`
	res = e.call("POST", "/api/auth/users", reqOpts{body: member, contentType: "application/json", cookie: ck})
	if res.StatusCode != 403 {
		t.Fatal("user mutation bypassed CSRF")
	}
	res.Body.Close()
	res = e.call("POST", "/api/auth/users", reqOpts{body: member, contentType: "application/json", cookie: ck, csrf: sess.CSRF})
	if res.StatusCode != 200 {
		t.Fatalf("create: %s", bodyString(t, res))
	}
	res.Body.Close()
	res = e.call("POST", "/api/auth/login", reqOpts{body: `{"username":"helper","password":"helper-passphrase"}`, contentType: "application/json"})
	if res.StatusCode != 200 {
		t.Fatalf("member login: %s", bodyString(t, res))
	}
	memberCookie := sessionCookie(t, res)
	res.Body.Close()
	for _, path := range []string{"/api/auth/users", "/api/auth/oidc/settings"} {
		res = e.call("GET", path, reqOpts{cookie: memberCookie})
		if res.StatusCode != 403 {
			t.Fatalf("member accessed %s (%d)", path, res.StatusCode)
		}
		res.Body.Close()
	}
	res = e.call("GET", "/api/auth/me", reqOpts{cookie: memberCookie})
	if res.StatusCode != 200 {
		t.Fatal("member profile unavailable")
	}
	var me identity.User
	_ = json.NewDecoder(res.Body).Decode(&me)
	res.Body.Close()
	if me.Username != "helper" || me.Role != "MEMBER" {
		t.Fatalf("wrong identity: %+v", me)
	}
	res = e.call("GET", "/api/auth/options", reqOpts{})
	body = bodyString(t, res)
	var opts struct {
		SetupRequired bool `json:"setupRequired"`
	}
	_ = json.Unmarshal([]byte(body), &opts)
	if opts.SetupRequired {
		t.Fatal("setup reopened")
	}
	// The public response never includes client configuration or user records.
	var count int
	if err := e.pool.QueryRow(context.Background(), "SELECT count(*) FROM auth_users").Scan(&count); err != nil || count != 2 {
		t.Fatalf("wrong account count: %d %v", count, err)
	}
}

func TestHandoverExchangeAndRegistrationHTTP(t *testing.T) {
	e := userHTTPEnv(t)
	owner, ws := e.bootstrap("original-owner-password")
	users := identity.NewUsers(e.pool, e.sessions)
	users.Hasher = identity.NewPasswordHasher(identity.Argon2Params{Memory: 64, Iterations: 1, Parallelism: 1})
	token, err := users.PrepareHandover(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	res := e.call("POST", "/api/auth/setup", reqOpts{body: `{"username":"new-owner","password":"new-owner-password","workspaceName":"Fresh","timezone":"UTC","currency":"USD"}`, contentType: "application/json"})
	if res.StatusCode != 409 {
		t.Fatal("public claim reopened")
	}
	res.Body.Close()
	res = e.call("POST", "/api/auth/setup/claim", reqOpts{body: `{"token":"` + token + `"}`, contentType: "application/json", origin: "https://evil.invalid"})
	if res.StatusCode != 403 {
		t.Fatal("cross-origin claim accepted")
	}
	res.Body.Close()
	res = e.call("POST", "/api/auth/setup/claim", reqOpts{body: `{"token":"` + token + `"}`, contentType: "application/json"})
	if res.StatusCode != 200 {
		t.Fatalf("exchange failed: %s", bodyString(t, res))
	}
	var cookie *http.Cookie
	for _, c := range res.Cookies() {
		if c.Name == "canopy_registration" {
			cookie = c
		}
	}
	res.Body.Close()
	if cookie == nil || !cookie.HttpOnly || cookie.SameSite != http.SameSiteStrictMode {
		t.Fatal("claim cookie lacks protections")
	}
	res = e.call("GET", "/api/auth/options", reqOpts{cookie: cookie})
	var opts struct {
		ClaimExisting bool `json:"claimExisting"`
	}
	if err = json.NewDecoder(res.Body).Decode(&opts); err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if !opts.ClaimExisting {
		t.Fatal("claim mode unavailable")
	}
	body := `{"username":"new-owner","password":"new-owner-password","workspaceName":"Do not change","timezone":"UTC","currency":"EUR"}`
	res = e.call("POST", "/api/auth/setup", reqOpts{body: body, contentType: "application/json", cookie: cookie})
	if res.StatusCode != 200 {
		t.Fatalf("claim failed: %s", bodyString(t, res))
	}
	res.Body.Close()
	var ownerAfter string
	if err = e.pool.QueryRow(context.Background(), "SELECT owner_id::text FROM workspace WHERE id=$1", ws).Scan(&ownerAfter); err != nil || ownerAfter != uuidString(owner) {
		t.Fatal("workspace owner changed", err)
	}
	res = e.call("POST", "/api/auth/setup", reqOpts{body: body, contentType: "application/json", cookie: cookie})
	if res.StatusCode != 400 {
		t.Fatal("handover replay accepted")
	}
	res.Body.Close()
}
