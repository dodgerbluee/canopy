package care

// T23 acceptance suite: durable occurrence materialization against the
// real DB (duplicate-tick safety, outage catch-up collapse), the
// complete/skip/snooze/correct-completion transactions (cursor math,
// completion+activity atomicity, undo before/after later decisions),
// the journal linked-completion guard, and the plan-edit regeneration
// contract. All tests run against PGTEST_DSN scratch schemas.

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	dbgen "canopy/server/db/generated"
	"canopy/server/internal/care/engine"
	"canopy/server/internal/journal"
	"canopy/server/internal/platform"
)

// ---- fixtures --------------------------------------------------------------

func mkPlant(t *testing.T, pool *pgxpool.Pool, a Actor) dbgen.Plant {
	t.Helper()
	p, err := dbgen.New(pool).InsertPlant(context.Background(), dbgen.InsertPlantParams{
		ID: platform.UUIDv4{}.New(), WorkspaceID: a.WorkspaceID,
		Name: "Mission fig", Type: dbgen.PlantTypeINDIVIDUAL, Category: dbgen.PlantCategoryFIG,
	})
	if err != nil {
		t.Fatalf("plant: %v", err)
	}
	return p
}

// publishedVersion builds a profile with the given rules, publishes it,
// and returns the REVIEWED version id.
func publishedVersion(t *testing.T, s *Service, a Actor, rules ...RuleInput) string {
	t.Helper()
	ctx := context.Background()
	src := mkSource(t, s, a, "Occurrence test source")
	vid := src.Versions[0].ID
	if len(rules) == 0 {
		rules = []RuleInput{waterRule(vid)}
	}
	p, probs, err := s.CreateCareProfileDraft(ctx, a, CreateCareProfileDraftInput{
		Category: "FIG", Rules: rules,
	}, nil)
	noProbs(t, probs, err)
	pub, probs, err := s.PublishCareProfile(ctx, a, PublishCareProfileInput{
		ProfileID: p.ID, ExpectedRevision: p.Revision, ReviewerLabel: "owner", ReviewedOn: "2026-11-02",
	}, nil)
	noProbs(t, probs, err)
	for _, v := range pub.Versions {
		if v.Status == "REVIEWED" {
			return v.ID
		}
	}
	t.Fatalf("no reviewed version: %+v", pub.Versions)
	return ""
}

func mkPlan(t *testing.T, s *Service, a Actor, plantID, versionID string) dbgen.CarePlan {
	t.Helper()
	plan, probs, err := s.CreateCarePlan(context.Background(), a, CreatePlanInput{
		PlantID: plantID, ProfileVersionID: &versionID, Timezone: "UTC",
	}, nil)
	noProbs(t, probs, err)
	return plan
}

func enlcRule(vid string, days int) RuleInput {
	return RuleInput{
		RuleKey: "water-enlc", Kind: "ACTION", ActivityKind: ptr("WATER"),
		Recurrence: RecurrenceSpecInput{EveryNDays: &EveryNDaysInput{
			Days: int32(days), Anchor: "LAST_QUALIFYING_COMPLETION"}},
		RequiredFacts:    []string{"REGION"},
		GuidanceText:     "Deep soak after the last watering.",
		SourceVersionIDs: []string{vid},
	}
}

func weeklyCheck(vid string) RuleInput {
	return RuleInput{
		RuleKey: "inspect", Kind: "CHECK",
		Recurrence:       RecurrenceSpecInput{WeeklyDays: &WeeklyDaysInput{Weekdays: []string{"MONDAY"}}},
		RequiredFacts:    []string{"REGION"},
		GuidanceText:     "Scan leaves for pests.",
		SourceVersionIDs: []string{vid},
	}
}

// ---- query helpers ---------------------------------------------------------

func allOccs(t *testing.T, pool *pgxpool.Pool, planID pgtype.UUID) []dbgen.CareOccurrence {
	t.Helper()
	rows, err := dbgen.New(pool).ListPlanOccurrences(context.Background(),
		dbgen.ListPlanOccurrencesParams{PlanID: planID, LimitCount: 500})
	if err != nil {
		t.Fatalf("list occurrences: %v", err)
	}
	return rows
}

func dueOn(occ dbgen.CareOccurrence, d engine.Date) bool {
	return occ.CurrentDueDate.Time.In(time.UTC).Format("2006-01-02") == d.String()
}

func cursorOf(t *testing.T, pool *pgxpool.Pool, planID pgtype.UUID, ruleKey string) dbgen.CareRuleCursor {
	t.Helper()
	c, err := dbgen.New(pool).GetCareRuleCursor(context.Background(),
		dbgen.GetCareRuleCursorParams{PlanID: planID, RuleKey: ruleKey})
	if err != nil {
		t.Fatalf("cursor %s: %v", ruleKey, err)
	}
	return c
}

