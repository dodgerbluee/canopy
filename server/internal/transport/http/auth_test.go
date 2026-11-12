package httpx

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	dbgen "canopy/server/db/generated"
	"canopy/server/internal/identity"
)

// AUTH suite per 05-verification: generic auth errors, cookie modes,
// CSRF+Origin, expiry/revocation/rotation, cross-workspace principals,
// recovery invalidation, private media/export denial, no signup.

func TestGenericAuthErrorsNoEnumeration(t *testing.T) {
	e := newTestEnv(t, false)
	// before any owner exists
	res := e.call("POST", "/api/auth/login", reqOpts{body: e.loginBody("x"), contentType: "application/json"})
	before := bodyString(t, res)
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("pre-bootstrap login: %d", res.StatusCode)
	}
	e.bootstrap("right-pw")
	res = e.call("POST", "/api/auth/login", reqOpts{body: e.loginBody("wrong-pw"), contentType: "application/json"})
	after := bodyString(t, res)
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("wrong password: %d", res.StatusCode)
	}
	if before != after {
		t.Fatalf("responses differ (enumeration): %q vs %q", before, after)
	}
	if !json.Valid([]byte(after)) || !strings.Contains(after, "INVALID_CREDENTIALS") {
		t.Fatalf("unexpected error envelope: %s", after)
	}
}

func TestLoginCookieAttributes(t *testing.T) {
	e := newTestEnv(t, false)
	e.bootstrap("pw")
	res := e.call("POST", "/api/auth/login", reqOpts{body: e.loginBody("pw"), contentType: "application/json"})
	defer res.Body.Close()
	ck := sessionCookie(t, res)
	if ck.Name != "canopy_session" || ck.Value == "" {
		t.Fatalf("bad cookie: %+v", ck)
	}
	if !ck.HttpOnly {
		t.Fatal("cookie must be HttpOnly")
	}
	if ck.SameSite != http.SameSiteLaxMode {
		t.Fatalf("SameSite = %v, want Lax", ck.SameSite)
	}
	if ck.Path != "/" {
		t.Fatalf("Path = %q", ck.Path)
	}
	if ck.Secure {
		t.Fatal("dev mode: Secure must be off (local HTTP exception)")
	}
	if ck.MaxAge != int((7 * 24 * time.Hour).Seconds()) {
		t.Fatalf("MaxAge = %d", ck.MaxAge)
	}
	if len(ck.Value) < 40 {
		t.Fatalf("cookie token too short: %d", len(ck.Value))
	}
}

func TestCookieSecureProdMode(t *testing.T) {
	e := newTestEnv(t, true) // CookieSecure=true (prod policy)
	e.bootstrap("pw")
	res := e.call("POST", "/api/auth/login", reqOpts{body: e.loginBody("pw"), contentType: "application/json"})
	defer res.Body.Close()
	ck := sessionCookie(t, res)
	if !ck.Secure {
		t.Fatal("prod: cookie must carry Secure")
	}
}

func TestLoginRejectsFormEncoding(t *testing.T) {
	e := newTestEnv(t, false)
	e.bootstrap("pw")
	res := e.call("POST", "/api/auth/login", reqOpts{
		body: "password=pw", contentType: "application/x-www-form-urlencoded",
	})
	defer res.Body.Close()
	if res.StatusCode != http.StatusUnsupportedMediaType {
		t.Fatalf("form login: %d", res.StatusCode)
	}
}

func TestLoginHostileOrigin(t *testing.T) {
	e := newTestEnv(t, false)
	e.bootstrap("pw")
	res := e.call("POST", "/api/auth/login", reqOpts{
		body: e.loginBody("pw"), contentType: "application/json", origin: "http://evil.example",
	})
	defer res.Body.Close()
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("hostile origin: %d", res.StatusCode)
	}
}

func TestLoginRateLimited(t *testing.T) {
	e := newTestEnv(t, false)
	e.bootstrap("pw")
	for i := range 10 {
		res := e.call("POST", "/api/auth/login", reqOpts{body: e.loginBody("wrong"), contentType: "application/json"})
		if res.StatusCode != http.StatusUnauthorized {
			t.Fatalf("attempt %d: %d, want 401", i+1, res.StatusCode)
		}
		res.Body.Close()
	}
	res := e.call("POST", "/api/auth/login", reqOpts{body: e.loginBody("pw"), contentType: "application/json"})
	defer res.Body.Close()
	if res.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("after limit: %d, want 429", res.StatusCode)
	}
}

