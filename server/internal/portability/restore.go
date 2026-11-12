package portability

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	candb "canopy/server/db"
	dbgen "canopy/server/db/generated"
	"canopy/server/internal/platform"
)

// RestoreOptions configures one restore run. Archive is the .tar or
// .tar.enc file; the external manifest is derived by trimming ".enc"
// and ".tar" and appending ".manifest.json". Ledger is optional but
// required to re-apply the deletion ledger (tombstones, epoch, receipt
// gone-flags).
type RestoreOptions struct {
	Archive string
	Ledger  string
	KeyFile string
}

// RestoreSummary reports what landed and how long it took (05: restore
// timing is measured, not assumed).
type RestoreSummary struct {
	SetID      string
	Tables     int
	Rows       int64
	MediaFiles int
	LedgerRows int64
	Reconcile  map[string]int64
	Duration   time.Duration
}

// Restore applies a backup set to a blank host only: it refuses any
// database where the app schema already exists, verifies the manifest
// checksum and every entry hash before touching anything, applies the
// schema migrations, inserts tables in FK order, restores the immutable
// media files, re-applies the deletion ledger, and only then reconciles
// counts, financial totals and file hashes.
func (s *Service) Restore(ctx context.Context, opt RestoreOptions) (RestoreSummary, error) {
	start := s.clock.Now()
	if s.DSN == "" {
		return RestoreSummary{}, errors.New("restore: service has no DSN")
	}

	// refuse a non-blank target before reading anything else
	if err := s.requireBlank(ctx); err != nil {
		return RestoreSummary{}, err
	}

	manifestPath := strings.TrimSuffix(trimEnc(opt.Archive), ".tar") + ".manifest.json"
	mraw, err := os.ReadFile(manifestPath)
	if err != nil {
		return RestoreSummary{}, fmt.Errorf("manifest: %w", err)
	}
	var m BackupManifest
	if err := json.Unmarshal(mraw, &m); err != nil {
		return RestoreSummary{}, fmt.Errorf("manifest: %w", err)
	}
	if m.SchemaVersion != SchemaVersion {
		return RestoreSummary{}, fmt.Errorf("unsupported backup schemaVersion %q", m.SchemaVersion)
	}

	raw, err := os.ReadFile(opt.Archive)
	if err != nil {
		return RestoreSummary{}, err
	}
	sum := sha256.Sum256(raw)
	if hex.EncodeToString(sum[:]) != m.PayloadSha256 {
		return RestoreSummary{}, errors.New("archive failed its manifest checksum")
	}
	tarBytes := raw
	if m.Sealed {
		if opt.KeyFile == "" {
			return RestoreSummary{}, ErrKeyRequired
		}
		key, err := LoadKey(opt.KeyFile)
		if err != nil {
			return RestoreSummary{}, err
		}
		tarBytes, err = OpenSealed(raw, key)
		if err != nil {
			return RestoreSummary{}, fmt.Errorf("unseal: %w", err)
		}
	}

	// read + verify every entry before writing anything
	type entry = archiveEntryData
	var entries []entry
	var inner BackupManifest
	tr := tar.NewReader(bytes.NewReader(tarBytes))
	seen := map[string]bool{}
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return RestoreSummary{}, fmt.Errorf("archive: %w", err)
		}
		if h.Typeflag != tar.TypeReg {
			return RestoreSummary{}, fmt.Errorf("archive entry %q is not a regular file", h.Name)
		}
		if err := SafeArchivePath(h.Name); err != nil {
			return RestoreSummary{}, err
		}
		if seen[h.Name] {
			return RestoreSummary{}, fmt.Errorf("duplicate archive entry %q", h.Name)
		}
		seen[h.Name] = true
		data, err := readAllLimited(tr, h.Size)
		if err != nil {
			return RestoreSummary{}, err
		}
		if h.Name == "manifest.json" {
			if err := json.Unmarshal(data, &inner); err != nil {
				return RestoreSummary{}, fmt.Errorf("inner manifest: %w", err)
			}
			continue
		}
		entries = append(entries, entry{name: h.Name, data: data})
	}

	if inner.BackupSet != m.BackupSet || inner.SchemaVersion != m.SchemaVersion {
		return RestoreSummary{}, errors.New("inner manifest disagrees with external manifest")
	}
	if !inner.CreatedAt.Equal(m.CreatedAt) || inner.PrivacyEpoch != m.PrivacyEpoch {
		return RestoreSummary{}, errors.New("inner manifest disagrees with external manifest")
	}
	want := map[string]ArchiveEntry{}
	for _, e := range inner.Entries {
		if err := SafeArchivePath(e.Path); err != nil {
			return RestoreSummary{}, err
		}
		want[e.Path] = e
	}
	for _, e := range entries {
		exp, ok := want[e.name]
		if !ok {
			return RestoreSummary{}, fmt.Errorf("archive entry %q is not in the manifest", e.name)
		}
		esum := sha256.Sum256(e.data)
		if hex.EncodeToString(esum[:]) != exp.SHA256 || int64(len(e.data)) != exp.Bytes {
			return RestoreSummary{}, fmt.Errorf("archive entry %q failed its checksum", e.name)
		}
		delete(want, e.name)
	}
	if len(want) > 0 {
		return RestoreSummary{}, fmt.Errorf("archive is missing %d manifest entries", len(want))
	}

	// schema first: goose migrations + river's own tables
	sqlDB, err := sql.Open("pgx", s.DSN)
	if err != nil {
		return RestoreSummary{}, err
	}
	defer sqlDB.Close()
	goose.SetBaseFS(candb.Migrations)
	if err := goose.SetDialect("postgres"); err != nil {
		return RestoreSummary{}, err
	}
	if err := goose.RunContext(ctx, "up", sqlDB, "migrations"); err != nil {
		return RestoreSummary{}, fmt.Errorf("migrate: %w", err)
	}
	if err := platform.MigrateRiver(ctx, s.Pool); err != nil {
		return RestoreSummary{}, fmt.Errorf("river migrate: %w", err)
	}

	// data in FK order
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return RestoreSummary{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var rowsLoaded int64
	tablesLoaded := 0
	for _, name := range dumpTables {
		blob := findEntry(entries, "db/"+name+".jsonl")
		// Owner triggers bootstrap legacy archives; new archives restore exact users.
		if name == "auth_users" && blob != nil {
			if _, err := tx.Exec(ctx, "DELETE FROM auth_users"); err != nil {
				return RestoreSummary{}, err
			}
		}
		n, err := insertJSONL(ctx, tx, name, blob)
		if err != nil {
			return RestoreSummary{}, fmt.Errorf("restore %s: %w", name, err)
		}
		rowsLoaded += n
		tablesLoaded++
	}
	if blob := findEntry(entries, "river/river_job.jsonl"); blob != nil {
		if _, err := insertJSONL(ctx, tx, "river_job", blob); err != nil {
			return RestoreSummary{}, fmt.Errorf("restore river_job: %w", err)
		}
		// a job that was mid-flight when the backup ran retries on the
		// restored host instead of staying stuck 'running'
		if _, err := tx.Exec(ctx,
			`UPDATE river_job SET state = 'available' WHERE state = 'running'`); err != nil {
			return RestoreSummary{}, fmt.Errorf("reset running jobs: %w", err)
		}
	}
	// Verify the restored hostname and encryption key before enabling SSO.
	if _, err := tx.Exec(ctx, "UPDATE oidc_connections SET enabled=false,revision=revision+1"); err != nil {
		return RestoreSummary{}, err
	}
	// Restored installations never reactivate copied agent bearer credentials.
	if _, err := tx.Exec(ctx, `UPDATE agent_grants SET revoked_at=COALESCE(revoked_at,now()); UPDATE agent_credentials SET revoked_at=COALESCE(revoked_at,now()); UPDATE agent_actions SET state='REVOKED' WHERE state='PENDING'`); err != nil {
		return RestoreSummary{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return RestoreSummary{}, err
	}

	// media files (keys already validated by SafeArchivePath)
	mediaCount := 0
	if err := os.MkdirAll(s.StoreRoot, 0o750); err != nil {
		return RestoreSummary{}, err
	}
	for _, e := range entries {
		if len(e.name) <= len("media/") || e.name[:len("media/")] != "media/" {
			continue
		}
		key := e.name[len("media/"):]
		if err := writeFileFsync(filepath.Join(s.StoreRoot, key), e.data); err != nil {
			return RestoreSummary{}, err
		}
		mediaCount++
	}

	// deletion ledger: tombstones + epoch + receipt gone-flags, then
	// every session revoked (03: restore re-applies the ledger before
	// access reopens)
	ledgerRows := int64(0)
	if opt.Ledger != "" {
		n, err := s.applyLedger(ctx, opt.Ledger, opt.KeyFile)
		if err != nil {
			return RestoreSummary{}, fmt.Errorf("ledger: %w", err)
		}
		ledgerRows = n
	}
	if _, err := s.Pool.Exec(ctx, `UPDATE sessions SET revoked_at = now() WHERE revoked_at IS NULL`); err != nil {
		return RestoreSummary{}, fmt.Errorf("revoke sessions: %w", err)
	}

	// reconciliation: counts, financial anchors, on-disk file hashes
	recon, err := s.reconcile(ctx, &inner)
	if err != nil {
		return RestoreSummary{}, fmt.Errorf("reconciliation failed: %w", err)
	}

	return RestoreSummary{
		SetID: m.BackupSet, Tables: tablesLoaded, Rows: rowsLoaded,
		MediaFiles: mediaCount, LedgerRows: ledgerRows,
		Reconcile: recon, Duration: s.clock.Now().Sub(start),
	}, nil
}

// requireBlank refuses to apply an archive onto a database that already
// carries the app schema — restores never merge, they land on fresh
// hosts only.
func (s *Service) requireBlank(ctx context.Context) error {
	var found string
	err := s.Pool.QueryRow(ctx,
		`SELECT COALESCE(to_regclass('plants')::text, '') ||
		        COALESCE(to_regclass('workspace')::text, '')`).Scan(&found)
	if err != nil {
		return err
	}
	if found != "" {
		return fmt.Errorf("refusing to apply archive to a non-blank database (found %s)", found)
	}
	return nil
}

// insertJSONL bulk-inserts one dumped table. The table name comes only
// from the package whitelist; rows arrive as JSON and are cast through
// jsonb_populate_record so enums, numerics and timestamps go through
// Postgres' own input functions.
func insertJSONL(ctx context.Context, tx pgx.Tx, table string, blob []byte) (int64, error) {
	if !validDumpTable(table) {
		return 0, fmt.Errorf("table %q is not in the restore whitelist", table)
	}
	if len(blob) == 0 {
		return 0, nil
	}
	var arr []json.RawMessage
	for _, line := range splitLines(blob) {
		arr = append(arr, line)
	}
	if len(arr) == 0 {
		return 0, nil
	}
	blobJSON, err := json.Marshal(arr)
	if err != nil {
		return 0, err
	}
	tag, err := tx.Exec(ctx,
		`INSERT INTO `+table+` SELECT (jsonb_populate_record(NULL::"`+table+`", r)).*
		 FROM jsonb_array_elements($1::jsonb) AS r`, blobJSON)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

func splitLines(b []byte) [][]byte {
	var out [][]byte
	start := 0
	for i := 0; i < len(b); i++ {
		if b[i] == '\n' {
			if i > start {
				out = append(out, b[start:i])
			}
			start = i + 1
		}
	}
	if start < len(b) {
		out = append(out, b[start:])
	}
	return out
}

// archiveEntryData is one verified payload entry from the archive.
type archiveEntryData struct {
	name string
	data []byte
}

func findEntry(entries []archiveEntryData, name string) []byte {
	for _, e := range entries {
		if e.name == name {
			return e.data
		}
	}
	return nil
}

func trimEnc(p string) string {
	if len(p) >= 4 && p[len(p)-4:] == ".enc" {
		return p[:len(p)-4]
	}
	return p
}

// pgxQueryer accepts both pgx.Tx and *pgxpool.Pool.
type pgxQueryer interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

func readAllLimited(r io.Reader, size int64) ([]byte, error) {
	const cap = int64(512 << 20)
	if size < 0 || size > cap {
		return nil, fmt.Errorf("archive entry size %d out of range", size)
	}
	buf, err := io.ReadAll(io.LimitReader(r, size))
	if err != nil {
		return nil, err
	}
	if int64(len(buf)) != size {
		return nil, fmt.Errorf("archive entry truncated: want %d bytes, got %d", size, len(buf))
	}
	return buf, nil
}

// applyLedger re-inserts tombstones, raises each workspace's privacy
// epoch to at least the ledger watermark, and re-marks receipts whose
// stored response mentions a purged id — so a purge that happened after
// the archive was taken still replays as RESOURCE_GONE on the restored
// host.
func (s *Service) applyLedger(ctx context.Context, path, keyFile string) (int64, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	if IsSealed(raw) {
		if keyFile == "" {
			return 0, ErrKeyRequired
		}
		key, err := LoadKey(keyFile)
		if err != nil {
			return 0, err
		}
		raw, err = OpenSealed(raw, key)
		if err != nil {
			return 0, err
		}
	}
	type tomb struct {
		ID           string    `json:"id"`
		WorkspaceID  string    `json:"workspace_id"`
		EntityKind   string    `json:"entity_kind"`
		EntityID     string    `json:"entity_id"`
		Action       string    `json:"action"`
		ActorID      *string   `json:"actor_id"`
		OccurredAt   time.Time `json:"occurred_at"`
		PrivacyEpoch int64     `json:"privacy_epoch"`
	}
	var tombs []tomb
	for _, line := range splitLines(raw) {
		var t tomb
		if err := json.Unmarshal(line, &t); err != nil {
			return 0, fmt.Errorf("ledger row: %w", err)
		}
		tombs = append(tombs, t)
	}

	q := dbgen.New(s.Pool)
	perWS := map[string][]string{}
	maxEpoch := map[string]int64{}
	for _, t := range tombs {
		id, ok1 := parseUUID(t.ID)
		ws, ok2 := parseUUID(t.WorkspaceID)
		ent, ok3 := parseUUID(t.EntityID)
		if !ok1 || !ok2 || !ok3 {
			return 0, fmt.Errorf("ledger row %s has malformed ids", t.ID)
		}
		actor := pgtype.UUID{}
		if t.ActorID != nil {
			if a, ok := parseUUID(*t.ActorID); ok {
				actor = a
			}
		}
		if _, err := q.InsertTombstone(ctx, dbgen.InsertTombstoneParams{
			ID: id, WorkspaceID: ws, EntityKind: t.EntityKind, EntityID: ent,
			Action: t.Action, ActorID: actor,
			OccurredAt:   ts(t.OccurredAt),
			PrivacyEpoch: t.PrivacyEpoch,
		}); err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return 0, err // ErrNoRows: the ON CONFLICT DO NOTHING of an already-dumped tombstone
		}
		perWS[t.WorkspaceID] = append(perWS[t.WorkspaceID], t.EntityID)
		if t.PrivacyEpoch > maxEpoch[t.WorkspaceID] {
			maxEpoch[t.WorkspaceID] = t.PrivacyEpoch
		}
	}
	for wsStr, epoch := range maxEpoch {
		ws, _ := parseUUID(wsStr)
		if _, err := s.Pool.Exec(ctx,
			`UPDATE workspace SET privacy_epoch = GREATEST(privacy_epoch, $1) WHERE id = $2`,
			epoch, ws); err != nil {
			return 0, err
		}
	}
	for wsStr, refs := range perWS {
		ws, _ := parseUUID(wsStr)
		if err := q.TombstoneReceiptsTouching(ctx, dbgen.TombstoneReceiptsTouchingParams{
			WorkspaceID: ws, Refs: refs,
		}); err != nil {
			return 0, err
		}
	}
	return int64(len(tombs)), nil
}

// reconcile re-counts every table, re-checks the financial anchors and
// re-hashes the restored media files against the manifest. Any drift
// fails the restore loudly.
func (s *Service) reconcile(ctx context.Context, m *BackupManifest) (map[string]int64, error) {
	out := map[string]int64{}
	for _, name := range append(append([]string{}, dumpTables...), "river_job") {
		var n int64
		if err := s.Pool.QueryRow(ctx, "SELECT count(*) FROM "+name).Scan(&n); err != nil {
			return nil, err
		}
		if n != m.TableCounts[name] {
			return nil, fmt.Errorf("table %s: restored %d rows, manifest says %d",
				name, n, m.TableCounts[name])
		}
		out[name] = n
	}
	fin, err := finTotalsAll(ctx, s.Pool)
	if err != nil {
		return nil, err
	}
	if len(fin) != len(m.FinancialTotals) {
		return nil, fmt.Errorf("financial totals: restored %d currencies, manifest has %d",
			len(fin), len(m.FinancialTotals))
	}
	for i := range fin {
		if fin[i] != m.FinancialTotals[i] {
			return nil, fmt.Errorf("financial totals differ for %s: restored %d, manifest %d",
				fin[i].Currency, fin[i].TotalMinor, m.FinancialTotals[i].TotalMinor)
		}
	}
	var alloc int64
	if err := s.Pool.QueryRow(ctx,
		`SELECT COALESCE(SUM(line_minor + tax_minor + shipping_minor), 0) FROM purchase_allocations`,
	).Scan(&alloc); err != nil {
		return nil, err
	}
	if alloc != m.AllocatedMinor {
		return nil, fmt.Errorf("allocation totals differ: restored %d, manifest %d", alloc, m.AllocatedMinor)
	}
	out["financialCurrencyCount"] = int64(len(fin))
	out["allocatedMinor"] = alloc

	for _, e := range m.Entries {
		if len(e.Path) <= len("media/") || e.Path[:len("media/")] != "media/" {
			continue
		}
		key := e.Path[len("media/"):]
		f, err := os.Open(filepath.Join(s.StoreRoot, key))
		if err != nil {
			return nil, fmt.Errorf("restored media file %s missing: %w", key, err)
		}
		h := sha256.New()
		if _, err := io.Copy(h, f); err != nil {
			f.Close()
			return nil, err
		}
		f.Close()
		if hex.EncodeToString(h.Sum(nil)) != e.SHA256 {
			return nil, fmt.Errorf("restored media file %s failed its checksum", key)
		}
	}
	out["mediaFilesVerified"] = countMedia(m)
	return out, nil
}

func countMedia(m *BackupManifest) int64 {
	var n int64
	for _, e := range m.Entries {
		if len(e.Path) > len("media/") && e.Path[:len("media/")] == "media/" {
			n++
		}
	}
	return n
}
