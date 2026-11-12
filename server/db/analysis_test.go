// T02.ai data-contract acceptance suite: analysis_runs + suggestions
// constraints, generation fencing, semantic-key dedup, budget aggregation and
// keyset paging verified on real PostgreSQL 17 (PGTEST_DSN), one isolated
// schema per test via the shared mustPool harness from db_test.go.
package db_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	dbgen "canopy/server/db/generated"
)

const testHash = "sha256:" + "6d3f0c9b8a5e4c1f9a7b2d0e8c5f3a1b6d9e2c4f7a0b8d1e3c5f7a9b1d3e5f7c"

// insertRun seeds one QUEUED TEXT run through the frozen query surface.
func insertRun(t *testing.T, s seed, key string, reserved int64) dbgen.AnalysisRun {
	t.Helper()
	r, err := s.q.InsertAnalysisRun(context.Background(), dbgen.InsertAnalysisRunParams{
		ID: uid(), WorkspaceID: s.wsID, Kind: dbgen.AnalysisKindTEXT,
		Capability: "TEXT_ANALYZER", PlantID: s.plantID,
		InputSnapshot: []byte(`{"activityIds":[]}`), InputHash: testHash,
		SemanticKey: key, CostReservedMicroUsd: pgtype.Int8{Int64: reserved, Valid: true},
	})
	if err != nil {
		t.Fatalf("insert run: %v", err)
	}
	return r
}

func TestAnalysisRunLifecycleAndFencing(t *testing.T) {
	s := seedWorld(t)
	ctx := context.Background()
	r := insertRun(t, s, "TEXT:plant:"+s.plantID.String()+":h1", 4000)
	if r.State != dbgen.AnalysisRunStateQUEUED || r.Generation != 0 || r.Attempts != 0 || r.Revision != 1 {
		t.Fatalf("fresh run = state %s gen %d attempts %d rev %d", r.State, r.Generation, r.Attempts, r.Revision)
	}
	if r.StartedAt.Valid || r.FinishedAt.Valid {
		t.Fatal("QUEUED run must not carry timeline stamps")
	}
	// Claim bumps generation; a commit carrying the OLD generation must not land.
	claimed, err := s.q.ClaimAnalysisRun(ctx, dbgen.ClaimAnalysisRunParams{ID: r.ID, Generation: 0})
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if claimed.State != dbgen.AnalysisRunStateRUNNING || claimed.Generation != 1 || claimed.Attempts != 1 {
		t.Fatalf("claimed = gen %d attempts %d", claimed.Generation, claimed.Attempts)
	}
	result, _ := json.Marshal(map[string]any{"observations": []string{"leaf curl"}})
	if _, err := s.q.CommitAnalysisRun(ctx, dbgen.CommitAnalysisRunParams{
		ID: r.ID, Generation: 0, Result: result,
	}); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("stale-generation commit = %v, want ErrNoRows", err)
	}
	done, err := s.q.CommitAnalysisRun(ctx, dbgen.CommitAnalysisRunParams{
		ID: r.ID, Generation: 1, Result: result,
		Provider:      pgtype.Text{String: "configured-text-provider", Valid: true},
		Model:         pgtype.Text{String: "example-text-model-2026", Valid: true},
		PromptVersion: pgtype.Text{String: "p7", Valid: true}, ResultSchemaVersion: pgtype.Text{String: "r2", Valid: true},
		CostActualMicroUsd: pgtype.Int8{Int64: 2310, Valid: true},
	})
	if err != nil {
		t.Fatalf("commit: %v", err)
	}
	if done.State != dbgen.AnalysisRunStateSUCCEEDED || done.Result == nil {
		t.Fatalf("done = %+v", done)
	}
	if !done.FinishedAt.Valid || done.CostActualMicroUsd.Int64 != 2310 {
		t.Fatalf("done timeline/cost = %+v", done)
	}
	// Terminal run leaves the semantic key free again.
	if _, err := s.q.FindActiveRunBySemanticKey(ctx, dbgen.FindActiveRunBySemanticKeyParams{
		WorkspaceID: s.wsID, SemanticKey: "TEXT:plant:" + s.plantID.String() + ":h1",
	}); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("active lookup after terminal = %v, want ErrNoRows", err)
	}
	if r2 := insertRun(t, s, "TEXT:plant:"+s.plantID.String()+":h1", 4000); r2.ID == r.ID {
		t.Fatal("rerun after terminal must be a new row")
	}
}

