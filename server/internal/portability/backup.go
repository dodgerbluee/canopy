package portability

import (
	"archive/tar"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river/riverdriver"
)

// dumpTables is the whitelist of tables a backup copies, listed in
// FK-dependency order so a restore can insert them straight through.
// sessions and export_jobs are deliberately absent: sessions are
// invalidated on restore and export archives are not part of a backup
// set (re-request after restore).
var dumpTables = []string{
	"owner", "auth_users", "oidc_connections", "oidc_identities", "workspace", "agent_grants", "agent_credentials", "agent_operations", "agent_actions", "locations", "tags", "plants", "plant_contexts",
	"ai_connections", "ai_feature_bindings", "garden_knowledge_settings", "knowledge_profiles", "rain_events", "plant_tags", "activities", "measurements", "purchases", "purchase_lines",
	"purchase_allocations", "acquisitions", "assets", "asset_variants",
	"plant_photos", "purchase_receipts", "journal_entries", "audit_revisions",
	"mutation_receipts", "deletion_tombstones",
}

// BackupOptions configures one backup run.
type BackupOptions struct {
	Dest    string // directory the set files are written into
	KeyFile string // optional operator key (held outside the archive)
}

// BackupSummary is what the operator sees on stdout.
type BackupSummary struct {
	SetID      string
	Archive    string
	Ledger     string
	Manifest   string
	Tables     int
	Rows       int64
	MediaFiles int
	Sealed     bool
	Duration   time.Duration
}

