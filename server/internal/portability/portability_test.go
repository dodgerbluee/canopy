// PORTABILITY verification suite (T38.a): export build + fencing +
// cleanup, backup quiesce/unquiesce, sealed sets, blank-host restore
// with reconciliation, and hostile-archive rejection. Runs against a
// real Postgres (PGTEST_DSN) with the full migration set, same
// disposable-schema discipline as every other suite here.
package portability

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	candb "canopy/server/db"
	dbgen "canopy/server/db/generated"
	"canopy/server/internal/agentaccess"
	"canopy/server/internal/aiconnection"
	"canopy/server/internal/platform"
)

// ---------- harness ----------

type env struct {
	ctx   context.Context
	pool  *pgxpool.Pool
	svc   *Service
	ws    pgtype.UUID
	store string
	dsn   string
}

func newSchema(t *testing.T, migrate bool) (string, *pgxpool.Pool) {
	t.Helper()
	dsn := os.Getenv("PGTEST_DSN")
	if dsn == "" {
		t.Skip("PGTEST_DSN not set")
	}
	ctx := context.Background()
	admin, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	schema := fmt.Sprintf("t38_%d", time.Now().UnixNano())
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	t.Cleanup(func() {
		c, err := pgx.Connect(context.Background(), dsn)
		if err == nil {
			_, _ = c.Exec(context.Background(), "DROP SCHEMA IF EXISTS "+schema+" CASCADE")
			c.Close(context.Background())
		}
	})
	if migrate {
		up, err := candb.AllUp()
		if err != nil {
			t.Fatalf("migrations: %v", err)
		}
		if _, err := admin.Exec(ctx, "SET search_path TO "+schema); err != nil {
			t.Fatalf("search_path: %v", err)
		}
		if _, err := admin.Exec(ctx, up); err != nil {
			t.Fatalf("apply migrations: %v", err)
		}
	}
	admin.Close(ctx)
	schemaDSN := dsn + "&search_path=" + schema
	pool, err := pgxpool.New(ctx, schemaDSN)
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return schemaDSN, pool
}

// newEnv is a fully migrated, seeded stack with a live insert-only queue.
func newEnv(t *testing.T) *env {
	t.Helper()
	dsn, pool := newSchema(t, true)
	ctx := context.Background()
	if err := platform.MigrateRiver(ctx, pool); err != nil {
		t.Fatalf("river migrate: %v", err)
	}
	ids := platform.UUIDv4{}
	q := dbgen.New(pool)
	owner, err := q.InsertOwner(ctx, dbgen.InsertOwnerParams{ID: ids.New(), PasswordHash: "x"})
	if err != nil {
		t.Fatalf("owner: %v", err)
	}
	ws, err := q.InsertWorkspace(ctx, dbgen.InsertWorkspaceParams{
		ID: ids.New(), OwnerID: owner.ID, DisplayName: "T38", Timezone: "UTC", DefaultCurrency: "USD",
	})
	if err != nil {
		t.Fatalf("workspace: %v", err)
	}
	queue, err := platform.NewInsertOnlyRiverClient(pool)
	if err != nil {
		t.Fatalf("river client: %v", err)
	}
	svc := New(pool, slog.New(slog.DiscardHandler), t.TempDir(), queue)
	svc.DSN = dsn
	return &env{ctx: ctx, pool: pool, svc: svc, ws: ws.ID, store: svc.StoreRoot, dsn: dsn}
}

// blankEnv is a fresh host: empty schema, no migrations, no data.
func blankEnv(t *testing.T) *env {
	t.Helper()
	dsn, pool := newSchema(t, false)
	svc := New(pool, slog.New(slog.DiscardHandler), t.TempDir(), nil)
	svc.DSN = dsn
	return &env{ctx: context.Background(), pool: pool, svc: svc, store: svc.StoreRoot, dsn: dsn}
}

func (e *env) mustPlant(t *testing.T, name string) pgtype.UUID {
	t.Helper()
	id := platform.UUIDv4{}.New()
	if _, err := dbgen.New(e.pool).InsertPlant(e.ctx, dbgen.InsertPlantParams{
		ID: id, WorkspaceID: e.ws, Name: name,
		Type: dbgen.PlantTypeINDIVIDUAL, Category: dbgen.PlantCategoryOTHER,
	}); err != nil {
		t.Fatalf("plant: %v", err)
	}
	return id
}

