// Deletion-matrix tests for purgePlant (T38.a): owned history cascades,
// financial references become deleted-plant markers with amounts
// preserved, shared assets survive one plant's purge, unreferenced
// assets enter DELETING with a grace-period purge job, the deletion
// ledger records every id, audit content is redacted, stored responses
// mentioning purged ids replay as RESOURCE_GONE, and live exports are
// revoked with their archives scheduled for deletion.
package collection

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	candb "canopy/server/db"
	dbgen "canopy/server/db/generated"
	"canopy/server/internal/platform"
)

// scratchQueued is scratch() plus river tables, an insert-only queue and
// a temp storage root — the shape purgePlant needs to schedule the
// deferred cleanup jobs.
func scratchQueued(t *testing.T) (context.Context, *pgxpool.Pool, *Service, Actor, string) {
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
	schema := fmt.Sprintf("t38p_%d", time.Now().UnixNano())
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
	t.Cleanup(pool.Close)
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
	s := New(pool, slog.New(slog.DiscardHandler), NoopHook{})
	s.Queue = queue
	store := t.TempDir()
	return ctx, pool, s, Actor{WorkspaceID: ws.ID, OwnerID: owner.ID, SessionID: ids.New()}, store
}

func mustReadyAsset(t *testing.T, pool *pgxpool.Pool, a Actor, purpose dbgen.AssetPurpose, store string) (dbgen.Asset, []byte) {
	t.Helper()
	ctx := context.Background()
	ids := platform.UUIDv4{}
	q := dbgen.New(pool)
	key := uuidString(ids.New())
	data := []byte("immutable-bytes-" + key)
	sha := sha256.Sum256(data)
	if err := os.WriteFile(filepath.Join(store, key), data, 0o640); err != nil {
		t.Fatal(err)
	}
	asset, err := q.InsertAsset(ctx, dbgen.InsertAssetParams{
		ID: ids.New(), WorkspaceID: a.WorkspaceID, Purpose: purpose,
		ByteCount: int32(len(data)), Sha256: hex.EncodeToString(sha[:]), StorageKey: key,
	})
	if err != nil {
		t.Fatalf("asset: %v", err)
	}
	asset, err = q.MarkAssetStateFrom(ctx, dbgen.MarkAssetStateFromParams{
		ID: asset.ID, WorkspaceID: a.WorkspaceID,
		FromState: dbgen.AssetStateRESERVED, ToState: dbgen.AssetStateREADY,
		ErrorCode: pgtype.Text{},
	})
	if err != nil {
		t.Fatalf("ready: %v", err)
	}
	return asset, data
}

