package processor

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	dbgen "canopy/server/db/generated"
	"canopy/server/internal/media"
	pmedia "canopy/server/internal/platform/media"
)

// Worker executes media:process_asset jobs enqueued by finalize.
// At-least-once semantics are handled structurally: variant storage keys
// are deterministic (VariantKey) and rows upsert on (asset_id, name), so
// a retried or crashed-mid-run job converges to the same variants. A
// deleted (DELETING) asset is never revived: the final transition is
// guarded on PROCESSING and partial files are discarded.
type Worker struct {
	river.WorkerDefaults[media.ProcessAssetArgs]

	Pool      *pgxpool.Pool
	Store     pmedia.Store
	SB        Sandbox
	MaxPixels int64
	// JobTimeout bounds one asset's wall-clock. It is deliberately not
	// named Timeout: river.WorkerDefaults exposes a Timeout method and a
	// field of that name would shadow it.
	JobTimeout time.Duration
	Log        *slog.Logger
}

func (w *Worker) Work(ctx context.Context, job *river.Job[media.ProcessAssetArgs]) error {
	ctx, cancel := context.WithTimeout(ctx, w.JobTimeout)
	defer cancel()

	assetID, ok := parseUUIDString(job.Args.AssetID)
	if !ok {
		w.Log.Error("process job with malformed asset id", "asset_id", job.Args.AssetID)
		return nil // poison job: retrying cannot help
	}
	asset, err := dbgen.New(w.Pool).GetAssetByID(ctx, assetID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil // asset purged while queued
	}
	if err != nil {
		return err // infra read: let river retry
	}

	switch asset.State {
	case dbgen.AssetStateREADY:
		return nil // idempotent repeat
	case dbgen.AssetStatePROCESSING:
		// proceed
	default:
		// RESERVED/UPLOADED/FAILED/DELETING: never revive; drop any
		// partial derivative files a previous attempt left behind
		w.cleanupVariants(assetID)
		return nil
	}

	present, err := w.Store.Exists(asset.StorageKey)
	if err != nil {
		return err
	}
	if !present {
		return w.markFailed(ctx, asset, CodeObjectMissing)
	}

	variants, err := w.build(ctx, asset)
	if err != nil {
		return w.classifyFailure(ctx, asset, err)
	}
	return w.commit(ctx, asset, variants)
}

// build produces the variant set: photos go through the sandboxed
// normalization pipeline; receipt PDFs get a normalized copy only
// (OCR/text work belongs to a later lane).
func (w *Worker) build(ctx context.Context, asset dbgen.Asset) ([]variantRow, error) {
	if asset.Purpose == dbgen.AssetPurposeRECEIPT {
		key := VariantKey(uuidString(asset.ID), "original")
		src, err := w.Store.Open(asset.StorageKey)
		if err != nil {
			return nil, err
		}
		defer src.Close()
		st, err := w.Store.Stage(key, src, int64(asset.ByteCount))
		if err != nil {
			return nil, err
		}
		if err := w.Store.Commit(key); err != nil {
			return nil, err
		}
		return []variantRow{{name: "original", key: key, bytes: st.Size}}, nil
	}

	workDir, err := os.MkdirTemp("", "canopy-media-")
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.RemoveAll(workDir) }()

	// stream the source into the sandbox work dir; EXIF is then read
	// from the file's head+tail (HEIC Exif items can sit near EOF)
	src, err := w.Store.Open(asset.StorageKey)
	if err != nil {
		return nil, err
	}
	defer src.Close()
	inPath := filepath.Join(workDir, "in.bin")
	f, err := os.Create(inPath)
	if err != nil {
		return nil, err
	}
	if _, err := io.Copy(f, src); err != nil {
		f.Close()
		return nil, err
	}
	if err := f.Close(); err != nil {
		return nil, err
	}
	head, err := readHeadTail(inPath, 128*1024)
	if err != nil {
		return nil, err
	}

	mime := media.SniffMIME(head)
	ext, ok := extForMIME(mime)
	if !ok {
		return nil, &SandboxError{Argv: []string{"sniff"}, Exit: 0,
			Stderr: "stored object is not a supported image: " + mime}
	}
	final := filepath.Join(workDir, "in."+ext)
	if err := os.Rename(inPath, final); err != nil {
		return nil, err
	}
	orientation := Orientation(head)

	ds, err := Run(ctx, w.SB, workDir, "in."+ext, orientation, w.MaxPixels)
	if err != nil {
		return nil, err
	}

	rows := make([]variantRow, 0, len(ds))
	for _, d := range ds {
		key := VariantKey(uuidString(asset.ID), d.Name)
		hf, err := os.Open(d.SrcPath)
		if err != nil {
			return nil, err
		}
		st, err := w.Store.Stage(key, hf, d.Bytes)
		hf.Close()
		if err != nil {
			return nil, err
		}
		if st.Size != d.Bytes {
			return nil, fmt.Errorf("variant %s size drift: staged %d, expected %d", d.Name, st.Size, d.Bytes)
		}
		if err := w.Store.Commit(key); err != nil {
			return nil, err
		}
		rows = append(rows, variantRow{name: d.Name, key: key, bytes: d.Bytes, w: d.Width, h: d.Height, hasDims: true})
	}
	return rows, nil
}

