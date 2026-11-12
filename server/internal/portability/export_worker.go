package portability

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/riverqueue/river"

	dbgen "canopy/server/db/generated"
	"canopy/server/internal/agentcontext"
)

// ExportBuildArgs carries only the export id; workspace scoping comes
// from the job row itself.
type ExportBuildArgs struct {
	ExportID string `json:"export_id"`
}

func (ExportBuildArgs) Kind() string { return "export:build" }

// ExportBuildWorker builds one export archive: JSONL entity data, CSV
// summaries, every READY media object, and a checksum manifest written
// last. The build is fenced on the workspace privacy epoch: a plant
// purge mid-build revokes the export (03 deletion matrix), and the
// archive is never published.
type ExportBuildWorker struct {
	river.WorkerDefaults[ExportBuildArgs]
	Svc *Service
}

func (w *ExportBuildWorker) Work(ctx context.Context, job *river.Job[ExportBuildArgs]) error {
	s := w.Svc
	uid, ok := parseUUID(job.Args.ExportID)
	if !ok {
		s.Log.Error("export: bad export id", "export", job.Args.ExportID)
		return nil
	}
	q := dbgen.New(s.Pool)
	j, err := q.GetExportJobForWorker(ctx, uid)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	} else if err != nil {
		return err
	}
	if !agentcontext.JobAllowed(ctx, s.Pool, uuidString(j.AgentGrantID), uuidString(j.AgentCredentialID), uuidString(j.WorkspaceID), "exports:request", "") {
		_, err := q.UpdateExportJobState(ctx, dbgen.UpdateExportJobStateParams{ID: uid, WorkspaceID: j.WorkspaceID, State: dbgen.ExportStateFAILED, ErrorCode: textPtr("REVOKED")})
		return err
	}
	started, err := q.StartExportJob(ctx, dbgen.StartExportJobParams{ID: uid, WorkspaceID: j.WorkspaceID})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil // already RUNNING, FAILED or revoked
	} else if err != nil {
		return err
	}
	epoch, err := q.GetWorkspaceEpoch(ctx, j.WorkspaceID)
	if err != nil {
		return err
	}

	if err := os.MkdirAll(s.ExportsDir(), 0o750); err != nil {
		return err
	}
	tmp := filepath.Join(s.ExportsDir(), "."+job.Args.ExportID+".part")
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	published := false
	defer func() {
		_ = f.Close()
		if !published {
			_ = os.Remove(tmp)
		}
	}()

	tw := tar.NewWriter(f)
	var entries []ArchiveEntry
	add := func(name string, data []byte) error {
		if err := SafeExportPath(name); err != nil {
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

	counts := map[string]int64{}
	type dataset struct {
		name string
		blob []byte
	}
	var datasets []dataset
	collect := func(name string, rows [][]byte) {
		counts[name] = int64(len(rows))
		var b bytes.Buffer
		for _, r := range rows {
			b.Write(r)
			b.WriteByte('\n')
		}
		datasets = append(datasets, dataset{name: name + ".jsonl", blob: b.Bytes()})
	}

	ws := j.WorkspaceID
	wsRow, err := q.ExportWorkspaceRow(ctx, ws)
	if err != nil {
		return err
	}
	collect("workspace", [][]byte{wsRow})

	plants, err := q.ExportPlants(ctx, ws)
	if err != nil {
		return err
	}
	collect("plants", plants)
	for _, ds := range []struct {
		name string
		get  func() ([][]byte, error)
	}{
		{"locations", func() ([][]byte, error) { return q.ExportLocations(ctx, ws) }},
		{"tags", func() ([][]byte, error) { return q.ExportTags(ctx, ws) }},
		{"plant_tags", func() ([][]byte, error) { return q.ExportPlantTags(ctx, ws) }},
		{"plant_contexts", func() ([][]byte, error) { return q.ExportPlantContexts(ctx, ws) }},
		{"garden_knowledge_settings", func() ([][]byte, error) { return q.ExportKnowledgeSettings(ctx, ws) }},
		{"knowledge_profiles", func() ([][]byte, error) { return q.ExportKnowledgeProfiles(ctx, ws) }},
		{"plant_knowledge", func() ([][]byte, error) { return q.ExportPlantKnowledge(ctx, ws) }},
		{"agent_grants", func() ([][]byte, error) { return q.ExportAgentGrantMetadata(ctx, ws) }},
		{"agent_actions", func() ([][]byte, error) { return q.ExportAgentActions(ctx, ws) }},
		{"ai_connections", func() ([][]byte, error) { return q.ExportAIConnectionMetadata(ctx, ws) }},
		{"ai_feature_bindings", func() ([][]byte, error) { return q.ExportAIFeatureBindings(ctx, ws) }},
		{"rain_events", func() ([][]byte, error) { return q.ExportRain(ctx, ws) }},
		{"activities", func() ([][]byte, error) { return q.ExportActivities(ctx, ws) }},
		{"measurements", func() ([][]byte, error) { return q.ExportMeasurements(ctx, ws) }},
		{"plant_photos", func() ([][]byte, error) { return q.ExportPlantPhotos(ctx, ws) }},
		{"assets", func() ([][]byte, error) { return q.ExportAssets(ctx, ws) }},
		{"asset_variants", func() ([][]byte, error) { return q.ExportAssetVariants(ctx, ws) }},
		{"purchases", func() ([][]byte, error) { return q.ExportPurchases(ctx, ws) }},
		{"purchase_lines", func() ([][]byte, error) { return q.ExportPurchaseLines(ctx, ws) }},
		{"purchase_allocations", func() ([][]byte, error) { return q.ExportPurchaseAllocations(ctx, ws) }},
		{"acquisitions", func() ([][]byte, error) { return q.ExportAcquisitions(ctx, ws) }},
		{"purchase_receipts", func() ([][]byte, error) { return q.ExportPurchaseReceipts(ctx, ws) }},
		{"journal_entries", func() ([][]byte, error) { return q.ExportJournalEntries(ctx, ws) }},
	} {
		rows, err := ds.get()
		if err != nil {
			return err
		}
		collect(ds.name, rows)
	}

	// CSV summaries for the most portable reads of the data.
	plantsCSV, err := csvBytes(plants,
		[]string{"id", "name", "scientific_name", "cultivar", "type", "category", "status", "acquired_on", "planted_on", "revision"},
		[]string{"id", "name", "scientific_name", "cultivar", "type", "category", "status", "acquired_on", "planted_on", "revision"})
	if err != nil {
		return err
	}
	if err := add("data/plants.csv", plantsCSV); err != nil {
		return err
	}
	journalRows, err := q.ExportJournalEntries(ctx, ws)
	if err != nil {
		return err
	}
	journalCSV, err := csvBytes(journalRows,
		[]string{"id", "plant_id", "entry_type", "occurred_at", "summary", "tombstoned_at"},
		[]string{"id", "plant_id", "entry_type", "occurred_at", "summary", "tombstoned_at"})
	if err != nil {
		return err
	}
	if err := add("data/journal.csv", journalCSV); err != nil {
		return err
	}
	purchaseRows, err := q.ExportPurchases(ctx, ws)
	if err != nil {
		return err
	}
	purchasesCSV, err := csvBytes(purchaseRows,
		[]string{"id", "seller", "purchased_on", "currency", "total_minor", "shipping_minor", "tax_minor", "voided_at"},
		[]string{"id", "seller", "purchased_on", "currency", "total_minor", "shipping_minor", "tax_minor", "voided_at"})
	if err != nil {
		return err
	}
	if err := add("data/purchases.csv", purchasesCSV); err != nil {
		return err
	}

	for _, ds := range datasets {
		if err := add("data/"+ds.name, ds.blob); err != nil {
			return err
		}
	}

	// every READY original and derivative; a missing file is reported in
	// the manifest, never silently skipped
	keys, err := q.ListExportMediaKeys(ctx, ws)
	if err != nil {
		return err
	}
	var missing []string
	for _, key := range keys {
		if !uuidRe.MatchString(key) {
			missing = append(missing, key)
			continue
		}
		data, err := os.ReadFile(filepath.Join(s.StoreRoot, key))
		if err != nil {
			if os.IsNotExist(err) {
				missing = append(missing, key)
				continue
			}
			return err
		}
		if err := add("media/"+key, data); err != nil {
			return err
		}
	}

	em := ExportManifest{
		SchemaVersion: SchemaVersion,
		WorkspaceID:   uuidString(ws),
		GeneratedAt:   s.clock.Now().UTC(),
		SnapshotAt:    started.SnapshotAt.Time.UTC(),
		PrivacyEpoch:  epoch,
		Counts:        counts,
		Entries:       entries,
		MissingMedia:  missing,
		Exclusions:    ExportExclusions,
	}
	manifestJSON, err := json.Marshal(em)
	if err != nil {
		return err
	}
	if err := add("manifest.json", manifestJSON); err != nil {
		return err
	}
	manifestSum := sha256.Sum256(manifestJSON)
	if err := tw.Close(); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return err
	}
	_ = f.Close()

	// privacy fence: a purge during the build bumps the epoch or revokes
	// the job; the archive is destroyed instead of published.
	epoch2, err := q.GetWorkspaceEpoch(ctx, j.WorkspaceID)
	if err != nil {
		return err
	}
	j2, err := q.GetExportJobForWorker(ctx, uid)
	if err != nil {
		return err
	}
	if epoch2 != epoch || j2.State != dbgen.ExportStateRUNNING || !agentcontext.JobAllowed(ctx, s.Pool, uuidString(j.AgentGrantID), uuidString(j.AgentCredentialID), uuidString(j.WorkspaceID), "exports:request", "") {
		if j2.State == dbgen.ExportStateRUNNING {
			if _, err := q.UpdateExportJobState(ctx, dbgen.UpdateExportJobStateParams{
				State: dbgen.ExportStateFAILED, ErrorCode: textPtr("REVOKED"),
				ID: uid, WorkspaceID: j.WorkspaceID,
			}); err != nil {
				return err
			}
		}
		s.Log.Info("export: revoked during build, archive destroyed", "export", job.Args.ExportID)
		return nil
	}

	final := filepath.Join(s.ExportsDir(), job.Args.ExportID+".tar")
	if err := os.Rename(tmp, final); err != nil {
		return err
	}
	published = true
	if _, err := q.UpdateExportJobState(ctx, dbgen.UpdateExportJobStateParams{
		State: dbgen.ExportStateREADY, ManifestSha256: textPtr(hex.EncodeToString(manifestSum[:])),
		ID: uid, WorkspaceID: j.WorkspaceID,
	}); err != nil {
		return err
	}
	s.Log.Info("export: ready", "export", job.Args.ExportID, "entries", len(entries), "missing", len(missing))
	return nil
}