func TestSessionBootstrapReturnsCSRF(t *testing.T) {
	e := newTestEnv(t, false)
	_, wsID := e.bootstrap("pw")
	ck, sr := e.login("pw")
	if sr.CSRFToken == "" || sr.WorkspaceID != uuidOf(wsID) || sr.ExpiresAt == "" {
		t.Fatalf("login payload: %+v", sr)
	}
	res := e.get(ck)
	b := bodyString(t, res)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("session: %d %s", res.StatusCode, b)
	}
	var got sessionResp
	if err := json.Unmarshal([]byte(b), &got); err != nil {
		t.Fatal(err)
	}
	if !got.Authenticated || got.CSRFToken == "" {
		t.Fatalf("session bootstrap: %+v", got)
	}
	// unauthenticated: generic 401, no csrf material anywhere
	res = e.call("GET", "/api/auth/session", reqOpts{noOrigin: true})
	b = bodyString(t, res)
	if res.StatusCode != http.StatusUnauthorized || strings.Contains(b, got.CSRFToken) {
		t.Fatalf("unauthenticated session: %d %s", res.StatusCode, b)
	}
	if !strings.Contains(b, "UNAUTHENTICATED") {
		t.Fatalf("cold anonymous must not claim expiry: %s", b)
	}
}

// T07 shell needs the client to explain a post-cold-load redirect: a stale
// cookie the client itself presented is reported as SESSION_EXPIRED, while a
// request with no session at all stays UNAUTHENTICATED. Both are 401 with the
// same generic message, so nothing is leaked.
func TestSessionExpiredVsUnauthenticated(t *testing.T) {
	e := newTestEnv(t, false)
	e.bootstrap("pw")
	ck, _ := e.login("pw")

	if _, err := e.pool.Exec(context.Background(),
		`UPDATE sessions SET last_seen_at = now() - interval '13 hours'`); err != nil {
		t.Fatal(err)
	}
	res := e.call("GET", "/api/auth/session", reqOpts{cookie: ck, noOrigin: true})
	b := bodyString(t, res)
	if res.StatusCode != http.StatusUnauthorized || !strings.Contains(b, "SESSION_EXPIRED") {
		t.Fatalf("aged session: %d %s", res.StatusCode, b)
	}

	res = e.call("GET", "/api/auth/session", reqOpts{noOrigin: true})
	b = bodyString(t, res)
	if res.StatusCode != http.StatusUnauthorized || !strings.Contains(b, "UNAUTHENTICATED") {
		t.Fatalf("no cookie: %d %s", res.StatusCode, b)
	}
}

func TestCSRFEnforcement(t *testing.T) {
	e := newTestEnv(t, false)
	e.bootstrap("pw")
	ck, sr := e.login("pw")

	res := e.call("POST", "/api/auth/logout", reqOpts{cookie: ck, contentType: "application/json"})
	if res.StatusCode != http.StatusForbidden || !strings.Contains(bodyString(t, res), "CSRF_REJECTED") {
		t.Fatalf("logout without csrf: %d", res.StatusCode)
	}
	res = e.call("POST", "/api/auth/logout", reqOpts{cookie: ck, contentType: "application/json", csrf: "forged"})
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("logout forged csrf: %d", res.StatusCode)
	}
	res = e.call("POST", "/api/auth/logout", reqOpts{cookie: ck, contentType: "application/json", csrf: sr.CSRFToken})
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("logout with csrf: %d", res.StatusCode)
	}
	// session is gone
	if res := e.get(ck); res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("post-logout session: %d", res.StatusCode)
	}
	res.Body.Close()
}

func TestMutationHostileOrigin(t *testing.T) {
	e := newTestEnv(t, false)
	e.bootstrap("pw")
	ck, sr := e.login("pw")
	res := e.call("POST", "/api/auth/logout", reqOpts{
		cookie: ck, contentType: "application/json", csrf: sr.CSRFToken, origin: "http://evil.example",
	})
	defer res.Body.Close()
	if res.StatusCode != http.StatusForbidden || !strings.Contains(bodyString(t, res), "ORIGIN_REJECTED") {
		t.Fatalf("hostile origin mutation: %d", res.StatusCode)
	}
}

