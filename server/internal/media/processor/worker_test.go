package processor

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	candb "canopy/server/db"
	dbgen "canopy/server/db/generated"
	"canopy/server/internal/media"
	pmedia "canopy/server/internal/platform/media"
)

// Worker integration against a real disposable schema, a real FSStore
// and the real codec sandbox. The packet's core guarantees are pinned
// here: retried jobs converge on deterministic variants, a deleted
// asset is never revived, and corrupt/bomb inputs land in FAILED with
// honest codes.

type workerEnv struct {
	pool  *pgxpool.Pool
	store pmedia.Store
	w     *Worker
	wsID  pgtype.UUID
}

func newWorkerEnv(t *testing.T) *workerEnv {
	t.Helper()
	requireMediaImage(t)
	dsn := os.Getenv("PGTEST_DSN")
	if dsn == "" {
		t.Skip("PGTEST_DSN not set")
	}
	ctx := context.Background()

	admin, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	schema := fmt.Sprintf("t14_%d", time.Now().UnixNano())
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

	ownerID, wsID := uuid.NewString(), uuid.NewString()
	if _, err := pool.Exec(ctx, `INSERT INTO owner(id, password_hash) VALUES ($1, 'not-a-real-hash')`, ownerID); err != nil {
		t.Fatalf("owner: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO workspace(id, owner_id, display_name, timezone, default_currency)
		 VALUES ($1, $2, 'T14 Worker', 'America/Chicago', 'USD')`, wsID, ownerID); err != nil {
		t.Fatalf("workspace: %v", err)
	}

	store, err := pmedia.NewFSStore(t.TempDir())
	if err != nil {
		t.Fatalf("fs store: %v", err)
	}
	w := &Worker{
		Pool:       pool,
		Store:      store,
		SB:         newTestSandbox(),
		MaxPixels:  DefaultMaxPixels,
		JobTimeout: 180 * time.Second,
		Log:        slog.New(slog.DiscardHandler),
	}
	return &workerEnv{pool: pool, store: store, w: w, wsID: pgtype.UUID{Bytes: uuid.MustParse(wsID), Valid: true}}
}

// newAsset stages a corpus fixture as the source object and inserts the
// asset row in PROCESSING (the state finalize leaves behind).
func (e *workerEnv) newAsset(t *testing.T, fixture string, purpose dbgen.AssetPurpose) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("../../../testdata/media-corpus", fixture))
	if err != nil {
		t.Fatal(err)
	}
	key := uuid.NewString()
	if _, err := e.store.Stage(key, bytes.NewReader(b), int64(len(b))); err != nil {
		t.Fatal(err)
	}
	if err := e.store.Commit(key); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(b)
	id := uuid.NewString()
	_, err = e.pool.Exec(context.Background(),
		`INSERT INTO assets(id, workspace_id, purpose, state, byte_count, mime_type, sha256, storage_key)
		 VALUES ($1, $2, $3, 'PROCESSING', $4, $5, $6, $7)`,
		id, uuid.MustParse(e.wsIDString()), string(purpose), len(b), mimeFor(fixture), hex.EncodeToString(sum[:]), key)
	if err != nil {
		t.Fatalf("insert asset: %v", err)
	}
	return id
}

func (e *workerEnv) wsIDString() string {
	return uuid.UUID(e.wsID.Bytes).String()
}

func mimeFor(fixture string) string {
	switch filepath.Ext(fixture) {
	case ".heic":
		return "image/heic"
	case ".jpg":
		return "image/jpeg"
	case ".png":
		return "image/png"
	case ".webp":
		return "image/webp"
	case ".pdf":
		return "application/pdf"
	}
	return "application/octet-stream"
}

func (e *workerEnv) run(t *testing.T, assetID string) error {
	t.Helper()
	job := &river.Job[media.ProcessAssetArgs]{Args: media.ProcessAssetArgs{AssetID: assetID}}
	return e.w.Work(context.Background(), job)
}

func (e *workerEnv) asset(t *testing.T, assetID string) dbgen.Asset {
	t.Helper()
	a, err := dbgen.New(e.pool).GetAssetByID(context.Background(), uuidToPgtype(t, assetID))
	if err != nil {
		t.Fatalf("get asset: %v", err)
	}
	return a
}

func uuidToPgtype(t *testing.T, s string) pgtype.UUID {
	t.Helper()
	u, err := uuid.Parse(s)
	if err != nil {
		t.Fatal(err)
	}
	return pgtype.UUID{Bytes: u, Valid: true}
}

type variantScan struct {
	Name       string
	WidthPx    pgtype.Int4
	HeightPx   pgtype.Int4
	StorageKey string
	ByteCount  int32
}

func (e *workerEnv) variants(t *testing.T, assetID string) []variantScan {
	t.Helper()
	rows, err := e.pool.Query(context.Background(),
		`SELECT name, width_px, height_px, storage_key, byte_count
		 FROM asset_variants WHERE asset_id = $1 ORDER BY name`, assetID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []variantScan
	for rows.Next() {
		var v variantScan
		if err := rows.Scan(&v.Name, &v.WidthPx, &v.HeightPx, &v.StorageKey, &v.ByteCount); err != nil {
			t.Fatal(err)
		}
		out = append(out, v)
	}
	return out
}

func TestWorkerHappyPathIPhoneHEIC(t *testing.T) {
	e := newWorkerEnv(t)
	id := e.newAsset(t, "iphone-12mp.heic", dbgen.AssetPurposePHOTO)
	if err := e.run(t, id); err != nil {
		t.Fatalf("work: %v", err)
	}
	a := e.asset(t, id)
	if a.State != dbgen.AssetStateREADY || a.ErrorCode.Valid {
		t.Fatalf("state=%s err=%v, want READY with no error", a.State, a.ErrorCode)
	}
	vs := e.variants(t, id)
	if len(vs) != 4 {
		t.Fatalf("got %d variants, want 4", len(vs))
	}
	for _, v := range vs {
		want := VariantKey(id, v.Name)
		if v.StorageKey != want {
			t.Errorf("variant %s key = %s, want deterministic %s", v.Name, v.StorageKey, want)
		}
		ok, err := e.store.Exists(v.StorageKey)
		if err != nil || !ok {
			t.Errorf("variant %s object missing: %v", v.Name, err)
		}
		if v.Name != "original" && (!v.WidthPx.Valid || !v.HeightPx.Valid || v.WidthPx.Int32 >= v.HeightPx.Int32) {
			t.Errorf("variant %s dims %v/%v, want portrait (orientation applied)", v.Name, v.WidthPx, v.HeightPx)
		}
	}
}

func TestWorkerRerunConverges(t *testing.T) {
	e := newWorkerEnv(t)
	id := e.newAsset(t, "iphone-12mp.heic", dbgen.AssetPurposePHOTO)
	if err := e.run(t, id); err != nil {
		t.Fatal(err)
	}
	first := e.variants(t, id)
	// READY repeat is a no-op
	if err := e.run(t, id); err != nil {
		t.Fatal(err)
	}
	if got := e.variants(t, id); len(got) != len(first) {
		t.Fatalf("READY re-run changed variants: %d vs %d", len(got), len(first))
	}

	// crash-after-derivatives: files exist, rows lost, state back to
	// PROCESSING — the re-run must converge to the same keys, not new ones
	if _, err := e.pool.Exec(context.Background(), `DELETE FROM asset_variants WHERE asset_id = $1`, id); err != nil {
		t.Fatal(err)
	}
	if _, err := e.pool.Exec(context.Background(), `UPDATE assets SET state = 'PROCESSING' WHERE id = $1`, id); err != nil {
		t.Fatal(err)
	}
	if err := e.run(t, id); err != nil {
		t.Fatal(err)
	}
	second := e.variants(t, id)
	if len(second) != 4 {
		t.Fatalf("recovered run produced %d variants, want 4", len(second))
	}
	for i := range first {
		if first[i].StorageKey != second[i].StorageKey {
			t.Errorf("key drift after recovery: %s vs %s", first[i].StorageKey, second[i].StorageKey)
		}
	}
}

// TestWorkerDeletedNeverRevived: a DELETING asset with leftover partial
// derivative files must end with the files gone and the row untouched.
func TestWorkerDeletedNeverRevived(t *testing.T) {
	e := newWorkerEnv(t)
	id := e.newAsset(t, "iphone-12mp.heic", dbgen.AssetPurposePHOTO)
	if _, err := e.pool.Exec(context.Background(), `UPDATE assets SET state = 'DELETING' WHERE id = $1`, id); err != nil {
		t.Fatal(err)
	}
	// simulate a previous attempt that left derivative files behind
	for _, v := range Variants {
		key := VariantKey(id, v.Name)
		if _, err := e.store.Stage(key, bytes.NewReader([]byte("partial")), 7); err != nil {
			t.Fatal(err)
		}
		if err := e.store.Commit(key); err != nil {
			t.Fatal(err)
		}
	}
	if err := e.run(t, id); err != nil {
		t.Fatalf("work on DELETING asset: %v", err)
	}
	if a := e.asset(t, id); a.State != dbgen.AssetStateDELETING {
		t.Fatalf("state = %s, must stay DELETING (no revive)", a.State)
	}
	if vs := e.variants(t, id); len(vs) != 0 {
		t.Fatalf("variant rows appeared: %v", vs)
	}
	for _, v := range Variants {
		if ok, err := e.store.Exists(VariantKey(id, v.Name)); err != nil || ok {
			t.Errorf("partial file for %s survived cleanup (exists=%v err=%v)", v.Name, ok, err)
		}
	}
}

func TestWorkerCorruptMarksFailed(t *testing.T) {
	e := newWorkerEnv(t)
	id := e.newAsset(t, "corrupt.jpg", dbgen.AssetPurposePHOTO)
	if err := e.run(t, id); err != nil {
		t.Fatalf("work must not return a retryable error for corrupt input: %v", err)
	}
	a := e.asset(t, id)
	if a.State != dbgen.AssetStateFAILED || a.ErrorCode.String != CodeDecodeFailed {
		t.Fatalf("state=%s code=%q, want FAILED/%s", a.State, a.ErrorCode.String, CodeDecodeFailed)
	}
}

func TestWorkerBombMarksFailed(t *testing.T) {
	e := newWorkerEnv(t)
	id := e.newAsset(t, "bomb.png", dbgen.AssetPurposePHOTO)
	start := time.Now()
	if err := e.run(t, id); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(start); elapsed > 20*time.Second {
		t.Errorf("bomb handling took %v, budget check must be pre-decode", elapsed)
	}
	a := e.asset(t, id)
	if a.State != dbgen.AssetStateFAILED || a.ErrorCode.String != CodePixelBudget {
		t.Fatalf("state=%s code=%q, want FAILED/%s", a.State, a.ErrorCode.String, CodePixelBudget)
	}
	if vs := e.variants(t, id); len(vs) != 0 {
		t.Errorf("no variants may exist for a refused bomb, got %v", vs)
	}
}

func TestWorkerReceiptPDF(t *testing.T) {
	e := newWorkerEnv(t)
	id := e.newAsset(t, "receipt.pdf", dbgen.AssetPurposeRECEIPT)
	if err := e.run(t, id); err != nil {
		t.Fatal(err)
	}
	if a := e.asset(t, id); a.State != dbgen.AssetStateREADY {
		t.Fatalf("state = %s, want READY", a.State)
	}
	vs := e.variants(t, id)
	if len(vs) != 1 || vs[0].Name != "original" {
		t.Fatalf("receipt variants = %+v, want exactly one 'original'", vs)
	}
	if vs[0].WidthPx.Valid || vs[0].HeightPx.Valid {
		t.Errorf("receipt original must have null dims, got %v/%v", vs[0].WidthPx, vs[0].HeightPx)
	}
	if vs[0].StorageKey != VariantKey(id, "original") {
		t.Errorf("receipt key not deterministic")
	}
	if ok, err := e.store.Exists(vs[0].StorageKey); err != nil || !ok {
		t.Errorf("receipt original object missing: %v", err)
	}
}

func TestWorkerMissingObject(t *testing.T) {
	e := newWorkerEnv(t)
	id := uuid.NewString()
	if _, err := e.pool.Exec(context.Background(),
		`INSERT INTO assets(id, workspace_id, purpose, state, byte_count, sha256, storage_key)
		 VALUES ($1, $2, 'PHOTO', 'PROCESSING', 10, $3, $4)`,
		id, uuid.MustParse(e.wsIDString()), strings.Repeat("0", 64), uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	if err := e.run(t, id); err != nil {
		t.Fatal(err)
	}
	a := e.asset(t, id)
	if a.State != dbgen.AssetStateFAILED || a.ErrorCode.String != CodeObjectMissing {
		t.Fatalf("state=%s code=%q, want FAILED/%s", a.State, a.ErrorCode.String, CodeObjectMissing)
	}
}
