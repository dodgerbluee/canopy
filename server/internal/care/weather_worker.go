package care

// The weather fetch worker (T26): a periodic River job that turns each
// opted-in location's coordinates into one immutable snapshot row via
// the outbound adapter, plus the retention sweep.
//
// Guardrails (03-contracts): only locations whose OWNER entered
// coordinates are ever fetched — nothing here geolocates. A provider
// failure skips that location and logs; care proceeds on calendar
// cadence alone (manual care is never gated on the forecast API). The
// cache rule ("timeout/rate/cache") is one fetch per location per
// freshness window: a location whose latest snapshot is still inside
// the engine's 6h FRESH band is not re-fetched.

import (
	"context"
	"log/slog"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/riverqueue/river"

	dbgen "canopy/server/db/generated"
	"canopy/server/internal/care/weather"
)

// WeatherFetchInterval is both the periodic cadence and the cache
// window: it matches the engine's FRESH band (engine.Weather.Freshness),
// so advice never advances on a snapshot older than what the band
// claims.
const WeatherFetchInterval = 6 * time.Hour

// weatherSnapshotRetention bounds stored provider bytes: snapshots are
// advisory evidence, kept a week past their day, then swept; occurrences
// keep their typed reason text while the evidence reference SET NULLs.
const weatherSnapshotRetention = 7 * 24 * time.Hour

// WeatherFetchArgs carries no payload: one run sweeps retention and
// refreshes every opted-in location.
type WeatherFetchArgs struct{}

func (WeatherFetchArgs) Kind() string { return "weather.fetch" }

// WeatherFetchWorker runs one fetch pass. Per-location provider errors
// never fail the job; only database failures do (River retries those).
type WeatherFetchWorker struct {
	river.WorkerDefaults[WeatherFetchArgs]
	Svc *Service
	Log *slog.Logger
}

func (w *WeatherFetchWorker) Work(ctx context.Context, job *river.Job[WeatherFetchArgs]) error {
	return w.Svc.RunWeatherFetch(ctx, w.Svc.clock.Now().UTC())
}

// WeatherFetchPeriodicJob is the recurring refresh. RunOnStart gives the
// same outage catch-up as care.tick; ByArgs uniqueness keeps a busy
// cluster from stacking duplicate passes.
func WeatherFetchPeriodicJob() *river.PeriodicJob {
	return river.NewPeriodicJob(
		river.PeriodicInterval(WeatherFetchInterval),
		func() (river.JobArgs, *river.InsertOpts) {
			return WeatherFetchArgs{}, &river.InsertOpts{
				UniqueOpts: river.UniqueOpts{ByArgs: true, ByPeriod: 5 * time.Hour},
			}
		},
		&river.PeriodicJobOpts{ID: "weather.fetch", RunOnStart: true},
	)
}

// RunWeatherFetch performs one pass: sweep expired snapshots, then for
// every location with owner-entered coordinates either skip (cache) or
// fetch + insert one snapshot row.
func (s *Service) RunWeatherFetch(ctx context.Context, now time.Time) error {
	if s.Weather == nil {
		return nil // deployment opted out: no provider, no snapshots
	}
	q := s.q()
	if err := q.DeleteExpiredWeatherSnapshots(ctx, pgtype.Timestamptz{Time: now, Valid: true}); err != nil {
		return err
	}
	locs, err := q.ListLocationsWithCoordinates(ctx)
	if err != nil {
		return err
	}
	for _, loc := range locs {
		lat, err1 := loc.Latitude.Float64Value()
		lon, err2 := loc.Longitude.Float64Value()
		if err1 != nil || err2 != nil || !lat.Valid || !lon.Valid {
			continue // unreadable coordinates: never guess a location
		}
		latest, err := q.LatestWeatherSnapshot(ctx, dbgen.LatestWeatherSnapshotParams{
			WorkspaceID: loc.WorkspaceID, LocationID: loc.ID})
		switch {
		case err == nil:
			if now.Sub(latest.FetchedAt.Time) < WeatherFetchInterval {
				continue // cache hit: the newest snapshot is still fresh
			}
		case !isNoRows(err):
			return err
		}
		d, err := s.Weather.FetchDaily(ctx, lat.Float64, lon.Float64)
		if err != nil {
			// Provider failure preserves manual care: no row, no abort.
			s.Log.Warn("weather fetch failed; guidance proceeds on calendar cadence",
				"location", uuidString(loc.ID), "err", err)
			continue
		}
		from, until, err := d.Window()
		if err != nil {
			s.Log.Warn("weather snapshot window refused", "location", uuidString(loc.ID), "err", err)
			continue
		}
		if _, err := q.InsertWeatherSnapshot(ctx, dbgen.InsertWeatherSnapshotParams{
			ID: s.uuid.New(), WorkspaceID: loc.WorkspaceID, LocationID: loc.ID,
			Provider:   weather.Provider,
			FetchedAt:  pgtype.Timestamptz{Time: now, Valid: true},
			ValidFrom:  pgtype.Timestamptz{Time: from, Valid: true},
			ValidUntil: pgtype.Timestamptz{Time: until, Valid: true},
			HighTempC:  numericOf(d.HighTempC), LowTempC: numericOf(d.LowTempC),
			RainfallMm: numericOf(d.RainfallMm), Et0Mm: numericOf(d.Et0Mm),
			License:   pgtype.Text{String: weather.License, Valid: true},
			ExpiresAt: pgtype.Timestamptz{Time: now.Add(weatherSnapshotRetention), Valid: true},
		}); err != nil {
			return err
		}
	}
	return nil
}

// numericOf stores a provider value honestly: nil stays NULL (the
// provider did not supply it), a value is stored exactly as reported.
func numericOf(v *float64) *pgtype.Numeric {
	if v == nil {
		return nil
	}
	var n pgtype.Numeric
	if err := n.Scan(strconv.FormatFloat(*v, 'g', -1, 64)); err != nil {
		return nil
	}
	return &n
}
