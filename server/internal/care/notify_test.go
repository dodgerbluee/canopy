package care

// T24: notification intents raised by materialization ticks. The dedup
// key (occurrence + local date + channel) makes replays and restarts
// idempotent (03: "unique occurrence/date/channel prevents double
// sending").

import (
	"testing"
)

func TestNotificationIntentsDedupAcrossReticks(t *testing.T) {
	ctx, pool, a := scratch(t)
	s := svc(pool)

	allDays := []string{"MONDAY", "TUESDAY", "WEDNESDAY", "THURSDAY",
		"FRIDAY", "SATURDAY", "SUNDAY"}
	vid := mustReviewedVersion(t, s, a, func(v string) RuleInput {
		return RuleInput{
			RuleKey: "daily-inspect", Kind: "CHECK",
			Recurrence:       RecurrenceSpecInput{WeeklyDays: &WeeklyDaysInput{Weekdays: allDays}},
			GuidanceText:     "Scan leaf undersides for pests.",
			SourceVersionIDs: []string{v},
		}
	})
	plant := mkPlant(t, pool, a)
	plan := mkPlan(t, s, a, uuidString(plant.ID), vid)

	count := func() int {
		t.Helper()
		var n int
		if err := pool.QueryRow(ctx,
			"SELECT count(*) FROM notifications WHERE workspace_id = $1",
			a.WorkspaceID).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	// The creation tick materializes today's check and raises one intent.
	if n := count(); n != 1 {
		t.Fatalf("plan creation must raise exactly one notification, got %d", n)
	}
	var refKind, refID string
	if err := pool.QueryRow(ctx,
		"SELECT ref_kind, ref_id::text FROM notifications WHERE workspace_id = $1",
		a.WorkspaceID).Scan(&refKind, &refID); err != nil {
		t.Fatal(err)
	}
	if refKind != "OCCURRENCE" {
		t.Fatalf("notification ref_kind = %s, want OCCURRENCE", refKind)
	}
	var occCount int
	if err := pool.QueryRow(ctx,
		"SELECT count(*) FROM care_occurrences WHERE id = $1::uuid", refID).Scan(&occCount); err != nil {
		t.Fatal(err)
	}
	if occCount != 1 {
		t.Fatalf("notification ref must resolve to an occurrence, got %d", occCount)
	}

	// Replay: duplicate ticks never double-send.
	if err := s.MaterializePlan(ctx, plan.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.MaterializePlan(ctx, plan.ID); err != nil {
		t.Fatal(err)
	}
	if n := count(); n != 1 {
		t.Fatalf("re-ticks must dedup notification intents, got %d", n)
	}
}