func TestSemanticKeyDedupActiveOnly(t *testing.T) {
	s := seedWorld(t)
	ctx := context.Background()
	key := "TEXT:plant:" + s.plantID.String() + ":dup"
	a := insertRun(t, s, key, 4000)
	// Second ACTIVE run on the same semantic key: partial unique index rejects.
	_, err := s.q.InsertAnalysisRun(ctx, dbgen.InsertAnalysisRunParams{
		ID: uid(), WorkspaceID: s.wsID, Kind: dbgen.AnalysisKindTEXT,
		Capability: "TEXT_ANALYZER", PlantID: s.plantID,
		InputSnapshot: []byte(`{}`), InputHash: testHash, SemanticKey: key,
	})
	expectDBError(t, err, "analysis_runs_active_semantic_uk")
	// Cancelling the active run frees the key.
	if _, err := s.q.CancelAnalysisRun(ctx, dbgen.CancelAnalysisRunParams{
		ID: a.ID, WorkspaceID: s.wsID, Revision: a.Revision,
	}); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	if _, err := s.q.InsertAnalysisRun(ctx, dbgen.InsertAnalysisRunParams{
		ID: uid(), WorkspaceID: s.wsID, Kind: dbgen.AnalysisKindTEXT,
		Capability: "TEXT_ANALYZER", PlantID: s.plantID,
		InputSnapshot: []byte(`{}`), InputHash: testHash, SemanticKey: key,
	}); err != nil {
		t.Fatalf("insert after cancel: %v", err)
	}
}