func countActivities(t *testing.T, pool *pgxpool.Pool, ws pgtype.UUID) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(),
		"SELECT count(*) FROM activities WHERE workspace_id = $1", ws).Scan(&n); err != nil {
		t.Fatalf("count activities: %v", err)
	}
	return n
}

func utcToday() engine.Date { return engine.DateFrom(time.Now().UTC()) }

// ---- materialization -------------------------------------------------------

func TestPlanCreateMaterializesAndDuplicateTicksAreHarmless(t *testing.T) {
	ctx, pool, a := scratch(t)
	s := svc(pool)
	plant := mkPlant(t, pool, a)
	plan := mkPlan(t, s, a, uuidString(plant.ID),
		mustReviewedVersion(t, s, a, waterRule, weeklyCheck))
	open := allOccs(t, pool, plan.ID)
	if len(open) == 0 {
		t.Fatal("plan creation must materialize the horizon")
	}
	keys := map[string]bool{}
	for _, o := range open {
		if keys[o.OccurrenceKey] {
			t.Fatalf("duplicate logical key %s", o.OccurrenceKey)
		}
		keys[o.OccurrenceKey] = true
		if o.RuleKey == "water-established" && !hasFact(o.MissingFacts, "REGION") {
			t.Fatalf("REGION fact missing not recorded: %+v", o.MissingFacts)
		}
	}

	// Duplicate ticks: no new rows, no key churn.
	for i := 0; i < 2; i++ {
		if err := s.MaterializePlan(ctx, plan.ID); err != nil {
			t.Fatalf("tick: %v", err)
		}
	}
	after := allOccs(t, pool, plan.ID)
	if len(after) != len(open) {
		t.Fatalf("duplicate tick changed occurrence count: %d -> %d", len(open), len(after))
	}

	// Watermark (the persisted scheduler cursor) is set.
	p, err := dbgen.New(pool).GetCarePlanForUpdate(ctx, plan.ID)
	if err != nil {
		t.Fatalf("get plan: %v", err)
	}
	if !p.LastGeneratedThrough.Valid {
		t.Fatal("watermark not set after materialization")
	}
}

// mustReviewedVersion publishes a profile whose rules are built with the
// real source version id.
func mustReviewedVersion(t *testing.T, s *Service, a Actor, mk ...func(string) RuleInput) string {
	t.Helper()
	ctx := context.Background()
	src := mkSource(t, s, a, "Materialization source")
	vid := src.Versions[0].ID
	rules := make([]RuleInput, 0, len(mk))
	for _, f := range mk {
		rules = append(rules, f(vid))
	}
	p, probs, err := s.CreateCareProfileDraft(ctx, a, CreateCareProfileDraftInput{
		Category: "FIG", Rules: rules,
	}, nil)
	noProbs(t, probs, err)
	pub, probs, err := s.PublishCareProfile(ctx, a, PublishCareProfileInput{
		ProfileID: p.ID, ExpectedRevision: p.Revision, ReviewerLabel: "owner", ReviewedOn: "2026-11-02",
	}, nil)
	noProbs(t, probs, err)
	for _, v := range pub.Versions {
		if v.Status == "REVIEWED" {
			return v.ID
		}
	}
	t.Fatal("no reviewed version")
	return ""
}

func TestOutageCatchUpCollapsesBacklog(t *testing.T) {
	ctx, pool, a := scratch(t)
	s := svc(pool)
	plant := mkPlant(t, pool, a)
	today := utcToday()
	vid := mustReviewedVersion(t, s, a,
		func(v string) RuleInput {
			return RuleInput{
				RuleKey: "water-7", Kind: "ACTION", ActivityKind: ptr("WATER"),
				Recurrence:       RecurrenceSpecInput{EveryNDays: &EveryNDaysInput{Days: 7, Anchor: "PLAN_START"}},
				RequiredFacts:    []string{"REGION"},
				GuidanceText:     "Deep soak.",
				SourceVersionIDs: []string{v},
			}
		},
		func(v string) RuleInput {
			return RuleInput{
				RuleKey: "event", Kind: "CHECK",
				Recurrence:       RecurrenceSpecInput{OneTime: &OneTimeInput{Date: today.AddDays(3).String()}},
				RequiredFacts:    []string{"REGION"},
				GuidanceText:     "Harvest window.",
				SourceVersionIDs: []string{v},
			}
		},
	)
	plan := mkPlan(t, s, a, uuidString(plant.ID), vid)

	// Simulate the next scheduler tick 30 days after creation (the outage).
	asOf := today.AddDays(30)
	if err := s.materializePlanAt(ctx, plan.ID, &asOf); err != nil {
		t.Fatalf("catch-up tick: %v", err)
	}

	var overdueWater, futureWater, event int
	for _, o := range allOccs(t, pool, plan.ID) {
		switch {
		case o.Status == dbgen.OccurrenceStatusDUE && o.RuleKey == "water-7" && pgToEng(o.CurrentDueDate).Before(asOf):
			overdueWater++
		case o.Status == dbgen.OccurrenceStatusDUE && o.RuleKey == "water-7":
			futureWater++
		case o.Status == dbgen.OccurrenceStatusDUE && o.RuleKey == "event":
			event++
		}
	}
	if overdueWater != 1 {
		t.Fatalf("outage must collapse to one overdue actionable check, got %d", overdueWater)
	}
	if futureWater == 0 {
		t.Fatal("future horizon must survive the collapse")
	}
	if event != 1 {
		t.Fatalf("one-time occurrences are not collapsed, got %d", event)
	}
}

