package graphql

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

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	candb "canopy/server/db"
	dbgen "canopy/server/db/generated"
	"canopy/server/internal/identity"
	"canopy/server/internal/platform"
	httpx "canopy/server/internal/transport/http"
)

// Full-stack harness: real auth endpoints + real GraphQL endpoint against
// an isolated disposable schema (same discipline as T06/T07 suites).
type testEnv struct {
	t       *testing.T
	srv     *httptest.Server
	pool    *pgxpool.Pool
	q       *dbgen.Queries
	cookie  *http.Cookie // set after login
	csrf    string
	ownerID pgtype.UUID
	wsID    pgtype.UUID
}

func newEnv(t *testing.T, lim Limits) *testEnv {
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
	schema := fmt.Sprintf("t08gql_%d", time.Now().UnixNano())
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
	log := slog.New(slog.DiscardHandler)
	cfg := identity.DefaultSessionConfig(false, false)
	sm := identity.NewSessionManager(q, platform.UUIDv4{}, platform.SystemClock{}, cfg)
	hasher := identity.NewPasswordHasher(identity.Argon2Params{Memory: 64, Iterations: 1, Parallelism: 1})
	owners := identity.NewOwnerService(q, sm, hasher, platform.UUIDv4{}, log)

	owner, ws, err := owners.Bootstrap(ctx, identity.BootstrapInput{Password: "t08-passphrase"})
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}

	mux := http.NewServeMux()
	auth := httpx.NewAuth(owners, sm, identity.NewLoginLimiter(10), cfg, log)
	auth.Register(mux)
	root := NewResolverFromPool(pool, log)
	mux.Handle("POST /api/graphql", auth.RequirePrincipal(NewHandler(root, lim, log).ServeHTTP))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	e := &testEnv{t: t, srv: srv, pool: pool, q: q, ownerID: owner.ID, wsID: ws.ID}
	e.login()
	return e
}

func (e *testEnv) login() {
	e.t.Helper()
	res := e.raw("POST", "/api/auth/login", `{"password":"t08-passphrase"}`, "application/json", nil, "")
	body := readAll(e.t, res)
	if res.StatusCode != http.StatusOK {
		e.t.Fatalf("login %d: %s", res.StatusCode, body)
	}
	var sr struct {
		CSRFToken string `json:"csrfToken"`
	}
	if err := json.Unmarshal([]byte(body), &sr); err != nil {
		e.t.Fatal(err)
	}
	for _, c := range res.Cookies() {
		if c.Name == "canopy_session" {
			e.cookie = c
		}
	}
	if e.cookie == nil || sr.CSRFToken == "" {
		e.t.Fatal("login produced no cookie/csrf")
	}
	e.csrf = sr.CSRFToken
}

func (e *testEnv) raw(method, path, body, ct string, ck *http.Cookie, csrf string) *http.Response {
	e.t.Helper()
	var rdr io.Reader
	if body != "" {
		rdr = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, e.srv.URL+path, rdr)
	if err != nil {
		e.t.Fatal(err)
	}
	if ct != "" {
		req.Header.Set("Content-Type", ct)
	}
	req.Header.Set("Origin", e.srv.URL)
	if csrf != "" {
		req.Header.Set("X-CSRF-Token", csrf)
	}
	if ck != nil {
		req.AddCookie(ck)
	}
	res, err := e.srv.Client().Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	return res
}

type gqlResp struct {
	Status int
	Data   map[string]any `json:"data"`
	Errors []gqlErrItem   `json:"errors"`
	Raw    string
}

type gqlErrItem struct {
	Message    string         `json:"message"`
	Extensions map[string]any `json:"extensions"`
}

func (e *testEnv) gql(query string, vars map[string]any) gqlResp {
	e.t.Helper()
	return e.gqlAs(e.cookie, e.csrf, query, vars)
}

func (e *testEnv) gqlAs(ck *http.Cookie, csrf, query string, vars map[string]any) gqlResp {
	e.t.Helper()
	payload := map[string]any{"query": query}
	if vars != nil {
		payload["variables"] = vars
	}
	b, err := json.Marshal(payload)
	if err != nil {
		e.t.Fatal(err)
	}
	res := e.raw("POST", "/api/graphql", string(b), "application/json", ck, csrf)
	raw := readAll(e.t, res)
	var r gqlResp
	r.Status = res.StatusCode
	r.Raw = raw
	if err := json.Unmarshal([]byte(raw), &r); err != nil {
		e.t.Fatalf("decode %d body: %v (%s)", res.StatusCode, err, raw)
	}
	return r
}

