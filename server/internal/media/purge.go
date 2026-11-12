// Grace-period purge (T15): the cleanup job enqueued when the last live
// link to an asset was removed. It re-checks references at run time and
// only then removes the stored objects. The asset row stays DELETING as
// an honest tombstone — "deleted assets cannot revive" is enforced by
// the state guards everywhere; row erasure belongs to portability (T32).
package media

import (
	"context"
	"errors"
	"log/slog"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	dbgen "canopy/server/db/generated"
	"canopy/server/internal/platform"
	platformmedia "canopy/server/internal/platform/media"
)

// PurgeAssetArgs carries only the asset id; workspace scoping comes from
// the row itself.
type PurgeAssetArgs struct {
	AssetID string `json:"asset_id"`
}

func (PurgeAssetArgs) Kind() string { return "media:purge_asset" }

// PurgeWorker deletes the stored objects of an unreferenced DELETING
// asset after the grace period.
type PurgeWorker struct {
	river.WorkerDefaults[PurgeAssetArgs]

	Pool  *pgxpool.Pool
	Store platformmedia.Store
	Log   *slog.Logger
}

func (w *PurgeWorker) Work(ctx context.Context, job *river.Job[PurgeAssetArgs]) error {
	id, ok := parseUUID(job.Args.AssetID)
	if !ok {
		w.Log.Error("purge: bad asset id", "asset", job.Args.AssetID)
		return nil
	}
	return platform.RunTx(ctx, w.Pool, func(tx pgx.Tx) error {
		q := dbgen.New(tx)
		asset, err := q.LockAssetByID(ctx, id)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil // already erased
		} else if err != nil {
			return err
		}
		if asset.State != dbgen.AssetStateDELETING {
			w.Log.Info("purge: skipped, state moved", "asset", job.Args.AssetID, "state", asset.State)
			return nil
		}
		// reference re-check at run time: never delete referenced data
		photos, err := q.CountLivePhotoLinksByAsset(ctx, dbgen.CountLivePhotoLinksByAssetParams{AssetID: id, WorkspaceID: asset.WorkspaceID})
		if err != nil {
			return err
		}
		receipts, err := q.CountReceiptLinksByAsset(ctx, dbgen.CountReceiptLinksByAssetParams{AssetID: id, WorkspaceID: asset.WorkspaceID})
		if err != nil {
			return err
		}
		if photos+receipts > 0 {
			w.Log.Warn("purge: skipped, still referenced", "asset", job.Args.AssetID, "photos", photos, "receipts", receipts)
			return nil
		}
		variants, err := q.ListAssetVariants(ctx, []pgtype.UUID{id})
		if err != nil {
			return err
		}
		var delErr error
		for _, v := range variants {
			if err := w.Store.Delete(v.StorageKey); err != nil {
				delErr = errors.Join(delErr, err)
			}
		}
		if err := w.Store.Delete(asset.StorageKey); err != nil {
			delErr = errors.Join(delErr, err)
		}
		if delErr != nil {
			return delErr // river retries; missing objects are not errors
		}
		w.Log.Info("purge: objects removed", "asset", job.Args.AssetID, "variants", len(variants))
		return nil
	})
}
