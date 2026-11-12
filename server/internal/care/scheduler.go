package care

// The scheduler tick (T23): a River worker that materializes occurrence
// horizons, plus the periodic registration that gives startup catch-up.
//
// 03-contracts: "A persisted scheduler cursor enables startup catch-up;
// application uniqueness keys make duplicate ticks harmless." The
// watermark (care_plans.last_generated_through) is the persisted cursor;
// the (plan_id, occurrence_key) uniqueness anchor absorbs replays; the
// per-tick backlog collapse keeps an outage from flooding the agenda.

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/riverqueue/river"
)

// CareTickArgs: PlanID empty means "every enabled plan" (the periodic
// tick and the startup catch-up); set means one plan (e.g. a targeted
// repair).
type CareTickArgs struct {
	PlanID string `json:"plan_id,omitempty"`
}

func (CareTickArgs) Kind() string { return "care.tick" }

// CareTickWorker runs one scheduler tick. Each plan materializes in its
// own transaction with the plan row locked, so a killed process leaves
// every plan either fully ticked or untouched, and concurrent ticks
// serialize per plan.
type CareTickWorker struct {
	river.WorkerDefaults[CareTickArgs]
	Svc *Service
	Log *slog.Logger
}

func (w *CareTickWorker) Work(ctx context.Context, job *river.Job[CareTickArgs]) error {
	if job.Args.PlanID != "" {
		id, ok := parseID(job.Args.PlanID)
		if !ok {
			return fmt.Errorf("care.tick: bad plan id %q", job.Args.PlanID)
		}
		return w.Svc.MaterializePlan(ctx, id)
	}
	return w.Svc.RunCareCatchUp(ctx)
}

// CarePeriodicJob is the recurring tick. RunOnStart inserts one tick when
// the scheduler starts: that is the startup catch-up. ByArgs uniqueness
// with a slightly-shorter-than-period window keeps a busy cluster from
// stacking duplicate full-scan ticks.
func CarePeriodicJob() *river.PeriodicJob {
	return river.NewPeriodicJob(
		river.PeriodicInterval(5*time.Minute),
		func() (river.JobArgs, *river.InsertOpts) {
			return CareTickArgs{}, &river.InsertOpts{
				UniqueOpts: river.UniqueOpts{ByArgs: true, ByPeriod: 4 * time.Minute},
			}
		},
		&river.PeriodicJobOpts{ID: "care.tick", RunOnStart: true},
	)
}