func readAll(t *testing.T, res *http.Response) string {
	t.Helper()
	defer res.Body.Close()
	b, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

const sessionQuery = `query Session {
  session {
    authenticated
    workspace {
      id
      displayName
      timezone
      defaultCurrency
      settingsRevision
      location { latitude longitude label }
    }
  }
}`

const updateWorkspaceMutation = `mutation UpdateWorkspace($input: UpdateWorkspaceInput!, $clientMutationId: String) {
  updateWorkspace(input: $input, clientMutationId: $clientMutationId) {
    workspace { id displayName timezone defaultCurrency settingsRevision location { latitude longitude label } }
    problems { code field message currentRevision }
    clientMutationId
  }
}`

// ---- tests ----------------------------------------------------------------

func TestSessionRoundTrip(t *testing.T) {
	e := newEnv(t, DefaultLimits())
	r := e.gql(sessionQuery, nil)
	if r.Status != 200 || r.Errors != nil {
		t.Fatalf("status=%d errors=%v raw=%s", r.Status, r.Errors, r.Raw)
	}
	sess, _ := r.Data["session"].(map[string]any)
	if sess == nil || sess["authenticated"] != true {
		t.Fatalf("want authenticated session, got %v", r.Data)
	}
	ws, _ := sess["workspace"].(map[string]any)
	if ws["displayName"] != "My garden" || ws["timezone"] != "UTC" || ws["defaultCurrency"] != "USD" {
		t.Fatalf("bootstrap defaults wrong: %v", ws)
	}
	if ws["settingsRevision"] != float64(1) {
		t.Fatalf("want settingsRevision 1, got %v", ws["settingsRevision"])
	}
	if ws["location"] != nil {
		t.Fatalf("want null location, got %v", ws["location"])
	}
	if ws["id"] == nil || ws["id"] == "" {
		t.Fatal("workspace id missing")
	}
}

func TestUpdateWorkspaceHappyPath(t *testing.T) {
	e := newEnv(t, DefaultLimits())
	r := e.gql(updateWorkspaceMutation, map[string]any{
		"input": map[string]any{
			"expectedRevision": 1,
			"displayName":      "Back porch garden",
			"timezone":         "America/New_York",
			"defaultCurrency":  "EUR",
			"location": map[string]any{
				"latitude":  "40.7128",
				"longitude": "-74.0060",
				"label":     "Brooklyn windowsill",
			},
		},
		"clientMutationId": "cmid-1",
	})
	if r.Status != 200 || r.Errors != nil {
		t.Fatalf("status=%d errors=%v raw=%s", r.Status, r.Errors, r.Raw)
	}
	mu, _ := r.Data["updateWorkspace"].(map[string]any)
	if mu["clientMutationId"] != "cmid-1" {
		t.Fatalf("clientMutationId not echoed: %v", mu)
	}
	if probs, _ := mu["problems"].([]any); len(probs) != 0 {
		t.Fatalf("want no problems, got %v", probs)
	}
	ws, _ := mu["workspace"].(map[string]any)
	if ws["displayName"] != "Back porch garden" || ws["timezone"] != "America/New_York" || ws["defaultCurrency"] != "EUR" {
		t.Fatalf("update not applied: %v", ws)
	}
	if ws["settingsRevision"] != float64(2) {
		t.Fatalf("want settingsRevision 2, got %v", ws["settingsRevision"])
	}
	loc, _ := ws["location"].(map[string]any)
	// canonical Decimal strings out; -74.0060 stored, emitted without trailing zero
	if loc["latitude"] != "40.7128" || loc["longitude"] != "-74.006" {
		t.Fatalf("location round-trip wrong: %v", loc)
	}
	if loc["label"] != "Brooklyn windowsill" {
		t.Fatalf("label wrong: %v", loc)
	}

	// The session query now reflects the same state (single source of truth).
	r2 := e.gql(sessionQuery, nil)
	sess, _ := r2.Data["session"].(map[string]any)
	ws2, _ := sess["workspace"].(map[string]any)
	if ws2["displayName"] != "Back porch garden" || ws2["settingsRevision"] != float64(2) {
		t.Fatalf("session query stale: %v", ws2)
	}
}

func TestUpdateWorkspaceConflict(t *testing.T) {
	e := newEnv(t, DefaultLimits())
	r := e.gql(updateWorkspaceMutation, map[string]any{
		"input": map[string]any{"expectedRevision": 99, "displayName": "Should not land"},
	})
	if r.Status != 200 || r.Errors != nil {
		t.Fatalf("status=%d errors=%v raw=%s", r.Status, r.Errors, r.Raw)
	}
	mu, _ := r.Data["updateWorkspace"].(map[string]any)
	if mu["workspace"] != nil {
		t.Fatalf("want null workspace on conflict, got %v", mu["workspace"])
	}
	probs, _ := mu["problems"].([]any)
	if len(probs) != 1 {
		t.Fatalf("want 1 problem, got %v", probs)
	}
	p, _ := probs[0].(map[string]any)
	if p["code"] != "CONFLICT" || p["currentRevision"] != float64(1) {
		t.Fatalf("want CONFLICT + currentRevision 1, got %v", p)
	}
	// No partial change: displayName untouched, revision untouched.
	ws, err := e.q.GetWorkspace(context.Background(), e.ownerID)
	if err != nil {
		t.Fatal(err)
	}
	if ws.DisplayName != "My garden" || ws.SettingsRevision != 1 {
		t.Fatalf("partial write detected: %+v", ws)
	}
}

func TestUpdateWorkspaceValidation(t *testing.T) {
	e := newEnv(t, DefaultLimits())
	r := e.gql(updateWorkspaceMutation, map[string]any{
		"input": map[string]any{
			"expectedRevision": 1,
			"timezone":         "Mars/Phobos",
			"defaultCurrency":  "usd",
			"location":         map[string]any{"latitude": "91", "longitude": "0"},
		},
	})
	if r.Status != 200 || r.Errors != nil {
		t.Fatalf("status=%d errors=%v raw=%s", r.Status, r.Errors, r.Raw)
	}
	mu, _ := r.Data["updateWorkspace"].(map[string]any)
	probs, _ := mu["problems"].([]any)
	if len(probs) != 3 {
		t.Fatalf("want 3 VALIDATION problems, got %v", probs)
	}
	wantFields := map[string]bool{"input.timezone": true, "input.defaultCurrency": true, "input.location.latitude": true}
	for _, any_ := range probs {
		p, _ := any_.(map[string]any)
		if p["code"] != "VALIDATION" {
			t.Fatalf("want VALIDATION, got %v", p)
		}
		f, _ := p["field"].(string)
		if !wantFields[f] {
			t.Fatalf("unexpected field %q in %v", f, p)
		}
	}
	ws, err := e.q.GetWorkspace(context.Background(), e.ownerID)
	if err != nil {
		t.Fatal(err)
	}
	if ws.SettingsRevision != 1 || ws.Timezone != "UTC" {
		t.Fatalf("rejected write changed state: %+v", ws)
	}
}

// Auth failures are HTTP-level problem envelopes; payload failures are typed
// problems inside the GraphQL result. The two channels never mix.
func TestAuthAndPayloadErrorsDifferentiated(t *testing.T) {
	e := newEnv(t, DefaultLimits())

	// No cookie: 401 problem envelope, not a GraphQL result.
	r := e.gqlAs(nil, "", sessionQuery, nil)
	if r.Status != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d: %s", r.Status, r.Raw)
	}
	var env struct {
		Problems []struct {
			Code string `json:"code"`
		} `json:"problems"`
	}
	if err := json.Unmarshal([]byte(r.Raw), &env); err != nil || len(env.Problems) == 0 || env.Problems[0].Code != "UNAUTHENTICATED" {
		t.Fatalf("want UNAUTHENTICATED problem envelope, got %s", r.Raw)
	}

	// Cookie but no CSRF header: 403 problem envelope.
	r2 := e.gqlAs(e.cookie, "", sessionQuery, nil)
	if r2.Status != http.StatusForbidden {
		t.Fatalf("want 403, got %d: %s", r2.Status, r2.Raw)
	}

	// Contrast: a payload-level failure is 200 + typed problems (see conflict test).
}