// ---- completion transactions ------------------------------------------------

func TestCompleteCreatesActivityAndDoubleClickConflicts(t *testing.T) {
	ctx, pool, a := scratch(t)
	s := svc(pool)
	plant := mkPlant(t, pool, a)
	vid := mustReviewedVersion(t, s, a, waterRule)
	plan := mkPlan(t, s, a, uuidString(plant.ID), vid)

	occ := firstDue(t, pool, plan.ID)
	res, probs, err := s.CompleteOccurrence(ctx, a, CompleteOccurrenceInput{
		OccurrenceID: uuidString(occ.ID), ExpectedRevision: occ.Revision,
		Quantity: ptr("500"), Unit: ptr("ML"), Note: ptr("deep soak at dawn"),
	}, nil)
	noProbs(t, probs, err)
	if res.Status != dbgen.OccurrenceStatusCOMPLETED || !res.CompletedActivityID.Valid {
		t.Fatalf("completion shape: %+v", res)
	}
	act, err := dbgen.New(pool).GetActivity(ctx, dbgen.GetActivityParams{
		ID: res.CompletedActivityID, WorkspaceID: a.WorkspaceID})
	if err != nil {
		t.Fatalf("linked activity missing: %v", err)
	}
	if act.Kind != dbgen.ActivityKindWATER || act.PlantID != plant.ID {
		t.Fatalf("activity mismatch: %+v", act)
	}
	if countActivities(t, pool, a.WorkspaceID) != 1 {
		t.Fatal("completion must create exactly one activity")
	}

	// Double-click (no idempotency key): rejected, no second activity.
	_, probs, err = s.CompleteOccurrence(ctx, a, CompleteOccurrenceInput{
		OccurrenceID: uuidString(occ.ID), ExpectedRevision: res.Revision,
		Quantity: ptr("500"), Unit: ptr("ML"),
	}, nil)
	wantCode(t, probs, err, CodeConflict)
	if countActivities(t, pool, a.WorkspaceID) != 1 {
		t.Fatal("double-click created a second activity")
	}
}

func firstDue(t *testing.T, pool *pgxpool.Pool, planID pgtype.UUID) dbgen.CareOccurrence {
	t.Helper()
	for _, o := range allOccs(t, pool, planID) {
		if o.Status == dbgen.OccurrenceStatusDUE {
			return o
		}
	}
	t.Fatal("no DUE occurrence")
	return dbgen.CareOccurrence{}
}

func TestBackdatedCompletionRegeneratesOnlyEligibleFuture(t *testing.T) {
	ctx, pool, a := scratch(t)
	s := svc(pool)
	plant := mkPlant(t, pool, a)
	vid := mustReviewedVersion(t, s, a, func(v string) RuleInput { return enlcRule(v, 7) })
	plan := mkPlan(t, s, a, uuidString(plant.ID), vid)
	today := utcToday()

	occ := firstDue(t, pool, plan.ID)
	if !dueOn(occ, today) {
		t.Fatalf("first ENLC slot should be due at plan start: %+v", occ)
	}
	_, probs, err := s.CompleteOccurrence(ctx, a, CompleteOccurrenceInput{
		OccurrenceID: uuidString(occ.ID), ExpectedRevision: occ.Revision,
		OccurredOn: ptr(today.AddDays(-3).String()), Quantity: ptr("5"), Unit: ptr("L"),
	}, nil)
	noProbs(t, probs, err)

	cur := cursorOf(t, pool, plan.ID, "water-enlc")
	if got := pgToEng(cur.NextDueDate); got != today.AddDays(4) {
		t.Fatalf("cursor next: want %s, got %s", today.AddDays(4), got)
	}
	var open []dbgen.CareOccurrence
	for _, o := range allOccs(t, pool, plan.ID) {
		if o.Status == dbgen.OccurrenceStatusDUE {
			open = append(open, o)
		}
	}
	if len(open) != 1 || !dueOn(open[0], today.AddDays(4)) {
		t.Fatalf("expected one future slot at %s, got %+v", today.AddDays(4), open)
	}
}

