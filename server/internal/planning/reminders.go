package planning

import (
	"context"
	"errors"
	"fmt"
	"time"

	dbgen "canopy/server/db/generated"
	"canopy/server/internal/care/engine"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/riverqueue/river"
)

type ReminderSweepArgs struct{}

func (ReminderSweepArgs) Kind() string { return "planning.reminders" }

type ReminderWorker struct {
	river.WorkerDefaults[ReminderSweepArgs]
	Service *Service
}

func (w *ReminderWorker) Work(ctx context.Context, _ *river.Job[ReminderSweepArgs]) error {
	return w.Service.SweepReminders(ctx)
}
func ReminderPeriodicJob() *river.PeriodicJob {
	return river.NewPeriodicJob(river.PeriodicInterval(time.Minute), func() (river.JobArgs, *river.InsertOpts) {
		return ReminderSweepArgs{}, &river.InsertOpts{MaxAttempts: 3, UniqueOpts: river.UniqueOpts{ByPeriod: time.Minute}}
	}, &river.PeriodicJobOpts{ID: "planning.reminders", RunOnStart: true})
}
func (s *Service) SweepReminders(ctx context.Context) error {
	now := s.Clock.Now()
	rows, err := s.q().PlanningReminderCandidates(ctx, pgtype.Timestamptz{Time: now, Valid: true})
	if err != nil {
		return err
	}
	for _, r := range rows {
		if err := s.deliverReminder(ctx, r, now); err != nil {
			return err
		}
	}
	return nil
}

// No future notification is materialized. The parent lock serializes edits,
// cancellation and competing workers; workspace lock fences timezone changes.
func (s *Service) deliverReminder(ctx context.Context, r dbgen.PlanningReminderCandidatesRow, now time.Time) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := dbgen.New(tx)
	p, err := q.LockPlantingPlan(ctx, dbgen.LockPlantingPlanParams{WorkspaceID: r.WorkspaceID, ID: r.PlanID})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if !active(p.State) {
		return nil
	}
	a, err := q.GetPlantingAction(ctx, dbgen.GetPlantingActionParams{WorkspaceID: r.WorkspaceID, PlanID: r.PlanID, ID: r.ID})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if a.State != dbgen.PlantingActionStateOPEN || !a.ReminderOn.Valid {
		return nil
	}
	ws, err := q.LockPlanningReminderWorkspace(ctx, r.WorkspaceID)
	if err != nil {
		return err
	}
	zone, err := time.LoadLocation(ws.Timezone)
	if err != nil {
		return err
	}
	today := now.In(zone).Format(time.DateOnly)
	day := a.ReminderOn.Time.Format(time.DateOnly)
	if day > today || day < now.In(zone).AddDate(0, 0, -7).Format(time.DateOnly) {
		return nil
	}
	date := engine.DateFrom(a.ReminderOn.Time)
	gate, _, err := engine.NotificationInstant(date, "09:00", ws.Timezone)
	if err != nil {
		return err
	}
	if now.Before(gate) {
		return nil
	}
	key := fmt.Sprintf("planting:%s:%d:%s:INAPP", idString(a.ID), a.ScheduleRevision, idString(ws.OwnerID))
	title := fmt.Sprintf("Planned: %s", a.Title)
	body := fmt.Sprintf("%s · %s. Planned for %s in %s. Review your plan; this does not record completed garden work.", p.Name, a.Title, a.ScheduledOn.Time.Format(time.DateOnly), ws.Timezone)
	if len([]rune(title)) > 280 {
		title = string([]rune(title)[:280])
	}
	err = q.InsertNotification(ctx, dbgen.InsertNotificationParams{ID: s.IDs.New(), WorkspaceID: r.WorkspaceID, OwnerID: ws.OwnerID, Channel: dbgen.NotificationChannelINAPP, DedupKey: key, RefKind: dbgen.NotificationRefKindPLANTINGACTION, RefID: a.ID, Title: title, Body: body})
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}
