package graphql

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	candb "canopy/server/db"
	dbgen "canopy/server/db/generated"
	"canopy/server/internal/identity"
	"canopy/server/internal/media"
	"canopy/server/internal/platform"
	platformmedia "canopy/server/internal/platform/media"
	httpx "canopy/server/internal/transport/http"
)

// T13 acceptance over the real stack: reserveUpload / PUT bytes /
// finalizeUpload against a real filesystem ObjectStore, a real River
// queue and an isolated disposable schema. Covers the negative edges
// from the packet: duplicate/truncated/cross-owner uploads, concurrent
// quota reservations, opaque keys, the file-before-DB boundary and the
// reservation sweeper.

type mediaEnv struct {
	*testEnv
	svc   *media.Service
	store platformmedia.Store
}

func newMediaEnv(t *testing.T, quota int64) *mediaEnv {
	return newMediaEnvTraced(t, quota, nil)
}

// newMediaEnvTraced installs a pgx QueryTracer on the app pool so tests
// can count every query a request issues (T18 query-budget acceptance).
func newMediaEnvTraced(t *testing.T, quota int64, tracer pgx.QueryTracer) *mediaEnv {
	return newMediaEnvStorage(t, quota, tracer, "")
}
func newMediaEnvStorage(t *testing.T, quota int64, tracer pgx.QueryTracer, storageRoot string) *mediaEnv {
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
	schema := fmt.Sprintf("t13_%d", time.Now().UnixNano())
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

	poolCfg, err := pgxpool.ParseConfig(dsn + "&search_path=" + schema)
	if err != nil {
		t.Fatalf("pool cfg: %v", err)
	}
	if tracer != nil {
		poolCfg.ConnConfig.Tracer = tracer
	}
	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
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

	if err := platform.MigrateRiver(ctx, pool); err != nil {
		t.Fatalf("river migrate: %v", err)
	}

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

	if storageRoot == "" {
		storageRoot = t.TempDir()
	}
	store, err := platformmedia.NewFSStore(storageRoot)
	if err != nil {
		t.Fatalf("fs store: %v", err)
	}
	queue, err := platform.NewInsertOnlyRiverClient(pool)
	if err != nil {
		t.Fatalf("river client: %v", err)
	}
	svc := media.New(pool, log, store, queue, quota)

	mux := http.NewServeMux()
	auth := httpx.NewAuth(owners, sm, identity.NewLoginLimiter(10), cfg, log)
	auth.Register(mux)
	root := NewResolverFromPool(pool, log)
	root.Media = svc
	mux.Handle("POST /api/graphql", auth.RequirePrincipal(NewHandler(root, DefaultLimits(), log).ServeHTTP))
	mux.Handle("PUT /api/uploads/{assetID}", auth.RequirePrincipal(
		httpx.NewUploads(pool, store, log).Handler()))
	// T15 delivery route mirrors cmd/api wiring
	mux.Handle("GET /api/media/{assetID}/{variant}", auth.RequirePrincipal(
		httpx.NewMediaDelivery(pool, store, log).Handler()))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	e := &testEnv{t: t, srv: srv, pool: pool, q: q, ownerID: owner.ID, wsID: ws.ID}
	e.login()
	return &mediaEnv{testEnv: e, svc: svc, store: store}
}

// ---- fixtures & helpers ----------------------------------------------------

func jpegBytes(n int) []byte {
	b := []byte{0xFF, 0xD8, 0xFF, 0xE0}
	for len(b) < n {
		b = append(b, byte(len(b)%251))
	}
	return b
}