func TestSkipAnchorsOriginalDue(t *testing.T) {
	ctx, pool, a := scratch(t)
	s := svc(pool)
	plant := mkPlant(t, pool, a)
	vid := mustReviewedVersion(t, s, a, func(v string) RuleInput { return enlcRule(v, 7) })
	plan := mkPlan(t, s, a, uuidString(plant.ID), vid)
	today := utcToday()

	occ := firstDue(t, pool, plan.ID)
	_, probs, err := s.SkipOccurrence(ctx, a, SkipOccurrenceInput{
		OccurrenceID: uuidString(occ.ID), ExpectedRevision: occ.Revision,
	}, nil)
	noProbs(t, probs, err)

	cur := cursorOf(t, pool, plan.ID, "water-enlc")
	if pgToEng(cur.AnchorDate) != today {
		t.Fatalf("skip must anchor on the original due date, got %s", pgToEng(cur.AnchorDate))
	}
	if pgToEng(cur.NextDueDate) != today.AddDays(7) {
		t.Fatalf("cursor next: %s", pgToEng(cur.NextDueDate))
	}
}

func TestSnoozeThenSkipAnchorsOriginalDue(t *testing.T) {
	ctx, pool, a := scratch(t)
	s := svc(pool)
	plant := mkPlant(t, pool, a)
	vid := mustReviewedVersion(t, s, a, func(v string) RuleInput { return enlcRule(v, 7) })
	plan := mkPlan(t, s, a, uuidString(plant.ID), vid)
	today := utcToday()

	occ := firstDue(t, pool, plan.ID)
	snoozed, probs, err := s.SnoozeOccurrence(ctx, a, SnoozeOccurrenceInput{
		OccurrenceID: uuidString(occ.ID), ExpectedRevision: occ.Revision,
		Until: today.AddDays(5).String(),
	}, nil)
	noProbs(t, probs, err)
	if !dueOn(snoozed, today.AddDays(5)) || snoozed.Revision != occ.Revision+1 {
		t.Fatalf("snooze shape: %+v", snoozed)
	}
	_, probs, err = s.SkipOccurrence(ctx, a, SkipOccurrenceInput{
		OccurrenceID: uuidString(occ.ID), ExpectedRevision: snoozed.Revision,
	}, nil)
	noProbs(t, probs, err)

	cur := cursorOf(t, pool, plan.ID, "water-enlc")
	if pgToEng(cur.AnchorDate) != today {
		t.Fatalf("skip after snooze must anchor on the ORIGINAL due date, got %s",
			pgToEng(cur.AnchorDate))
	}
	if pgToEng(cur.NextDueDate) != today.AddDays(7) {
		t.Fatalf("cursor next: %s", pgToEng(cur.NextDueDate))
	}
}

func TestUndoLatestCompletionReopensAndReplays(t *testing.T) {
	ctx, pool, a := scratch(t)
	s := svc(pool)
	plant := mkPlant(t, pool, a)
	vid := mustReviewedVersion(t, s, a, func(v string) RuleInput { return enlcRule(v, 7) })
	plan := mkPlan(t, s, a, uuidString(plant.ID), vid)
	today := utcToday()

	occ := firstDue(t, pool, plan.ID)
	done, probs, err := s.CompleteOccurrence(ctx, a, CompleteOccurrenceInput{
		OccurrenceID: uuidString(occ.ID), ExpectedRevision: occ.Revision,
		Quantity: ptr("5"), Unit: ptr("L"),
	}, nil)
	noProbs(t, probs, err)

	res, probs, err := s.CorrectCompletion(ctx, a, CorrectCompletionInput{
		OccurrenceID: uuidString(occ.ID), ExpectedRevision: done.Revision, Undo: true,
	}, nil)
	noProbs(t, probs, err)
	if res.Status != dbgen.OccurrenceStatusDUE || res.CompletedActivityID.Valid {
		t.Fatalf("undo must reopen the occurrence: %+v", res)
	}
	act, err := dbgen.New(pool).GetActivity(ctx, dbgen.GetActivityParams{
		ID: done.CompletedActivityID, WorkspaceID: a.WorkspaceID})
	if err != nil {
		t.Fatalf("activity gone: %v", err)
	}
	if !act.VoidedAt.Valid {
		t.Fatal("undo must void the linked activity")
	}
	// The future slot is cancelled and the cursor replays to plan start.
	for _, o := range allOccs(t, pool, plan.ID) {
		if o.RuleKey == "water-enlc" && o.Status == dbgen.OccurrenceStatusDUE && !dueOn(o, today) {
			t.Fatalf("stale future slot still open: %+v", o)
		}
	}
	cur := cursorOf(t, pool, plan.ID, "water-enlc")
	if pgToEng(cur.NextDueDate) != today {
		t.Fatalf("cursor must replay to plan start, got %s", pgToEng(cur.NextDueDate))
	}
}

