package journal

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	dbgen "canopy/server/db/generated"
	"canopy/server/internal/care/weather"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/riverqueue/river"
)

const RainCheckInterval = time.Hour

type RainCheckArgs struct{}

func (RainCheckArgs) Kind() string { return "rain.check" }

type RainCheckWorker struct {
	river.WorkerDefaults[RainCheckArgs]
	Svc     *Service
	Weather *weather.Client
	Log     *slog.Logger
}

func (w *RainCheckWorker) Work(ctx context.Context, _ *river.Job[RainCheckArgs]) error {
	return w.Svc.RunRainCheck(ctx, w.Weather, w.Log, time.Now())
}
func RainCheckPeriodicJob() *river.PeriodicJob {
	return river.NewPeriodicJob(river.PeriodicInterval(RainCheckInterval), func() (river.JobArgs, *river.InsertOpts) {
		return RainCheckArgs{}, &river.InsertOpts{UniqueOpts: river.UniqueOpts{ByArgs: true, ByPeriod: 50 * time.Minute}}
	}, &river.PeriodicJobOpts{ID: "rain.check", RunOnStart: true})
}

func rainDate(t time.Time) pgtype.Date {
	d, _ := time.Parse(time.DateOnly, t.Format(time.DateOnly))
	return pgtype.Date{Time: d, Valid: true}
}

// RunRainCheck uses garden coordinates, rather than requiring a plant location
// or a care plan. Provider failures preserve the last successful check so the
// read side can explain staleness. No weather result writes actual rain.
func (s *Service) RunRainCheck(ctx context.Context, provider *weather.Client, log *slog.Logger, now time.Time) error {
	if provider == nil {
		return nil
	}
	if err := s.q.DeleteOldRainChecks(ctx, rainDate(now.UTC().AddDate(0, 0, -14))); err != nil {
		return err
	}
	workspaces, err := s.q.ListRainWorkspaces(ctx)
	if err != nil {
		return err
	}
	for _, ws := range workspaces {
		loc, err := time.LoadLocation(ws.Timezone)
		if err != nil {
			continue
		}
		today := now.In(loc)
		checks, err := s.q.ListCurrentRainChecks(ctx, dbgen.ListCurrentRainChecksParams{WorkspaceID: ws.ID, FromDay: rainDate(today.AddDate(0, 0, -2)), ToDay: rainDate(today)})
		if err != nil {
			return err
		}
		fresh := false
		for _, c := range checks {
			if c.EventDate.Time.Format(time.DateOnly) == today.Format(time.DateOnly) && !c.CheckedAt.Time.After(now) && now.Sub(c.CheckedAt.Time) < RainCheckInterval {
				fresh = true
				break
			}
		}
		if fresh {
			continue
		}
		lat, e1 := ws.Latitude.Float64Value()
		lon, e2 := ws.Longitude.Float64Value()
		if e1 != nil || e2 != nil || !lat.Valid || !lon.Valid {
			continue
		}
		days, err := provider.FetchRecentRain(ctx, lat.Float64, lon.Float64, ws.Timezone, now)
		if err != nil {
			log.Warn("garden rain check unavailable", "workspace", uuidString(ws.ID), "err", err)
			continue
		}
		if err = s.storeRainCheck(ctx, ws, days, now); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) storeRainCheck(ctx context.Context, original dbgen.Workspace, days []weather.RainDay, now time.Time) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	q := dbgen.New(tx)
	ws, err := q.LockRainWorkspace(ctx, original.ID)
	if err != nil {
		return err
	}
	// A user may remove or move the garden while the request is running.
	if ws.Latitude == nil || ws.Longitude == nil || !ws.Latitude.Valid || !ws.Longitude.Valid || ws.Timezone != original.Timezone || numericString(*ws.Latitude) != numericString(*original.Latitude) || numericString(*ws.Longitude) != numericString(*original.Longitude) {
		return nil
	}
	for _, d := range days {
		day, err := time.Parse(time.DateOnly, d.Day)
		if err != nil {
			return err
		}
		var mm *pgtype.Numeric
		if d.AmountMm != nil {
			n, ok := parseDecimal(floatText(*d.AmountMm))
			if !ok {
				return fmt.Errorf("invalid rain estimate")
			}
			mm = &n
		}
		if err = q.UpsertRainCheck(ctx, dbgen.UpsertRainCheckParams{ID: s.uuid.New(), WorkspaceID: ws.ID, EventDate: rainDate(day), Latitude: *ws.Latitude, Longitude: *ws.Longitude, Timezone: ws.Timezone, EstimatedMm: mm, CheckedAt: pgtype.Timestamptz{Time: now, Valid: true}}); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}