func TestIdleExpiryHTTP(t *testing.T) {
	e := newTestEnv(t, false)
	e.bootstrap("pw")
	ck, _ := e.login("pw")
	if _, err := e.pool.Exec(context.Background(),
		"UPDATE sessions SET last_seen_at = now() - interval '13 hours'"); err != nil {
		t.Fatal(err)
	}
	res := e.get(ck)
	defer res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("idle-expired session: %d", res.StatusCode)
	}
}

func TestAbsoluteExpiryHTTP(t *testing.T) {
	e := newTestEnv(t, false)
	e.bootstrap("pw")
	ck, _ := e.login("pw")
	if _, err := e.pool.Exec(context.Background(),
		"UPDATE sessions SET expires_at = now() - interval '1 minute'"); err != nil {
		t.Fatal(err)
	}
	res := e.get(ck)
	defer res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("absolutely-expired session: %d", res.StatusCode)
	}
}

func TestConcurrentSessionRevocation(t *testing.T) {
	e := newTestEnv(t, false)
	e.bootstrap("pw")
	ck1, sr1 := e.login("pw") // browser 1
	ck2, _ := e.login("pw")   // browser 2

	res := e.call("POST", "/api/auth/logout", reqOpts{cookie: ck1, contentType: "application/json", csrf: sr1.CSRFToken})
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("logout browser 1: %d", res.StatusCode)
	}
	if res := e.get(ck1); res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("revoked session still live: %d", res.StatusCode)
	}
	res.Body.Close()
	if res := e.get(ck2); res.StatusCode != http.StatusOK {
		t.Fatalf("concurrent session collateral damage: %d", res.StatusCode)
	}
	res.Body.Close()
}

func TestPasswordRotationHTTP(t *testing.T) {
	e := newTestEnv(t, false)
	e.bootstrap("old-pw")
	ck, sr := e.login("old-pw")

	// wrong current: generic failure, session untouched
	res := e.call("POST", "/api/auth/password", reqOpts{
		cookie: ck, contentType: "application/json", csrf: sr.CSRFToken,
		body: `{"currentPassword":"nope","newPassword":"new-pw"}`,
	})
	if res.StatusCode != http.StatusUnauthorized || !strings.Contains(bodyString(t, res), "INVALID_CREDENTIALS") {
		t.Fatalf("bad current password: %d", res.StatusCode)
	}

	res = e.call("POST", "/api/auth/password", reqOpts{
		cookie: ck, contentType: "application/json", csrf: sr.CSRFToken,
		body: `{"currentPassword":"old-pw","newPassword":"new-pw"}`,
	})
	if res.StatusCode != http.StatusOK {
		t.Fatalf("password change: %d %s", res.StatusCode, bodyString(t, res))
	}
	newCk := sessionCookie(t, res)
	res.Body.Close()
	if newCk.Value == ck.Value {
		t.Fatal("session token must rotate on password change")
	}
	// old cookie dead, new cookie live
	if res := e.get(ck); res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("pre-change cookie still live: %d", res.StatusCode)
	}
	res.Body.Close()
	if res := e.get(newCk); res.StatusCode != http.StatusOK {
		t.Fatalf("rotated cookie rejected: %d", res.StatusCode)
	}
	res.Body.Close()
	// old password no longer valid
	res = e.call("POST", "/api/auth/login", reqOpts{body: e.loginBody("old-pw"), contentType: "application/json"})
	defer res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("old password still works: %d", res.StatusCode)
	}
}

func TestRecoveryInvalidatesSessionsHTTP(t *testing.T) {
	e := newTestEnv(t, false)
	e.bootstrap("old-pw")
	ck1, _ := e.login("old-pw")
	ck2, _ := e.login("old-pw")

	if _, err := e.owners.Recover(context.Background(), "forced-pw"); err != nil {
		t.Fatal(err)
	}
	for _, ck := range []*http.Cookie{ck1, ck2} {
		res := e.get(ck)
		if res.StatusCode != http.StatusUnauthorized {
			t.Fatalf("session survived recovery: %d", res.StatusCode)
		}
		res.Body.Close()
	}
	res := e.call("POST", "/api/auth/login", reqOpts{body: e.loginBody("forced-pw"), contentType: "application/json"})
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("recovered password rejected: %d", res.StatusCode)
	}
}