func TestUndoAfterLaterCompletionRetracts(t *testing.T) {
	ctx, pool, a := scratch(t)
	s := svc(pool)
	plant := mkPlant(t, pool, a)
	vid := mustReviewedVersion(t, s, a, func(v string) RuleInput { return enlcRule(v, 1) })
	plan := mkPlan(t, s, a, uuidString(plant.ID), vid)
	today := utcToday()

	// Two backdated completions so a later decision exists.
	o1 := firstDue(t, pool, plan.ID)
	d1, probs, err := s.CompleteOccurrence(ctx, a, CompleteOccurrenceInput{
		OccurrenceID: uuidString(o1.ID), ExpectedRevision: o1.Revision,
		OccurredOn: ptr(today.AddDays(-2).String()), Quantity: ptr("5"), Unit: ptr("L"),
	}, nil)
	noProbs(t, probs, err)
	o2 := firstDue(t, pool, plan.ID) // due today-1
	if o2.ID == o1.ID {
		t.Fatal("expected a regenerated slot")
	}
	_, probs, err = s.CompleteOccurrence(ctx, a, CompleteOccurrenceInput{
		OccurrenceID: uuidString(o2.ID), ExpectedRevision: o2.Revision,
		OccurredOn: ptr(today.AddDays(-1).String()), Quantity: ptr("5"), Unit: ptr("L"),
	}, nil)
	noProbs(t, probs, err)

	res, probs, err := s.CorrectCompletion(ctx, a, CorrectCompletionInput{
		OccurrenceID: uuidString(o1.ID), ExpectedRevision: d1.Revision, Undo: true,
	}, nil)
	noProbs(t, probs, err)
	if res.Status != dbgen.OccurrenceStatusCOMPLETIONRETRACTED {
		t.Fatalf("undo with a later decision must retract, not reopen: %+v", res)
	}
	// The live schedule (driven by the later completion) is untouched.
	cur := cursorOf(t, pool, plan.ID, "water-enlc")
	if pgToEng(cur.NextDueDate) != today {
		t.Fatalf("cursor must stay on the later decision, got %s", pgToEng(cur.NextDueDate))
	}
	var open int
	for _, o := range allOccs(t, pool, plan.ID) {
		if o.Status == dbgen.OccurrenceStatusDUE {
			open++
		}
	}
	if open != 1 {
		t.Fatalf("expected exactly one open slot, got %d", open)
	}
}