// Backup produces one consistent set: a checksum manifest, a tar of
// every table (JSONL) plus every immutable media file the snapshot
// references, and the deletion ledger retained as a separate file. It
// quiesces the stack first (maintenance flag + queue pause + drain) and
// always unquiesces afterwards, even when the dump fails — a failed
// backup must never leave the stack locked.
func (s *Service) Backup(ctx context.Context, opt BackupOptions) (BackupSummary, error) {
	start := s.clock.Now()
	var key []byte
	if opt.KeyFile != "" {
		k, err := LoadKey(opt.KeyFile)
		if err != nil {
			return BackupSummary{}, err
		}
		key = k
	}
	if err := os.MkdirAll(opt.Dest, 0o750); err != nil {
		return BackupSummary{}, err
	}

	// quiesce: refuse new writes/uploads, stop claiming jobs, drain the
	// running ones so the snapshot is consistent.
	if err := SetMaintenance(ctx, s.Pool, true); err != nil {
		return BackupSummary{}, fmt.Errorf("quiesce: %w", err)
	}
	paused := false
	if s.Queue != nil {
		// "*" is river's all-queues name: it pauses every registered
		// queue and, unlike a named queue that was never started, can
		// never fail with ErrNotFound on a stack whose worker is down.
		if err := s.Queue.QueuePause(ctx, riverdriver.AllQueuesString, nil); err != nil {
			_ = SetMaintenance(ctx, s.Pool, false)
			return BackupSummary{}, fmt.Errorf("pause queue: %w", err)
		}
		paused = true
	}
	unquiesce := func() {
		if paused {
			if err := s.Queue.QueueResume(ctx, riverdriver.AllQueuesString, nil); err != nil {
				s.Log.Error("backup: queue resume failed", "err", err)
			}
		}
		if err := SetMaintenance(ctx, s.Pool, false); err != nil {
			s.Log.Error("backup: unquiesce failed", "err", err)
		}
	}
	defer unquiesce()

	if err := s.drainJobs(ctx); err != nil {
		return BackupSummary{}, err
	}

	setID := uuidString(s.uuid.New())

	// one REPEATABLE READ snapshot for the whole dump
	conn, err := s.Pool.Acquire(ctx)
	if err != nil {
		return BackupSummary{}, err
	}
	defer conn.Release()
	tx, err := conn.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	if err != nil {
		return BackupSummary{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var entries []ArchiveEntry
	var totalRows int64
	tableCounts := map[string]int64{}

	// collect table dumps
	type tbl struct {
		name string
		blob []byte
	}
	var tables []tbl
	for _, name := range append(append([]string{}, dumpTables...), "river_job") {
		if name == "river_job" {
			var ok bool
			if err := tx.QueryRow(ctx,
				"SELECT to_regclass('river_job') IS NOT NULL").Scan(&ok); err != nil {
				return BackupSummary{}, err
			}
			if !ok {
				tableCounts[name] = 0
				continue
			}
		}
		rows, err := dumpQuery(ctx, tx, name)
		if err != nil {
			return BackupSummary{}, err
		}
		tableCounts[name] = int64(len(rows))
		totalRows += int64(len(rows))
		tables = append(tables, tbl{name: name, blob: jsonl(rows)})
	}

	// reconciliation anchors
	fin, err := finTotalsAll(ctx, tx)
	if err != nil {
		return BackupSummary{}, err
	}
	var allocTotal int64
	if err := tx.QueryRow(ctx,
		`SELECT COALESCE(SUM(line_minor + tax_minor + shipping_minor), 0) FROM purchase_allocations`,
	).Scan(&allocTotal); err != nil {
		return BackupSummary{}, err
	}
	var epoch int64
	if err := tx.QueryRow(ctx, `SELECT COALESCE(MAX(privacy_epoch), 0) FROM workspace`).Scan(&epoch); err != nil {
		return BackupSummary{}, err
	}

	// deletion ledger (retained separately, re-applied on restore)
	ledgerRows, err := dumpQuery(ctx, tx, "deletion_tombstones")
	if err != nil {
		return BackupSummary{}, err
	}

	// every media object the snapshot references; a referenced file
	// missing from the store is an honest failure, not a silent skip
	var mediaKeys []string
	mrows, err := tx.Query(ctx,
		`SELECT storage_key FROM assets
		 UNION
		 SELECT storage_key FROM asset_variants
		 ORDER BY 1`)
	if err != nil {
		return BackupSummary{}, err
	}
	for mrows.Next() {
		var k string
		if err := mrows.Scan(&k); err != nil {
			mrows.Close()
			return BackupSummary{}, err
		}
		mediaKeys = append(mediaKeys, k)
	}
	mrows.Close()
	if mrows.Err() != nil {
		return BackupSummary{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return BackupSummary{}, err
	}

	// write the archive
	plain := filepath.Join(opt.Dest, "."+setID+".tar.part")
	f, err := os.Create(plain)
	if err != nil {
		return BackupSummary{}, err
	}
	published := false
	defer func() {
		_ = f.Close()
		if !published {
			_ = os.Remove(plain)
		}
	}()
	tw := tar.NewWriter(f)

	add := func(name string, data []byte) error {
		if err := SafeArchivePath(name); err != nil {
			return err
		}
		sum := sha256.Sum256(data)
		entries = append(entries, ArchiveEntry{Path: name, SHA256: hex.EncodeToString(sum[:]), Bytes: int64(len(data))})
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o640, Size: int64(len(data)), Format: tar.FormatPAX}); err != nil {
			return err
		}
		_, err := tw.Write(data)
		return err
	}

	for _, t := range tables {
		if t.name == "river_job" {
			if err := add("river/river_job.jsonl", t.blob); err != nil {
				return BackupSummary{}, err
			}
			continue
		}
		if err := add("db/"+t.name+".jsonl", t.blob); err != nil {
			return BackupSummary{}, err
		}
	}
	mediaCount := 0
	for _, k := range mediaKeys {
		if !uuidRe.MatchString(k) {
			return BackupSummary{}, fmt.Errorf("storage key %q is not a UUID", k)
		}
		data, err := os.ReadFile(filepath.Join(s.StoreRoot, k))
		if err != nil {
			return BackupSummary{}, fmt.Errorf("referenced media file %s missing: %w", k, err)
		}
		if err := add("media/"+k, data); err != nil {
			return BackupSummary{}, err
		}
		mediaCount++
	}

	m := BackupManifest{
		SchemaVersion:   SchemaVersion,
		BackupSet:       setID,
		CreatedAt:       s.clock.Now().UTC(),
		PrivacyEpoch:    epoch,
		Sealed:          key != nil,
		TableCounts:     tableCounts,
		FinancialTotals: fin,
		AllocatedMinor:  allocTotal,
		Tombstones:      int64(len(ledgerRows)),
		Entries:         entries,
		Exclusions:      BackupExclusions,
		BackupDuration:  s.clock.Now().Sub(start).String(),
	}
	mj, err := json.Marshal(m)
	if err != nil {
		return BackupSummary{}, err
	}
	if err := add("manifest.json", mj); err != nil {
		return BackupSummary{}, err
	}
	if err := tw.Close(); err != nil {
		return BackupSummary{}, err
	}
	if err := f.Sync(); err != nil {
		return BackupSummary{}, err
	}
	_ = f.Close()

	// seal (optional) and publish under final names
	tarBytes, err := os.ReadFile(plain)
	if err != nil {
		return BackupSummary{}, err
	}
	payload := tarBytes
	archiveName := setID + ".tar"
	if key != nil {
		payload, err = Seal(tarBytes, key)
		if err != nil {
			return BackupSummary{}, err
		}
		archiveName = setID + ".tar.enc"
	}
	sum := sha256.Sum256(payload)
	m.PayloadSha256 = hex.EncodeToString(sum[:])
	mj, err = json.Marshal(m)
	if err != nil {
		return BackupSummary{}, err
	}

	archivePath := filepath.Join(opt.Dest, archiveName)
	if err := writeFileFsync(archivePath, payload); err != nil {
		return BackupSummary{}, err
	}
	ledgerName := setID + ".ledger"
	ledgerBlob := jsonl(ledgerRows)
	if key != nil {
		ledgerBlob, err = Seal(ledgerBlob, key)
		if err != nil {
			return BackupSummary{}, err
		}
		ledgerName = setID + ".ledger.enc"
	}
	ledgerPath := filepath.Join(opt.Dest, ledgerName)
	if err := writeFileFsync(ledgerPath, ledgerBlob); err != nil {
		return BackupSummary{}, err
	}
	manifestPath := filepath.Join(opt.Dest, setID+".manifest.json")
	if err := writeFileFsync(manifestPath, mj); err != nil {
		return BackupSummary{}, err
	}
	published = true
	_ = os.Remove(plain)

	return BackupSummary{
		SetID: setID, Archive: archivePath, Ledger: ledgerPath, Manifest: manifestPath,
		Tables: len(tableCounts), Rows: totalRows, MediaFiles: mediaCount,
		Sealed: key != nil, Duration: s.clock.Now().Sub(start),
	}, nil
}

// drainJobs waits for river jobs in 'running' state to finish so the
// snapshot cannot split a half-applied job.
func (s *Service) drainJobs(ctx context.Context) error {
	deadline := s.clock.Now().Add(DrainTimeout)
	for {
		var ok bool
		if err := s.Pool.QueryRow(ctx, "SELECT to_regclass('river_job') IS NOT NULL").Scan(&ok); err != nil {
			return err
		}
		if !ok {
			return nil
		}
		var n int64
		if err := s.Pool.QueryRow(ctx, "SELECT count(*) FROM river_job WHERE state = 'running'").Scan(&n); err != nil {
			return err
		}
		if n == 0 {
			return nil
		}
		if s.clock.Now().After(deadline) {
			return fmt.Errorf("drain: %d jobs still running after %s", n, DrainTimeout)
		}
		s.Log.Info("backup: draining jobs", "running", n)
		if err := sleepCtx(ctx, 500*time.Millisecond); err != nil {
			return err
		}
	}
}

// dumpQuery copies one whitelisted table into JSON rows. The table name
// comes only from the package whitelist, never from input.
func dumpQuery(ctx context.Context, tx pgx.Tx, table string) ([][]byte, error) {
	if !validDumpTable(table) {
		return nil, fmt.Errorf("table %q is not in the backup whitelist", table)
	}
	rows, err := tx.Query(ctx, "SELECT to_jsonb(t) FROM "+table+" t")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out [][]byte
	for rows.Next() {
		var b []byte
		if err := rows.Scan(&b); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

func validDumpTable(name string) bool {
	for _, t := range append(append([]string{}, dumpTables...), "river_job") {
		if t == name {
			return true
		}
	}
	return false
}

func finTotalsAll(ctx context.Context, q pgxQueryer) ([]FinTotal, error) {
	rows, err := q.Query(ctx,
		`SELECT currency, SUM(total_minor)::bigint FROM purchases
		 WHERE voided_at IS NULL GROUP BY currency ORDER BY currency`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []FinTotal
	for rows.Next() {
		var f FinTotal
		if err := rows.Scan(&f.Currency, &f.TotalMinor); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

func jsonl(rows [][]byte) []byte {
	var b []byte
	for _, r := range rows {
		b = append(b, r...)
		b = append(b, '\n')
	}
	return b
}

func writeFileFsync(path string, data []byte) error {
	tmp := path + ".part"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	defer func() {
		_ = f.Close()
		if err != nil {
			_ = os.Remove(tmp)
		}
	}()
	if _, err := f.Write(data); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	err = os.Rename(tmp, path)
	return err
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
