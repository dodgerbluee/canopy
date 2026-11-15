package planning

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"
	"time"

	dbgen "canopy/server/db/generated"
	"github.com/jackc/pgx/v5/pgxpool"
)

func scheduleWorld(t *testing.T, windows ...GuidanceWindow) (*Service, Actor, *guidanceStub, *Plan, ConfirmScheduleInput) {
	t.Helper()
	s, a := scratch(t)
	if len(windows) == 0 {
		windows = []GuidanceWindow{annualMonthWindow(3, 4), annualMonthWindow(9, 10)}
	}
	stub := &guidanceStub{research: func(context.Context, GuidanceSubject) (GuidanceResult, error) {
		return GuidanceResult{State: "READY", Message: "Verified synthetic fixture", Facts: []GuidanceFact{scheduleFact(windows...)}, Assumptions: []string{}}, nil
	}}
	s.Guidance = stub
	r := mustCreate(t, s, a, Intent{Name: "Future fig", Category: "FIG", Method: ptr("NURSERY_PLANT"), Budget: &Budget{MinorUnits: "12000", Currency: "USD"}, TargetPeriod: &Period{Precision: YearPrecision, Year: ptr(2027)}})
	if err := s.SweepGuidance(context.Background()); err != nil {
		t.Fatal(err)
	}
	p := mustGet(t, s, a, r.ID)
	in := ConfirmScheduleInput{PlanID: r.ID, ExpectedRevision: p.Row.Revision, SnapshotID: idString(p.LatestGuidance.ID), Generation: p.Row.GuidanceGeneration, FactID: "source-planting", WindowIndex: 0, Year: 2027, TargetPeriod: &Period{Precision: DateRangePrecision, Dates: &DateRange{"2027-03-14", "2027-03-14"}}}
	return s, a, stub, p, in
}
func scheduleRequest(id string) ScheduleQuery {
	return ScheduleQuery{PlanID: id, Range: MonthRange{"2027-01", "2027-12"}, First: 25}
}
func TestScheduleConfirmationStoresEvidenceAndPreservesActualGardenAndActions(t *testing.T) {
	s, a, stub, p, in := scheduleWorld(t)
	ctx := context.Background()
	res, ps, err := s.CreateAction(ctx, a, ActionInput{PlanID: in.PlanID, ExpectedRevision: p.Row.Revision, Kind: "BUY", Title: "Buy when ready", ScheduledOn: "2027-03-01"}, nil)
	if err != nil || len(ps) != 0 {
		t.Fatal(ps, err)
	}
	in.ExpectedRevision = res.Revision
	before := mustGet(t, s, a, in.PlanID)
	key := "confirm-once"
	r, ps, err := s.ConfirmSchedule(ctx, a, in, &key)
	if err != nil || len(ps) != 0 {
		t.Fatal(ps, err)
	}
	after := mustGet(t, s, a, in.PlanID)
	selection, err := DecodeScheduleSelection(after.Row.AcceptedSchedule)
	if err != nil || selection == nil || selection.FactID != in.FactID || selection.Version != 1 || *selection.SupportedPeriod.Months != (MonthRange{"2027-03", "2027-04"}) || selection.SupportedPeriod.Dates != nil {
		t.Fatal(selection, err)
	}
	if !reflect.DeepEqual(PeriodFromRow(after.Row), in.TargetPeriod) || after.Row.Revision != before.Row.Revision+1 || after.Row.GuidanceGeneration != before.Row.GuidanceGeneration || after.Row.AcceptedGuidanceID != before.LatestGuidance.ID || after.Row.State != before.Row.State || !reflect.DeepEqual(after.Actions, before.Actions) || after.Row.BudgetMinor != before.Row.BudgetMinor || stub.calls.Load() != 1 {
		t.Fatal("confirmation changed unrelated intent/research/actions", after)
	}
	if count(t, s, "plants")+count(t, s, "activities")+count(t, s, "purchases") != 0 {
		t.Fatal("schedule became recorded garden activity")
	}
	audits := count(t, s, "audit_revisions")
	replay, ps, err := s.ConfirmSchedule(ctx, a, in, &key)
	if err != nil || len(ps) > 0 || replay != r || count(t, s, "audit_revisions") != audits {
		t.Fatal("retry did not replay", replay, ps, err)
	}
	in.TargetPeriod = &Period{Precision: DateRangePrecision, Dates: &DateRange{"2027-03-15", "2027-03-15"}}
	_, ps, err = s.ConfirmSchedule(ctx, a, in, &key)
	assertCode(t, ps, err, "IDEMPOTENCY_CONFLICT")
}
func TestScheduleConfirmationFailuresLeaveChoiceAndReceiptUntouched(t *testing.T) {
	s, a, stub, p, original := scheduleWorld(t)
	ctx := context.Background()
	before, _ := json.Marshal(p.Row)
	for _, tc := range []struct {
		name string
		edit func(*ConfirmScheduleInput)
		code string
	}{
		{"outside source", func(in *ConfirmScheduleInput) { in.TargetPeriod = &Period{Precision: YearPrecision, Year: ptr(2027)} }, "VALIDATION"},
		{"wrong fact", func(in *ConfirmScheduleInput) { in.FactID = "other" }, "VALIDATION"},
		{"wrong index", func(in *ConfirmScheduleInput) { in.WindowIndex = 7 }, "VALIDATION"},
		{"unknown period", func(in *ConfirmScheduleInput) { in.TargetPeriod = nil }, "VALIDATION"},
		{"wrong occurrence", func(in *ConfirmScheduleInput) { in.Year = 2028 }, "VALIDATION"},
		{"stale generation", func(in *ConfirmScheduleInput) { in.Generation++ }, "CONFLICT"},
		{"stale edit", func(in *ConfirmScheduleInput) { in.ExpectedRevision++ }, "CONFLICT"},
		{"private plan", func(in *ConfirmScheduleInput) { in.PlanID = idString(s.IDs.New()) }, "NOT_FOUND"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := original
			tc.edit(&in)
			key := "failure-" + tc.name
			_, ps, err := s.ConfirmSchedule(ctx, a, in, &key)
			assertCode(t, ps, err, tc.code)
		})
	}
	stub.policy = "changed-model-or-region"
	_, ps, err := s.ConfirmSchedule(ctx, a, original, nil)
	assertCode(t, ps, err, "CONFLICT")
	stub.policy = ""
	s.Clock = fixedClock{time.Now().Add(31 * 24 * time.Hour)}
	_, ps, err = s.ConfirmSchedule(ctx, a, original, nil)
	assertCode(t, ps, err, "CONFLICT")
	after, _ := json.Marshal(mustGet(t, s, a, original.PlanID).Row)
	if string(before) != string(after) || count(t, s, "mutation_receipts") != 0 {
		t.Fatal("failed confirmation mutated intent or stored receipt")
	}
}
func TestScheduleConfirmationRequiresNamedAnchorAndEligibleMethod(t *testing.T) {
	w := GuidanceWindow{Precision: "RELATIVE", Label: "After frost", Relative: &RelativeWindow{Anchor: "LAST_FROST", FromDays: ptr(0), ToDays: ptr(14)}}
	s, a, _, _, in := scheduleWorld(t, w)
	ctx := context.Background()
	in.TargetPeriod = &Period{Precision: DateRangePrecision, Dates: &DateRange{"2027-04-07", "2027-04-07"}}
	in.Assumptions.Anchors = []ScheduleAnchor{{"SOWING", "2027-04-01"}}
	_, ps, err := s.ConfirmSchedule(ctx, a, in, nil)
	assertCode(t, ps, err, "VALIDATION")
	in.Assumptions.Anchors = []ScheduleAnchor{{"LAST_FROST", "2027-04-01"}}
	res, ps, err := s.ConfirmSchedule(ctx, a, in, nil)
	if err != nil || len(ps) > 0 {
		t.Fatal(ps, err)
	}
	selection, err := DecodeScheduleSelection(mustGet(t, s, a, in.PlanID).Row.AcceptedSchedule)
	if err != nil || !reflect.DeepEqual(selection.Assumptions.Anchors, in.Assumptions.Anchors) || selection.SupportedPeriod.Dates.From != "2027-04-01" || selection.SupportedPeriod.Dates.To != "2027-04-15" {
		t.Fatal(selection, err)
	}
	// A source-relative transplant window cannot be selected for direct sowing.
	if _, err = s.Pool.Exec(ctx, `UPDATE planting_plans SET method='DIRECT_SOW' WHERE id=$1`, mustGet(t, s, a, in.PlanID).Row.ID); err != nil {
		t.Fatal(err)
	}
	in.ExpectedRevision = res.Revision
	_, ps, err = s.ConfirmSchedule(ctx, a, in, nil)
	assertCode(t, ps, err, "CONFLICT") // Context changed before confirmation.
}
func TestScheduleReadPaginationIsScopedAndHasNoResearchSideEffects(t *testing.T) {
	s, a, stub, p, _ := scheduleWorld(t)
	ctx := context.Background()
	s.Clock = fixedClock{time.Date(2026, 11, 15, 2, 30, 0, 0, time.UTC)}
	in := scheduleRequest(idString(p.Row.ID))
	in.First = 1
	before := count(t, s, "audit_revisions")
	page, err := s.Schedule(ctx, a, in)
	if err != nil || len(page.Windows) != 1 || !page.HasNext || page.EndCursor == nil || page.Today != "2026-11-14" || page.Timezone != "America/Chicago" || !page.GuidanceCurrent {
		t.Fatal(page, err)
	}
	if err = s.q().ReviewPlanningGuidanceLater(ctx, dbgen.ReviewPlanningGuidanceLaterParams{WorkspaceID: a.WorkspaceID, PlanID: p.Row.ID, ID: p.LatestGuidance.ID}); err != nil {
		t.Fatal(err)
	}
	in.After = page.EndCursor
	next, err := s.Schedule(ctx, a, in)
	if err != nil || next.HasNext || len(next.Windows) != 1 || next.Windows[0].WindowIndex != 1 || stub.calls.Load() != 1 || count(t, s, "audit_revisions") != before {
		t.Fatal(next, err)
	}
	for _, changed := range []ScheduleQuery{
		{PlanID: in.PlanID, Range: MonthRange{"2027-02", "2027-12"}, First: 1, After: in.After},
		{PlanID: in.PlanID, Range: in.Range, Assumptions: ScheduleAssumptions{Anchors: []ScheduleAnchor{{"LAST_FROST", "2027-04-01"}}}, First: 1, After: in.After},
	} {
		if _, err = s.Schedule(ctx, a, changed); !errors.Is(err, ErrInvalidCursor) {
			t.Fatal("cursor accepted a changed view", err)
		}
	}
	other := workspace(t, s)
	if private, err := s.Schedule(ctx, other, scheduleRequest(in.PlanID)); err != nil || private != nil {
		t.Fatal("private schedule enumerated", private, err)
	}
	stub.policy = "new context"
	in.After = nil
	stale, err := s.Schedule(ctx, a, in)
	if err != nil || stale.GuidanceCurrent || len(stale.Windows) != 0 || stale.TargetPeriod.Year == nil || *stale.TargetPeriod.Year != 2027 {
		t.Fatal("stale guidance replaced manual intent", stale, err)
	}
}
func TestScheduleAcceptedSeasonRetainsHemisphereAndRefreshNeverReschedules(t *testing.T) {
	season := Winter
	w := GuidanceWindow{Precision: "ANNUAL", Label: "Winter", Annual: &AnnualPeriod{Precision: "SEASON", Season: &season}, Hemisphere: ptr("NORTHERN")}
	s, a, _, _, in := scheduleWorld(t, w)
	ctx := context.Background()
	in.TargetPeriod = &Period{Precision: SeasonPrecision, Season: &SeasonPeriod{Winter, 2027}}
	r, ps, err := s.ConfirmSchedule(ctx, a, in, nil)
	if err != nil || len(ps) > 0 {
		t.Fatal(ps, err)
	}
	accepted := mustGet(t, s, a, in.PlanID)
	raw := string(accepted.Row.AcceptedSchedule)
	if _, err = s.Pool.Exec(ctx, `UPDATE workspace SET latitude=-33,longitude=151 WHERE id=$1`, a.WorkspaceID); err != nil {
		t.Fatal(err)
	}
	query := scheduleRequest(in.PlanID)
	query.Range = MonthRange{"2027-03", "2028-02"}
	page, err := s.Schedule(ctx, a, query)
	if err != nil || page.TargetDisplayMonths == nil || *page.TargetDisplayMonths != (MonthRange{"2027-12", "2028-02"}) || page.Windows[0].Resolution != "HEMISPHERE_MISMATCH" {
		t.Fatal("accepted season moved to new site's winter", page, err)
	}
	_, ps, err = s.RefreshGuidance(ctx, a, RevisionInput{ID: in.PlanID, ExpectedRevision: r.Revision}, nil)
	if err != nil || len(ps) > 0 {
		t.Fatal(ps, err)
	}
	if err = s.SweepGuidance(ctx); err != nil {
		t.Fatal(err)
	}
	after := mustGet(t, s, a, in.PlanID)
	if string(after.Row.AcceptedSchedule) != raw || after.Row.AcceptedGuidanceID != accepted.Row.AcceptedGuidanceID || !reflect.DeepEqual(PeriodFromRow(after.Row), in.TargetPeriod) {
		t.Fatal("refresh rewrote accepted choice")
	}
	// A manual date change deliberately detaches acceptance and its assumptions.
	_, ps, err = s.Update(ctx, a, UpdateInput{ID: in.PlanID, ExpectedRevision: after.Row.Revision, Intent: Intent{Name: after.Row.Name, Category: "FIG", Method: ptr("NURSERY_PLANT"), TargetPeriod: &Period{Precision: YearPrecision, Year: ptr(2028)}}}, nil)
	if err != nil || len(ps) > 0 {
		t.Fatal(ps, err)
	}
	after = mustGet(t, s, a, in.PlanID)
	if after.Row.AcceptedGuidanceID.Valid || len(after.Row.AcceptedSchedule) != 0 {
		t.Fatal("manual target kept stale acceptance")
	}
}
func TestScheduleConfirmationAuditFailureRollsBackAndConcurrentSmallPoolIsSafe(t *testing.T) {
	s, a, stub, _, in := scheduleWorld(t)
	ctx := context.Background()
	if _, err := s.Pool.Exec(ctx, `CREATE FUNCTION reject_schedule_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.action='confirmPlantingSchedule' THEN RAISE EXCEPTION 'fixture failure'; END IF; RETURN NEW; END $$; CREATE TRIGGER reject_schedule_audit BEFORE INSERT ON audit_revisions FOR EACH ROW EXECUTE FUNCTION reject_schedule_audit()`); err != nil {
		t.Fatal(err)
	}
	key := "audit-failed"
	if _, _, err := s.ConfirmSchedule(ctx, a, in, &key); err == nil {
		t.Fatal("audit failure was ignored")
	}
	if p := mustGet(t, s, a, in.PlanID); p.Row.AcceptedGuidanceID.Valid || p.Row.Revision != in.ExpectedRevision || count(t, s, "mutation_receipts") != 0 {
		t.Fatal("failed audit committed selection")
	}
	if _, err := s.Pool.Exec(ctx, `DROP TRIGGER reject_schedule_audit ON audit_revisions`); err != nil {
		t.Fatal(err)
	}
	config := s.Pool.Config().Copy()
	config.MaxConns = 2
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	s.Pool = pool
	stub.contextRead = func(ctx context.Context) error {
		_, err := dbgen.New(pool).KnowledgeSettings(ctx, a.WorkspaceID)
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	results := make(chan string, 12)
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			key := fmt.Sprintf("concurrent-%d", i)
			_, ps, err := s.ConfirmSchedule(ctx, a, in, &key)
			if err != nil {
				results <- err.Error()
				return
			}
			if len(ps) == 0 {
				results <- "OK"
				return
			}
			results <- ps[0].Code
		}(i)
	}
	wg.Wait()
	close(results)
	success := 0
	for result := range results {
		if result == "OK" {
			success++
		} else if result != "CONFLICT" {
			t.Fatal(result)
		}
	}
	if success != 1 || mustGet(t, s, a, in.PlanID).Row.Revision != in.ExpectedRevision+1 {
		t.Fatal("concurrent confirmations did not serialize", success)
	}
}