// commit writes variant rows and the READY transition under the asset
// row lock, re-checking state so a concurrent delete can never be
// revived.
func (w *Worker) commit(ctx context.Context, asset dbgen.Asset, rows []variantRow) error {
	aborted := false
	var quality pgtype.Float8
	qualityAvailable := false
	if asset.Purpose == dbgen.AssetPurposePHOTO {
		for _, row := range rows {
			if row.name == "thumb" {
				var err error
				quality, err = thumbnailQuality(w.Store, row.key)
				qualityAvailable = err == nil
				break
			}
		}
	}
	err := pgx.BeginFunc(ctx, w.Pool, func(tx pgx.Tx) error {
		q := dbgen.New(tx)
		a, err := q.LockAsset(ctx, dbgen.LockAssetParams{ID: asset.ID, WorkspaceID: asset.WorkspaceID})
		if errors.Is(err, pgx.ErrNoRows) {
			aborted = true
			return nil
		}
		if err != nil {
			return err
		}
		if a.State != dbgen.AssetStatePROCESSING {
			aborted = true
			return nil
		}
		for _, r := range rows {
			v := dbgen.UpsertAssetVariantParams{
				AssetID: asset.ID, Name: r.name, StorageKey: r.key,
				ByteCount: int32(r.bytes),
			}
			if r.hasDims {
				v.WidthPx = pgtype.Int4{Int32: int32(r.w), Valid: true}
				v.HeightPx = pgtype.Int4{Int32: int32(r.h), Valid: true}
			}
			if _, err := q.UpsertAssetVariant(ctx, v); err != nil {
				return err
			}
		}
		if asset.Purpose == dbgen.AssetPurposePHOTO && qualityAvailable {
			if err := q.SavePhotoCoverQuality(ctx, dbgen.SavePhotoCoverQualityParams{ID: asset.ID, WorkspaceID: asset.WorkspaceID, Quality: quality}); err != nil {
				return err
			}
		}
		_, err = q.MarkAssetStateFrom(ctx, dbgen.MarkAssetStateFromParams{
			ID: asset.ID, WorkspaceID: asset.WorkspaceID,
			ToState: dbgen.AssetStateREADY, FromState: dbgen.AssetStatePROCESSING,
		})
		return err
	})
	if err != nil {
		return err
	}
	if aborted {
		// deleted or reset while we worked: discard every derivative file,
		// the row stays whatever the other writer made it
		w.cleanupVariants(asset.ID)
		w.Log.Info("processing discarded: asset no longer PROCESSING",
			"asset_id", uuidString(asset.ID), "state", string(asset.State))
	}
	return nil
}

// classifyFailure maps pipeline errors to honest outcomes: domain
// failures mark the asset FAILED (no retry), infrastructure failures
// return an error so river retries with backoff.
func (w *Worker) classifyFailure(ctx context.Context, asset dbgen.Asset, err error) error {
	var be *BudgetError
	if errors.As(err, &be) {
		return w.markFailed(ctx, asset, CodePixelBudget)
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return w.markFailed(ctx, asset, CodeProcessTimeout)
	}
	var se *SandboxError
	if errors.As(err, &se) {
		return w.markFailed(ctx, asset, CodeDecodeFailed)
	}
	if errors.Is(err, pmedia.ErrNotFound) {
		return w.markFailed(ctx, asset, CodeObjectMissing)
	}
	return err // infra: retry
}

func (w *Worker) markFailed(ctx context.Context, asset dbgen.Asset, code string) error {
	_, err := dbgen.New(w.Pool).MarkAssetStateFrom(ctx, dbgen.MarkAssetStateFromParams{
		ID: asset.ID, WorkspaceID: asset.WorkspaceID,
		ToState: dbgen.AssetStateFAILED, FromState: dbgen.AssetStatePROCESSING,
		ErrorCode: pgtype.Text{String: code, Valid: true},
	})
	if errors.Is(err, pgx.ErrNoRows) {
		// state moved under us (e.g. deletion won): do not overwrite
		return nil
	}
	w.Log.Warn("asset marked FAILED", "asset_id", uuidString(asset.ID), "code", code, "err", err)
	return err
}

func (w *Worker) cleanupVariants(id pgtype.UUID) {
	aid := uuidString(id)
	for _, v := range Variants {
		if err := w.Store.Delete(VariantKey(aid, v.Name)); err != nil {
			w.Log.Warn("variant cleanup failed", "asset_id", aid, "variant", v.Name, "err", err)
		}
	}
}

type variantRow struct {
	name, key string
	bytes     int64
	w, h      int
	hasDims   bool
}

func extForMIME(mime string) (string, bool) {
	switch mime {
	case "image/jpeg":
		return "jpg", true
	case "image/png":
		return "png", true
	case "image/webp":
		return "webp", true
	case "image/heic":
		return "heic", true
	case "image/heif":
		return "heif", true
	}
	return "", false
}

// readHeadTail returns the first n and last n bytes of a file (the
// whole file when smaller than 2n). EXIF lives up front in JPEG but can
// sit near EOF in HEIC, so both ends are enough for the metadata walk.
func readHeadTail(path string, n int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if st.Size() <= 2*n {
		return os.ReadFile(path)
	}
	head := make([]byte, n)
	if _, err := f.ReadAt(head, 0); err != nil {
		return nil, err
	}
	tail := make([]byte, n)
	if _, err := f.ReadAt(tail, st.Size()-n); err != nil {
		return nil, err
	}
	return append(head, tail...), nil
}

func parseUUIDString(s string) (pgtype.UUID, bool) {
	u, err := uuid.Parse(s)
	if err != nil {
		return pgtype.UUID{}, false
	}
	return pgtype.UUID{Bytes: u, Valid: true}, true
}

func uuidString(u pgtype.UUID) string {
	if !u.Valid {
		return ""
	}
	return uuid.UUID(u.Bytes).String()
}
