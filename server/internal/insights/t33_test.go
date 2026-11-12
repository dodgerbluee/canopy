// T33 acceptance: cost/care/history synthesis (snapshot cost section
// matching the money ledger, the qualified-language causal guard, the
// suggestion projection at commit) and the owner commands over advice
// rows (accept creates exactly one explicit task, dismiss, expiry,
// review-touch confirmation, keyset paging). Real PostgreSQL 17.
package insights

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	dbgen "canopy/server/db/generated"
	"canopy/server/internal/platform"
)

// ---- t33 fixtures -----------------------------------------------------------

func numericScan(t *testing.T, s string) pgtype.Numeric {
	t.Helper()
	var n pgtype.Numeric
	if err := n.Scan(s); err != nil {
		t.Fatalf("numeric %q: %v", s, err)
	}
	return n
}

// seedPurchase wires purchase -> line -> allocation to the world's plant,
// the same shape the money lane writes, so the snapshot cost section and
// the ledger read see identical rows.
func seedPurchase(t *testing.T, ctx context.Context, pool *pgxpool.Pool, w world,
	currency, minor, on string,
) pgtype.UUID {
	t.Helper()
	ids := platform.UUIDv4{}
	pid, lid, aid := ids.New(), ids.New(), ids.New()
	if _, err := pool.Exec(ctx,
		`INSERT INTO purchases (id, workspace_id, seller, purchased_on, currency, total_minor, shipping_minor, tax_minor)
		 VALUES ($1,$2,'Nursery',$3,$4,$5,0,0)`,
		pid, w.WorkspaceID, on, currency, numericScan(t, minor)); err != nil {
		t.Fatalf("purchase: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO purchase_lines (id, workspace_id, purchase_id, description, quantity, line_total_minor)
		 VALUES ($1,$2,$3,'soil',1,$4)`,
		lid, w.WorkspaceID, pid, numericScan(t, minor)); err != nil {
		t.Fatalf("line: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO purchase_allocations (id, workspace_id, purchase_line_id, plant_id, line_minor, tax_minor, shipping_minor)
		 VALUES ($1,$2,$3,$4,$5,0,0)`,
		aid, w.WorkspaceID, lid, w.PlantID, numericScan(t, minor)); err != nil {
		t.Fatalf("allocation: %v", err)
	}
	return pid
}

func seedPlanT33(t *testing.T, ctx context.Context, pool *pgxpool.Pool, w world) dbgen.CarePlan {
	t.Helper()
	plan, err := dbgen.New(pool).InsertCarePlan(ctx, dbgen.InsertCarePlanParams{
		ID: platform.UUIDv4{}.New(), WorkspaceID: w.WorkspaceID, PlantID: w.PlantID,
		Timezone: "UTC", Enabled: true, NotificationTime: "09:00",
	})
	if err != nil {
		t.Fatalf("care plan: %v", err)
	}
	return plan
}

func synthReq(w world) RequestInput {
	return RequestInput{Kind: "SYNTHESIS", PlantID: uuidString(w.PlantID)}
}

func seedSuggestion(t *testing.T, ctx context.Context, pool *pgxpool.Pool, w world,
	kind dbgen.SuggestionKind, action string, expires time.Time,
) dbgen.Suggestion {
	t.Helper()
	s, err := dbgen.New(pool).InsertSuggestion(ctx, dbgen.InsertSuggestionParams{
		ID: platform.UUIDv4{}.New(), WorkspaceID: w.WorkspaceID, PlantID: w.PlantID,
		Kind: kind, Severity: dbgen.SuggestionSeverityMEDIUM, Action: action,
		EvidenceIds: []byte(`[]`), MissingFacts: []byte(`[]`),
		ExpiresAt: pgtype.Timestamptz{Time: expires, Valid: true},
	})
	if err != nil {
		t.Fatalf("suggestion: %v", err)
	}
	return s
}

func reloadSuggestion(t *testing.T, ctx context.Context, pool *pgxpool.Pool, w world, id pgtype.UUID) dbgen.Suggestion {
	t.Helper()
	s, err := dbgen.New(pool).GetSuggestion(ctx, dbgen.GetSuggestionParams{ID: id, WorkspaceID: w.WorkspaceID})
	if err != nil {
		t.Fatalf("get suggestion: %v", err)
	}
	return s
}

// expireNow backdates the row past its expiry. The DB check keeps
// expires_at after created_at, so both timestamps move together — the
// same shape real time produces.
func expireNow(t *testing.T, ctx context.Context, pool *pgxpool.Pool, id pgtype.UUID) {
	t.Helper()
	if _, err := pool.Exec(ctx,
		`UPDATE suggestions SET created_at = now() - interval '2 days',
		 expires_at = now() - interval '1 day' WHERE id=$1`, id); err != nil {
		t.Fatalf("expire: %v", err)
	}
}

func snapshotFor(ctx context.Context, pool *pgxpool.Pool, w world, includeCosts bool) *Snapshot {
	q := dbgen.New(pool)
	ws := mustRow(q.GetWorkspaceByID(ctx, w.WorkspaceID))
	plant := mustRow(q.GetPlant(ctx, dbgen.GetPlantParams{ID: w.PlantID, WorkspaceID: w.WorkspaceID}))
	snap, err := BuildSnapshot(ctx, q, ws, plant, includeCosts)
	if err != nil {
		panic("snapshot: " + err.Error())
	}
	return snap
}

// ---- snapshot cost section ---------------------------------------------------

func TestT33SnapshotCostsMatchLedger(t *testing.T) {
	ctx, pool := scratch(t)
	w := seedWorld(t, ctx, pool)
	p1 := seedPurchase(t, ctx, pool, w, "USD", "1200", "2026-01-15")
	p2 := seedPurchase(t, ctx, pool, w, "USD", "3450", "2026-02-20")
	p3 := seedPurchase(t, ctx, pool, w, "EUR", "999", "2026-03-01")

	snap := snapshotFor(ctx, pool, w, true)
	if len(snap.Costs) != 3 {
		t.Fatalf("cost rows: %d", len(snap.Costs))
	}
	if len(snap.CostTotals) != 2 {
		t.Fatalf("cost totals: %d", len(snap.CostTotals))
	}
	snapTotals := map[string]string{}
	for _, tt := range snap.CostTotals {
		snapTotals[tt.Currency] = tt.TotalMinor
	}
	if snapTotals["USD"] != "4650" || snapTotals["EUR"] != "999" {
		t.Fatalf("snapshot totals: %v", snapTotals)
	}
	// The money read path is the same arithmetic; equality is by
	// construction, and this test pins it against drift.
	led, err := dbgen.New(pool).PlantLifetimeSpendingByCurrency(ctx, dbgen.PlantLifetimeSpendingByCurrencyParams{
		WorkspaceID: w.WorkspaceID, PlantID: w.PlantID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(led) != 2 {
		t.Fatalf("ledger rows: %d", len(led))
	}
	for _, l := range led {
		if snapTotals[l.Currency] != numericText(l.TotalMinor) {
			t.Fatalf("snapshot %s=%s vs ledger %s=%s", l.Currency, snapTotals[l.Currency],
				l.Currency, numericText(l.TotalMinor))
		}
	}
	// Purchase ids are evidence: a synthesis evidence/citation ref to a
	// purchase closes against the snapshot.
	ev := snap.EvidenceIDSet()
	for _, p := range []pgtype.UUID{p1, p2, p3} {
		if !ev[uuidString(p)] {
			t.Fatalf("purchase %s not in evidence set", uuidString(p))
		}
	}

	// Non-synthesis snapshots never carry money data.
	snap2 := snapshotFor(ctx, pool, w, false)
	if len(snap2.Costs) != 0 || len(snap2.CostTotals) != 0 {
		t.Fatalf("non-synthesis snapshot leaked costs: %d/%d", len(snap2.Costs), len(snap2.CostTotals))
	}
}

// ---- qualified-language guard -------------------------------------------------

func TestT33CausalGuardRejectsAssertedCausation(t *testing.T) {
	ctx, pool := scratch(t)
	w := seedWorld(t, ctx, pool)
	snap := snapshotFor(ctx, pool, w, true)

	hypJSON := func(hyp string) json.RawMessage {
		return json.RawMessage(fmt.Sprintf(
			`{"visibleObservations":[{"text":"Leaf edges are pale.","evidenceIds":[]}],`+
				`"hypotheses":[{"text":%q,"confidence":"MEDIUM","evidenceIds":[]}],`+
				`"missingInformation":[],"followUpChecks":[],"citations":[]}`, hyp))
	}
	for _, hyp := range []string{
		"Overwatering caused the yellow leaves.",
		"Root rot is responsible for the decline.",
		"The pale edges are due to a nutrient shortfall.",
		"Aeration deficit led to the collapse.",
	} {
		_, err := ValidateResult(dbgen.AnalysisKindSYNTHESIS, snap, hypJSON(hyp), 1<<20)
		var ve *ValidationError
		if !errors.As(err, &ve) {
			t.Fatalf("asserted causation accepted: %q -> %v", hyp, err)
		}
	}
	// Qualified correlations stay valid output.
	for _, hyp := range []string{
		"Overwatering may be a factor; the pattern correlates with the recent watering change.",
		"Nitrogen uptake appears limited; this is consistent with the pale margins.",
	} {
		if _, err := ValidateResult(dbgen.AnalysisKindSYNTHESIS, snap, hypJSON(hyp), 1<<20); err != nil {
			t.Fatalf("qualified hypothesis rejected: %q -> %v", hyp, err)
		}
	}
	// The guard is synthesis-scoped: plain observation summaries keep the
	// old rules (they do not synthesize correlations into advice).
	if _, err := ValidateResult(dbgen.AnalysisKindTEXT, snap,
		hypJSON("Overwatering caused the yellow leaves."), 1<<20); err != nil {
		t.Fatalf("TEXT runs should not carry the synthesis guard: %v", err)
	}
}

// ---- projection at commit ------------------------------------------------------

func TestT33SynthesisProjectionCapsAndCosts(t *testing.T) {
	ctx, pool := scratch(t)
	w := seedWorld(t, ctx, pool)
	p1 := seedPurchase(t, ctx, pool, w, "USD", "1200", "2026-01-15")
	p2 := seedPurchase(t, ctx, pool, w, "EUR", "500", "2026-02-01")

	fake := newFakePort()
	checks := make([]string, 7)
	for i := range checks {
		checks[i] = fmt.Sprintf("Check soil moisture at depth %d.", i+1)
	}
	fake.resp = func(in AnalyzeInput, call int) (AnalyzeOutput, error) {
		body, _ := json.Marshal(ObsResult{
			VisibleObservations: []Observation{{Text: "Leaf edges are pale.", EvidenceIDs: []string{}}},
			Hypotheses:          []Hypothesis{{Text: "Watering may be inconsistent.", Confidence: "MEDIUM", EvidenceIDs: []string{}}},
			MissingInformation:  []string{"soil moisture reading"},
			FollowUpChecks:      checks,
			Citations:           []Citation{},
		})
		return AnalyzeOutput{Raw: body, Provider: "fakeprov", Model: "fake-model",
			UsageCostMicroUSD: ptr64(1000)}, nil
	}
	s := newSvc(pool, defaultLimits(), fake)

	run, probs, err := s.Request(ctx, w.Actor, synthReq(w), nil)
	noProbs(t, probs, err)
	if err := s.ProcessRun(ctx, jobFor(run.ID)); err != nil {
		t.Fatalf("process: %v", err)
	}
	done := getRun(t, ctx, pool, run.ID)
	if done.State != dbgen.AnalysisRunStateSUCCEEDED {
		t.Fatalf("run state: %s failure=%+v msg=%s", done.State, done.FailureCode, done.FailureMessage.String)
	}

	rows, err := s.RunSuggestions(ctx, w.Actor, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	// 7 follow-ups cap to 5 CARE; 2 currencies add 2 COST; total cap 8 not hit.
	if len(rows) != 7 {
		t.Fatalf("suggestions: %d", len(rows))
	}
	var care, cost []dbgen.Suggestion
	for _, r := range rows {
		switch r.Kind {
		case dbgen.SuggestionKindCARE:
			care = append(care, r)
		case dbgen.SuggestionKindCOST:
			cost = append(cost, r)
		}
	}
	if len(care) != 5 || len(cost) != 2 {
		t.Fatalf("kinds: care=%d cost=%d", len(care), len(cost))
	}
	// List order is (createdAt DESC, id DESC); compare the action set.
	wantActions := map[string]bool{}
	for _, c := range checks[:5] {
		wantActions[c] = true
	}
	for i, r := range care {
		if !wantActions[r.Action] {
			t.Fatalf("care[%d] action not a projected follow-up: %q", i, r.Action)
		}
		delete(wantActions, r.Action)
	}
	if len(wantActions) != 0 {
		t.Fatalf("dropped follow-ups: %v", wantActions)
	}
	for i, r := range care {
		if r.Severity != dbgen.SuggestionSeverityMEDIUM || r.Status != dbgen.SuggestionStatusPROPOSED {
			t.Fatalf("care[%d] shape: %s %s", i, r.Severity, r.Status)
		}
		if !r.ExpiresAt.Valid || r.ExpiresAt.Time.Before(s.now().Add(29*24*time.Hour)) {
			t.Fatalf("care[%d] expiry: %+v", i, r.ExpiresAt)
		}
		if r.Observation.String != "Leaf edges are pale." ||
			r.Hypothesis.String != "Watering may be inconsistent." {
			t.Fatalf("care[%d] structured parts: %q %q", i, r.Observation.String, r.Hypothesis.String)
		}
		if string(r.MissingFacts) != `["soil moisture reading"]` {
			t.Fatalf("care[%d] missing facts: %s", i, r.MissingFacts)
		}
	}
	// Cost advice quotes the exact ledger figures and cites the purchases.
	figures := 0
	for _, r := range cost {
		if r.Severity != dbgen.SuggestionSeverityLOW {
			t.Fatalf("cost severity: %s", r.Severity)
		}
		if !strings.Contains(r.Observation.String, "ledger totals") {
			t.Fatalf("cost observation must state ledger provenance: %q", r.Observation.String)
		}
		matched := 0
		for _, want := range []string{"1200 USD", "500 EUR"} {
			if strings.Contains(r.Observation.String, want) {
				matched++
			}
		}
		if matched != 1 {
			t.Fatalf("cost figures wrong: %q", r.Observation.String)
		}
		var ev []string
		if err := json.Unmarshal(r.EvidenceIds, &ev); err != nil || len(ev) != 1 {
			t.Fatalf("cost evidence: %v %s", err, r.EvidenceIds)
		}
		if ev[0] != uuidString(p1) && ev[0] != uuidString(p2) {
			t.Fatalf("cost evidence not a purchase: %s", ev[0])
		}
		figures++
	}
	if figures != 2 {
		t.Fatalf("cost suggestions: %d", figures)
	}
}

// ---- accept ---------------------------------------------------------------------

func TestT33AcceptCreatesOneTaskAndJournalInsight(t *testing.T) {
	ctx, pool := scratch(t)
	w := seedWorld(t, ctx, pool)
	plan := seedPlanT33(t, ctx, pool, w)

	fake := newFakePort()
	s := newSvc(pool, defaultLimits(), fake)
	run, probs, err := s.Request(ctx, w.Actor, synthReq(w), nil)
	noProbs(t, probs, err)
	if err := s.ProcessRun(ctx, jobFor(run.ID)); err != nil {
		t.Fatal(err)
	}
	sugg := seedSuggestion(t, ctx, pool, w, dbgen.SuggestionKindCARE,
		"Check drainage holes before the next watering.", s.now().Add(30*24*time.Hour))
	if _, err := pool.Exec(ctx, `UPDATE suggestions SET run_id=$2 WHERE id=$1`,
		sugg.ID, run.ID); err != nil {
		t.Fatal(err)
	}

	cmid := "accept-1"
	got, probs, err := s.Accept(ctx, w.Actor, AcceptInput{
		SuggestionID:     uuidString(sugg.ID),
		ExpectedRevision: sugg.Revision,
	}, &cmid)
	noProbs(t, probs, err)
	if got.Status != dbgen.SuggestionStatusACCEPTED || !got.AcceptedTaskID.Valid {
		t.Fatalf("accept shape: %s task=%v", got.Status, got.AcceptedTaskID)
	}
	if got.Revision != sugg.Revision+1 {
		t.Fatalf("revision: %d -> %d", sugg.Revision, got.Revision)
	}

	// exactly one explicit task: a one-off CHECK occurrence, never a
	// completed activity.
	occ, err := dbgen.New(pool).GetCareOccurrence(ctx, got.AcceptedTaskID)
	if err != nil {
		t.Fatalf("occurrence: %v", err)
	}
	if occ.Kind != dbgen.RuleKindCHECK || occ.ActivityKind.Valid {
		t.Fatalf("task must be a CHECK with no activity kind: %s valid=%v", occ.Kind, occ.ActivityKind.Valid)
	}
	if occ.Status != dbgen.OccurrenceStatusDUE || occ.ResolvedAt.Valid {
		t.Fatalf("task must be open: %s %v", occ.Status, occ.ResolvedAt)
	}
	if occ.OccurrenceKey != "suggestion:"+uuidString(sugg.ID) || occ.RuleKey != "suggestion" {
		t.Fatalf("task identity: %q %q", occ.OccurrenceKey, occ.RuleKey)
	}
	if occ.PlanID != plan.ID {
		t.Fatalf("task on wrong plan")
	}

	// the accepted advice enters the journal as one INSIGHT line per run+plant
	var jtype, summary string
	if err := pool.QueryRow(ctx,
		`SELECT entry_type::text, summary FROM journal_entries WHERE source_id=$1 AND plant_id=$2`,
		run.ID, w.PlantID).Scan(&jtype, &summary); err != nil {
		t.Fatalf("journal INSIGHT line: %v", err)
	}
	if jtype != "INSIGHT" || summary != "Check drainage holes before the next watering." {
		t.Fatalf("journal line: %s %q", jtype, summary)
	}

	// replay with the same clientMutationId returns the same result and
	// creates nothing new.
	again, probs, err := s.Accept(ctx, w.Actor, AcceptInput{
		SuggestionID:     uuidString(sugg.ID),
		ExpectedRevision: sugg.Revision,
	}, &cmid)
	noProbs(t, probs, err)
	if again.AcceptedTaskID != got.AcceptedTaskID || again.Revision != got.Revision {
		t.Fatalf("replay diverged: %+v vs %+v", again, got)
	}

	// fresh key on an ACCEPTED row: CONFLICT, no second task, and the
	// returned row names the one that exists.
	second, probs, err := s.Accept(ctx, w.Actor, AcceptInput{
		SuggestionID:     uuidString(sugg.ID),
		ExpectedRevision: got.Revision,
	}, strPtr("accept-2"))
	noErr(t, err)
	if !hasCode(probs, CodeConflict) {
		t.Fatalf("second accept: %v", probs)
	}
	if second.AcceptedTaskID != got.AcceptedTaskID {
		t.Fatalf("conflict row must name the existing task")
	}
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM care_occurrences WHERE plan_id=$1`, plan.ID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("acceptance created %d tasks", n)
	}
}

func TestT33AcceptGuards(t *testing.T) {
	ctx, pool := scratch(t)
	w := seedWorld(t, ctx, pool)
	seedPlanT33(t, ctx, pool, w)
	s := newSvc(pool, defaultLimits(), newFakePort())

	// revision moved -> CONFLICT carrying the current revision
	sugg := seedSuggestion(t, ctx, pool, w, dbgen.SuggestionKindCARE, "Check A.", s.now().Add(24*time.Hour))
	_, probs, err := s.Accept(ctx, w.Actor, AcceptInput{
		SuggestionID:     uuidString(sugg.ID),
		ExpectedRevision: sugg.Revision + 3,
	}, nil)
	noErr(t, err)
	if !hasCode(probs, CodeConflict) {
		t.Fatalf("stale revision accept: %v", probs)
	}
	if probs[0].CurrentRevision == nil || *probs[0].CurrentRevision != sugg.Revision {
		t.Fatalf("conflict must carry current revision: %+v", probs[0])
	}

	// expired -> VALIDATION, nothing created
	expired := seedSuggestion(t, ctx, pool, w, dbgen.SuggestionKindCARE, "Check B.", s.now().Add(24*time.Hour))
	expireNow(t, ctx, pool, expired.ID)
	_, probs, err = s.Accept(ctx, w.Actor, AcceptInput{
		SuggestionID:     uuidString(expired.ID),
		ExpectedRevision: expired.Revision,
	}, nil)
	noErr(t, err)
	if !hasCode(probs, CodeValidation) {
		t.Fatalf("expired accept: %v", probs)
	}

	// past dueOn -> VALIDATION
	fresh := seedSuggestion(t, ctx, pool, w, dbgen.SuggestionKindCARE, "Check C.", s.now().Add(24*time.Hour))
	yesterday := s.now().Add(-24 * time.Hour)
	_, probs, err = s.Accept(ctx, w.Actor, AcceptInput{
		SuggestionID:     uuidString(fresh.ID),
		ExpectedRevision: fresh.Revision,
		DueOn:            &yesterday,
	}, nil)
	noErr(t, err)
	if !hasCode(probs, CodeValidation) {
		t.Fatalf("past dueOn accept: %v", probs)
	}

	// no care plan -> VALIDATION (the UI's create-a-plan-first path)
	other, err := dbgen.New(pool).InsertPlant(ctx, dbgen.InsertPlantParams{
		ID: platform.UUIDv4{}.New(), WorkspaceID: w.WorkspaceID, Name: "No Plan",
		Type: dbgen.PlantTypeINDIVIDUAL, Category: dbgen.PlantCategoryOTHER,
	})
	if err != nil {
		t.Fatal(err)
	}
	orphan, err := dbgen.New(pool).InsertSuggestion(ctx, dbgen.InsertSuggestionParams{
		ID: platform.UUIDv4{}.New(), WorkspaceID: w.WorkspaceID, PlantID: other.ID,
		Kind: dbgen.SuggestionKindCARE, Severity: dbgen.SuggestionSeverityMEDIUM, Action: "Check D.",
		EvidenceIds: []byte(`[]`), MissingFacts: []byte(`[]`),
		ExpiresAt: pgtype.Timestamptz{Time: s.now().Add(24 * time.Hour), Valid: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, probs, err = s.Accept(ctx, w.Actor, AcceptInput{
		SuggestionID:     uuidString(orphan.ID),
		ExpectedRevision: orphan.Revision,
	}, nil)
	noErr(t, err)
	if !hasCode(probs, CodeValidation) {
		t.Fatalf("no-plan accept: %v", probs)
	}
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM care_occurrences`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("failed accepts created %d tasks", n)
	}
}

func TestT33NeedsReviewConfirmFlow(t *testing.T) {
	ctx, pool := scratch(t)
	w := seedWorld(t, ctx, pool)
	seedPlanT33(t, ctx, pool, w)
	s := newSvc(pool, defaultLimits(), newFakePort())

	sugg := seedSuggestion(t, ctx, pool, w, dbgen.SuggestionKindCARE, "Check EC.", s.now().Add(24*time.Hour))
	if _, err := pool.Exec(ctx, `UPDATE suggestions SET needs_review = true WHERE id=$1`, sugg.ID); err != nil {
		t.Fatal(err)
	}

	// first accept performs the owner review touch and asks for
	// confirmation: nothing is created, revision bumps, needs_review clears.
	got, probs, err := s.Accept(ctx, w.Actor, AcceptInput{
		SuggestionID:     uuidString(sugg.ID),
		ExpectedRevision: sugg.Revision,
	}, nil)
	noErr(t, err)
	if !hasCode(probs, CodeValidation) {
		t.Fatalf("needs_review accept: %v", probs)
	}
	if !strings.Contains(probs[0].Message, "REVIEW_REQUIRED") {
		t.Fatalf("must name the confirmation state: %+v", probs[0])
	}
	if got.ID != sugg.ID || got.NeedsReview || got.Revision != sugg.Revision+1 {
		t.Fatalf("review touch shape: %+v", got)
	}
	if got.Status != dbgen.SuggestionStatusPROPOSED {
		t.Fatalf("review touch must not accept: %s", got.Status)
	}
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM care_occurrences`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("confirmation touch created %d tasks", n)
	}

	// the owner re-reads at the new revision and confirms: clean accept.
	accepted, probs, err := s.Accept(ctx, w.Actor, AcceptInput{
		SuggestionID:     uuidString(sugg.ID),
		ExpectedRevision: got.Revision,
	}, nil)
	noProbs(t, probs, err)
	if accepted.Status != dbgen.SuggestionStatusACCEPTED || !accepted.AcceptedTaskID.Valid {
		t.Fatalf("confirm accept: %s", accepted.Status)
	}
}

// ---- dismiss --------------------------------------------------------------------

func TestT33DismissMatrix(t *testing.T) {
	ctx, pool := scratch(t)
	w := seedWorld(t, ctx, pool)
	s := newSvc(pool, defaultLimits(), newFakePort())

	open := seedSuggestion(t, ctx, pool, w, dbgen.SuggestionKindCARE, "Check A.", s.now().Add(24*time.Hour))
	// stale revision first: CONFLICT, row untouched
	_, probs, err := s.Dismiss(ctx, w.Actor, DismissInput{
		SuggestionID:     uuidString(open.ID),
		ExpectedRevision: open.Revision + 5,
		Reason:           strPtr("nope"),
	}, nil)
	noErr(t, err)
	if !hasCode(probs, CodeConflict) {
		t.Fatalf("stale dismiss: %v", probs)
	}
	if got := reloadSuggestion(t, ctx, pool, w, open.ID); got.Status != dbgen.SuggestionStatusPROPOSED {
		t.Fatalf("failed dismiss touched the row: %s", got.Status)
	}
	// clean dismiss stores the reason verbatim
	got, probs, err := s.Dismiss(ctx, w.Actor, DismissInput{
		SuggestionID:     uuidString(open.ID),
		ExpectedRevision: open.Revision,
		Reason:           strPtr("already handled"),
	}, nil)
	noProbs(t, probs, err)
	if got.Status != dbgen.SuggestionStatusDISMISSED || got.DismissReason.String != "already handled" {
		t.Fatalf("dismiss shape: %s %q", got.Status, got.DismissReason.String)
	}
	// dismissal is final
	_, probs, err = s.Dismiss(ctx, w.Actor, DismissInput{
		SuggestionID:     uuidString(open.ID),
		ExpectedRevision: got.Revision,
	}, nil)
	noErr(t, err)
	if !hasCode(probs, CodeConflict) {
		t.Fatalf("re-dismiss: %v", probs)
	}
	// accepting a dismissed suggestion is a CONFLICT naming the final row
	_, probs, err = s.Accept(ctx, w.Actor, AcceptInput{
		SuggestionID:     uuidString(open.ID),
		ExpectedRevision: got.Revision,
	}, nil)
	noErr(t, err)
	if !hasCode(probs, CodeConflict) {
		t.Fatalf("accept dismissed: %v", probs)
	}
	// expired advice needs no dismissal: VALIDATION
	expired := seedSuggestion(t, ctx, pool, w, dbgen.SuggestionKindCOST, "Review costs.", s.now().Add(24*time.Hour))
	expireNow(t, ctx, pool, expired.ID)
	_, probs, err = s.Dismiss(ctx, w.Actor, DismissInput{
		SuggestionID:     uuidString(expired.ID),
		ExpectedRevision: expired.Revision,
	}, nil)
	noErr(t, err)
	if !hasCode(probs, CodeValidation) {
		t.Fatalf("dismiss expired: %v", probs)
	}
	// reason length is bounded by the DB check; the service refuses first
	long := seedSuggestion(t, ctx, pool, w, dbgen.SuggestionKindCARE, "Check B.", s.now().Add(24*time.Hour))
	_, probs, err = s.Dismiss(ctx, w.Actor, DismissInput{
		SuggestionID:     uuidString(long.ID),
		ExpectedRevision: long.Revision,
		Reason:           strPtr(strings.Repeat("x", MaxDismissReason+1)),
	}, nil)
	noErr(t, err)
	if !hasCode(probs, CodeValidation) {
		t.Fatalf("overlong reason: %v", probs)
	}
}

// ---- expiry sweeper ---------------------------------------------------------------

func TestT33ExpirySweeper(t *testing.T) {
	ctx, pool := scratch(t)
	w := seedWorld(t, ctx, pool)
	s := newSvc(pool, defaultLimits(), newFakePort())

	due := seedSuggestion(t, ctx, pool, w, dbgen.SuggestionKindCARE, "Old advice.", s.now().Add(24*time.Hour))
	expireNow(t, ctx, pool, due.ID)
	live := seedSuggestion(t, ctx, pool, w, dbgen.SuggestionKindCARE, "Fresh advice.", s.now().Add(24*time.Hour))

	n, err := s.ExpireDueSuggestions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("expired %d", n)
	}
	if got := reloadSuggestion(t, ctx, pool, w, due.ID); got.Status != dbgen.SuggestionStatusEXPIRED {
		t.Fatalf("due row status: %s", got.Status)
	}
	if got := reloadSuggestion(t, ctx, pool, w, live.ID); got.Status != dbgen.SuggestionStatusPROPOSED {
		t.Fatalf("live row status: %s", got.Status)
	}
}

// ---- list paging -------------------------------------------------------------------

func TestT33ListSuggestionsPaging(t *testing.T) {
	ctx, pool := scratch(t)
	w := seedWorld(t, ctx, pool)
	s := newSvc(pool, defaultLimits(), newFakePort())
	for i := 0; i < 4; i++ {
		seedSuggestion(t, ctx, pool, w, dbgen.SuggestionKindCARE,
			fmt.Sprintf("Check %d.", i), s.now().Add(24*time.Hour))
	}

	page, probs, err := s.ListSuggestions(ctx, w.Actor, SuggestionQuery{First: 2})
	noProbs(t, probs, err)
	if len(page.Items) != 2 || !page.HasNext || page.EndCursor == nil {
		t.Fatalf("page 1: %d hasNext=%v", len(page.Items), page.HasNext)
	}
	page2, probs, err := s.ListSuggestions(ctx, w.Actor, SuggestionQuery{First: 2, After: page.EndCursor})
	noProbs(t, probs, err)
	if len(page2.Items) != 2 || page2.HasNext {
		t.Fatalf("page 2: %d hasNext=%v", len(page2.Items), page2.HasNext)
	}
	seen := map[string]bool{}
	for _, r := range append(append([]dbgen.Suggestion{}, page.Items...), page2.Items...) {
		seen[uuidString(r.ID)] = true
	}
	if len(seen) != 4 {
		t.Fatalf("paging lost or duplicated rows: %d", len(seen))
	}

	// a cursor bound to a plant filter cannot resume an unfiltered query
	plantID := w.PlantID
	_, probs, err = s.ListSuggestions(ctx, w.Actor, SuggestionQuery{First: 2, After: page.EndCursor, PlantID: &plantID})
	noErr(t, err)
	if !hasCode(probs, CodeValidation) {
		t.Fatalf("cursor/filter mismatch must be VALIDATION: %v", probs)
	}
}

// ---- flagging (SQL-level plumbing; care package tests cover the wiring) -----------

func TestT33FlaggingPlumbing(t *testing.T) {
	ctx, pool := scratch(t)
	w := seedWorld(t, ctx, pool)
	q := dbgen.New(pool)

	// plant context change flags that plant's pending advice
	sugg := seedSuggestion(t, ctx, pool, w, dbgen.SuggestionKindCARE, "Check A.", time.Now().Add(24*time.Hour))
	ids, err := q.FlagSuggestionsNeedingReview(ctx, dbgen.FlagSuggestionsNeedingReviewParams{
		WorkspaceID: w.WorkspaceID, RunIds: []pgtype.UUID{}, PlantIds: []pgtype.UUID{w.PlantID},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 1 {
		t.Fatalf("flagged %d", len(ids))
	}
	if got := reloadSuggestion(t, ctx, pool, w, sugg.ID); !got.NeedsReview {
		t.Fatalf("context change did not flag the suggestion")
	}

	// a SUCCEEDED run citing a profile version links its advice to that
	// version; RunsCitingSource finds it by either id.
	prof := mustRow(q.InsertCareProfile(ctx, dbgen.InsertCareProfileParams{
		ID: platform.UUIDv4{}.New(), WorkspaceID: w.WorkspaceID,
		Category: dbgen.PlantCategoryOTHER, Status: dbgen.ProfileStatusDRAFT,
	}))
	ver := mustRow(q.InsertCareProfileVersion(ctx, dbgen.InsertCareProfileVersionParams{
		ID: platform.UUIDv4{}.New(), ProfileID: prof.ID, WorkspaceID: w.WorkspaceID,
		Version: 1, Status: dbgen.ProfileStatusDRAFT, Environment: []byte(`{}`),
	}))
	runID := platform.UUIDv4{}.New()
	result := fmt.Sprintf(`{"citations":[{"sourceId":%q,"sourceVersionId":%q,"title":"t","publisher":"p","retrievedOn":"2026-01-01","region":"x","license":"CC"}]}`,
		uuidString(prof.ID), uuidString(ver.ID))
	if _, err := pool.Exec(ctx,
		`INSERT INTO analysis_runs (id, workspace_id, kind, capability, plant_id, input_snapshot, input_hash,
			semantic_key, state, result, started_at, finished_at)
		 VALUES ($1,$2,'SYNTHESIS','TEXT_ANALYZER',$3,'{}','sha256:`+strings.Repeat("0", 64)+`','sk-$1','SUCCEEDED',$4, now() - interval '1 hour', now())`,
		runID, w.WorkspaceID, w.PlantID, result); err != nil {
		t.Fatal(err)
	}
	cited, err := q.InsertSuggestion(ctx, dbgen.InsertSuggestionParams{
		ID: platform.UUIDv4{}.New(), WorkspaceID: w.WorkspaceID, PlantID: w.PlantID, RunID: runID,
		Kind: dbgen.SuggestionKindCARE, Severity: dbgen.SuggestionSeverityMEDIUM, Action: "Check B.",
		EvidenceIds: []byte(`[]`), MissingFacts: []byte(`[]`),
		ExpiresAt: pgtype.Timestamptz{Time: time.Now().Add(24 * time.Hour), Valid: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	found, err := q.RunsCitingSource(ctx, dbgen.RunsCitingSourceParams{
		WorkspaceID: w.WorkspaceID, SourceID: uuidString(prof.ID), SourceVersionID: uuidString(ver.ID),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 1 || uuidString(found[0]) != uuidString(runID) {
		t.Fatalf("RunsCitingSource: %v", found)
	}
	if _, err := q.FlagSuggestionsNeedingReview(ctx, dbgen.FlagSuggestionsNeedingReviewParams{
		WorkspaceID: w.WorkspaceID, RunIds: found, PlantIds: []pgtype.UUID{},
	}); err != nil {
		t.Fatal(err)
	}
	if got := reloadSuggestion(t, ctx, pool, w, cited.ID); !got.NeedsReview {
		t.Fatalf("cited-run flagging missed the suggestion")
	}
	// already-flagged rows are not re-bumped (idempotent flagging)
	before := reloadSuggestion(t, ctx, pool, w, cited.ID).Revision
	again, err := q.FlagSuggestionsNeedingReview(ctx, dbgen.FlagSuggestionsNeedingReviewParams{
		WorkspaceID: w.WorkspaceID, RunIds: found, PlantIds: []pgtype.UUID{},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(again) != 0 {
		t.Fatalf("flagging re-bumped an already-flagged row")
	}
	if reloadSuggestion(t, ctx, pool, w, cited.ID).Revision != before {
		t.Fatalf("revision moved on idempotent flag")
	}
}

// ---- small helpers -------------------------------------------------------------------

func strPtr(s string) *string { return &s }

func noErr(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func hasCode(ps []Problem, code string) bool {
	for _, p := range ps {
		if p.Code == code {
			return true
		}
	}
	return false
}