func TestScheduleConfirmationRejectsUnverifiedAndNonPlantingFacts(t *testing.T) {
	for _, kind := range []string{"unverified", "harvest"} {
		t.Run(kind, func(t *testing.T) {
			s, a, stub, _, in := scheduleWorld(t)
			stub.research = func(context.Context, GuidanceSubject) (GuidanceResult, error) {
				fact := scheduleFact(annualMonthWindow(3, 4))
				if kind == "unverified" {
					fact.SourceID = ""
				} else {
					fact.Phase = "HARVEST"
				}
				return GuidanceResult{State: "READY", Facts: []GuidanceFact{fact}}, nil
			}
			res, ps, err := s.RefreshGuidance(context.Background(), a, RevisionInput{ID: in.PlanID, ExpectedRevision: in.ExpectedRevision}, nil)
			if err != nil || len(ps) > 0 {
				t.Fatal(ps, err)
			}
			if err = s.SweepGuidance(context.Background()); err != nil {
				t.Fatal(err)
			}
			p := mustGet(t, s, a, in.PlanID)
			in.ExpectedRevision, in.SnapshotID, in.Generation = res.Revision, idString(p.LatestGuidance.ID), p.Row.GuidanceGeneration
			_, ps, err = s.ConfirmSchedule(context.Background(), a, in, nil)
			assertCode(t, ps, err, "VALIDATION")
			page, err := s.Schedule(context.Background(), a, scheduleRequest(in.PlanID))
			if err != nil {
				t.Fatal(err)
			}
			if kind == "unverified" && (page.GuidanceCurrent || len(page.Windows) != 0) {
				t.Fatal("unverified provenance was projected", page)
			}
			if kind == "harvest" && (len(page.Windows) != 1 || page.Windows[0].Selectable || !page.Windows[0].FirstHarvestUnknown) {
				t.Fatal("harvest became a planting choice", page)
			}
		})
	}
}