// mustReadyAsset inserts a READY asset and writes its immutable bytes
// into the storage root, mirroring the committed state of the store.
func (e *env) mustReadyAsset(t *testing.T, purpose dbgen.AssetPurpose) (dbgen.Asset, []byte) {
	t.Helper()
	ids := platform.UUIDv4{}
	q := dbgen.New(e.pool)
	key := uuidString(ids.New())
	data := []byte("immutable-media-" + key)
	sha := sha256.Sum256(data)
	if err := os.WriteFile(filepath.Join(e.store, key), data, 0o640); err != nil {
		t.Fatal(err)
	}
	asset, err := q.InsertAsset(e.ctx, dbgen.InsertAssetParams{
		ID: ids.New(), WorkspaceID: e.ws, Purpose: purpose,
		ByteCount: int32(len(data)), Sha256: hex.EncodeToString(sha[:]), StorageKey: key,
	})
	if err != nil {
		t.Fatalf("asset: %v", err)
	}
	asset, err = q.MarkAssetStateFrom(e.ctx, dbgen.MarkAssetStateFromParams{
		ID: asset.ID, WorkspaceID: e.ws,
		FromState: dbgen.AssetStateRESERVED, ToState: dbgen.AssetStateREADY,
		ErrorCode: pgtype.Text{},
	})
	if err != nil {
		t.Fatalf("ready: %v", err)
	}
	return asset, data
}

func (e *env) mustPhotoLink(t *testing.T, plant, asset pgtype.UUID) {
	t.Helper()
	if _, err := dbgen.New(e.pool).InsertPhoto(e.ctx, dbgen.InsertPhotoParams{
		ID: platform.UUIDv4{}.New(), WorkspaceID: e.ws, PlantID: plant, AssetID: asset,
		TakenAt: ts(time.Now()), DateSource: dbgen.PhotoDateSourceUSER,
		Viewpoint: dbgen.PhotoViewpointWHOLE,
	}); err != nil {
		t.Fatalf("photo link: %v", err)
	}
}