func TestRunTargetChecks(t *testing.T) {
	s := seedWorld(t)
	ctx := context.Background()
	asset := func(purpose string) pgtype.UUID {
		a := uid()
		if _, err := s.pool.Exec(ctx,
			`INSERT INTO assets (id, workspace_id, purpose, state, byte_count, sha256, storage_key)
			 VALUES ($1,$2,$3,'READY',10,$4,$5)`,
			a, s.wsID, purpose, strings.Repeat("0a", 32),
			fmt.Sprintf("t02ai-%s-%x", purpose, a.Bytes)); err != nil {
			t.Fatalf("asset: %v", err)
		}
		return a
	}
	receipt := asset("RECEIPT")
	photoA, photoB := asset("PHOTO"), asset("PHOTO")

	base := func(kind dbgen.AnalysisKind) dbgen.InsertAnalysisRunParams {
		return dbgen.InsertAnalysisRunParams{
			ID: uid(), WorkspaceID: s.wsID, Kind: kind, Capability: "TEXT_ANALYZER",
			InputSnapshot: []byte(`{}`), InputHash: testHash,
			SemanticKey: fmt.Sprintf("t02ai-target-%s-%x", kind, uid().Bytes),
		}
	}
	cases := []struct {
		name  string
		p     dbgen.InsertAnalysisRunParams
		check string
	}{
		{"receipt-extraction-missing-receipt", func() dbgen.InsertAnalysisRunParams {
			p := base(dbgen.AnalysisKindRECEIPTEXTRACTION)
			return p
		}(), "chk_run_target_by_kind"},
		{"receipt-extraction-with-plant", func() dbgen.InsertAnalysisRunParams {
			p := base(dbgen.AnalysisKindRECEIPTEXTRACTION)
			p.ReceiptAssetID, p.PlantID = receipt, s.plantID
			return p
		}(), "chk_run_target_by_kind"},
		{"vision-single-photo", func() dbgen.InsertAnalysisRunParams {
			p := base(dbgen.AnalysisKindVISION)
			p.Capability, p.PlantID, p.PhotoIDA = "VISION_ANALYZER", s.plantID, photoA
			return p
		}(), "chk_run_target_by_kind"},
		{"vision-same-photo-twice", func() dbgen.InsertAnalysisRunParams {
			p := base(dbgen.AnalysisKindVISION)
			p.Capability, p.PlantID, p.PhotoIDA, p.PhotoIDB = "VISION_ANALYZER", s.plantID, photoA, photoA
			return p
		}(), "chk_run_photo_pair_distinct"},
		{"text-with-receipt", func() dbgen.InsertAnalysisRunParams {
			p := base(dbgen.AnalysisKindTEXT)
			p.PlantID, p.ReceiptAssetID = s.plantID, receipt
			return p
		}(), "chk_run_target_by_kind"},
		{"bad-capability-name", func() dbgen.InsertAnalysisRunParams {
			p := base(dbgen.AnalysisKindTEXT)
			p.PlantID, p.Capability = s.plantID, "MY_VIBE_CHECK"
			return p
		}(), "analysis_runs_capability_check"},
		{"bad-input-hash", func() dbgen.InsertAnalysisRunParams {
			p := base(dbgen.AnalysisKindTEXT)
			p.PlantID, p.InputHash = s.plantID, "md5:nope"
			return p
		}(), "analysis_runs_input_hash_check"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := s.q.InsertAnalysisRun(ctx, tc.p)
			expectDBError(t, err, tc.check)
		})
	}
	// The valid receipt-extraction shape lands.
	p := base(dbgen.AnalysisKindRECEIPTEXTRACTION)
	p.Capability, p.ReceiptAssetID = "RECEIPT_EXTRACTOR", receipt
	if _, err := s.q.InsertAnalysisRun(ctx, p); err != nil {
		t.Fatalf("valid receipt extraction: %v", err)
	}
	// As does the valid two-photo VISION shape.
	v := base(dbgen.AnalysisKindVISION)
	v.Capability, v.PlantID, v.PhotoIDA, v.PhotoIDB = "VISION_ANALYZER", s.plantID, photoA, photoB
	if _, err := s.q.InsertAnalysisRun(ctx, v); err != nil {
		t.Fatalf("valid vision pair: %v", err)
	}
}

func TestRunStateTimelineAndResultChecks(t *testing.T) {
	s := seedWorld(t)
	ctx := context.Background()
	r := insertRun(t, s, "TEXT:plant:"+s.plantID.String()+":timeline", 1000)
	// QUEUED row may not carry finished_at.
	_, err := s.pool.Exec(ctx, `UPDATE analysis_runs SET finished_at = now() WHERE id = $1`, r.ID)
	expectDBError(t, err, "chk_run_state_timeline")
	// SUCCEEDED requires a result.
	if _, err := s.q.ClaimAnalysisRun(ctx, dbgen.ClaimAnalysisRunParams{ID: r.ID, Generation: 0}); err != nil {
		t.Fatalf("claim: %v", err)
	}
	_, err = s.pool.Exec(ctx,
		`UPDATE analysis_runs SET state='SUCCEEDED', finished_at=now(), result=NULL WHERE id=$1`, r.ID)
	expectDBError(t, err, "chk_run_success_result")
	// FAILED requires a failure code.
	_, err = s.pool.Exec(ctx,
		`UPDATE analysis_runs SET state='FAILED', finished_at=now(), failure_code=NULL WHERE id=$1`, r.ID)
	expectDBError(t, err, "chk_run_failure_code")
	// Attempts bound: claims walk attempts to 3, the 4th claim finds no row.
	for i := 0; i < 2; i++ {
		if _, err := s.pool.Exec(ctx,
			`UPDATE analysis_runs SET state='RUNNING', finished_at=NULL, started_at=now() WHERE id=$1`, r.ID); err != nil {
			t.Fatalf("rearm: %v", err)
		}
		cur, err := s.q.GetAnalysisRun(ctx, dbgen.GetAnalysisRunParams{ID: r.ID, WorkspaceID: s.wsID})
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		if _, err := s.q.ClaimAnalysisRun(ctx, dbgen.ClaimAnalysisRunParams{ID: r.ID, Generation: cur.Generation}); err != nil {
			t.Fatalf("re-claim %d: %v", i, err)
		}
	}
	cur, _ := s.q.GetAnalysisRun(ctx, dbgen.GetAnalysisRunParams{ID: r.ID, WorkspaceID: s.wsID})
	if cur.Attempts != 3 {
		t.Fatalf("attempts = %d, want 3", cur.Attempts)
	}
	if _, err := s.q.ClaimAnalysisRun(ctx, dbgen.ClaimAnalysisRunParams{ID: r.ID, Generation: cur.Generation}); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("claim at attempts=%d = %v, want ErrNoRows", cur.Attempts, err)
	}
}

