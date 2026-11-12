package httpx

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	candb "canopy/server/db"
	dbgen "canopy/server/db/generated"
	"canopy/server/internal/identity"
	"canopy/server/internal/platform"
)

// testEnv wires the real auth stack (no stubs) against an isolated schema,
// same discipline as the T02/T06 identity suites.
type testEnv struct {
	t        *testing.T
	srv      *httptest.Server
	pool     *pgxpool.Pool
	q        *dbgen.Queries
	owners   *identity.OwnerService
	sessions *identity.SessionManager
	cfg      identity.SessionConfig
}

func newTestEnv(t *testing.T, secure bool) *testEnv {
	t.Helper()
	ctx := context.Background()
	dsn := os.Getenv("PGTEST_DSN")
	if dsn == "" {
		t.Skip("PGTEST_DSN not set")
	}
	admin, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	schema := fmt.Sprintf("t06http_%d", time.Now().UnixNano())
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	up, err := candb.AllUp()
	if err != nil {
		t.Fatalf("migrations: %v", err)
	}
	if _, err := admin.Exec(ctx, "SET search_path TO "+schema); err != nil {
		t.Fatalf("search_path: %v", err)
	}
	if _, err := admin.Exec(ctx, up); err != nil {
		t.Fatalf("apply migration: %v", err)
	}
	admin.Close(ctx)

	pool, err := pgxpool.New(ctx, dsn+"&search_path="+schema)
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	t.Cleanup(func() {
		pool.Close()
		if a, err := pgx.Connect(ctx, dsn); err == nil {
			_, _ = a.Exec(ctx, "DROP SCHEMA IF EXISTS "+schema+" CASCADE")
			a.Close(ctx)
		}
	})

	q := dbgen.New(pool)
	cfg := identity.DefaultSessionConfig(secure, false)
	sm := identity.NewSessionManager(q, platform.UUIDv4{}, platform.SystemClock{}, cfg)
	hasher := identity.NewPasswordHasher(identity.Argon2Params{Memory: 64, Iterations: 1, Parallelism: 1})
	log := slog.New(slog.DiscardHandler)
	owners := identity.NewOwnerService(q, sm, hasher, platform.UUIDv4{}, log)

	mux := http.NewServeMux()
	NewAuth(owners, sm, identity.NewLoginLimiter(10), cfg, log).Register(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	return &testEnv{t: t, srv: srv, pool: pool, q: q, owners: owners, sessions: sm, cfg: cfg}
}

func (e *testEnv) bootstrap(password string) (pgtype.UUID, pgtype.UUID) {
	e.t.Helper()
	owner, ws, err := e.owners.Bootstrap(context.Background(), identity.BootstrapInput{Password: password})
	if err != nil {
		e.t.Fatalf("bootstrap: %v", err)
	}
	return owner.ID, ws.ID
}

type reqOpts struct {
	body        string
	contentType string
	origin      string // "" -> omit; set to e.srv.URL for the browser-same-origin case
	noOrigin    bool
	cookie      *http.Cookie
	csrf        string
}

func (e *testEnv) call(method, path string, o reqOpts) *http.Response {
	e.t.Helper()
	var rdr io.Reader
	if o.body != "" {
		rdr = strings.NewReader(o.body)
	}
	req, err := http.NewRequest(method, e.srv.URL+path, rdr)
	if err != nil {
		e.t.Fatal(err)
	}
	if o.contentType != "" {
		req.Header.Set("Content-Type", o.contentType)
	}
	if !o.noOrigin {
		origin := o.origin
		if origin == "" {
			origin = e.srv.URL
		}
		req.Header.Set("Origin", origin)
	}
	if o.csrf != "" {
		req.Header.Set("X-CSRF-Token", o.csrf)
	}
	if o.cookie != nil {
		req.AddCookie(o.cookie)
	}
	res, err := e.srv.Client().Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	return res
}

func bodyString(t *testing.T, res *http.Response) string {
	t.Helper()
	defer res.Body.Close()
	b, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func sessionCookie(t *testing.T, res *http.Response) *http.Cookie {
	t.Helper()
	for _, c := range res.Cookies() {
		if c.Name == "canopy_session" {
			return c
		}
	}
	t.Fatal("no canopy_session cookie in response")
	return nil
}

type sessionResp struct {
	Authenticated bool   `json:"authenticated"`
	CSRFToken     string `json:"csrfToken"`
	ExpiresAt     string `json:"expiresAt"`
	WorkspaceID   string `json:"workspaceId"`
}

func (e *testEnv) loginBody(pw string) string {
	return `{"password":"` + pw + `"}`
}

// login performs a full browser-like login and returns the cookie plus the
// session bootstrap payload.
func (e *testEnv) login(pw string) (*http.Cookie, sessionResp) {
	e.t.Helper()
	res := e.call("POST", "/api/auth/login", reqOpts{
		body: e.loginBody(pw), contentType: "application/json",
	})
	if res.StatusCode != http.StatusOK {
		e.t.Fatalf("login status %d: %s", res.StatusCode, bodyString(e.t, res))
	}
	var sr sessionResp
	if err := json.NewDecoder(res.Body).Decode(&sr); err != nil {
		e.t.Fatal(err)
	}
	ck := sessionCookie(e.t, res)
	return ck, sr
}

func (e *testEnv) get(ck *http.Cookie) *http.Response {
	return e.call("GET", "/api/auth/session", reqOpts{cookie: ck, noOrigin: true})
}

func uid() pgtype.UUID {
	return pgtype.UUID{Bytes: uuid.New(), Valid: true}
}
