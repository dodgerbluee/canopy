// Email delivery worker (T27): a periodic sweep that delivers pending
// EMAIL notification intents through the outbound adapter with bounded
// retry. Delivery is at-least-once by design — a crash between a
// successful send and the database ack redelivers the same stable
// Message-ID; the contract discloses this rather than pretending to
// exactly-once. SMTP failure is recorded in delivery_attempts (visible)
// and never blocks care: in-app notification and the care tick are
// unaffected.
package care

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/riverqueue/river"

	dbgen "canopy/server/db/generated"
	"canopy/server/internal/email"
)

// emailMaxAttempts bounds retries per intent: five tries, then the
// intent stays FAILED-visible in the attempt log without further sends.
const emailMaxAttempts int32 = 5

// emailSweepBatch keeps one pass bounded; the next periodic pass
// continues where this one left off (ordered by the delivery gate).
const emailSweepBatch int32 = 20

type EmailDeliveryArgs struct{}

func (EmailDeliveryArgs) Kind() string { return "email.deliver" }

type EmailDeliveryWorker struct {
	river.WorkerDefaults[EmailDeliveryArgs]
	Svc *Service
	Log *slog.Logger
}

func (w *EmailDeliveryWorker) Work(ctx context.Context, job *river.Job[EmailDeliveryArgs]) error {
	return w.Svc.RunEmailDelivery(ctx, w.Svc.clock.Now().UTC())
}

// EmailDeliveryPeriodicJob: five-minute cadence matches the care tick;
// RunOnStart gives outage catch-up (a laptop off all day still gets its
// digest when the worker returns).
func EmailDeliveryPeriodicJob() *river.PeriodicJob {
	return river.NewPeriodicJob(
		river.PeriodicInterval(5*time.Minute),
		func() (river.JobArgs, *river.InsertOpts) {
			return EmailDeliveryArgs{}, &river.InsertOpts{
				UniqueOpts: river.UniqueOpts{ByArgs: true, ByPeriod: 4 * time.Minute},
			}
		},
		&river.PeriodicJobOpts{ID: "email.deliver", RunOnStart: true},
	)
}

// RunEmailDelivery sweeps gated, undelivered EMAIL intents. Adapter
// failures are recorded per intent and skipped; only database failures
// fail the job (River retries those).
func (s *Service) RunEmailDelivery(ctx context.Context, now time.Time) error {
	if s.Email == nil {
		return nil // deployment opted out: intents stay pending and visible
	}
	q := s.q()
	rows, err := q.ListPendingEmailNotifications(ctx, dbgen.ListPendingEmailNotificationsParams{
		NowGate:     pgtype.Timestamptz{Time: now, Valid: true},
		MaxAttempts: emailMaxAttempts,
		LimitCount:  emailSweepBatch,
	})
	if err != nil {
		return err
	}
	for _, r := range rows {
		mid := r.MessageID.String
		if mid == "" {
			mid = digestMessageID(r.DedupKey) // stable identity even if the row predates it
		}
		sendErr := s.Email.Send(ctx, email.Message{
			To: s.EmailTo, Subject: r.Title, Text: r.Body, MessageID: mid,
		})
		if sendErr == nil {
			if err := q.InsertDeliveryAttempt(ctx, dbgen.InsertDeliveryAttemptParams{
				ID: s.uuid.New(), NotificationID: r.ID, WorkspaceID: r.WorkspaceID,
				State: dbgen.DeliveryStateSENT, ProviderMessageID: pgtype.Text{String: mid, Valid: true},
			}); err != nil {
				return err
			}
			if _, err := q.AckEmailNotification(ctx, dbgen.AckEmailNotificationParams{
				ID: r.ID, DeliveredAt: pgtype.Timestamptz{Time: now, Valid: true},
				MessageID: pgtype.Text{String: mid, Valid: true},
			}); isNoRows(err) {
				continue // a concurrent pass already acked this intent
			} else if err != nil {
				return err
			}
			continue
		}
		code := "send"
		var de *email.DeliveryError
		if errors.As(sendErr, &de) {
			code = de.Code
		}
		if err := q.InsertDeliveryAttempt(ctx, dbgen.InsertDeliveryAttemptParams{
			ID: s.uuid.New(), NotificationID: r.ID, WorkspaceID: r.WorkspaceID,
			State: dbgen.DeliveryStateFAILED, ErrorCode: pgtype.Text{String: code, Valid: true},
		}); err != nil {
			return err
		}
		// Visible, bounded, non-blocking: care proceeds regardless.
		s.Log.Warn("email delivery failed", "notification", uuidString(r.ID),
			"code", code, "attempts", r.Attempts, "err", sendErr)
	}
	return nil
}