func TestCancelRevisionGuard(t *testing.T) {
	s := seedWorld(t)
	ctx := context.Background()
	r := insertRun(t, s, "TEXT:plant:"+s.plantID.String()+":cancel", 1000)
	if _, err := s.q.CancelAnalysisRun(ctx, dbgen.CancelAnalysisRunParams{
		ID: r.ID, WorkspaceID: s.wsID, Revision: r.Revision + 5,
	}); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("wrong-revision cancel = %v, want ErrNoRows", err)
	}
	c, err := s.q.CancelAnalysisRun(ctx, dbgen.CancelAnalysisRunParams{
		ID: r.ID, WorkspaceID: s.wsID, Revision: r.Revision,
	})
	if err != nil {
		t.Fatalf("cancel: %v", err)
	}
	if c.State != dbgen.AnalysisRunStateCANCELLED || !c.FinishedAt.Valid {
		t.Fatalf("cancelled = %+v", c)
	}
	// Reservation stays encumbered: cancel touched no cost columns.
	if c.CostReservedMicroUsd.Int64 != 1000 || c.CostActualMicroUsd.Valid {
		t.Fatalf("cost after cancel = %+v", c)
	}
	// Terminal: second cancel rejects on state.
	if _, err := s.q.CancelAnalysisRun(ctx, dbgen.CancelAnalysisRunParams{
		ID: r.ID, WorkspaceID: s.wsID, Revision: c.Revision,
	}); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("re-cancel = %v, want ErrNoRows", err)
	}
}

func TestStaleAndSettle(t *testing.T) {
	s := seedWorld(t)
	ctx := context.Background()
	r := insertRun(t, s, "TEXT:plant:"+s.plantID.String()+":stale", 1000)
	stale, err := s.q.MarkAnalysisRunStale(ctx, r.ID)
	if err != nil {
		t.Fatalf("stale: %v", err)
	}
	if stale.State != dbgen.AnalysisRunStateSTALE || stale.Generation != r.Generation+1 {
		t.Fatalf("stale = %+v", stale)
	}
	// A superseded claim can no longer commit.
	if _, err := s.q.CommitAnalysisRun(ctx, dbgen.CommitAnalysisRunParams{
		ID: r.ID, Generation: r.Generation, Result: []byte(`{}`),
	}); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("commit after stale = %v, want ErrNoRows", err)
	}
	// Post-dispatch reconciliation fills actual on a terminal run exactly once.
	settled, err := s.q.SettleAnalysisRunCost(ctx, dbgen.SettleAnalysisRunCostParams{
		ID: r.ID, CostActualMicroUsd: pgtype.Int8{Int64: 900, Valid: true},
	})
	if err != nil {
		t.Fatalf("settle: %v", err)
	}
	if settled.CostActualMicroUsd.Int64 != 900 || settled.State != dbgen.AnalysisRunStateSTALE {
		t.Fatalf("settled = %+v", settled)
	}
	if _, err := s.q.SettleAnalysisRunCost(ctx, dbgen.SettleAnalysisRunCostParams{
		ID: r.ID, CostActualMicroUsd: pgtype.Int8{Int64: 1, Valid: true},
	}); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("re-settle = %v, want ErrNoRows", err)
	}
}

