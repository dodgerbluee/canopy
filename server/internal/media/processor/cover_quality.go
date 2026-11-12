package processor

import (
	dbgen "canopy/server/db/generated"
	"canopy/server/internal/media/photoquality"
	pmedia "canopy/server/internal/platform/media"
	"context"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"io"
	"time"
)

func thumbnailQuality(store pmedia.Store, key string) (pgtype.Float8, error) {
	reader, err := store.Open(key)
	if err != nil {
		return pgtype.Float8{}, err
	}
	defer reader.Close()
	data, err := io.ReadAll(io.LimitReader(reader, (2<<20)+1))
	if err != nil {
		return pgtype.Float8{}, err
	}
	score, err := photoquality.Score(data)
	return pgtype.Float8{Float64: score, Valid: err == nil}, nil
}

type CoverQualityArgs struct{}

func (CoverQualityArgs) Kind() string { return "media.cover_quality" }

// CoverQualityWorker backfills derived scores for existing normalized photos.
// It never changes existing covers and never reads an original image.
type CoverQualityWorker struct {
	river.WorkerDefaults[CoverQualityArgs]
	Pool  *pgxpool.Pool
	Store pmedia.Store
}

func (w *CoverQualityWorker) Timeout(*river.Job[CoverQualityArgs]) time.Duration {
	return 30 * time.Second
}
func (w *CoverQualityWorker) Work(ctx context.Context, _ *river.Job[CoverQualityArgs]) error {
	rows, err := dbgen.New(w.Pool).ListPendingPhotoCoverQuality(ctx)
	if err != nil {
		return err
	}
	for _, row := range rows {
		if err := ctx.Err(); err != nil {
			return err
		}
		score, err := thumbnailQuality(w.Store, row.StorageKey)
		if err != nil {
			return err
		}
		err = pgx.BeginFunc(ctx, w.Pool, func(tx pgx.Tx) error {
			var maintenance bool
			if err := tx.QueryRow(ctx, `SELECT maintenance FROM system_state WHERE id=true FOR SHARE`).Scan(&maintenance); err != nil {
				return err
			}
			if maintenance {
				return river.JobSnooze(30 * time.Second)
			}
			return dbgen.New(tx).SavePhotoCoverQuality(ctx, dbgen.SavePhotoCoverQualityParams{ID: row.ID, WorkspaceID: row.WorkspaceID, Quality: score})
		})
		if err != nil {
			return err
		}
	}
	return nil
}
func CoverQualityPeriodicJob() *river.PeriodicJob {
	return river.NewPeriodicJob(river.PeriodicInterval(time.Minute), func() (river.JobArgs, *river.InsertOpts) {
		return CoverQualityArgs{}, &river.InsertOpts{MaxAttempts: 3, UniqueOpts: river.UniqueOpts{ByPeriod: time.Minute}}
	}, &river.PeriodicJobOpts{ID: "media.cover_quality", RunOnStart: true})
}