func TestCorrectCompletionFieldEdits(t *testing.T) {
	ctx, pool, a := scratch(t)
	s := svc(pool)
	plant := mkPlant(t, pool, a)
	vid := mustReviewedVersion(t, s, a, func(v string) RuleInput { return enlcRule(v, 7) })
	plan := mkPlan(t, s, a, uuidString(plant.ID), vid)
	today := utcToday()

	occ := firstDue(t, pool, plan.ID)
	done, probs, err := s.CompleteOccurrence(ctx, a, CompleteOccurrenceInput{
		OccurrenceID: uuidString(occ.ID), ExpectedRevision: occ.Revision,
		Quantity: ptr("5"), Unit: ptr("L"), Note: ptr("gave 5L"),
	}, nil)
	noProbs(t, probs, err)

	// Note edit: rides the linked activity, cadence untouched.
	corr, probs, err := s.CorrectCompletion(ctx, a, CorrectCompletionInput{
		OccurrenceID: uuidString(occ.ID), ExpectedRevision: done.Revision,
		Note: ptr("gave 6L"),
	}, nil)
	noProbs(t, probs, err)
	if corr.Status != dbgen.OccurrenceStatusCOMPLETED {
		t.Fatalf("field edit must keep the completion: %+v", corr)
	}
	act, err := dbgen.New(pool).GetActivity(ctx, dbgen.GetActivityParams{
		ID: done.CompletedActivityID, WorkspaceID: a.WorkspaceID})
	if err != nil {
		t.Fatalf("activity: %v", err)
	}
	if act.Note.String != "gave 6L" {
		t.Fatalf("note not corrected: %q", act.Note.String)
	}
	cur := cursorOf(t, pool, plan.ID, "water-enlc")
	if pgToEng(cur.NextDueDate) != today.AddDays(7) {
		t.Fatalf("note edit must not move the cursor: %s", pgToEng(cur.NextDueDate))
	}

	// OccurredOn edit on the latest decision recalculates the cadence.
	_, probs, err = s.CorrectCompletion(ctx, a, CorrectCompletionInput{
		OccurrenceID: uuidString(occ.ID), ExpectedRevision: corr.Revision,
		OccurredOn: ptr(today.AddDays(-2).String()),
	}, nil)
	noProbs(t, probs, err)
	cur = cursorOf(t, pool, plan.ID, "water-enlc")
	if pgToEng(cur.NextDueDate) != today.AddDays(5) {
		t.Fatalf("occurredOn edit must recalculate: %s", pgToEng(cur.NextDueDate))
	}
	var open []dbgen.CareOccurrence
	for _, o := range allOccs(t, pool, plan.ID) {
		if o.Status == dbgen.OccurrenceStatusDUE {
			open = append(open, o)
		}
	}
	if len(open) != 1 || !dueOn(open[0], today.AddDays(5)) {
		t.Fatalf("future slot must move to %s, got %+v", today.AddDays(5), open)
	}
}

// ---- journal linked-completion guard ----------------------------------------

func TestLinkedCompletionEditsRejected(t *testing.T) {
	ctx, pool, a := scratch(t)
	s := svc(pool)
	plant := mkPlant(t, pool, a)
	vid := mustReviewedVersion(t, s, a, waterRule)
	plan := mkPlan(t, s, a, uuidString(plant.ID), vid)

	occ := firstDue(t, pool, plan.ID)
	done, probs, err := s.CompleteOccurrence(ctx, a, CompleteOccurrenceInput{
		OccurrenceID: uuidString(occ.ID), ExpectedRevision: occ.Revision,
		Quantity: ptr("500"), Unit: ptr("ML"),
	}, nil)
	noProbs(t, probs, err)

	js := journal.New(pool)
	ja := journal.Actor{WorkspaceID: a.WorkspaceID, OwnerID: a.OwnerID, SessionID: a.SessionID}
	actID := uuidString(done.CompletedActivityID)

	_, jprobs, err := js.UpdateActivity(ctx, ja, journal.ActivityUpdate{
		ID: actID, ExpectedRevision: 1, Note: ptr("changed"),
	}, nil)
	if !jHasCode(jprobs, err, journal.CodeLinkedCompletion) {
		t.Fatalf("update on linked completion: %+v %v", jprobs, err)
	}
	_, jprobs, err = js.VoidActivity(ctx, ja, actID, 1, nil)
	if !jHasCode(jprobs, err, journal.CodeLinkedCompletion) {
		t.Fatalf("void on linked completion: %+v %v", jprobs, err)
	}
}

func jHasCode(probs []journal.Problem, err error, code string) bool {
	if err != nil {
		return false
	}
	for _, p := range probs {
		if p.Code == code {
			return true
		}
	}
	return false
}

// ---- plan edits ---------------------------------------------------------------

func TestTimezoneEditRequiresConfirmAndRegenerates(t *testing.T) {
	ctx, pool, a := scratch(t)
	s := svc(pool)
	plant := mkPlant(t, pool, a)
	vid := mustReviewedVersion(t, s, a, waterRule)
	plan := mkPlan(t, s, a, uuidString(plant.ID), vid)

	_, probs, err := s.UpdateCarePlan(ctx, a, UpdatePlanInput{
		PlanID: uuidString(plan.ID), ExpectedRevision: plan.Revision,
		Timezone: ptr("America/New_York"),
	}, nil)
	wantCode(t, probs, err, CodeValidation)

	up, probs, err := s.UpdateCarePlan(ctx, a, UpdatePlanInput{
		PlanID: uuidString(plan.ID), ExpectedRevision: plan.Revision,
		Timezone: ptr("America/New_York"), ConfirmRecalculation: true,
	}, nil)
	noProbs(t, probs, err)
	if up.Timezone != "America/New_York" {
		t.Fatalf("timezone not applied: %+v", up)
	}
	// Regeneration: the horizon is open again (cancelled slots revived
	// or fresh slots generated), nothing left in a cancelled limbo.
	var open, cancelled int
	for _, o := range allOccs(t, pool, plan.ID) {
		switch o.Status {
		case dbgen.OccurrenceStatusDUE:
			open++
		case dbgen.OccurrenceStatusCANCELLED:
			cancelled++
		}
	}
	if open == 0 {
		t.Fatalf("plan edit must regenerate the horizon (cancelled=%d)", cancelled)
	}
}

