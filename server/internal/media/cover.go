package media

import (
	dbgen "canopy/server/db/generated"
	"canopy/server/internal/media/photoquality"
	"context"
	"errors"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"time"
)

// The caller holds the plant row lock, so comparing/demoting/promoting covers
// shares the attachment's transaction and cannot race another cover change.
func automaticCover(ctx context.Context, q *dbgen.Queries, workspace, plant pgtype.UUID, asset dbgen.Asset, viewpoint string, taken pgtype.Timestamptz, source string) (bool, error) {
	current, err := q.GetCoverPhoto(ctx, dbgen.GetCoverPhotoParams{PlantID: plant, WorkspaceID: workspace})
	if errors.Is(err, pgx.ErrNoRows) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	oldAsset, err := q.GetAsset(ctx, dbgen.GetAssetParams{ID: current.AssetID, WorkspaceID: workspace})
	if err != nil {
		return false, err
	}
	return photoquality.Prefer(photoquality.Candidate{Score: asset.CoverQuality.Float64, Scored: asset.CoverQuality.Valid, Viewpoint: viewpoint, CapturedAt: coverCapture(taken, source)}, photoquality.Candidate{Score: oldAsset.CoverQuality.Float64, Scored: oldAsset.CoverQuality.Valid, Viewpoint: string(current.Viewpoint), CapturedAt: coverCapture(current.TakenAt, string(current.DateSource))}), nil
}
func coverCapture(taken pgtype.Timestamptz, source string) *time.Time {
	if source == "UPLOAD_FALLBACK" || !taken.Valid {
		return nil
	}
	value := taken.Time
	return &value
}
