package planning

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	dbgen "canopy/server/db/generated"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/riverqueue/river"
)

type GuidanceSweepArgs struct{}

func (GuidanceSweepArgs) Kind() string { return "planning.guidance" }

type GuidanceWorker struct {
	river.WorkerDefaults[GuidanceSweepArgs]
	Service *Service
}

func (w *GuidanceWorker) Timeout(*river.Job[GuidanceSweepArgs]) time.Duration {
	return 15 * time.Minute
}
func (w *GuidanceWorker) Work(ctx context.Context, _ *river.Job[GuidanceSweepArgs]) error {
	return w.Service.SweepGuidance(ctx)
}
func GuidancePeriodicJob() *river.PeriodicJob {
	return river.NewPeriodicJob(river.PeriodicInterval(30*time.Second), func() (river.JobArgs, *river.InsertOpts) {
		return GuidanceSweepArgs{}, &river.InsertOpts{MaxAttempts: 3, UniqueOpts: river.UniqueOpts{ByPeriod: 30 * time.Second}}
	}, &river.PeriodicJobOpts{ID: "planning.guidance", RunOnStart: true})
}
func (s *Service) SweepGuidance(ctx context.Context) error {
	if s.Guidance == nil {
		return nil
	}
	conn, err := s.Pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()
	var locked bool
	if err = conn.QueryRow(ctx, `SELECT pg_try_advisory_lock(hashtext(current_schema()||'planning.guidance.worker'))`).Scan(&locked); err != nil {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = conn.Conn().Close(cleanup)
		return err
	}
	if !locked {
		return nil
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		var released bool
		if err := conn.QueryRow(cleanup, `SELECT pg_advisory_unlock(hashtext(current_schema()||'planning.guidance.worker'))`).Scan(&released); err != nil || !released {
			_ = conn.Conn().Close(cleanup)
		}
	}()
	if err = s.q().CleanupPlanningProviderUsage(ctx); err != nil {
		return err
	}
	candidates, err := s.q().PlanningGuidanceCandidates(ctx)
	if err != nil {
		return err
	}
	researched := 0
	for _, candidate := range candidates {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		snapshot, plan, c, err := s.prepareGuidance(ctx, candidate, researched < 2)
		if err != nil {
			return err
		}
		if snapshot == nil {
			continue
		}
		researched++
		attempt, cancel := context.WithTimeout(ctx, 6*time.Minute)
		result, err := s.Guidance.Research(attempt, plan.WorkspaceID, subjectFromRow(plan), c)
		cancel()
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if errors.Is(err, ErrGuidanceContextChanged) {
			result = GuidanceResult{ContextChanged: true, State: "FAILED", Message: "Context changed during research.", Facts: []GuidanceFact{}, Assumptions: []string{}}
		} else if err != nil {
			result = GuidanceResult{State: "FAILED", Message: "Research could not finish. Your plan is saved; refresh to retry.", Facts: []GuidanceFact{}, Assumptions: []string{}}
		}
		if err = s.finishGuidance(ctx, plan, *snapshot, c, result); err != nil {
			return err
		}
	}
	return nil
}
func (s *Service) prepareGuidance(ctx context.Context, candidate dbgen.PlantingPlan, allowResearch bool) (*dbgen.PlantingGuidanceSnapshot, dbgen.PlantingPlan, GuidanceContext, error) {
	var zero GuidanceContext
	c, err := s.Guidance.Context(ctx, candidate.WorkspaceID, subjectFromRow(candidate))
	if err != nil {
		return nil, candidate, zero, err
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, candidate, zero, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := dbgen.New(tx)
	row, err := q.LockPlantingPlan(ctx, dbgen.LockPlantingPlanParams{WorkspaceID: candidate.WorkspaceID, ID: candidate.ID})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, candidate, zero, nil
	}
	if err != nil {
		return nil, candidate, zero, err
	}
	if !active(row.State) || row.GuidanceGeneration != candidate.GuidanceGeneration || row.Revision != candidate.Revision {
		return nil, row, zero, nil
	}
	snapshot, err := q.GetCurrentPlanningGuidance(ctx, dbgen.GetCurrentPlanningGuidanceParams{WorkspaceID: row.WorkspaceID, PlanID: row.ID, Generation: row.GuidanceGeneration})
	missing := errors.Is(err, pgx.ErrNoRows)
	if err != nil && !missing {
		return nil, row, zero, err
	}
	changed := !missing && snapshot.ContextSha256 != c.Fingerprint
	expired := !missing && (snapshot.State == "READY" || snapshot.State == "UNSUPPORTED") && snapshot.CreatedAt.Time.Before(s.Clock.Now().Add(-30*24*time.Hour))
	if changed || expired {
		if err = s.abandonGuidance(ctx, q, snapshot); err != nil {
			return nil, row, zero, err
		}
		row, err = q.AdvancePlanningGuidance(ctx, dbgen.AdvancePlanningGuidanceParams{WorkspaceID: row.WorkspaceID, ID: row.ID})
		if err != nil {
			return nil, row, zero, err
		}
		missing = true
	}
	if missing {
		snapshot, err = s.insertGuidance(ctx, q, row, c)
		if err != nil {
			return nil, row, zero, err
		}
	}
	if snapshot.State != "PENDING" && snapshot.State != "PROCESSING" {
		if err = q.ReviewPlanningGuidanceLater(ctx, dbgen.ReviewPlanningGuidanceLaterParams{WorkspaceID: row.WorkspaceID, PlanID: row.ID, ID: snapshot.ID}); err != nil {
			return nil, row, zero, err
		}
		return nil, row, c, tx.Commit(ctx)
	}
	if !allowResearch {
		return nil, row, c, tx.Commit(ctx)
	}
	claimed, err := q.ClaimPlanningGuidance(ctx, dbgen.ClaimPlanningGuidanceParams{WorkspaceID: row.WorkspaceID, PlanID: row.ID, ID: snapshot.ID, Generation: row.GuidanceGeneration})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, row, c, nil
	}
	if err != nil {
		return nil, row, c, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, row, c, err
	}
	return &claimed, row, c, nil
}
func (s *Service) finishGuidance(ctx context.Context, plan dbgen.PlantingPlan, snapshot dbgen.PlantingGuidanceSnapshot, expected GuidanceContext, res GuidanceResult) error {
	current, err := s.Guidance.Context(ctx, plan.WorkspaceID, subjectFromRow(plan))
	if err != nil {
		return err
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := dbgen.New(tx)
	row, err := q.LockPlantingPlan(ctx, dbgen.LockPlantingPlanParams{WorkspaceID: plan.WorkspaceID, ID: plan.ID})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if row.GuidanceGeneration == snapshot.Generation {
		owned, err := q.GetCurrentPlanningGuidance(ctx, dbgen.GetCurrentPlanningGuidanceParams{WorkspaceID: row.WorkspaceID, PlanID: row.ID, Generation: row.GuidanceGeneration})
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		if owned.ID != snapshot.ID || owned.State != "PROCESSING" || !owned.UpdatedAt.Time.Equal(snapshot.UpdatedAt.Time) {
			return nil
		}
	}
	if !active(row.State) || row.GuidanceGeneration != snapshot.Generation || current.Fingerprint != expected.Fingerprint || res.ContextChanged {
		if err = s.abandonGuidance(ctx, q, snapshot); err != nil {
			return err
		}
		if active(row.State) && row.GuidanceGeneration == snapshot.Generation {
			row, err = q.AdvancePlanningGuidance(ctx, dbgen.AdvancePlanningGuidanceParams{WorkspaceID: row.WorkspaceID, ID: row.ID})
			if err != nil {
				return err
			}
			if _, err = s.insertGuidance(ctx, q, row, current); err != nil {
				return err
			}
		}
		return tx.Commit(ctx)
	}
	if !contains(res.State, "READY", "FAILED", "UNSUPPORTED", "DISABLED", "NEEDS_LOCATION") || len(res.Message) > 1000 || len(res.Assumptions) > MaxGuidanceAssumptions {
		res = GuidanceResult{State: "FAILED", Message: "The research result could not be validated. Your plan is saved; refresh to retry."}
	}
	if res.Facts == nil {
		res.Facts = []GuidanceFact{}
	}
	if res.Assumptions == nil {
		res.Assumptions = []string{}
	}
	if res.State == "READY" {
		if len(res.Facts) == 0 || ValidateGuidanceFacts(res.Facts) != nil {
			res.State = "FAILED"
			res.Message = "The research facts could not be verified. Your plan is saved; review the source or retry."
			res.Facts = []GuidanceFact{}
		}
	} else {
		res.Facts = []GuidanceFact{}
	}
	raw, err := json.Marshal(res.Facts)
	if err != nil {
		return err
	}
	if len(raw) > 262144 {
		res.State = "FAILED"
		res.Message = "The research exceeded its validated size bound."
		res.Facts = []GuidanceFact{}
		raw = []byte("[]")
	}
	retrieved := pgtype.Timestamptz{}
	if res.State == "READY" {
		retrieved = pgtype.Timestamptz{Time: s.Clock.Now(), Valid: true}
	}
	_, err = q.FinishPlanningGuidance(ctx, dbgen.FinishPlanningGuidanceParams{WorkspaceID: row.WorkspaceID, PlanID: row.ID, ID: snapshot.ID, Generation: snapshot.Generation, State: dbgen.PlantingGuidanceState(res.State), Message: res.Message, Facts: raw, Assumptions: res.Assumptions, RetrievedAt: retrieved, ClaimedAt: snapshot.UpdatedAt})
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}