func TestCrossWorkspacePrincipal(t *testing.T) {
	e := newTestEnv(t, false)
	ownerA, wsA := e.bootstrap("pw-a")

	// second owner+workspace fixture (cross-workspace linkage check)
	hasher := identity.NewPasswordHasher(identity.Argon2Params{Memory: 64, Iterations: 1, Parallelism: 1})
	hB, err := hasher.Hash("pw-b")
	if err != nil {
		t.Fatal(err)
	}
	ownerB, err := e.q.InsertOwner(context.Background(), dbgen.InsertOwnerParams{ID: uid(), PasswordHash: hB})
	if err != nil {
		t.Fatal(err)
	}
	wsB, err := e.q.InsertWorkspace(context.Background(), dbgen.InsertWorkspaceParams{
		ID: uid(), OwnerID: ownerB.ID, DisplayName: "Other garden", Timezone: "UTC", DefaultCurrency: "USD",
	})
	if err != nil {
		t.Fatal(err)
	}

	ckA, srA := e.login("pw-a")
	if srA.WorkspaceID != uuidOf(wsA) || srA.WorkspaceID == uuidOf(wsB.ID) {
		t.Fatalf("principal A carries wrong workspace: %s vs %s/%s", srA.WorkspaceID, uuidOf(wsA), uuidOf(wsB.ID))
	}
	// v1 login targets the single owner; the second owner's session is
	// issued directly to test principal scoping, not multi-account login.
	issB, err := e.sessions.New(context.Background(), ownerB.ID)
	if err != nil {
		t.Fatal(err)
	}
	ckB := &http.Cookie{Name: e.cfg.CookieName, Value: issB.Token}
	resB := e.get(ckB)
	var srB sessionResp
	if err := json.NewDecoder(resB.Body).Decode(&srB); err != nil {
		t.Fatal(err)
	}
	resB.Body.Close()
	if srB.WorkspaceID != uuidOf(wsB.ID) {
		t.Fatalf("principal B carries wrong workspace: %s", srB.WorkspaceID)
	}

	// owner B cannot revoke owner A's session (scoped by owner id)
	var sidA pgtype.UUID
	if err := e.pool.QueryRow(context.Background(),
		"SELECT id FROM sessions WHERE owner_id=$1", ownerA).Scan(&sidA); err != nil {
		t.Fatal(err)
	}
	if err := e.sessions.Revoke(context.Background(), sidA, ownerB.ID); !errors.Is(err, identity.ErrSessionNotFound) {
		t.Fatalf("cross-owner revoke: %v", err)
	}
	res := e.get(ckA)
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("A's session affected by B: %d", res.StatusCode)
	}
}

func TestMediaExportGuards(t *testing.T) {
	e := newTestEnv(t, false)
	e.bootstrap("pw")

	for _, path := range []string{"/api/media/secret/photo.jpg", "/api/exports/abc/download"} {
		res := e.call("GET", path, reqOpts{noOrigin: true})
		if res.StatusCode != http.StatusUnauthorized {
			t.Fatalf("unauthenticated %s: %d", path, res.StatusCode)
		}
		res.Body.Close()
	}
	ck, _ := e.login("pw")
	for _, path := range []string{"/api/media/secret/photo.jpg", "/api/exports/abc/download"} {
		res := e.get2(path, ck)
		body := bodyString(t, res)
		if res.StatusCode != http.StatusNotImplemented || !strings.Contains(body, "NOT_IMPLEMENTED") {
			t.Fatalf("authenticated %s: %d %s", path, res.StatusCode, body)
		}
	}
}

func TestNoSignupOrResetRoutes(t *testing.T) {
	e := newTestEnv(t, false)
	for _, path := range []string{"/api/auth/signup", "/api/auth/register", "/api/auth/reset", "/api/auth/forgot"} {
		res := e.call("POST", path, reqOpts{body: `{}`, contentType: "application/json"})
		if res.StatusCode != http.StatusNotFound {
			t.Fatalf("%s routed: %d", path, res.StatusCode)
		}
		res.Body.Close()
	}
}

func (e *testEnv) get2(path string, ck *http.Cookie) *http.Response {
	return e.call("GET", path, reqOpts{cookie: ck, noOrigin: true})
}

func uuidOf(u pgtype.UUID) string {
	if !u.Valid {
		return ""
	}
	return uuid.UUID(u.Bytes).String()
}