func TestFailedPlanEditLeavesOccurrencesIntact(t *testing.T) {
	ctx, pool, a := scratch(t)
	s := svc(pool)
	plant := mkPlant(t, pool, a)
	vid := mustReviewedVersion(t, s, a, waterRule)
	plan := mkPlan(t, s, a, uuidString(plant.ID), vid)
	before := allOccs(t, pool, plan.ID)

	_, probs, err := s.UpdateCarePlan(ctx, a, UpdatePlanInput{
		PlanID: uuidString(plan.ID), ExpectedRevision: plan.Revision,
		Overrides: []PlanOverrideInput{{RuleKey: "ghost-rule", Enabled: ptr(false)}},
	}, nil)
	wantCode(t, probs, err, CodeValidation)

	after := allOccs(t, pool, plan.ID)
	if len(after) != len(before) {
		t.Fatalf("failed edit changed occurrences: %d -> %d", len(before), len(after))
	}
	for _, o := range after {
		if o.Status == dbgen.OccurrenceStatusCANCELLED {
			t.Fatal("failed edit must roll back its cancellations")
		}
	}
}

func TestSecondPlanForPlantConflicts(t *testing.T) {
	_, pool, a := scratch(t)
	s := svc(pool)
	plant := mkPlant(t, pool, a)
	vid := publishedVersion(t, s, a)
	mkPlan(t, s, a, uuidString(plant.ID), vid)
	_, probs, err := s.CreateCarePlan(context.Background(), a, CreatePlanInput{
		PlantID: uuidString(plant.ID), ProfileVersionID: &vid, Timezone: "UTC",
	}, nil)
	wantCode(t, probs, err, CodeConflict)
}

// ---- concurrency and worker seam ----------------------------------------------

func TestConcurrentCompleteProducesOneActivity(t *testing.T) {
	ctx, pool, a := scratch(t)
	s := svc(pool)
	plant := mkPlant(t, pool, a)
	vid := mustReviewedVersion(t, s, a, waterRule)
	plan := mkPlan(t, s, a, uuidString(plant.ID), vid)
	occ := firstDue(t, pool, plan.ID)

	var wg sync.WaitGroup
	var mu sync.Mutex
	var okCount, conflictCount int
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, probs, err := s.CompleteOccurrence(ctx, a, CompleteOccurrenceInput{
				OccurrenceID: uuidString(occ.ID), ExpectedRevision: occ.Revision,
				Quantity: ptr("500"), Unit: ptr("ML"),
			}, nil)
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil && len(probs) == 0:
				okCount++
			case len(probs) > 0 && probs[0].Code == CodeConflict:
				conflictCount++
			default:
				t.Errorf("unexpected concurrent outcome: %+v %v", probs, err)
			}
		}()
	}
	wg.Wait()
	if okCount != 1 || conflictCount != 1 {
		t.Fatalf("concurrent completion: ok=%d conflict=%d", okCount, conflictCount)
	}
	if n := countActivities(t, pool, a.WorkspaceID); n != 1 {
		t.Fatalf("concurrent completion created %d activities", n)
	}
}

func TestWorkerTickCoversAllPlansAndCatchesUp(t *testing.T) {
	ctx, pool, a := scratch(t)
	s := svc(pool)
	w := &CareTickWorker{Svc: s}

	plantA := mkPlant(t, pool, a)
	plantB := mkPlant(t, pool, a)
	vid := publishedVersion(t, s, a)
	planA := mkPlan(t, s, a, uuidString(plantA.ID), vid)
	planB := mkPlan(t, s, a, uuidString(plantB.ID), vid)

	// Simulate a crash before any persistence mattered: wipe rows and
	// the watermark, then run the worker tick (the startup catch-up).
	if _, err := pool.Exec(ctx, "DELETE FROM care_occurrences"); err != nil {
		t.Fatalf("wipe: %v", err)
	}
	if _, err := pool.Exec(ctx, "UPDATE care_plans SET last_generated_through = NULL"); err != nil {
		t.Fatalf("reset watermark: %v", err)
	}
	if err := w.Work(ctx, &river.Job[CareTickArgs]{}); err != nil {
		t.Fatalf("worker tick: %v", err)
	}
	if len(allOccs(t, pool, planA.ID)) == 0 || len(allOccs(t, pool, planB.ID)) == 0 {
		t.Fatal("startup catch-up must rebuild every enabled plan")
	}
	// Targeted repair path.
	if err := w.Work(ctx, &river.Job[CareTickArgs]{Args: CareTickArgs{PlanID: uuidString(planA.ID)}}); err != nil {
		t.Fatalf("targeted tick: %v", err)
	}
}

