package planning

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	dbgen "canopy/server/db/generated"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

type guidanceStub struct {
	mu            sync.Mutex
	policy, state string
	calls         atomic.Int32
	contextRead   func(context.Context) error
	research      func(context.Context, GuidanceSubject) (GuidanceResult, error)
}

func (g *guidanceStub) Context(ctx context.Context, ws pgtype.UUID, subject GuidanceSubject) (GuidanceContext, error) {
	if g.contextRead != nil {
		if err := g.contextRead(ctx); err != nil {
			return GuidanceContext{}, err
		}
	}
	g.mu.Lock()
	policy, state := g.policy, g.state
	g.mu.Unlock()
	if state == "" {
		state = "PENDING"
	}
	fingerprint, _ := requestHash([]any{policy, idString(ws), subject, state})
	return GuidanceContext{Fingerprint: fingerprint, State: state, Message: "Synthetic research context", Assumptions: []string{}}, nil
}
func (g *guidanceStub) Research(ctx context.Context, _ pgtype.UUID, subject GuidanceSubject, _ GuidanceContext) (GuidanceResult, error) {
	g.calls.Add(1)
	if g.research != nil {
		return g.research(ctx, subject)
	}
	return readyGuidance(), nil
}
func readyGuidance() GuidanceResult {
	return GuidanceResult{State: "READY", Message: "Synthetic verified fixture", Facts: []GuidanceFact{{ID: "fixture-fact", Phase: "PLANT_OUT", Label: "Planting window", Summary: "Synthetic fixture only, not planting advice.", Conditions: []string{}, Windows: []GuidanceWindow{{Precision: "ANNUAL", Label: "December through February", Annual: &AnnualPeriod{Precision: "MONTH_RANGE", Months: &AnnualMonthRange{From: 12, To: 2}}}}, SourceTitle: "Synthetic publication", SourceURL: "https://fixture.example.invalid/guide", Publisher: "Fixture", SourceRegion: "Test region", RetrievedAt: time.Now().UTC(), ContentSHA256: strings.Repeat("a", 64)}}, Assumptions: []string{}}
}
func TestGuidanceAutomaticOutboxRefreshAndAcceptedHistory(t *testing.T) {
	s, a := scratch(t)
	ctx := context.Background()
	stub := &guidanceStub{}
	s.Guidance = stub
	intent := Intent{Name: "Future fig", Category: "FIG", Method: ptr("NURSERY_PLANT"), TargetPeriod: &Period{Precision: YearPrecision, Year: ptr(2027)}}
	r := mustCreate(t, s, a, intent)
	p := mustGet(t, s, a, r.ID)
	if p.LatestGuidance == nil || p.LatestGuidance.State != "PENDING" || p.Row.Revision != 1 {
		t.Fatal("no automatic outbox", p)
	}
	same, ps, err := s.RefreshGuidance(ctx, a, RevisionInput{ID: r.ID, ExpectedRevision: 1}, ptr("pending-refresh"))
	if err != nil || len(ps) > 0 || same.Revision != 1 || count(t, s, "planting_guidance_snapshots") != 1 {
		t.Fatal("pending refresh duplicated work", same, ps, err)
	}
	if err = s.SweepGuidance(ctx); err != nil {
		t.Fatal(err)
	}
	p = mustGet(t, s, a, r.ID)
	accepted := p.LatestGuidance.ID
	if p.LatestGuidance.State != "READY" || stub.calls.Load() != 1 || p.Row.TargetYear.Int32 != 2027 || p.Row.Revision != 1 {
		t.Fatal("research modified intent", p)
	}
	if _, err = s.Pool.Exec(ctx, `UPDATE planting_plans SET accepted_guidance_id=$1 WHERE id=$2`, accepted, mustID(r.ID)); err != nil {
		t.Fatal(err)
	}
	r, ps, err = s.RefreshGuidance(ctx, a, RevisionInput{ID: r.ID, ExpectedRevision: 1}, ptr("refresh-ready"))
	if err != nil || len(ps) > 0 || r.Revision != 2 {
		t.Fatal(r, ps, err)
	}
	p = mustGet(t, s, a, r.ID)
	if p.Row.GuidanceGeneration != 2 || p.AcceptedGuidance == nil || p.AcceptedGuidance.ID != accepted {
		t.Fatal("refresh lost accepted provenance")
	}
	if err = s.SweepGuidance(ctx); err != nil {
		t.Fatal(err)
	}
	p = mustGet(t, s, a, r.ID)
	if p.LatestGuidance.State != "READY" || p.AcceptedGuidance.ID != accepted || count(t, s, "planting_guidance_snapshots") != 2 {
		t.Fatal("refresh overwrote source history")
	}
	intent.Name = "Renamed fig"
	r, ps, err = s.Update(ctx, a, UpdateInput{ID: r.ID, ExpectedRevision: r.Revision, Intent: intent}, nil)
	if err != nil || len(ps) > 0 {
		t.Fatal(ps, err)
	}
	p = mustGet(t, s, a, r.ID)
	previousContext := p.LatestGuidance.ContextSha256
	if p.LatestGuidance.State != "PENDING" || p.AcceptedGuidance == nil {
		t.Fatal("context edit discarded accepted dates")
	}
	intent.TargetPeriod = &Period{Precision: YearPrecision, Year: ptr(2028)}
	_, ps, err = s.Update(ctx, a, UpdateInput{ID: r.ID, ExpectedRevision: r.Revision, Intent: intent}, nil)
	if err != nil || len(ps) > 0 {
		t.Fatal(ps, err)
	}
	p = mustGet(t, s, a, r.ID)
	if p.AcceptedGuidance != nil || p.Row.TargetYear.Int32 != 2028 || p.LatestGuidance.ContextSha256 != previousContext {
		t.Fatal("projection year polluted publication context")
	}
	if count(t, s, "plants") != 0 || count(t, s, "activities") != 0 || count(t, s, "care_plans") != 0 {
		t.Fatal("research created real garden records")
	}
}
func TestGuidanceLateResultsCannotAttachAfterEditsSettingsOrCancel(t *testing.T) {
	for _, mode := range []string{"edit", "settings", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			s, a := scratch(t)
			ctx := context.Background()
			entered := make(chan struct{})
			release := make(chan struct{})
			unblock := sync.OnceFunc(func() { close(release) })
			defer unblock()
			stub := &guidanceStub{research: func(ctx context.Context, _ GuidanceSubject) (GuidanceResult, error) {
				close(entered)
				select {
				case <-release:
					return readyGuidance(), nil
				case <-ctx.Done():
					return GuidanceResult{}, ctx.Err()
				}
			}}
			s.Guidance = stub
			r := mustCreate(t, s, a, Intent{Name: "Old fig", Category: "FIG"})
			done := make(chan error, 1)
			go func() { done <- s.SweepGuidance(ctx) }()
			select {
			case <-entered:
			case <-time.After(5 * time.Second):
				t.Fatal("research did not start")
			}
			switch mode {
			case "edit":
				_, ps, err := s.Update(ctx, a, UpdateInput{ID: r.ID, ExpectedRevision: 1, Intent: Intent{Name: "New fig", Category: "FIG"}}, nil)
				if err != nil || len(ps) > 0 {
					t.Fatal(ps, err)
				}
			case "settings":
				stub.mu.Lock()
				stub.policy = "new region"
				stub.mu.Unlock()
				if err := s.q().InvalidateWorkspacePlanningGuidance(ctx, a.WorkspaceID); err != nil {
					t.Fatal(err)
				}
			case "cancel":
				_, ps, err := s.Cancel(ctx, a, RevisionInput{ID: r.ID, ExpectedRevision: 1}, nil)
				if err != nil || len(ps) > 0 {
					t.Fatal(ps, err)
				}
			}
			unblock()
			if err := <-done; err != nil {
				t.Fatal(err)
			}
			var state string
			if err := s.Pool.QueryRow(ctx, `SELECT state::text FROM planting_guidance_snapshots WHERE plan_id=$1 AND generation=1`, mustID(r.ID)).Scan(&state); err != nil || state != "FAILED" {
				t.Fatal("late result remained current", state, err)
			}
			p := mustGet(t, s, a, r.ID)
			if p.LatestGuidance != nil && p.LatestGuidance.State == "READY" {
				t.Fatal("stale READY attached")
			}
		})
	}
}
func TestGuidanceExpiredLeaseRejectsEarlierAttemptAndReadyMetadataIsImmutable(t *testing.T) {
	s, a := scratch(t)
	ctx := context.Background()
	s.Guidance = &guidanceStub{}
	r := mustCreate(t, s, a, Intent{Name: "Lease fig", Category: "FIG"})
	row := mustGet(t, s, a, r.ID).Row
	first, plan, c, err := s.prepareGuidance(ctx, row, true)
	if err != nil || first == nil {
		t.Fatal(first, err)
	}
	if _, err = s.Pool.Exec(ctx, `UPDATE planting_guidance_snapshots SET next_check_at=now()-interval '1 minute' WHERE id=$1`, first.ID); err != nil {
		t.Fatal(err)
	}
	second, _, _, err := s.prepareGuidance(ctx, row, true)
	if err != nil || second == nil {
		t.Fatal(second, err)
	}
	if err = s.finishGuidance(ctx, plan, *first, c, GuidanceResult{State: "FAILED", Message: "Old failed attempt"}); err != nil {
		t.Fatal(err)
	}
	p := mustGet(t, s, a, r.ID)
	if p.LatestGuidance.State != "PROCESSING" {
		t.Fatal("old attempt overwrote new lease")
	}
	if err = s.finishGuidance(ctx, plan, *second, c, readyGuidance()); err != nil {
		t.Fatal(err)
	}
	p = mustGet(t, s, a, r.ID)
	before := *p.LatestGuidance
	if before.State != "READY" {
		t.Fatal(before.State)
	}
	if err = s.q().ReviewPlanningGuidanceLater(ctx, dbgen.ReviewPlanningGuidanceLaterParams{WorkspaceID: a.WorkspaceID, PlanID: row.ID, ID: before.ID}); err != nil {
		t.Fatal(err)
	}
	after := mustGet(t, s, a, r.ID).LatestGuidance
	if string(before.Facts) != string(after.Facts) || !before.UpdatedAt.Time.Equal(after.UpdatedAt.Time) {
		t.Fatal("operational review rewrote provenance")
	}
	if _, err = s.Pool.Exec(ctx, `UPDATE planting_guidance_snapshots SET message='rewrite' WHERE id=$1`, before.ID); err == nil {
		t.Fatal("READY content became mutable")
	}
}
func TestGuidanceBoundedBatchFailureAndManualStates(t *testing.T) {
	s, a := scratch(t)
	ctx := context.Background()
	stub := &guidanceStub{}
	s.Guidance = stub
	for i := 0; i < 5; i++ {
		mustCreate(t, s, a, Intent{Name: fmt.Sprintf("Fig %d", i), Category: "FIG"})
	}
	if err := s.SweepGuidance(ctx); err != nil {
		t.Fatal(err)
	}
	if stub.calls.Load() != 2 {
		t.Fatal("unbounded research batch", stub.calls.Load())
	}
	stub.research = func(context.Context, GuidanceSubject) (GuidanceResult, error) {
		return GuidanceResult{}, errors.New("private-provider-secret must not persist")
	}
	if err := s.SweepGuidance(ctx); err != nil {
		t.Fatal(err)
	}
	var leaked int
	if err := s.Pool.QueryRow(ctx, `SELECT count(*) FROM planting_guidance_snapshots WHERE message LIKE '%private-provider-secret%'`).Scan(&leaked); err != nil || leaked != 0 {
		t.Fatal("provider error leaked", err)
	}
	for _, state := range []string{"DISABLED", "NEEDS_LOCATION", "UNSUPPORTED", "FAILED"} {
		stub.mu.Lock()
		stub.state = state
		stub.mu.Unlock()
		r := mustCreate(t, s, a, Intent{Name: "Manual " + state})
		p := mustGet(t, s, a, r.ID)
		if string(p.LatestGuidance.State) != state || p.Row.TargetPrecision.Valid {
			t.Fatal("manual state lost plan", p)
		}
	}
}
func TestGuidanceContextReadsDoNotExhaustSmallPoolDuringConcurrentSaves(t *testing.T) {
	s, a := scratch(t)
	config := s.Pool.Config().Copy()
	config.MaxConns = 2
	pool, err := pgxpool.NewWithConfig(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	s.Pool = pool
	stub := &guidanceStub{contextRead: func(ctx context.Context) error {
		_, err := dbgen.New(pool).KnowledgeSettings(ctx, a.WorkspaceID)
		return err
	}}
	s.Guidance = stub
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	errorsCh := make(chan error, 12)
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, ps, err := s.Create(ctx, a, CreateInput{Intent: Intent{Name: fmt.Sprintf("Concurrent %d", i)}}, nil)
			if err == nil && len(ps) > 0 {
				err = fmt.Errorf("%v", ps)
			}
			errorsCh <- err
		}(i)
	}
	wg.Wait()
	close(errorsCh)
	for err := range errorsCh {
		if err != nil {
			t.Fatal("save pool starvation", err)
		}
	}
	if count(t, s, "planting_plans") != 12 || count(t, s, "planting_guidance_snapshots") != 12 {
		t.Fatal("lost concurrent saves")
	}
}

func TestGuidanceInvalidProviderResultFailsHonestlyWithoutLosingPlan(t *testing.T) {
	s, a := scratch(t)
	s.Guidance = &guidanceStub{research: func(context.Context, GuidanceSubject) (GuidanceResult, error) {
		r := readyGuidance()
		r.Facts[0].SourceURL = "javascript:unsupported"
		return r, nil
	}}
	id := mustCreate(t, s, a, Intent{Name: "Manual retained plan"}).ID
	if err := s.SweepGuidance(context.Background()); err != nil {
		t.Fatal(err)
	}
	p := mustGet(t, s, a, id)
	if p.LatestGuidance.State != "FAILED" || string(p.LatestGuidance.Facts) != "[]" || p.Row.Revision != 1 {
		t.Fatal("invalid result did not fail cleanly", p)
	}
}