func TestBudgetAggregation(t *testing.T) {
	s := seedWorld(t)
	ctx := context.Background()
	a := insertRun(t, s, "TEXT:plant:"+s.plantID.String()+":b1", 4000)
	insertRun(t, s, "TEXT:plant:"+s.plantID.String()+":b2", 1000)

	n, err := s.q.CountRunsSince(ctx, dbgen.CountRunsSinceParams{
		WorkspaceID: s.wsID, RequestedAt: pgtype.Timestamptz{Time: time.Now().Add(-time.Hour), Valid: true},
	})
	if err != nil || n != 2 {
		t.Fatalf("CountRunsSince = %d (%v), want 2", n, err)
	}
	monthStart := time.Now().UTC().Truncate(24 * time.Hour * 30)
	tot, err := s.q.BudgetMonthTotals(ctx, dbgen.BudgetMonthTotalsParams{
		WorkspaceID:   s.wsID,
		RequestedAt:   pgtype.Timestamptz{Time: monthStart, Valid: true},
		RequestedAt_2: pgtype.Timestamptz{Time: monthStart.Add(31 * 24 * time.Hour), Valid: true},
	})
	if err != nil {
		t.Fatalf("budget totals: %v", err)
	}
	if tot.ReservedTotal != 5000 {
		t.Fatalf("reserved = %d, want 5000", tot.ReservedTotal)
	}
	// Nothing reconciled yet: count 0 means the caller must report NULL, not 0.
	if tot.ActualCount != 0 {
		t.Fatalf("actual count = %d, want 0 (unreconciled)", tot.ActualCount)
	}
	// Reconcile one terminal run: count distinguishes real zero from unknown.
	if _, err := s.q.CancelAnalysisRun(ctx, dbgen.CancelAnalysisRunParams{
		ID: a.ID, WorkspaceID: s.wsID, Revision: a.Revision,
	}); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	if _, err := s.q.SettleAnalysisRunCost(ctx, dbgen.SettleAnalysisRunCostParams{
		ID: a.ID, CostActualMicroUsd: pgtype.Int8{Int64: 0, Valid: true},
	}); err != nil {
		t.Fatalf("settle zero: %v", err)
	}
	tot, err = s.q.BudgetMonthTotals(ctx, dbgen.BudgetMonthTotalsParams{
		WorkspaceID:   s.wsID,
		RequestedAt:   pgtype.Timestamptz{Time: monthStart, Valid: true},
		RequestedAt_2: pgtype.Timestamptz{Time: monthStart.Add(31 * 24 * time.Hour), Valid: true},
	})
	if err != nil {
		t.Fatalf("budget totals 2: %v", err)
	}
	if tot.ActualCount != 1 || tot.ActualTotal != 0 {
		t.Fatalf("after settle: count %d total %d, want 1/0 (zero is REAL here)", tot.ActualCount, tot.ActualTotal)
	}
	live, err := s.q.CountConcurrentRunsPerCapability(ctx, s.wsID)
	if err != nil || len(live) != 1 || live[0].Capability != "TEXT_ANALYZER" || live[0].LiveCount != 1 {
		t.Fatalf("live per capability = %+v (%v)", live, err)
	}
}