// ---- preview -------------------------------------------------------------------

func TestPreviewCarePlanDryRun(t *testing.T) {
	ctx, pool, a := scratch(t)
	s := svc(pool)
	plant := mkPlant(t, pool, a)
	vid := mustReviewedVersion(t, s, a, waterRule)

	pv, probs, err := s.PreviewCarePlan(ctx, a, PreviewPlanInput{
		PlantID: uuidString(plant.ID), ProfileVersionID: &vid, Timezone: "UTC",
	})
	noProbs(t, probs, err)
	if len(pv.Occurrences) == 0 {
		t.Fatal("preview must project the horizon")
	}
	if len(pv.WouldCancel) != 0 {
		t.Fatal("no plan yet: nothing to cancel")
	}
	if len(allOccs(t, pool, plant.ID)) != 0 {
		t.Fatal("preview must not write occurrences")
	}
	if _, err := dbgen.New(pool).GetCarePlanByPlant(ctx, plant.ID); err == nil {
		t.Fatal("preview must not create a plan")
	}

	plan := mkPlan(t, s, a, uuidString(plant.ID), vid)
	pv, probs, err = s.PreviewCarePlan(ctx, a, PreviewPlanInput{
		PlantID: uuidString(plant.ID), ProfileVersionID: &vid, Timezone: "UTC",
	})
	noProbs(t, probs, err)
	if len(pv.WouldCancel) != len(allOccs(t, pool, plan.ID)) {
		t.Fatalf("wouldCancel must list every open occurrence: %d vs %d",
			len(pv.WouldCancel), len(allOccs(t, pool, plan.ID)))
	}
}

// ---- environment fail-closed ------------------------------------------------------

func TestEnvironmentHemisphereFailClosed(t *testing.T) {
	ctx, pool, a := scratch(t)
	s := svc(pool)
	plant := mkPlant(t, pool, a)

	env := EnvironmentInput{Hemispheres: []string{"SOUTHERN"}}
	vid := mustReviewedEnvVersion(t, s, a, env, waterRule)
	plan := mkPlan(t, s, a, uuidString(plant.ID), vid)
	// Unknown hemisphere: occurrences still exist, facts recorded as missing.
	open := allOccs(t, pool, plan.ID)
	if len(open) == 0 {
		t.Fatal("unknown hemisphere must fail closed with missing facts, not silence")
	}
	for _, o := range open {
		if !hasFact(o.MissingFacts, "HEMISPHERE") {
			t.Fatalf("HEMISPHERE missing fact not recorded: %+v", o.MissingFacts)
		}
	}

	// Known northern workspace vs SOUTHERN-only profile: no occurrences.
	if _, err := pool.Exec(ctx, "UPDATE workspace SET latitude = 37.77 WHERE id = $1", a.WorkspaceID); err != nil {
		t.Fatalf("set latitude: %v", err)
	}
	plant2 := mkPlant(t, pool, a)
	plan2 := mkPlan(t, s, a, uuidString(plant2.ID), vid)
	if got := allOccs(t, pool, plan2.ID); len(got) != 0 {
		t.Fatalf("profile does not apply to this hemisphere, got %+v", got)
	}
}

func mustReviewedEnvVersion(t *testing.T, s *Service, a Actor, env EnvironmentInput, mk func(string) RuleInput) string {
	t.Helper()
	ctx := context.Background()
	src := mkSource(t, s, a, "Env-gated source")
	vid := src.Versions[0].ID
	p, probs, err := s.CreateCareProfileDraft(ctx, a, CreateCareProfileDraftInput{
		Category: "FIG", Environment: &env, Rules: []RuleInput{mk(vid)},
	}, nil)
	noProbs(t, probs, err)
	pub, probs, err := s.PublishCareProfile(ctx, a, PublishCareProfileInput{
		ProfileID: p.ID, ExpectedRevision: p.Revision, ReviewerLabel: "owner", ReviewedOn: "2026-11-02",
	}, nil)
	noProbs(t, probs, err)
	for _, v := range pub.Versions {
		if v.Status == "REVIEWED" {
			return v.ID
		}
	}
	t.Fatal("no reviewed version")
	return ""
}

func hasFact(facts []string, want string) bool {
	for _, f := range facts {
		if f == want {
			return true
		}
	}
	return false
}