func TestUnimplementedFieldsHonest(t *testing.T) {
	// Collection ships with T09, journal with T16; exportJob is the standing
	// example of an unshipped field refusing honestly instead of faking data.
	e := newEnv(t, DefaultLimits())
	r := e.gql(`{ exportJob(id: "00000000-0000-0000-0000-000000000000") { id } }`, nil)
	if r.Status != 200 || len(r.Errors) != 1 {
		t.Fatalf("want one GraphQL error, got %d: %s", len(r.Errors), r.Raw)
	}
	if r.Errors[0].Extensions["code"] != "CAPABILITY_UNAVAILABLE" {
		t.Fatalf("want CAPABILITY_UNAVAILABLE, got %v", r.Errors[0].Extensions)
	}
	if r.Data["exportJob"] != nil {
		t.Fatalf("want null data for unimplemented field, got %v", r.Data["exportJob"])
	}
}

func TestLimitsOverHTTP(t *testing.T) {
	e := newEnv(t, DefaultLimits())

	// Page size cap.
	r := e.gql(`{ plants(first: 1000) { items { id } } }`, nil)
	if len(r.Errors) == 0 || r.Errors[0].Extensions["code"] != "CAPABILITY_UNAVAILABLE" ||
		!strings.Contains(r.Errors[0].Message, "out of range") {
		t.Fatalf("first:1000 not rejected by page cap: %s", r.Raw)
	}

	// Depth cap.
	deep := "{ locations " + strings.Repeat("{ parent ", DefaultLimits().MaxDepth) + "{ name }" + strings.Repeat(" }", DefaultLimits().MaxDepth) + " }"
	r = e.gql(deep, nil)
	if len(r.Errors) == 0 || r.Errors[0].Extensions["code"] != "CAPABILITY_UNAVAILABLE" ||
		!strings.Contains(r.Errors[0].Message, "levels deep") {
		t.Fatalf("deep query not rejected by depth cap: %s", r.Raw)
	}

	// Complexity charged on requested page size: same shape, different first.
	tight := DefaultLimits()
	tight.MaxComplexity = 150
	e2 := newEnv(t, tight)
	q := `{ plants(first: 100) { items { id name scientificName cultivar rootstock type count category } pageInfo { hasNextPage endCursor } } }`
	r = e2.gql(q, nil)
	if len(r.Errors) == 0 || !strings.Contains(r.Errors[0].Message, "complexity") {
		t.Fatalf("first:100 should exceed budget 150: %s", r.Raw)
	}
	r = e2.gql(strings.Replace(q, "first: 100", "first: 1", 1), nil)
	// Same shape at first:1 fits the budget: plants is implemented (T09), so
	// the query executes cleanly with no errors at all.
	if len(r.Errors) != 0 {
		t.Fatalf("first:1 must fit the same budget: %s", r.Raw)
	}

	// Body cap: 70 KiB of comments.
	huge := `{"query":"` + strings.Repeat("# pad ", 12000) + `{ session { authenticated } }"}`
	res := e.raw("POST", "/api/graphql", huge, "application/json", e.cookie, e.csrf)
	if res.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("want 413, got %d: %s", res.StatusCode, readAll(t, res))
	}
}