func TestSuggestionAcceptDismissReplay(t *testing.T) {
	cs := seedCareWorld(t)
	ctx := context.Background()
	s := seed{pool: cs.pool, ownerID: cs.ownerID, wsID: cs.wsID, plantID: cs.plantID, q: dbgen.New(cs.pool)}
	sg, err := s.q.InsertSuggestion(ctx, dbgen.InsertSuggestionParams{
		ID: uid(), WorkspaceID: cs.wsID, PlantID: cs.plantID,
		Kind: dbgen.SuggestionKindCARE, Severity: dbgen.SuggestionSeverityMEDIUM,
		Observation: pgtype.Text{String: "leaf curl on new growth", Valid: true},
		Hypothesis:  pgtype.Text{String: "possible aphid pressure", Valid: true},
		Action:      "Inspect undersides of new leaves and spray if aphids are present",
		EvidenceIds: []byte(`["a-0001"]`), MissingFacts: []byte(`["recent rainfall"]`),
		ExpiresAt: pgtype.Timestamptz{Time: time.Now().Add(72 * time.Hour), Valid: true},
	})
	if err != nil {
		t.Fatalf("insert suggestion: %v", err)
	}
	if sg.Status != dbgen.SuggestionStatusPROPOSED || sg.Revision != 1 || sg.NeedsReview {
		t.Fatalf("fresh suggestion = %+v", sg)
	}
	// Accept creates exactly one task; the same accept cannot replay.
	acc, err := s.q.AcceptSuggestion(ctx, dbgen.AcceptSuggestionParams{
		ID: sg.ID, WorkspaceID: cs.wsID, AcceptedTaskID: cs.occID, Revision: 1,
	})
	if err != nil {
		t.Fatalf("accept: %v", err)
	}
	if acc.Status != dbgen.SuggestionStatusACCEPTED || acc.AcceptedTaskID != cs.occID || acc.Revision != 2 {
		t.Fatalf("accepted = %+v", acc)
	}
	if _, err := s.q.AcceptSuggestion(ctx, dbgen.AcceptSuggestionParams{
		ID: sg.ID, WorkspaceID: cs.wsID, AcceptedTaskID: uid(), Revision: acc.Revision,
	}); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("re-accept = %v, want ErrNoRows", err)
	}
	// Stale revision on a second suggestion rejects with no rows (CONFLICT path).
	sg2, err := s.q.InsertSuggestion(ctx, dbgen.InsertSuggestionParams{
		ID: uid(), WorkspaceID: cs.wsID, PlantID: cs.plantID,
		Kind: dbgen.SuggestionKindHEALTH, Severity: dbgen.SuggestionSeverityLOW,
		Action: "Photograph the canopy from 1m",
	})
	if err != nil {
		t.Fatalf("insert sg2: %v", err)
	}
	if _, err := s.q.DismissSuggestion(ctx, dbgen.DismissSuggestionParams{
		ID: sg2.ID, WorkspaceID: cs.wsID, DismissReason: pgtype.Text{String: "already done", Valid: true}, Revision: 99,
	}); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("stale dismiss = %v, want ErrNoRows", err)
	}
	d, err := s.q.DismissSuggestion(ctx, dbgen.DismissSuggestionParams{
		ID: sg2.ID, WorkspaceID: cs.wsID, DismissReason: pgtype.Text{String: "already done", Valid: true}, Revision: 1,
	})
	if err != nil || d.Status != dbgen.SuggestionStatusDISMISSED || d.DismissReason.String != "already done" {
		t.Fatalf("dismiss = %+v (%v)", d, err)
	}
}

func TestSuggestionAcceptedTaskCheck(t *testing.T) {
	cs := seedCareWorld(t)
	ctx := context.Background()
	s := seed{pool: cs.pool, ownerID: cs.ownerID, wsID: cs.wsID, plantID: cs.plantID, q: dbgen.New(cs.pool)}
	sg, err := s.q.InsertSuggestion(ctx, dbgen.InsertSuggestionParams{
		ID: uid(), WorkspaceID: cs.wsID, PlantID: cs.plantID,
		Kind: dbgen.SuggestionKindOTHER, Severity: dbgen.SuggestionSeverityLOW, Action: "x",
	})
	if err != nil {
		t.Fatalf("insert: %v", err)
	}
	// Status/accepted-task pairing is a DB invariant, not caller courtesy.
	_, err = s.pool.Exec(ctx,
		`UPDATE suggestions SET accepted_task_id = $2 WHERE id = $1`, sg.ID, cs.occID)
	expectDBError(t, err, "chk_suggestion_accepted_task")
	// And ACCEPTED without a task is equally impossible.
	_, err = s.pool.Exec(ctx, `UPDATE suggestions SET status = 'ACCEPTED' WHERE id = $1`, sg.ID)
	expectDBError(t, err, "chk_suggestion_accepted_task")
}

