package portability

import (
	"archive/tar"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"canopy/server/internal/platform"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/riverqueue/river"
)

var plannerTables = []string{"planting_plans", "planting_actions", "planting_guidance_snapshots", "planting_fulfillments"}

func seedPlanner(t *testing.T, e *env) (pgtype.UUID, pgtype.UUID) {
	t.Helper()
	ids := platform.UUIDv4{}
	plan, idea, action, snapshot := ids.New(), ids.New(), ids.New(), ids.New()
	plant := e.mustPlant(t, "Acquired tree with future planting work")
	purgedPlant := e.mustPlant(t, "Will be purged")
	location := ids.New()
	if _, err := e.pool.Exec(e.ctx, `INSERT INTO locations(id,workspace_id,name,situation) VALUES($1,$2,'Future bed','OUTDOOR')`, location, e.ws); err != nil {
		t.Fatal(err)
	}
	if _, err := e.pool.Exec(e.ctx, `INSERT INTO planting_plans(id,workspace_id,name,category,desired_quantity,method,intended_location_id,intended_placement,target_precision,target_from_month,target_to_month,budget_minor,budget_currency,state,guidance_generation,notes) VALUES($1,$2,'Future fig','FIG',2,'NURSERY_PLANT',$3,'GROUND','MONTH_RANGE','2027-12','2028-02',15000,'USD','IN_PROGRESS',2,'Keep selected window')`, plan, e.ws, location); err != nil {
		t.Fatal(err)
	}
	if _, err := e.pool.Exec(e.ctx, `INSERT INTO planting_plans(id,workspace_id,name,budget_minor,budget_currency) VALUES($1,$2,'Unscheduled idea',0,'EUR')`, idea, e.ws); err != nil {
		t.Fatal(err)
	}
	if _, err := e.pool.Exec(e.ctx, `INSERT INTO planting_actions(id,workspace_id,plan_id,kind,title,scheduled_on,reminder_on,schedule_revision) VALUES($1,$2,$3,'PLANT_OUT','Plant outside','2028-01-02','2027-12-30',4)`, action, e.ws, plan); err != nil {
		t.Fatal(err)
	}
	source, err := os.ReadFile("../../../contracts/fixtures/api/planning-annual-guidance.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Data struct {
			Plan struct {
				Guidance struct {
					Facts json.RawMessage `json:"facts"`
				} `json:"latestGuidance"`
			} `json:"plantingPlan"`
		} `json:"data"`
	}
	if err = json.Unmarshal(source, &fixture); err != nil {
		t.Fatal(err)
	}
	if _, err = e.pool.Exec(e.ctx, `INSERT INTO planting_guidance_snapshots(id,workspace_id,plan_id,generation,context_sha256,state,facts,retrieved_at) VALUES($1,$2,$3,1,repeat('a',64),'READY',$4,'2026-11-14T12:00:00Z')`, snapshot, e.ws, plan, fixture.Data.Plan.Guidance.Facts); err != nil {
		t.Fatal(err)
	}
	if _, err = e.pool.Exec(e.ctx, `INSERT INTO planting_guidance_snapshots(id,workspace_id,plan_id,generation,context_sha256,state,message) VALUES($1,$2,$3,2,repeat('b',64),'FAILED','Fixture provider unavailable')`, ids.New(), e.ws, plan); err != nil {
		t.Fatal(err)
	}
	if _, err = e.pool.Exec(e.ctx, `UPDATE planting_plans SET accepted_guidance_id=$1 WHERE id=$2`, snapshot, plan); err != nil {
		t.Fatal(err)
	}
	if _, err = e.pool.Exec(e.ctx, `INSERT INTO planting_fulfillments(id,workspace_id,plan_id,plant_id,action_id,mode) VALUES($1,$2,$3,$4,$5,'EXISTING')`, ids.New(), e.ws, plan, plant, action); err != nil {
		t.Fatal(err)
	}
	if _, err = e.pool.Exec(e.ctx, `INSERT INTO planting_fulfillments(id,workspace_id,plan_id,plant_id,mode) VALUES($1,$2,$3,$4,'CREATED')`, ids.New(), e.ws, idea, purgedPlant); err != nil {
		t.Fatal(err)
	}
	if _, err = e.pool.Exec(e.ctx, `DELETE FROM plants WHERE id=$1`, purgedPlant); err != nil {
		t.Fatal(err)
	}
	return plan, idea
}
func plannerRows(t *testing.T, e *env, table string) []byte {
	t.Helper()
	var data []byte
	if err := e.pool.QueryRow(e.ctx, "SELECT COALESCE(jsonb_agg(to_jsonb(t) ORDER BY id),'[]'::jsonb) FROM "+table+" t").Scan(&data); err != nil {
		t.Fatal(err)
	}
	return data
}
func TestPlanningPopulatedBackupRestorePreservesCycleAndPurgeMarker(t *testing.T) {
	e := newEnv(t)
	plan, idea := seedPlanner(t, e)
	before := map[string][]byte{}
	for _, table := range plannerTables {
		before[table] = plannerRows(t, e, table)
	}
	sum, err := e.svc.Backup(e.ctx, BackupOptions{Dest: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	entries := readTar(t, sum.Archive)
	for _, table := range plannerTables {
		if len(entries["db/"+table+".jsonl"]) == 0 {
			t.Fatal("missing planner backup", table)
		}
	}
	b := blankEnv(t)
	restored, err := b.svc.Restore(b.ctx, RestoreOptions{Archive: sum.Archive, Ledger: sum.Ledger})
	if err != nil {
		t.Fatal("populated planner restore", err)
	}
	for _, table := range plannerTables {
		after := plannerRows(t, b, table)
		if !bytes.Equal(before[table], after) {
			t.Fatalf("planner round-trip changed %s\nbefore %s\nafter %s", table, before[table], after)
		}
		if restored.Reconcile[table] == 0 {
			t.Fatal("planner not reconciled", table)
		}
	}
	var state, kind string
	var linkedPlant *string
	var targetMonth string
	if err = b.pool.QueryRow(b.ctx, `SELECT p.state::text,a.state::text,p.target_from_month FROM planting_plans p JOIN planting_actions a ON a.plan_id=p.id WHERE p.id=$1`, plan).Scan(&state, &kind, &targetMonth); err != nil || state != "IN_PROGRESS" || kind != "OPEN" || targetMonth != "2027-12" {
		t.Fatal("ownership completed future work", state, kind, targetMonth, err)
	}
	if err = b.pool.QueryRow(b.ctx, `SELECT plant_id::text FROM planting_fulfillments WHERE plan_id=$1`, idea).Scan(&linkedPlant); err != nil || linkedPlant != nil {
		t.Fatal("purge marker resurrected plant", linkedPlant, err)
	}
}
func TestPlanningExportIsScopedAndIncludesGuidanceAndLinks(t *testing.T) {
	e := newEnv(t)
	seedPlanner(t, e)
	otherOwner, otherWS, otherPlan := platform.UUIDv4{}.New(), platform.UUIDv4{}.New(), platform.UUIDv4{}.New()
	if _, err := e.pool.Exec(e.ctx, `INSERT INTO owner(id,password_hash) VALUES($1,'fixture')`, otherOwner); err != nil {
		t.Fatal(err)
	}
	if _, err := e.pool.Exec(e.ctx, `INSERT INTO workspace(id,owner_id,display_name,timezone,default_currency) VALUES($1,$2,'Other','UTC','USD')`, otherWS, otherOwner); err != nil {
		t.Fatal(err)
	}
	if _, err := e.pool.Exec(e.ctx, `INSERT INTO planting_plans(id,workspace_id,name) VALUES($1,$2,'Private other plan')`, otherPlan, otherWS); err != nil {
		t.Fatal(err)
	}
	job, err := e.svc.RequestExport(e.ctx, e.ws)
	if err != nil {
		t.Fatal(err)
	}
	if err = (&ExportBuildWorker{Svc: e.svc}).Work(e.ctx, &river.Job[ExportBuildArgs]{Args: ExportBuildArgs{ExportID: uuidString(job.ID)}}); err != nil {
		t.Fatal(err)
	}
	path, err := ExportArchivePath(e.svc.ExportsDir(), uuidString(job.ID))
	if err != nil {
		t.Fatal(err)
	}
	entries := readTar(t, path)
	var manifest ExportManifest
	if err = json.Unmarshal(entries["manifest.json"], &manifest); err != nil {
		t.Fatal(err)
	}
	for table, want := range map[string]int64{"planting_plans": 2, "planting_actions": 1, "planting_guidance_snapshots": 2, "planting_fulfillments": 2} {
		if manifest.Counts[table] != want {
			t.Fatal("export planner count", table, manifest.Counts[table])
		}
		body := entries["data/"+table+".jsonl"]
		if len(body) == 0 || bytes.Contains(body, []byte("Private other plan")) || bytes.Contains(body, []byte(uuidString(otherWS))) {
			t.Fatal("missing or unscoped export", table)
		}
	}
	if !bytes.Contains(entries["data/planting_guidance_snapshots.jsonl"], []byte("https://example.invalid/planting-fixture")) {
		t.Fatal("citations missing from export")
	}
	if !bytes.Contains(entries["data/planting_fulfillments.jsonl"], []byte(`"plant_id": null`)) {
		t.Fatal("purge marker missing")
	}
}
func TestPlanningRestoreAcceptsLegacyArchiveWithoutPlannerTables(t *testing.T) {
	e := newEnv(t)
	e.mustPlant(t, "Existing garden from before planning")
	sum, err := e.svc.Backup(e.ctx, BackupOptions{Dest: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	entries := readTar(t, sum.Archive)
	var inner BackupManifest
	if err = json.Unmarshal(entries["manifest.json"], &inner); err != nil {
		t.Fatal(err)
	}
	// Construct the exact old table set with fresh valid checksums. No populated
	// planning data is dropped; the source has no plans in this compatibility test.
	for _, table := range plannerTables {
		delete(entries, "db/"+table+".jsonl")
		delete(inner.TableCounts, table)
	}
	kept := make([]ArchiveEntry, 0, len(inner.Entries))
	for _, entry := range inner.Entries {
		if _, ok := entries[entry.Path]; ok {
			kept = append(kept, entry)
		}
	}
	inner.Entries = kept
	encoded, err := json.Marshal(inner)
	if err != nil {
		t.Fatal(err)
	}
	entries["manifest.json"] = encoded
	var archive bytes.Buffer
	tw := tar.NewWriter(&archive)
	names := make([]string, 0, len(entries))
	for name := range entries {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		body := entries[name]
		if err = tw.WriteHeader(&tar.Header{Name: name, Mode: 0o640, Size: int64(len(body)), Format: tar.FormatPAX}); err != nil {
			t.Fatal(err)
		}
		if _, err = tw.Write(body); err != nil {
			t.Fatal(err)
		}
	}
	if err = tw.Close(); err != nil {
		t.Fatal(err)
	}
	outer := inner
	outer.PayloadSha256 = shaHex(archive.Bytes())
	outerBytes, err := json.Marshal(outer)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "legacy.tar")
	if err = os.WriteFile(path, archive.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(strings.TrimSuffix(path, ".tar")+".manifest.json", outerBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	b := blankEnv(t)
	result, err := b.svc.Restore(b.ctx, RestoreOptions{Archive: path, Ledger: sum.Ledger})
	if err != nil {
		t.Fatal("legacy archive rejected", err)
	}
	if result.Reconcile["plants"] != 1 {
		t.Fatal("legacy actual garden lost")
	}
	for _, table := range plannerTables {
		if result.Reconcile[table] != 0 || string(plannerRows(t, b, table)) != "[]" {
			t.Fatal("old archive invented plans", table)
		}
	}
}