func TestScalarBoundariesOverHTTP(t *testing.T) {
	e := newEnv(t, DefaultLimits())
	// Exponent notation is not a canonical Decimal: rejected, never coerced.
	r := e.gql(updateWorkspaceMutation, map[string]any{
		"input": map[string]any{
			"expectedRevision": 1,
			"location":         map[string]any{"latitude": "1e5", "longitude": "0"},
		},
	})
	if len(r.Errors) == 0 {
		t.Fatalf("exponent Decimal accepted: %s", r.Raw)
	}
	if r.Data != nil && r.Data["updateWorkspace"] != nil {
		t.Fatalf("mutation executed despite scalar error: %v", r.Data["updateWorkspace"])
	}
	ws, err := e.q.GetWorkspace(context.Background(), e.ownerID)
	if err != nil {
		t.Fatal(err)
	}
	if ws.SettingsRevision != 1 || ws.Latitude != nil {
		t.Fatalf("scalar violation changed state: %+v", ws)
	}
}

func TestGetNotAllowed(t *testing.T) {
	e := newEnv(t, DefaultLimits())
	res := e.raw("GET", "/api/graphql", "", "", e.cookie, "")
	if res.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("GET /api/graphql: want 405, got %d", res.StatusCode)
	}
}

func TestWrongContentTypeRejected(t *testing.T) {
	e := newEnv(t, DefaultLimits())
	res := e.raw("POST", "/api/graphql", `query { session { authenticated } }`, "application/graphql", e.cookie, e.csrf)
	if res.StatusCode != http.StatusUnsupportedMediaType {
		t.Fatalf("want 415, got %d: %s", res.StatusCode, readAll(t, res))
	}
}