func shaHex(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

const assetFieldsGQL = `id purpose state byteCount mimeType sha256 displayName errorCode
  expiresAt variants { name widthPx heightPx } createdAt updatedAt`

const reserveMutGQL = `mutation($input: ReserveUploadInput!, $cmid: String) {
  reserveUpload(input: $input, clientMutationId: $cmid) {
    asset { ` + assetFieldsGQL + ` }
    problems { code field message }
    clientMutationId
  }
}`

const finalizeMutGQL = `mutation($input: FinalizeUploadInput!, $cmid: String) {
  finalizeUpload(input: $input, clientMutationId: $cmid) {
    asset { id state errorCode }
    problems { code field message }
    clientMutationId
  }
}`

const pendingQ = `query($first: Int, $after: String) {
  pendingUploads(first: $first, after: $after) {
    items { id state }
    pageInfo { endCursor hasNextPage }
  }
}`

func (m *mediaEnv) reserve(t *testing.T, purpose, filename string, claimed int, cmid *string) map[string]any {
	t.Helper()
	res := m.gql(reserveMutGQL, map[string]any{
		"input": map[string]any{"purpose": purpose, "filename": filename, "claimedBytes": claimed},
		"cmid":  cmid,
	})
	if len(res.Errors) > 0 {
		t.Fatalf("reserve: %s", res.Raw)
	}
	payload := res.Data["reserveUpload"].(map[string]any)
	asset, _ := payload["asset"].(map[string]any)
	return asset
}

func (m *mediaEnv) put(t *testing.T, assetID string, body []byte, ck *http.Cookie, csrf string) (int, string) {
	t.Helper()
	req, err := http.NewRequest("PUT", m.srv.URL+"/api/uploads/"+assetID, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Origin", m.srv.URL)
	req.Header.Set("Content-Type", "application/octet-stream")
	if csrf != "" {
		req.Header.Set("X-CSRF-Token", csrf)
	}
	if ck != nil {
		req.AddCookie(ck)
	}
	res, err := m.srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return res.StatusCode, readAll(t, res)
}

func (m *mediaEnv) riverJobCount(t *testing.T) int {
	t.Helper()
	var n int
	err := m.pool.QueryRow(context.Background(),
		"SELECT count(*) FROM river_job WHERE kind = 'media:process_asset'").Scan(&n)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func problemCode(t *testing.T, body string) string {
	t.Helper()
	var env struct {
		Problems []struct {
			Code string `json:"code"`
		} `json:"problems"`
	}
	if err := json.Unmarshal([]byte(body), &env); err != nil {
		t.Fatalf("decode problem body: %v (%s)", err, body)
	}
	if len(env.Problems) == 0 {
		t.Fatalf("no problems in %s", body)
	}
	return env.Problems[0].Code
}

// ---- lifecycle --------------------------------------------------------------

func TestUploadLifecycleOverHTTP(t *testing.T) {
	e := newMediaEnv(t, 10<<20)
	body := jpegBytes(2048)

	asset := e.reserve(t, "PHOTO", "../../etc/field notes.jpg", len(body), nil)
	if asset == nil {
		t.Fatal("reserve returned no asset")
	}
	id := asset["id"].(string)
	if asset["state"] != "RESERVED" || asset["purpose"] != "PHOTO" {
		t.Fatalf("unexpected initial asset: %v", asset)
	}
	if asset["displayName"] != "field notes.jpg" {
		t.Errorf("displayName = %v (path must be stripped)", asset["displayName"])
	}
	if asset["expiresAt"] == nil {
		t.Error("reservation must carry an expiry")
	}
	if sha, _ := asset["sha256"].(string); len(sha) != 64 {
		t.Errorf("placeholder sha256 = %q", sha)
	}

	// bytes over HTTP
	code, respBody := e.put(t, id, body, e.cookie, e.csrf)
	if code != http.StatusOK {
		t.Fatalf("PUT = %d: %s", code, respBody)
	}
	var up struct {
		AssetID   string `json:"assetId"`
		State     string `json:"state"`
		ByteCount int64  `json:"byteCount"`
		SHA256    string `json:"sha256"`
		MIMEType  string `json:"mimeType"`
	}
	if err := json.Unmarshal([]byte(respBody), &up); err != nil {
		t.Fatal(err)
	}
	if up.State != "UPLOADED" || up.SHA256 != shaHex(body) || up.MIMEType != "image/jpeg" ||
		up.ByteCount != int64(len(body)) {
		t.Fatalf("upload receipt wrong: %s", respBody)
	}

	// finalize with the wrong checksum is rejected
	res := e.gql(finalizeMutGQL, map[string]any{
		"input": map[string]any{"assetId": id, "sha256": strings.Repeat("0", 64)},
	})
	ps := payloadProblems(t, res, "finalizeUpload")
	if len(ps) == 0 || ps[0]["code"] != "VALIDATION" {
		t.Fatalf("wrong-sha finalize: %s", res.Raw)
	}

	// correct finalize marks PROCESSING and enqueues exactly one job
	res = e.gql(finalizeMutGQL, map[string]any{
		"input": map[string]any{"assetId": id, "sha256": shaHex(body)},
	})
	asset2 := res.Data["finalizeUpload"].(map[string]any)["asset"].(map[string]any)
	if asset2["state"] != "PROCESSING" {
		t.Fatalf("finalize: %s", res.Raw)
	}
	if n := e.riverJobCount(t); n != 1 {
		t.Fatalf("river jobs = %d, want 1", n)
	}

	// repeat finalize is safe: same state, still one job
	res = e.gql(finalizeMutGQL, map[string]any{
		"input": map[string]any{"assetId": id, "sha256": shaHex(body)},
	})
	asset3 := res.Data["finalizeUpload"].(map[string]any)["asset"].(map[string]any)
	if asset3["state"] != "PROCESSING" || len(payloadProblems(t, res, "finalizeUpload")) > 0 {
		t.Fatalf("repeat finalize: %s", res.Raw)
	}
	if n := e.riverJobCount(t); n != 1 {
		t.Fatalf("repeat finalize enqueued a second job (%d)", n)
	}

	// pendingUploads shows the in-flight asset
	res = e.gql(pendingQ, map[string]any{"first": 10})
	items := res.Data["pendingUploads"].(map[string]any)["items"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["id"] != id {
		t.Fatalf("pendingUploads: %s", res.Raw)
	}

	// a PROCESSING asset no longer accepts bytes
	if code, _ := e.put(t, id, body, e.cookie, e.csrf); code != http.StatusConflict {
		t.Fatalf("PUT on PROCESSING = %d, want 409", code)
	}
}

func TestUploadSizeMismatchKeepsReservationRetryable(t *testing.T) {
	e := newMediaEnv(t, 10<<20)
	asset := e.reserve(t, "PHOTO", "a.jpg", 100, nil)
	id := asset["id"].(string)

	if code, body := e.put(t, id, jpegBytes(90), e.cookie, e.csrf); code != http.StatusBadRequest ||
		problemCode(t, body) != "UPLOAD_TRUNCATED" {
		t.Fatalf("truncated PUT = %d %s", code, body)
	}
	if code, body := e.put(t, id, jpegBytes(101), e.cookie, e.csrf); code != http.StatusRequestEntityTooLarge ||
		problemCode(t, body) != "UPLOAD_TOO_LARGE" {
		t.Fatalf("oversize PUT = %d %s", code, body)
	}
	// still RESERVED: finalize honestly refuses
	res := e.gql(finalizeMutGQL, map[string]any{
		"input": map[string]any{"assetId": id, "sha256": shaHex(jpegBytes(100))},
	})
	ps := payloadProblems(t, res, "finalizeUpload")
	if len(ps) == 0 || ps[0]["code"] != "VALIDATION" ||
		!strings.Contains(ps[0]["message"].(string), "not been uploaded") {
		t.Fatalf("finalize on RESERVED: %s", res.Raw)
	}
	// exact-size retry succeeds
	if code, body := e.put(t, id, jpegBytes(100), e.cookie, e.csrf); code != http.StatusOK {
		t.Fatalf("retry PUT = %d %s", code, body)
	}
}

func TestDuplicateAndConflictingUploadNeverOverwrite(t *testing.T) {
	e := newMediaEnv(t, 10<<20)
	body := jpegBytes(500)
	asset := e.reserve(t, "PHOTO", "a.jpg", len(body), nil)
	id := asset["id"].(string)

	if code, _ := e.put(t, id, body, e.cookie, e.csrf); code != http.StatusOK {
		t.Fatalf("first PUT failed")
	}
	// identical repeat replays the success
	code, respBody := e.put(t, id, body, e.cookie, e.csrf)
	if code != http.StatusOK || !strings.Contains(respBody, shaHex(body)) {
		t.Fatalf("duplicate PUT = %d %s", code, respBody)
	}
	// different content (same length) conflicts
	other := append([]byte(nil), body...)
	other[len(other)-1] ^= 0xFF
	code, body2 := e.put(t, id, other, e.cookie, e.csrf)
	if code != http.StatusConflict || problemCode(t, body2) != "CONFLICT" {
		t.Fatalf("conflicting PUT = %d %s", code, body2)
	}
	// stored object is still the original bytes
	row, err := e.q.GetAsset(context.Background(), dbgen.GetAssetParams{
		ID: toPGUUID(t, id), WorkspaceID: e.wsID})
	if err != nil {
		t.Fatal(err)
	}
	rc, err := e.store.Open(row.StorageKey)
	if err != nil {
		t.Fatal(err)
	}
	stored, _ := io.ReadAll(rc)
	rc.Close()
	if shaHex(stored) != shaHex(body) {
		t.Error("stored object was overwritten by a conflicting upload")
	}
}

func TestCrossOwnerUploadInvisible(t *testing.T) {
	e := newMediaEnv(t, 10<<20)
	ctx := context.Background()
	other, err := e.q.InsertWorkspace(ctx, dbgen.InsertWorkspaceParams{
		ID: platform.UUIDv4{}.New(), OwnerID: e.ownerID,
		DisplayName: "other", Timezone: "UTC", DefaultCurrency: "USD",
	})
	if err != nil {
		t.Fatal(err)
	}
	theirs, err := e.q.InsertAsset(ctx, dbgen.InsertAssetParams{
		ID: platform.UUIDv4{}.New(), WorkspaceID: other.ID,
		Purpose: dbgen.AssetPurposePHOTO, ByteCount: 10,
		Sha256: strings.Repeat("a", 64), StorageKey: strings.Repeat("b", 36),
	})
	if err != nil {
		t.Fatal(err)
	}
	id := uuidStrPG(theirs.ID)
	if code, _ := e.put(t, id, jpegBytes(10), e.cookie, e.csrf); code != http.StatusNotFound {
		t.Fatalf("cross-owner PUT = %d, want 404 (no enumeration)", code)
	}
	res := e.gql(finalizeMutGQL, map[string]any{
		"input": map[string]any{"assetId": id, "sha256": strings.Repeat("a", 64)},
	})
	ps := payloadProblems(t, res, "finalizeUpload")
	if len(ps) == 0 || ps[0]["code"] != "NOT_FOUND" {
		t.Fatalf("cross-owner finalize: %s", res.Raw)
	}
}

func TestConcurrentQuotaReservationsCannotExceedQuota(t *testing.T) {
	const quota = 250
	e := newMediaEnv(t, quota)
	actor := media.Actor{WorkspaceID: e.wsID, OwnerID: e.ownerID}

	var wg sync.WaitGroup
	var mu sync.Mutex
	ok, rejected := 0, 0
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, problems, err := e.svc.Reserve(context.Background(), actor,
				media.ReserveInput{Purpose: "PHOTO", Filename: fmt.Sprintf("p%d.jpg", i), ClaimedBytes: 100}, nil)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				t.Errorf("reserve: %v", err)
				return
			}
			if len(problems) > 0 {
				rejected++
				if !strings.Contains(problems[0].Message, "storage quota exceeded") {
					t.Errorf("unexpected rejection: %s", problems[0].Message)
				}
			} else {
				ok++
			}
		}(i)
	}
	wg.Wait()
	if ok != 2 || rejected != 3 {
		t.Fatalf("quota 250 with 5x100: accepted %d, want exactly 2", ok)
	}
}

func TestUnsupportedMediaTypeMarksFailed(t *testing.T) {
	e := newMediaEnv(t, 10<<20)
	body := []byte("<html><body>not an image</body></html>")
	asset := e.reserve(t, "PHOTO", "evil.jpg", len(body), nil) // extension lies; sniff wins
	id := asset["id"].(string)

	code, respBody := e.put(t, id, body, e.cookie, e.csrf)
	if code != http.StatusUnsupportedMediaType || problemCode(t, respBody) != "UNSUPPORTED_MEDIA_TYPE" {
		t.Fatalf("PUT html = %d %s", code, respBody)
	}
	row, err := e.q.GetAsset(context.Background(), dbgen.GetAssetParams{ID: toPGUUID(t, id), WorkspaceID: e.wsID})
	if err != nil {
		t.Fatal(err)
	}
	if row.State != dbgen.AssetStateFAILED || row.ErrorCode.String != "UNSUPPORTED_MEDIA_TYPE" {
		t.Fatalf("asset state = %s/%s", row.State, row.ErrorCode.String)
	}
	res := e.gql(finalizeMutGQL, map[string]any{
		"input": map[string]any{"assetId": id, "sha256": shaHex(body)},
	})
	ps := payloadProblems(t, res, "finalizeUpload")
	if len(ps) == 0 || ps[0]["code"] != "VALIDATION" {
		t.Fatalf("finalize on FAILED: %s", res.Raw)
	}
}

func TestFinalizeMissingObjectIsRecoverableFailure(t *testing.T) {
	e := newMediaEnv(t, 10<<20)
	body := jpegBytes(300)
	asset := e.reserve(t, "PHOTO", "a.jpg", len(body), nil)
	id := asset["id"].(string)
	if code, _ := e.put(t, id, body, e.cookie, e.csrf); code != http.StatusOK {
		t.Fatal("PUT failed")
	}
	row, err := e.q.GetAsset(context.Background(), dbgen.GetAssetParams{ID: toPGUUID(t, id), WorkspaceID: e.wsID})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.store.Delete(row.StorageKey); err != nil { // simulate lost object
		t.Fatal(err)
	}
	res := e.gql(finalizeMutGQL, map[string]any{
		"input": map[string]any{"assetId": id, "sha256": shaHex(body)},
	})
	ps := payloadProblems(t, res, "finalizeUpload")
	if len(ps) == 0 || ps[0]["code"] != "CONFLICT" {
		t.Fatalf("finalize with missing object: %s", res.Raw)
	}
	row, _ = e.q.GetAsset(context.Background(), dbgen.GetAssetParams{ID: toPGUUID(t, id), WorkspaceID: e.wsID})
	if row.State != dbgen.AssetStateFAILED || row.ErrorCode.String != "OBJECT_MISSING" {
		t.Fatalf("state = %s/%s, want FAILED/OBJECT_MISSING", row.State, row.ErrorCode.String)
	}
}

func TestFileBeforeDBBoundaryAndSweeper(t *testing.T) {
	e := newMediaEnv(t, 10<<20)
	ctx := context.Background()

	// crash model: bytes reached the store, the DB transition never ran
	asset := e.reserve(t, "PHOTO", "a.jpg", 100, nil)
	id := asset["id"].(string)
	row, err := e.q.GetAsset(ctx, dbgen.GetAssetParams{ID: toPGUUID(t, id), WorkspaceID: e.wsID})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.store.Stage(row.StorageKey, bytes.NewReader(jpegBytes(100)), 100); err != nil {
		t.Fatal(err)
	}
	if err := e.store.Commit(row.StorageKey); err != nil {
		t.Fatal(err)
	}
	// DB still says RESERVED — the object is an orphan candidate
	res := e.gql(finalizeMutGQL, map[string]any{
		"input": map[string]any{"assetId": id, "sha256": shaHex(jpegBytes(100))},
	})
	if ps := payloadProblems(t, res, "finalizeUpload"); len(ps) == 0 || ps[0]["code"] != "VALIDATION" {
		t.Fatalf("finalize after crash: %s", res.Raw)
	}
	// retry re-streams over the same opaque key and completes
	if code, body := e.put(t, id, jpegBytes(100), e.cookie, e.csrf); code != http.StatusOK {
		t.Fatalf("retry after crash = %d %s", code, body)
	}

	// sweeper: an unused reservation expires to DELETING and its
	// staged object is removed
	expiring := e.reserve(t, "PHOTO", "gone.jpg", 50, nil)
	eid := expiring["id"].(string)
	if _, err := e.pool.Exec(ctx, "UPDATE assets SET expires_at = now() - interval '1 hour' WHERE id = $1",
		toPGUUID(t, eid)); err != nil {
		t.Fatal(err)
	}
	erow, err := e.q.GetAsset(ctx, dbgen.GetAssetParams{ID: toPGUUID(t, eid), WorkspaceID: e.wsID})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.store.Stage(erow.StorageKey, bytes.NewReader(jpegBytes(50)), 50); err != nil {
		t.Fatal(err)
	}
	if err := e.store.Commit(erow.StorageKey); err != nil {
		t.Fatal(err)
	}
	n, err := e.svc.ExpireReservations(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if n < 1 {
		t.Fatal("sweeper expired nothing")
	}
	erow, _ = e.q.GetAsset(ctx, dbgen.GetAssetParams{ID: toPGUUID(t, eid), WorkspaceID: e.wsID})
	if erow.State != dbgen.AssetStateDELETING {
		t.Fatalf("swept asset state = %s", erow.State)
	}
	if ok, _ := e.store.Exists(erow.StorageKey); ok {
		t.Error("sweeper left the staged object behind")
	}
	// DELETING assets are out of pendingUploads
	res = e.gql(pendingQ, map[string]any{"first": 50})
	for _, it := range res.Data["pendingUploads"].(map[string]any)["items"].([]any) {
		if it.(map[string]any)["id"] == eid {
			t.Fatal("DELETING asset still in pendingUploads")
		}
	}
}

func TestReserveValidationAndIdempotency(t *testing.T) {
	e := newMediaEnv(t, 10<<20)

	// bounds
	res := e.gql(reserveMutGQL, map[string]any{
		"input": map[string]any{"purpose": "PHOTO", "filename": "a.jpg", "claimedBytes": 0},
	})
	if ps := payloadProblems(t, res, "reserveUpload"); len(ps) == 0 || ps[0]["code"] != "VALIDATION" {
		t.Fatalf("claimedBytes 0: %s", res.Raw)
	}
	res = e.gql(reserveMutGQL, map[string]any{
		"input": map[string]any{"purpose": "PHOTO", "filename": "a.jpg", "claimedBytes": 25<<20 + 1},
	})
	if ps := payloadProblems(t, res, "reserveUpload"); len(ps) == 0 {
		t.Fatalf("oversize claim: %s", res.Raw)
	}
	// receipts are honest: purpose is enum-checked at the SDL, claimedBytes at the service

	// idempotent replay returns the same asset without a second row
	cmid := "reserve-1"
	a1 := e.reserve(t, "PHOTO", "same.jpg", 100, &cmid)
	a2 := e.reserve(t, "PHOTO", "same.jpg", 100, &cmid)
	if a1["id"] != a2["id"] {
		t.Fatal("replay returned a different asset")
	}
	res = e.gql(pendingQ, map[string]any{"first": 50})
	if items := res.Data["pendingUploads"].(map[string]any)["items"].([]any); len(items) != 1 {
		t.Fatalf("replay created a second row: %d items", len(items))
	}
	// same key, different input conflicts
	res = e.gql(reserveMutGQL, map[string]any{
		"input": map[string]any{"purpose": "PHOTO", "filename": "other.jpg", "claimedBytes": 100},
		"cmid":  cmid,
	})
	if ps := payloadProblems(t, res, "reserveUpload"); len(ps) == 0 || ps[0]["code"] != "IDEMPOTENCY_CONFLICT" {
		t.Fatalf("idempotency conflict: %s", res.Raw)
	}
}

func TestPendingUploadsPaginationAndCursor(t *testing.T) {
	e := newMediaEnv(t, 10<<20)
	for i := 0; i < 3; i++ {
		e.reserve(t, "PHOTO", fmt.Sprintf("p%d.jpg", i), 10, nil)
	}
	res := e.gql(pendingQ, map[string]any{"first": 2})
	page := res.Data["pendingUploads"].(map[string]any)
	items := page["items"].([]any)
	if len(items) != 2 || !page["pageInfo"].(map[string]any)["hasNextPage"].(bool) {
		t.Fatalf("page 1: %s", res.Raw)
	}
	cur, _ := page["pageInfo"].(map[string]any)["endCursor"].(string)
	if cur == "" {
		t.Fatal("missing endCursor")
	}
	res = e.gql(pendingQ, map[string]any{"first": 2, "after": cur})
	page = res.Data["pendingUploads"].(map[string]any)
	items = page["items"].([]any)
	if len(items) != 1 || page["pageInfo"].(map[string]any)["hasNextPage"].(bool) {
		t.Fatalf("page 2: %s", res.Raw)
	}
	// garbage cursor is a top-level error, never a silent first page
	res = e.gql(pendingQ, map[string]any{"first": 2, "after": "!!!not-a-cursor!!!"})
	if len(res.Errors) == 0 {
		t.Fatalf("bad cursor accepted: %s", res.Raw)
	}
}

func TestUploadRequiresSessionAndCSRF(t *testing.T) {
	e := newMediaEnv(t, 10<<20)
	asset := e.reserve(t, "PHOTO", "a.jpg", 10, nil)
	id := asset["id"].(string)
	if code, _ := e.put(t, id, jpegBytes(10), nil, ""); code != http.StatusUnauthorized {
		t.Fatalf("anonymous PUT = %d, want 401", code)
	}
	if code, _ := e.put(t, id, jpegBytes(10), e.cookie, ""); code != http.StatusForbidden {
		t.Fatalf("CSRF-less PUT = %d, want 403", code)
	}
}

// ---- small conversions -------------------------------------------------------

func toPGUUID(t *testing.T, s string) pgtype.UUID {
	t.Helper()
	var u pgtype.UUID
	if err := u.Scan(s); err != nil {
		t.Fatalf("uuid %q: %v", s, err)
	}
	return u
}

func uuidStrPG(u pgtype.UUID) string {
	h := hex.EncodeToString(u.Bytes[:])
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:32]
}