func (e *env) mustPurchaseWithAllocation(t *testing.T, plant pgtype.UUID) (allocID pgtype.UUID) {
	t.Helper()
	ids := platform.UUIDv4{}
	purID, lineID, allocID := ids.New(), ids.New(), ids.New()
	if _, err := e.pool.Exec(e.ctx,
		`INSERT INTO purchases (id, workspace_id, currency, total_minor) VALUES ($1,$2,'USD',100)`,
		purID, e.ws); err != nil {
		t.Fatal(err)
	}
	if _, err := e.pool.Exec(e.ctx,
		`INSERT INTO purchase_lines (id, workspace_id, purchase_id, description, quantity, line_total_minor)
		 VALUES ($1,$2,$3,'soil',1,100)`, lineID, e.ws, purID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.pool.Exec(e.ctx,
		`INSERT INTO purchase_allocations (id, workspace_id, purchase_line_id, plant_id, line_minor)
		 VALUES ($1,$2,$3,$4,100)`, allocID, e.ws, lineID, plant); err != nil {
		t.Fatal(err)
	}
	return allocID
}

// ---------- unit: archive path + sealing ----------

func TestSafeArchivePath(t *testing.T) {
	ok := []string{
		"manifest.json", "db/plants.jsonl", "river/river_job.jsonl",
		"media/3f2a1b89-8b8f-4b56-9a0e-2c4d6e8f0a11",
	}
	for _, name := range ok {
		if err := SafeArchivePath(name); err != nil {
			t.Errorf("want accepted %q, got %v", name, err)
		}
	}
	bad := []string{
		"", "../escape", "db/../../escape.jsonl", "media/../evil",
		"/absolute.jsonl", "C:\\evil", "db/plants.jsonl/../../x",
		"unknown/file.jsonl", "media/not-a-uuid", "db/;drop.jsonl",
		"river/other.jsonl", "./manifest.json", "media/3F2A1B89-8B8F-4B56-9A0E-2C4D6E8F0A11",
	}
	for _, name := range bad {
		if err := SafeArchivePath(name); err == nil {
			t.Errorf("want rejected %q", name)
		}
	}
}

func TestSafeExportPath(t *testing.T) {
	ok := []string{
		"manifest.json", "data/plants.csv", "data/plants.jsonl",
		"data/activities.jsonl", "media/3f2a1b89-8b8f-4b56-9a0e-2c4d6e8f0a11",
	}
	for _, name := range ok {
		if err := SafeExportPath(name); err != nil {
			t.Errorf("want accepted %q, got %v", name, err)
		}
	}
	bad := []string{
		"", "../escape", "data/../../escape.csv", "/abs.csv",
		"data/evil.sh", "db/plants.jsonl", "river/river_job.jsonl",
		"media/not-a-uuid", "data/", "data/x.CSV",
	}
	for _, name := range bad {
		if err := SafeExportPath(name); err == nil {
			t.Errorf("want rejected %q", name)
		}
	}
}

func TestSealAndKeyFile(t *testing.T) {
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "key")
	hexKey := strings.Repeat("ab", 32)
	if err := os.WriteFile(keyPath, []byte(hexKey+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	key, err := LoadKey(keyPath)
	if err != nil || len(key) != 32 {
		t.Fatalf("LoadKey: %v", err)
	}
	for _, bad := range []string{"deadbeef", strings.Repeat("zz", 32), strings.Repeat("ab", 31)} {
		p := filepath.Join(dir, "bad-"+bad[:4])
		if err := os.WriteFile(p, []byte(bad), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadKey(p); err == nil {
			t.Errorf("malformed key file must be refused: %q", bad)
		}
	}

	plain := []byte("backup payload")
	sealed, err := Seal(plain, key)
	if err != nil {
		t.Fatal(err)
	}
	if !IsSealed(sealed) {
		t.Fatal("sealed payload must carry the magic header")
	}
	if IsSealed(plain) {
		t.Fatal("plain payload must not look sealed")
	}
	out, err := OpenSealed(sealed, key)
	if err != nil || !bytes.Equal(out, plain) {
		t.Fatalf("round trip: %v", err)
	}
	wrongKey := make([]byte, 32)
	copy(wrongKey, key)
	wrongKey[0] ^= 0xff
	if _, err := OpenSealed(sealed, wrongKey); err == nil {
		t.Fatal("wrong key must fail authentication")
	}
}

// ---------- export lifecycle ----------

func TestExportBuildAndCleanup(t *testing.T) {
	e := newEnv(t)
	keyFile := filepath.Join(t.TempDir(), "ai-key")
	manager, err := aiconnection.New(e.pool, keyFile, nil)
	if err != nil {
		t.Fatal(err)
	}
	secret := "fixture-provider-secret"
	connection, err := manager.Save(e.ctx, e.ws, aiconnection.SaveInput{ID: "11111111-1111-4111-8111-111111111111", Name: "Portable AI", Protocol: aiconnection.OpenAI, Endpoint: "http://127.0.0.1:1234/v1", Model: "fixture-model", DeploymentLocal: true, SupportsVision: true, APIKey: &secret, TokenLimitField: "MAX_TOKENS"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = manager.SaveBindings(e.ctx, e.ws, []aiconnection.BindingInput{{Feature: aiconnection.Garden, Mode: "CONNECTION", ConnectionID: connection.ID}}, nil); err != nil {
		t.Fatal(err)
	}

	if _, err := e.pool.Exec(e.ctx, `INSERT INTO rain_events(id,workspace_id,event_date,amount_mm,note) VALUES($1,$2,'2026-11-01',25.4,'Rain export')`, platform.UUIDv4{}.New(), e.ws); err != nil {
		t.Fatal(err)
	}
	if _, err := e.pool.Exec(e.ctx, `INSERT INTO garden_knowledge_settings(workspace_id,country,region,place,enabled) VALUES($1,'US','Texas','Export garden',false)`, e.ws); err != nil {
		t.Fatal(err)
	}
	if _, err := e.pool.Exec(e.ctx, `INSERT INTO knowledge_profiles(workspace_id,profile_key,facts) VALUES($1,'fixture-profile','[]')`, e.ws); err != nil {
		t.Fatal(err)
	}
	plant := e.mustPlant(t, "Exported Monstera")
	asset, data := e.mustReadyAsset(t, dbgen.AssetPurposePHOTO)
	e.mustPhotoLink(t, plant, asset.ID)

	job, err := e.svc.RequestExport(e.ctx, e.ws)
	if err != nil {
		t.Fatalf("requestExport: %v", err)
	}
	if job.State != dbgen.ExportStateQUEUED {
		t.Fatalf("want QUEUED, got %s", job.State)
	}
	// dedup: a second request returns the live job, never a second build
	job2, err := e.svc.RequestExport(e.ctx, e.ws)
	if err != nil || job2.ID != job.ID {
		t.Fatalf("live export must be deduplicated: %v %+v", err, job2)
	}
	var n int
	if err := e.pool.QueryRow(e.ctx,
		`SELECT count(*) FROM river_job WHERE kind = 'export:build'`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("export:build job must be enqueued atomically with the job row: %d %v", n, err)
	}

	id := uuidString(job.ID)
	w := &ExportBuildWorker{Svc: e.svc}
	if err := w.Work(e.ctx, &river.Job[ExportBuildArgs]{Args: ExportBuildArgs{ExportID: id}}); err != nil {
		t.Fatalf("build: %v", err)
	}

	done, err := e.svc.GetExportJob(e.ctx, id, e.ws)
	if err != nil {
		t.Fatal(err)
	}
	if done.State != dbgen.ExportStateREADY || !done.ManifestSha256.Valid || !done.FinishedAt.Valid {
		t.Fatalf("want READY with manifest sha: %+v", done)
	}
	archivePath, err := ExportArchivePath(e.svc.ExportsDir(), id)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(archivePath); err != nil {
		t.Fatalf("archive must exist: %v", err)
	}

	entries := readTar(t, archivePath)
	if !bytes.Contains(entries["data/ai_connections.jsonl"], []byte("Portable AI")) || bytes.Contains(entries["data/ai_connections.jsonl"], []byte("credential_ciphertext")) {
		t.Fatal("AI metadata export incorrect")
	}
	for name, body := range entries {
		if bytes.Contains(body, []byte(secret)) {
			t.Fatal("provider secret leaked into export", name)
		}
	}

	for _, want := range []string{"manifest.json", "data/plants.csv", "data/plants.jsonl",
		"data/assets.jsonl", "media/" + asset.StorageKey, "data/purchases.csv"} {
		if _, ok := entries[want]; !ok {
			t.Errorf("archive missing %q", want)
		}
	}
	if !bytes.Contains(entries["data/garden_knowledge_settings.jsonl"], []byte("Export garden")) || !bytes.Contains(entries["data/knowledge_profiles.jsonl"], []byte("fixture-profile")) {
		t.Fatal("knowledge/settings missing from export")
	}
	if !bytes.Contains(entries["data/rain_events.jsonl"], []byte("Rain export")) {
		t.Fatal("rain missing from user export")
	}
	if !bytes.Contains(entries["data/plants.csv"], []byte("Exported Monstera")) {
		t.Error("plants.csv must carry the plant row")
	}
	if !bytes.Equal(entries["media/"+asset.StorageKey], data) {
		t.Error("media bytes must match the stored object")
	}
	// no secrets, sessions or receipts in a user export (03)
	for name, blob := range entries {
		low := strings.ToLower(name)
		for _, banned := range []string{"session", "mutation_receipt", "audit", "owner"} {
			if strings.Contains(low, banned) {
				t.Errorf("export must not carry %q entries, found %q", banned, name)
			}
		}
		if bytes.Contains(blob, []byte("password_hash")) {
			t.Errorf("export entry %q leaks password material", name)
		}
	}
	var em ExportManifest
	if err := json.Unmarshal(entries["manifest.json"], &em); err != nil {
		t.Fatalf("manifest: %v", err)
	}
	if em.SchemaVersion != SchemaVersion || em.WorkspaceID != uuidString(e.ws) {
		t.Errorf("manifest provenance wrong: %+v", em)
	}
	if em.Counts["plants"] != 1 || len(em.Exclusions) != len(ExportExclusions) {
		t.Errorf("manifest counts/exclusions wrong: %+v", em)
	}

	// cleanup removes the archive bytes (revoked/expired path)
	cw := &ExportCleanupWorker{Svc: e.svc}
	if err := cw.Work(e.ctx, &river.Job[ExportCleanupArgs]{Args: ExportCleanupArgs{ExportID: id}}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(archivePath); !os.IsNotExist(err) {
		t.Fatal("cleanup must remove the archive file")
	}
}

func TestExportRevokedBeforeBuild(t *testing.T) {
	e := newEnv(t)
	e.mustPlant(t, "Doomed")
	job, err := e.svc.RequestExport(e.ctx, e.ws)
	if err != nil {
		t.Fatal(err)
	}
	id := uuidString(job.ID)

	// what purgePlant does to live exports: revoke + epoch bump
	if _, err := e.pool.Exec(e.ctx,
		`UPDATE export_jobs SET state = 'EXPIRED' WHERE workspace_id = $1`, e.ws); err != nil {
		t.Fatal(err)
	}
	if _, err := dbgen.New(e.pool).BumpPrivacyEpoch(e.ctx, e.ws); err != nil {
		t.Fatal(err)
	}

	w := &ExportBuildWorker{Svc: e.svc}
	if err := w.Work(e.ctx, &river.Job[ExportBuildArgs]{Args: ExportBuildArgs{ExportID: id}}); err != nil {
		t.Fatalf("revoked build must exit cleanly: %v", err)
	}
	after, err := e.svc.GetExportJob(e.ctx, id, e.ws)
	if err != nil {
		t.Fatal(err)
	}
	if after.State != dbgen.ExportStateEXPIRED {
		t.Fatalf("revoked job must stay EXPIRED, got %s", after.State)
	}
	p, _ := ExportArchivePath(e.svc.ExportsDir(), id)
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Fatal("a revoked export must never publish archive bytes")
	}
}

// ---------- backup / restore ----------

func TestBackupRestoreRoundTrip(t *testing.T) {
	e := newEnv(t)
	agentManager := agentaccess.New(e.pool)
	agentIssued, agentErr := agentManager.Save(e.ctx, e.ws, agentaccess.SaveInput{ID: uuidString(platform.UUIDv4{}.New()), Name: "Restored agent", Scopes: []string{"garden:read"}, ExpiresAt: time.Now().Add(time.Hour)})
	if agentErr != nil {
		t.Fatal(agentErr)
	}

	keyFile := filepath.Join(t.TempDir(), "ai-key")
	manager, err := aiconnection.New(e.pool, keyFile, nil)
	if err != nil {
		t.Fatal(err)
	}
	secret := "fixture-provider-secret"
	connection, err := manager.Save(e.ctx, e.ws, aiconnection.SaveInput{ID: "11111111-1111-4111-8111-111111111111", Name: "Portable AI", Protocol: aiconnection.OpenAI, Endpoint: "http://127.0.0.1:1234/v1", Model: "fixture-model", DeploymentLocal: true, SupportsVision: true, APIKey: &secret, TokenLimitField: "MAX_TOKENS"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = manager.SaveBindings(e.ctx, e.ws, []aiconnection.BindingInput{{Feature: aiconnection.Garden, Mode: "CONNECTION", ConnectionID: connection.ID}}, nil); err != nil {
		t.Fatal(err)
	}

	if _, err := e.pool.Exec(e.ctx, `INSERT INTO rain_events(id,workspace_id,event_date,amount_mm,note) VALUES($1,$2,'2026-11-01',25.4,'Rain restore')`, platform.UUIDv4{}.New(), e.ws); err != nil {
		t.Fatal(err)
	}
	if _, err := e.pool.Exec(e.ctx, `INSERT INTO garden_knowledge_settings(workspace_id,country,region,place,enabled) VALUES($1,'US','Texas','Restored garden',false)`, e.ws); err != nil {
		t.Fatal(err)
	}
	if _, err := e.pool.Exec(e.ctx, `INSERT INTO knowledge_profiles(workspace_id,profile_key,facts) VALUES($1,'fixture-profile','[]')`, e.ws); err != nil {
		t.Fatal(err)
	}
	plant := e.mustPlant(t, "Restored Fern")
	e.mustPurchaseWithAllocation(t, plant)
	asset, data := e.mustReadyAsset(t, dbgen.AssetPurposePHOTO)
	e.mustPhotoLink(t, plant, asset.ID)

	// deletion-ledger residue from an earlier purge: tombstone + gone receipt
	tombID, entID, recID := platform.UUIDv4{}.New(), platform.UUIDv4{}.New(), platform.UUIDv4{}.New()
	if _, err := dbgen.New(e.pool).InsertTombstone(e.ctx, dbgen.InsertTombstoneParams{
		ID: tombID, WorkspaceID: e.ws, EntityKind: "plant", EntityID: entID,
		Action: "purgePlant", ActorID: e.ws, OccurredAt: ts(time.Now()), PrivacyEpoch: 3,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.pool.Exec(e.ctx,
		`INSERT INTO mutation_receipts (id, workspace_id, operation, idempotency_key, request_hash, result, expires_at, gone)
		 VALUES ($1,$2,'createPlant','cm-1','h','{"id":"x"}', now()+interval '30 days', true)`,
		recID, e.ws); err != nil {
		t.Fatal(err)
	}
	if _, err := e.pool.Exec(e.ctx, `UPDATE workspace SET privacy_epoch = 3 WHERE id = $1`, e.ws); err != nil {
		t.Fatal(err)
	}

	dest := t.TempDir()
	sum, err := e.svc.Backup(e.ctx, BackupOptions{Dest: dest})
	if err != nil {
		t.Fatalf("backup: %v", err)
	}
	if sum.MediaFiles != 1 || sum.Rows == 0 || sum.Sealed {
		t.Fatalf("backup summary wrong: %+v", sum)
	}
	if IsMaintenance(e.ctx, e.pool) {
		t.Fatal("a successful backup must leave the stack unquiesced")
	}
	for _, f := range []string{sum.Archive, sum.Ledger, sum.Manifest} {
		if _, err := os.Stat(f); err != nil {
			t.Fatalf("backup set file %s: %v", f, err)
		}
	}

	// restore onto a blank host
	b := blankEnv(t)
	rsum, err := b.svc.Restore(b.ctx, RestoreOptions{Archive: sum.Archive, Ledger: sum.Ledger})
	if err != nil {
		t.Fatalf("restore: %v", err)
	}
	if _, err := agentaccess.New(b.pool).Verify(b.ctx, agentIssued.Token); err == nil {
		t.Fatal("restored bearer credential was reactivated")
	}
	var restoredAgentCount int
	if err := b.pool.QueryRow(b.ctx, `SELECT count(*) FROM agent_grants WHERE id=$1 AND revoked_at IS NOT NULL`, agentIssued.Grant.ID).Scan(&restoredAgentCount); err != nil || restoredAgentCount != 1 {
		t.Fatal("agent grant metadata did not restore revoked", err)
	}
	if rsum.Duration <= 0 {
		t.Fatal("restore timing must be measured")
	}
	t.Logf("restore timing: %s (tables=%d rows=%d media=%d ledger=%d)",
		rsum.Duration, rsum.Tables, rsum.Rows, rsum.MediaFiles, rsum.LedgerRows)
	if rsum.Reconcile["plants"] != 1 || rsum.Reconcile["mediaFilesVerified"] != 1 {
		t.Fatalf("reconcile map wrong: %+v", rsum.Reconcile)
	}
	if rsum.Reconcile["allocatedMinor"] != 100 || rsum.Reconcile["financialCurrencyCount"] != 1 {
		t.Fatalf("financial anchors wrong: %+v", rsum.Reconcile)
	}

	restoredAI, err := aiconnection.New(b.pool, keyFile, nil)
	if err != nil {
		t.Fatal(err)
	}
	selected, err := restoredAI.Resolve(b.ctx, e.ws, aiconnection.Garden)
	if err != nil || selected.Config == nil || selected.Config.APIKey != secret {
		t.Fatal("encrypted provider connection did not restore with its separately held key", err)
	}
	wrongAI, err := aiconnection.New(b.pool, filepath.Join(t.TempDir(), "wrong-key"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = wrongAI.Resolve(b.ctx, e.ws, aiconnection.Garden); err == nil {
		t.Fatal("restore without correct key silently succeeded")
	}
	var restoredPlace string
	if err := b.pool.QueryRow(b.ctx, `SELECT place FROM garden_knowledge_settings WHERE workspace_id=$1`, e.ws).Scan(&restoredPlace); err != nil || restoredPlace != "Restored garden" {
		t.Fatal("knowledge settings restore", err, restoredPlace)
	}
	var profileCount int
	if err := b.pool.QueryRow(b.ctx, `SELECT count(*) FROM knowledge_profiles WHERE workspace_id=$1 AND profile_key='fixture-profile'`, e.ws).Scan(&profileCount); err != nil || profileCount != 1 {
		t.Fatal("knowledge profile restore", err, profileCount)
	}
	var rainAmount string
	if err := b.pool.QueryRow(b.ctx, `SELECT amount_mm::text FROM rain_events WHERE workspace_id=$1 AND note='Rain restore'`, e.ws).Scan(&rainAmount); err != nil || rainAmount != "25.4" {
		t.Fatal("rain must survive restore exactly", err, rainAmount)
	}
	// ledger re-applied: tombstone present, gone flag present, epoch kept
	var tombs, gone int
	var epoch int64
	if err := b.pool.QueryRow(b.ctx,
		`SELECT count(*) FROM deletion_tombstones WHERE id = $1`, tombID).Scan(&tombs); err != nil || tombs != 1 {
		t.Fatalf("tombstone must survive restore: %d %v", tombs, err)
	}
	if err := b.pool.QueryRow(b.ctx,
		`SELECT count(*) FROM mutation_receipts WHERE id = $1 AND gone`, recID).Scan(&gone); err != nil || gone != 1 {
		t.Fatalf("gone receipt must survive restore: %d %v", gone, err)
	}
	if err := b.pool.QueryRow(b.ctx, `SELECT privacy_epoch FROM workspace WHERE id = $1`, e.ws).Scan(&epoch); err != nil || epoch < 3 {
		t.Fatalf("privacy epoch watermark must survive: %d %v", epoch, err)
	}
	// media bytes restored and hash-verified by reconcile
	got, err := os.ReadFile(filepath.Join(b.store, asset.StorageKey))
	if err != nil || !bytes.Equal(got, data) {
		t.Fatalf("restored media bytes: %v", err)
	}

	// restores never merge: a second apply onto the now-non-blank host is refused
	if _, err := b.svc.Restore(b.ctx, RestoreOptions{Archive: sum.Archive, Ledger: sum.Ledger}); err == nil ||
		!strings.Contains(err.Error(), "non-blank") {
		t.Fatalf("non-blank target must be refused, got %v", err)
	}
}

func TestBackupFailureUnquiesces(t *testing.T) {
	e := newEnv(t)
	asset, _ := e.mustReadyAsset(t, dbgen.AssetPurposePHOTO)
	// referenced object vanished: the backup must fail honestly…
	if err := os.Remove(filepath.Join(e.store, asset.StorageKey)); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Backup(e.ctx, BackupOptions{Dest: t.TempDir()}); err == nil ||
		!strings.Contains(err.Error(), "missing") {
		t.Fatalf("missing referenced media must fail the backup, got %v", err)
	}
	// …and must never leave the stack locked
	if IsMaintenance(e.ctx, e.pool) {
		t.Fatal("failed backup must still unquiesce the stack")
	}
}

func TestRestoreRejectsTamperedArchive(t *testing.T) {
	e := newEnv(t)
	e.mustPlant(t, "Tamper")
	dest := t.TempDir()
	sum, err := e.svc.Backup(e.ctx, BackupOptions{Dest: dest})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(sum.Archive)
	if err != nil {
		t.Fatal(err)
	}
	raw[len(raw)/2] ^= 0xff
	if err := os.WriteFile(sum.Archive, raw, 0o640); err != nil {
		t.Fatal(err)
	}
	b := blankEnv(t)
	if _, err := b.svc.Restore(b.ctx, RestoreOptions{Archive: sum.Archive, Ledger: sum.Ledger}); err == nil ||
		!strings.Contains(err.Error(), "checksum") {
		t.Fatalf("tampered archive must fail the manifest checksum, got %v", err)
	}
	// the refusal happened before any schema landed on the target
	var n int
	if err := b.pool.QueryRow(b.ctx,
		`SELECT count(*) FROM pg_tables WHERE schemaname = current_schema()`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("rejected restore must not touch the target schema: %d %v", n, err)
	}
}

func TestRestoreRejectsTraversalArchive(t *testing.T) {
	// hand-built hostile set: a valid external manifest wrapping a tar
	// whose entries escape the storage root
	dir := t.TempDir()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	payload := []byte("pwned")
	for _, name := range []string{"../evil", "db/plants.jsonl"} {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o640, Size: int64(len(payload)), Format: tar.FormatPAX}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(payload); err != nil {
			t.Fatal(err)
		}
	}
	inner := BackupManifest{
		SchemaVersion: SchemaVersion, BackupSet: "evil", CreatedAt: time.Now().UTC(),
		Entries: []ArchiveEntry{{Path: "../evil", SHA256: shaHex(payload), Bytes: int64(len(payload))}},
	}
	imj, _ := json.Marshal(inner)
	if err := tw.WriteHeader(&tar.Header{Name: "manifest.json", Mode: 0o640, Size: int64(len(imj)), Format: tar.FormatPAX}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(imj); err != nil {
		t.Fatal(err)
	}
	tw.Close()

	tarBytes := buf.Bytes()
	ps := sha256.Sum256(tarBytes)
	ext := inner
	ext.PayloadSha256 = hex.EncodeToString(ps[:])
	ext.Entries = append(ext.Entries, ArchiveEntry{Path: "db/plants.jsonl", SHA256: shaHex(payload), Bytes: int64(len(payload))})
	emj, _ := json.Marshal(ext)

	archive := filepath.Join(dir, "evil.tar")
	if err := os.WriteFile(archive, tarBytes, 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(strings.TrimSuffix(archive, ".tar")+".manifest.json", emj, 0o640); err != nil {
		t.Fatal(err)
	}

	b := blankEnv(t)
	if _, err := b.svc.Restore(b.ctx, RestoreOptions{Archive: archive}); err == nil {
		t.Fatal("traversal archive must be refused")
	} else {
		t.Logf("traversal refused: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "..", "evil")); err == nil {
		t.Fatal("refused restore must not write outside the storage root")
	}
}

func TestSealedBackupRestore(t *testing.T) {
	e := newEnv(t)
	e.mustPlant(t, "Sealed")
	asset, _ := e.mustReadyAsset(t, dbgen.AssetPurposePHOTO)

	keyPath := filepath.Join(t.TempDir(), "operator.key")
	if err := os.WriteFile(keyPath, []byte(strings.Repeat("cd", 32)), 0o600); err != nil {
		t.Fatal(err)
	}
	dest := t.TempDir()
	sum, err := e.svc.Backup(e.ctx, BackupOptions{Dest: dest, KeyFile: keyPath})
	if err != nil {
		t.Fatalf("sealed backup: %v", err)
	}
	if !sum.Sealed || !strings.HasSuffix(sum.Archive, ".tar.enc") || !strings.HasSuffix(sum.Ledger, ".ledger.enc") {
		t.Fatalf("sealed set names wrong: %+v", sum)
	}
	raw, err := os.ReadFile(sum.Archive)
	if err != nil {
		t.Fatal(err)
	}
	if !IsSealed(raw) {
		t.Fatal("archive must carry the seal header")
	}
	// the plaintext plant name must not be findable in the sealed bytes
	if bytes.Contains(raw, []byte("Sealed")) {
		t.Fatal("sealed archive must not contain plaintext payloads")
	}

	// sealed set without the key: honest refusal, nothing applied
	b1 := blankEnv(t)
	if _, err := b1.svc.Restore(b1.ctx, RestoreOptions{Archive: sum.Archive, Ledger: sum.Ledger}); !errors.Is(err, ErrKeyRequired) {
		t.Fatalf("sealed restore without key must be refused, got %v", err)
	}
	// with the key: full round trip
	b2 := blankEnv(t)
	rsum, err := b2.svc.Restore(b2.ctx, RestoreOptions{Archive: sum.Archive, Ledger: sum.Ledger, KeyFile: keyPath})
	if err != nil {
		t.Fatalf("sealed restore: %v", err)
	}
	if rsum.Reconcile["plants"] != 1 || rsum.Reconcile["mediaFilesVerified"] != 1 {
		t.Fatalf("sealed restore reconcile wrong: %+v", rsum.Reconcile)
	}
	if _, err := os.Stat(filepath.Join(b2.store, asset.StorageKey)); err != nil {
		t.Fatal("sealed restore must land the media files")
	}
}

func TestMaintenanceFlagRoundTrip(t *testing.T) {
	e := newEnv(t)
	if IsMaintenance(e.ctx, e.pool) {
		t.Fatal("fresh stack is not in maintenance")
	}
	if err := SetMaintenance(e.ctx, e.pool, true); err != nil {
		t.Fatal(err)
	}
	if !IsMaintenance(e.ctx, e.pool) {
		t.Fatal("maintenance flag must read back")
	}
	if err := SetMaintenance(e.ctx, e.pool, false); err != nil {
		t.Fatal(err)
	}
	if IsMaintenance(e.ctx, e.pool) {
		t.Fatal("maintenance flag must clear")
	}
}

// ---------- helpers ----------

func readTar(t *testing.T, path string) map[string][]byte {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	out := map[string][]byte{}
	tr := tar.NewReader(f)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		b, err := io.ReadAll(tr)
		if err != nil {
			t.Fatal(err)
		}
		out[h.Name] = b
	}
	return out
}

func shaHex(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}