func TestPurgeDeletionMatrix(t *testing.T) {
	ctx, pool, s, a, store := scratchQueued(t)
	q := dbgen.New(pool)
	ids := platform.UUIDv4{}

	// plant created under a receipt (stored response mentions the id)
	cmid := "create-1"
	p, problems, err := s.CreatePlant(ctx, a, CreatePlantInput{
		Name: "Doomed", Type: "INDIVIDUAL", Category: "OTHER",
	}, &cmid)
	if err != nil || len(problems) > 0 {
		t.Fatalf("createPlant: %v %+v", err, problems)
	}
	pid := mustParse(t, p.ID)

	// owned history: one activity (cascades)
	aid := ids.New()
	if _, err := q.InsertActivity(ctx, dbgen.InsertActivityParams{
		ID: aid, WorkspaceID: a.WorkspaceID, PlantID: pid,
		Kind: dbgen.ActivityKindWATER, OccurredAt: pgtype.Timestamptz{Time: time.Now(), Valid: true},
		Timezone: "UTC",
	}); err != nil {
		t.Fatal(err)
	}

	// financial: purchase + line + allocation to the plant
	purID, lineID, allocID := ids.New(), ids.New(), ids.New()
	if _, err := pool.Exec(ctx,
		`INSERT INTO purchases (id, workspace_id, currency, total_minor) VALUES ($1,$2,'USD',100)`,
		purID, a.WorkspaceID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO purchase_lines (id, workspace_id, purchase_id, description, quantity, line_total_minor)
		 VALUES ($1,$2,$3,'soil',1,100)`, lineID, a.WorkspaceID, purID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO purchase_allocations (id, workspace_id, purchase_line_id, plant_id, line_minor)
		 VALUES ($1,$2,$3,$4,100)`, allocID, a.WorkspaceID, lineID, pid); err != nil {
		t.Fatal(err)
	}

	// asset A: photo-only (unreferenced after purge → DELETING + job)
	assetA, _ := mustReadyAsset(t, pool, a, dbgen.AssetPurposePHOTO, store)
	// asset B: shared between this plant's photo and a purchase receipt
	assetB, _ := mustReadyAsset(t, pool, a, dbgen.AssetPurposePHOTO, store)
	for _, asset := range []dbgen.Asset{assetA, assetB} {
		if _, err := q.InsertPhoto(ctx, dbgen.InsertPhotoParams{
			ID: ids.New(), WorkspaceID: a.WorkspaceID, PlantID: pid, AssetID: asset.ID,
			TakenAt:    pgtype.Timestamptz{Time: time.Now(), Valid: true},
			DateSource: dbgen.PhotoDateSourceUSER, Viewpoint: dbgen.PhotoViewpointWHOLE,
		}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := q.InsertReceipt(ctx, dbgen.InsertReceiptParams{
		ID: ids.New(), WorkspaceID: a.WorkspaceID, PurchaseID: purID, AssetID: assetB.ID,
	}); err != nil {
		t.Fatal(err)
	}

	// a live export must not survive the purge
	exportID := ids.New()
	if _, err := pool.Exec(ctx,
		`INSERT INTO export_jobs (id, workspace_id, state, snapshot_at, expires_at)
		 VALUES ($1,$2,'QUEUED',now(),now()+interval '24 hours')`, exportID, a.WorkspaceID); err != nil {
		t.Fatal(err)
	}

	pcid := "purge-1"
	res, problems, err := s.PurgePlant(ctx, a, p.ID, 1, "PERMANENTLY_DELETE_PLANT", &pcid)
	if err != nil || len(problems) > 0 {
		t.Fatalf("purge: %v %+v", err, problems)
	}
	if res.PlantID != p.ID {
		t.Fatalf("purge result: %+v", res)
	}

	// plant and cascaded history are gone
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM plants WHERE id=$1`, pid).Scan(&n); err != nil || n != 0 {
		t.Fatalf("plant still present: %d %v", n, err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM activities WHERE id=$1`, aid).Scan(&n); err != nil || n != 0 {
		t.Fatalf("activity still present: %d %v", n, err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM plant_photos WHERE plant_id=$1`, pid).Scan(&n); err != nil || n != 0 {
		t.Fatalf("photo rows still present: %d %v", n, err)
	}

	// allocation survives as a deleted-plant marker with its amount
	var plantNull bool
	var lineMinor int
	if err := pool.QueryRow(ctx,
		`SELECT plant_id IS NULL, line_minor FROM purchase_allocations WHERE id=$1`, allocID,
	).Scan(&plantNull, &lineMinor); err != nil {
		t.Fatalf("allocation readback: %v", err)
	}
	if !plantNull || lineMinor != 100 {
		t.Fatalf("allocation must keep amounts with a null plant reference: null=%v minor=%d", plantNull, lineMinor)
	}

	// shared asset B stays READY (receipt link survives); asset A goes DELETING
	var stateA, stateB string
	if err := pool.QueryRow(ctx, `SELECT state FROM assets WHERE id=$1`, assetA.ID).Scan(&stateA); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT state FROM assets WHERE id=$1`, assetB.ID).Scan(&stateB); err != nil {
		t.Fatal(err)
	}
	if stateA != string(dbgen.AssetStateDELETING) {
		t.Fatalf("unreferenced asset: want DELETING, got %s", stateA)
	}
	if stateB != string(dbgen.AssetStateREADY) {
		t.Fatalf("receipt-shared asset must survive: got %s", stateB)
	}
	// bytes are on grace, not erased yet
	if _, err := os.Stat(filepath.Join(store, assetA.StorageKey)); err != nil {
		t.Fatalf("asset A bytes must survive the grace period: %v", err)
	}

	// river jobs: one asset purge (A only) + one export cleanup
	type job struct {
		Kind string
		Args []byte
	}
	rows, err := pool.Query(ctx, `SELECT kind, args::text FROM river_job`)
	if err != nil {
		t.Fatal(err)
	}
	var jobs []job
	for rows.Next() {
		var j job
		if err := rows.Scan(&j.Kind, &j.Args); err != nil {
			t.Fatal(err)
		}
		jobs = append(jobs, j)
	}
	rows.Close()
	var purgeJobs, cleanupJobs int
	for _, j := range jobs {
		if j.Kind == "media:purge_asset" && string(j.Args) != "" {
			purgeJobs++
		}
		if j.Kind == "export:cleanup" {
			cleanupJobs++
		}
	}
	if purgeJobs != 1 {
		t.Fatalf("want exactly one asset purge job (shared asset preserved), got %d", purgeJobs)
	}
	if cleanupJobs != 1 {
		t.Fatalf("want one export cleanup job, got %d", cleanupJobs)
	}

	// export revoked
	var expState string
	if err := pool.QueryRow(ctx, `SELECT state FROM export_jobs WHERE id=$1`, exportID).Scan(&expState); err != nil {
		t.Fatal(err)
	}
	if expState != string(dbgen.ExportStateEXPIRED) {
		t.Fatalf("live export must be revoked: got %s", expState)
	}

	// deletion ledger: plant + activity, epoch advanced
	var tombs int
	var maxEpoch int64
	if err := pool.QueryRow(ctx,
		`SELECT count(*), COALESCE(MAX(privacy_epoch),0) FROM deletion_tombstones WHERE workspace_id=$1`,
		a.WorkspaceID).Scan(&tombs, &maxEpoch); err != nil {
		t.Fatal(err)
	}
	if tombs != 2 {
		t.Fatalf("want tombstones for plant + activity, got %d", tombs)
	}
	var wsEpoch int64
	if err := pool.QueryRow(ctx, `SELECT privacy_epoch FROM workspace WHERE id=$1`, a.WorkspaceID).Scan(&wsEpoch); err != nil {
		t.Fatal(err)
	}
	if wsEpoch != maxEpoch || wsEpoch < 1 {
		t.Fatalf("workspace epoch %d must match ledger watermark %d", wsEpoch, maxEpoch)
	}

	// audit content mentioning the purged plant is redacted
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM audit_revisions WHERE workspace_id=$1 AND aggregate_id=$2 AND prior_values IS NULL`,
		a.WorkspaceID, pid).Scan(&n); err != nil || n == 0 {
		t.Fatalf("plant audit rows must be redacted: %d %v", n, err)
	}

	// replay of the createPlant receipt now answers RESOURCE_GONE
	_, problems, err = s.CreatePlant(ctx, a, CreatePlantInput{
		Name: "Doomed", Type: "INDIVIDUAL", Category: "OTHER",
	}, &cmid)
	if err != nil {
		t.Fatal(err)
	}
	if !hasCode(problems, CodeResourceGone) {
		t.Fatalf("replay after purge must be RESOURCE_GONE, got %+v", problems)
	}

	// replay of the purge itself replays its minimal success
	res2, problems, err := s.PurgePlant(ctx, a, p.ID, 1, "PERMANENTLY_DELETE_PLANT", &pcid)
	if err != nil || len(problems) > 0 {
		t.Fatalf("purge replay: %v %+v", err, problems)
	}
	if res2.PlantID != p.ID {
		t.Fatalf("purge replay result: %+v", res2)
	}
}

func TestPurgeRevisionConflictStillGuarded(t *testing.T) {
	ctx, _, s, a, _ := scratchQueued(t)
	p := mustCreatePlant(t, s, ctx, a, "Guarded")
	if _, problems, err := s.PurgePlant(ctx, a, p.ID, 99, "PERMANENTLY_DELETE_PLANT", nil); err != nil ||
		!hasCode(problems, CodeConflict) {
		t.Fatalf("stale revision must conflict: %v %+v", err, problems)
	}
}
