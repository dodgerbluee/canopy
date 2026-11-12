package insights

// T33 expiry sweeper: advice carries an optional expiry, and "expired
// suggestions cannot be accepted" needs a clock. A periodic pass flips
// due PROPOSED rows to EXPIRED (revision bumps make in-flight accepts
// fail CONFLICT, not silent staleness).

import (
	"context"
	"time"

	"github.com/riverqueue/river"
)

// SuggestionExpiryInterval bounds how long a past-expiry PROPOSED row
// can still look acceptable; hourly is plenty for 30-day advice windows.
const SuggestionExpiryInterval = 1 * time.Hour

type SuggestionExpiryArgs struct{}

func (SuggestionExpiryArgs) Kind() string { return "suggestions.expire" }

type SuggestionExpiryWorker struct {
	river.WorkerDefaults[SuggestionExpiryArgs]
	Svc *Service
}

func (w *SuggestionExpiryWorker) Work(ctx context.Context, job *river.Job[SuggestionExpiryArgs]) error {
	n, err := w.Svc.ExpireDueSuggestions(ctx)
	if err != nil {
		return err
	}
	if n > 0 {
		w.Svc.Log.Info("suggestions expired", "count", n)
	}
	return nil
}

// SuggestionExpiryPeriodicJob: RunOnStart gives outage catch-up (an
// offline worker must not leave expired advice acceptable), ByArgs
// uniqueness keeps a busy cluster from stacking passes.
func SuggestionExpiryPeriodicJob() *river.PeriodicJob {
	return river.NewPeriodicJob(
		river.PeriodicInterval(SuggestionExpiryInterval),
		func() (river.JobArgs, *river.InsertOpts) {
			return SuggestionExpiryArgs{}, &river.InsertOpts{
				UniqueOpts: river.UniqueOpts{ByArgs: true, ByPeriod: 50 * time.Minute},
			}
		},
		&river.PeriodicJobOpts{ID: "suggestions.expire", RunOnStart: true},
	)
}