// ExportCleanupArgs asks for a revoked or expired archive file to be
// removed (03: revoked archives are deleted within 24h; purge enqueues
// this immediately).
type ExportCleanupArgs struct {
	ExportID string `json:"export_id"`
}

func (ExportCleanupArgs) Kind() string { return "export:cleanup" }

type ExportCleanupWorker struct {
	river.WorkerDefaults[ExportCleanupArgs]
	Svc *Service
}

func (w *ExportCleanupWorker) Work(ctx context.Context, job *river.Job[ExportCleanupArgs]) error {
	p, err := ExportArchivePath(w.Svc.ExportsDir(), job.Args.ExportID)
	if err != nil {
		w.Svc.Log.Error("export cleanup: bad id", "export", job.Args.ExportID)
		return nil
	}
	if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func textPtr(s string) pgtype.Text { return pgtype.Text{String: s, Valid: true} }

func csvBytes(rows [][]byte, header, fields []string) ([]byte, error) {
	var b bytes.Buffer
	cw := csv.NewWriter(&b)
	if err := cw.Write(header); err != nil {
		return nil, err
	}
	for _, raw := range rows {
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.UseNumber()
		var m map[string]any
		if err := dec.Decode(&m); err != nil {
			return nil, err
		}
		rec := make([]string, len(fields))
		for i, f := range fields {
			switch v := m[f].(type) {
			case nil:
				rec[i] = ""
			case json.Number:
				rec[i] = v.String()
			case string:
				rec[i] = v
			default:
				rec[i] = fmt.Sprintf("%v", v)
			}
		}
		if err := cw.Write(rec); err != nil {
			return nil, err
		}
	}
	cw.Flush()
	return b.Bytes(), cw.Error()
}