func TestSuggestionExpirySweep(t *testing.T) {
	s := seedWorld(t)
	ctx := context.Background()
	sg, err := s.q.InsertSuggestion(ctx, dbgen.InsertSuggestionParams{
		ID: uid(), WorkspaceID: s.wsID, PlantID: s.plantID,
		Kind: dbgen.SuggestionKindCOST, Severity: dbgen.SuggestionSeverityMEDIUM,
		Action:    "Split the repotting bill across two months",
		ExpiresAt: pgtype.Timestamptz{Time: time.Now().Add(24 * time.Hour), Valid: true},
	})
	if err != nil {
		t.Fatalf("insert: %v", err)
	}
	// The CHECK pins expiry>created on UPDATE too, so age both sides in one
	// statement: expiry is past, created_at older still.
	if _, err := s.pool.Exec(ctx,
		`UPDATE suggestions SET created_at = now() - interval '3 days',
		 expires_at = now() - interval '1 hour' WHERE id = $1`, sg.ID); err != nil {
		t.Fatalf("age: %v", err)
	}
	ids, err := s.q.ExpireDueSuggestions(ctx)
	if err != nil || len(ids) != 1 || ids[0] != sg.ID {
		t.Fatalf("sweep = %v (%v)", ids, err)
	}
	got, err := s.q.GetSuggestion(ctx, dbgen.GetSuggestionParams{ID: sg.ID, WorkspaceID: s.wsID})
	if err != nil || got.Status != dbgen.SuggestionStatusEXPIRED || got.Revision != 2 {
		t.Fatalf("expired suggestion = %+v (%v)", got, err)
	}
	// Expired advice cannot be accepted (no task-creation path).
	if _, err := s.q.AcceptSuggestion(ctx, dbgen.AcceptSuggestionParams{
		ID: sg.ID, WorkspaceID: s.wsID, AcceptedTaskID: uid(), Revision: got.Revision,
	}); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("accept expired = %v, want ErrNoRows", err)
	}
	// Inserting an already-past expiry is rejected outright.
	_, err = s.q.InsertSuggestion(ctx, dbgen.InsertSuggestionParams{
		ID: uid(), WorkspaceID: s.wsID, PlantID: s.plantID,
		Kind: dbgen.SuggestionKindOTHER, Severity: dbgen.SuggestionSeverityLOW, Action: "x",
		ExpiresAt: pgtype.Timestamptz{Time: time.Now().Add(-time.Hour), Valid: true},
	})
	expectDBError(t, err, "chk_suggestion_expiry_future")
}

func TestSuggestionReviewFlag(t *testing.T) {
	cs := seedCareWorld(t)
	ctx := context.Background()
	s := seed{pool: cs.pool, ownerID: cs.ownerID, wsID: cs.wsID, plantID: cs.plantID, q: dbgen.New(cs.pool)}
	sg, err := s.q.InsertSuggestion(ctx, dbgen.InsertSuggestionParams{
		ID: uid(), WorkspaceID: cs.wsID, PlantID: cs.plantID,
		Kind: dbgen.SuggestionKindHEALTH, Severity: dbgen.SuggestionSeverityHIGH,
		Action: "Treat for scale now",
	})
	if err != nil {
		t.Fatalf("insert: %v", err)
	}
	// Source retirement / context change flags pending advice and bumps revision.
	ids, err := s.q.FlagSuggestionsNeedingReview(ctx, dbgen.FlagSuggestionsNeedingReviewParams{
		WorkspaceID: cs.wsID, RunIds: []pgtype.UUID{}, PlantIds: []pgtype.UUID{cs.plantID},
	})
	if err != nil || len(ids) != 1 || ids[0] != sg.ID {
		t.Fatalf("flag = %v (%v)", ids, err)
	}
	// In-flight accept on the old revision fails (CONFLICT, not silent staleness).
	if _, err := s.q.AcceptSuggestion(ctx, dbgen.AcceptSuggestionParams{
		ID: sg.ID, WorkspaceID: cs.wsID, AcceptedTaskID: cs.occID, Revision: 1,
	}); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("accept flagged on old rev = %v, want ErrNoRows", err)
	}
	// Even at the current revision, needs_review blocks acceptance.
	if _, err := s.q.AcceptSuggestion(ctx, dbgen.AcceptSuggestionParams{
		ID: sg.ID, WorkspaceID: cs.wsID, AcceptedTaskID: cs.occID, Revision: 2,
	}); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("accept needs_review = %v, want ErrNoRows", err)
	}
	// Explicit owner review touch is the only path back to acceptable.
	rev, err := s.q.ReviewSuggestion(ctx, dbgen.ReviewSuggestionParams{
		ID: sg.ID, WorkspaceID: cs.wsID, Revision: 2,
	})
	if err != nil || rev.NeedsReview || rev.Revision != 3 {
		t.Fatalf("review = %+v (%v)", rev, err)
	}
	if _, err := s.q.AcceptSuggestion(ctx, dbgen.AcceptSuggestionParams{
		ID: sg.ID, WorkspaceID: cs.wsID, AcceptedTaskID: cs.occID, Revision: 3,
	}); err != nil {
		t.Fatalf("accept after review: %v", err)
	}
}

func TestSuggestionsKeysetPaging(t *testing.T) {
	s := seedWorld(t)
	ctx := context.Background()
	var ids []pgtype.UUID
	for i := 0; i < 3; i++ {
		sg, err := s.q.InsertSuggestion(ctx, dbgen.InsertSuggestionParams{
			ID: uid(), WorkspaceID: s.wsID, PlantID: s.plantID,
			Kind: dbgen.SuggestionKindCARE, Severity: dbgen.SuggestionSeverityLOW,
			Action: "water",
		})
		if err != nil {
			t.Fatalf("insert %d: %v", i, err)
		}
		ids = append(ids, sg.ID)
	}
	page1, err := s.q.ListSuggestionsPage(ctx, dbgen.ListSuggestionsPageParams{
		WorkspaceID: s.wsID, Statuses: []string{"PROPOSED"}, LimitCount: 2,
	})
	if err != nil || len(page1) != 2 {
		t.Fatalf("page1 = %d rows (%v)", len(page1), err)
	}
	page2, err := s.q.ListSuggestionsPage(ctx, dbgen.ListSuggestionsPageParams{
		WorkspaceID: s.wsID, Statuses: []string{"PROPOSED"},
		AfterCreatedAt: page1[len(page1)-1].CreatedAt, AfterID: page1[len(page1)-1].ID,
		LimitCount: 2,
	})
	if err != nil || len(page2) != 1 {
		t.Fatalf("page2 = %d rows (%v)", len(page2), err)
	}
	seen := map[pgtype.UUID]bool{}
	for _, sg := range append(page1, page2...) {
		seen[sg.ID] = true
	}
	for _, id := range ids {
		if !seen[id] {
			t.Fatalf("suggestion %v missing from paged walk", id)
		}
	}
	// Kind filter excludes the CARE rows entirely.
	only, err := s.q.ListSuggestionsPage(ctx, dbgen.ListSuggestionsPageParams{
		WorkspaceID: s.wsID, Kinds: []string{"HEALTH"}, LimitCount: 10,
	})
	if err != nil || len(only) != 0 {
		t.Fatalf("kind filter = %d rows (%v), want 0", len(only), err)
	}
}

func TestJournalInsightEnumValue(t *testing.T) {
	s := seedWorld(t)
	ctx := context.Background()
	// 00004 stages the INSIGHT journal_entry_type value (CARE pattern): the
	// projection writer itself lands with T33.
	if _, err := s.pool.Exec(ctx,
		`INSERT INTO journal_entries (id, workspace_id, plant_id, entry_type, source_id, occurred_at, summary)
		 VALUES ($1,$2,$3,'INSIGHT',$4, now(), 'Accepted: treat for scale')`,
		uid(), s.wsID, s.plantID, uid()); err != nil {
		t.Fatalf("INSIGHT journal row: %v", err)
	}
}
