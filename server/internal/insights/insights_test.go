// T29 acceptance: run lifecycle, budget gating, dedup, and the receipt
// protocol for the AI analysis lane, on real PostgreSQL 17 (PGTEST_DSN),
// one disposable schema per test.
package insights

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	candb "canopy/server/db"
	dbgen "canopy/server/db/generated"
	"canopy/server/internal/platform"
)

// ---- harness ---------------------------------------------------------------

func scratch(t *testing.T) (context.Context, *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	dsn := os.Getenv("PGTEST_DSN")
	if dsn == "" {
		t.Skip("PGTEST_DSN not set")
	}
	admin, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	schema := fmt.Sprintf("t29_%d", time.Now().UnixNano())
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	t.Cleanup(func() {
		c, err := pgx.Connect(context.Background(), dsn)
		if err == nil {
			_, _ = c.Exec(context.Background(), "DROP SCHEMA IF EXISTS "+schema+" CASCADE")
			c.Close(context.Background())
		}
	})
	up, err := candb.AllUp()
	if err != nil {
		t.Fatalf("migrations: %v", err)
	}
	if _, err := admin.Exec(ctx, "SET search_path TO "+schema); err != nil {
		t.Fatalf("search_path: %v", err)
	}
	if _, err := admin.Exec(ctx, up); err != nil {
		t.Fatalf("apply migration: %v", err)
	}
	admin.Close(ctx)

	pool, err := pgxpool.New(ctx, dsn+"&search_path="+schema)
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	t.Cleanup(pool.Close)
	if err := platform.MigrateRiver(ctx, pool); err != nil {
		t.Fatalf("river migrate: %v", err)
	}
	return ctx, pool
}

type world struct {
	Actor
	PlantID pgtype.UUID
}

func seedWorld(t *testing.T, ctx context.Context, pool *pgxpool.Pool) world {
	t.Helper()
	ids := platform.UUIDv4{}
	q := dbgen.New(pool)
	owner, err := q.InsertOwner(ctx, dbgen.InsertOwnerParams{ID: ids.New(), PasswordHash: "x"})
	if err != nil {
		t.Fatalf("owner: %v", err)
	}
	ws, err := q.InsertWorkspace(ctx, dbgen.InsertWorkspaceParams{
		ID: ids.New(), OwnerID: owner.ID, DisplayName: "T29", Timezone: "UTC", DefaultCurrency: "USD",
	})
	if err != nil {
		t.Fatalf("workspace: %v", err)
	}
	plant, err := q.InsertPlant(ctx, dbgen.InsertPlantParams{
		ID: ids.New(), WorkspaceID: ws.ID, Name: "Monstera",
		Type: dbgen.PlantTypeINDIVIDUAL, Category: dbgen.PlantCategoryOTHER,
	})
	if err != nil {
		t.Fatalf("plant: %v", err)
	}
	return world{Actor: Actor{WorkspaceID: ws.ID, OwnerID: owner.ID, SessionID: ids.New()}, PlantID: plant.ID}
}

// fakePort is a scripted provider seam: per-call responses plus the
// recorded call inputs (repair chaining, call counts).
type fakePort struct {
	mu    sync.Mutex
	caps  map[Capability]CapabilityStatus
	resp  func(in AnalyzeInput, call int) (AnalyzeOutput, error)
	calls []AnalyzeInput
}

func newFakePort() *fakePort {
	return &fakePort{
		caps: map[Capability]CapabilityStatus{
			CapTextAnalyzer:     {Available: true, Model: "fake-model", ExternalData: true},
			CapVisionAnalyzer:   {Available: true, Model: "fake-vision", ExternalData: true},
			CapPlantIdentifier:  {Available: true, Model: "fake-id", ExternalData: true},
			CapReceiptExtractor: {Available: true, Model: "fake-ocr", ExternalData: true},
		},
		resp: func(in AnalyzeInput, call int) (AnalyzeOutput, error) {
			return AnalyzeOutput{Raw: validObsJSON(), Provider: "fakeprov", Model: "fake-model",
				UsageCostMicroUSD: ptr64(1500)}, nil
		},
	}
}

func (f *fakePort) Capabilities(context.Context) map[Capability]CapabilityStatus { return f.caps }

func (f *fakePort) Analyze(ctx context.Context, in AnalyzeInput) (AnalyzeOutput, error) {
	f.mu.Lock()
	f.calls = append(f.calls, in)
	n := len(f.calls)
	f.mu.Unlock()
	if ctx.Err() != nil {
		return AnalyzeOutput{}, &ProviderError{Class: ClassTimeout, Err: ctx.Err()}
	}
	return f.resp(in, n)
}

func (f *fakePort) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

func ptr64(v int64) *int64 { return &v }

func validObsJSON() json.RawMessage {
	return json.RawMessage(`{"visibleObservations":[{"text":"Leaf edges are pale.","evidenceIds":[]}],` +
		`"hypotheses":[{"text":"Nitrogen uptake may be limited.","confidence":"MEDIUM","evidenceIds":[]}],` +
		`"missingInformation":[],"followUpChecks":[],"citations":[]}`)
}

func newSvc(pool *pgxpool.Pool, lim Limits, provider Port) *Service {
	s := New(pool, slog.New(slog.DiscardHandler), lim, provider)
	queue, err := platform.NewInsertOnlyRiverClient(pool)
	if err != nil {
		panic("river client: " + err.Error())
	}
	s.Queue = queue
	s.PromptVersion = "p1"
	s.SchemaVersion = "r1"
	s.AttemptTimeout = 2 * time.Second
	s.MaxResultBytes = 64 << 10
	return s
}

func defaultLimits() Limits {
	return Limits{DailyRunLimit: 10, ConcurrentRunLimit: 1,
		MonthCeilingMicroUSD: 0, AttemptCostMicroUSD: 4000}
}

func textReq(w world) RequestInput {
	return RequestInput{Kind: "TEXT", PlantID: uuidString(w.PlantID)}
}

func noProbs(t *testing.T, probs []Problem, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("transport error: %v", err)
	}
	if len(probs) > 0 {
		t.Fatalf("unexpected problems: %+v", probs)
	}
}

func wantCode(t *testing.T, probs []Problem, code string) Problem {
	t.Helper()
	for _, p := range probs {
		if p.Code == code {
			return p
		}
	}
	t.Fatalf("no problem with code %s: %+v", code, probs)
	return Problem{}
}

func countRuns(t *testing.T, ctx context.Context, pool *pgxpool.Pool, ws pgtype.UUID) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM analysis_runs WHERE workspace_id = $1`, ws).Scan(&n); err != nil {
		t.Fatalf("count runs: %v", err)
	}
	return n
}

func countJobs(t *testing.T, ctx context.Context, pool *pgxpool.Pool) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM river_job WHERE kind = 'analysis:run'`).Scan(&n); err != nil {
		t.Fatalf("count jobs: %v", err)
	}
	return n
}

func getRun(t *testing.T, ctx context.Context, pool *pgxpool.Pool, id pgtype.UUID) dbgen.AnalysisRun {
	t.Helper()
	row, err := dbgen.New(pool).GetAnalysisRunByID(ctx, id)
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	return row
}

// ---- no-provider mode ------------------------------------------------------

func TestRequestNoProviderQueuesNothing(t *testing.T) {
	ctx, pool := scratch(t)
	w := seedWorld(t, ctx, pool)
	s := newSvc(pool, defaultLimits(), NoProvider{})

	run, probs, err := s.Request(ctx, w.Actor, textReq(w), nil)
	noProbs(t, nil, err)
	if run.ID.Valid {
		t.Fatal("no run may be returned in no-provider mode")
	}
	p := wantCode(t, probs, CodeCapabilityUnavailable)
	if p.Field != "input.kind" || p.Message == "" {
		t.Fatalf("problem shape: %+v", p)
	}
	if n := countRuns(t, ctx, pool, w.WorkspaceID); n != 0 {
		t.Fatalf("runs inserted: %d", n)
	}
	if n := countJobs(t, ctx, pool); n != 0 {
		t.Fatalf("jobs inserted: %d", n)
	}
}

// ---- input matrix validation ------------------------------------------------

func TestRequestInputMatrix(t *testing.T) {
	ctx, pool := scratch(t)
	w := seedWorld(t, ctx, pool)
	s := newSvc(pool, defaultLimits(), newFakePort())

	cases := []struct {
		name string
		in   RequestInput
		code string
	}{
		{"bad kind", RequestInput{Kind: "MYSTIC", PlantID: uuidString(w.PlantID)}, CodeValidation},
		{"missing plant", RequestInput{Kind: "TEXT"}, CodeValidation},
		{"bad plant uuid", RequestInput{Kind: "TEXT", PlantID: "nope"}, CodeValidation},
		{"receipt without asset", RequestInput{Kind: "RECEIPT_EXTRACTION"}, CodeValidation},
		{"receipt with plant", RequestInput{Kind: "RECEIPT_EXTRACTION", PlantID: uuidString(w.PlantID),
			ReceiptAssetID: uuidString(w.PlantID)}, CodeValidation},
		{"text with photos", RequestInput{Kind: "TEXT", PlantID: uuidString(w.PlantID),
			PhotoIDs: []string{uuidString(w.PlantID), uuidString(w.PlantID)}}, CodeValidation},
		{"vision one photo", RequestInput{Kind: "VISION", PlantID: uuidString(w.PlantID),
			PhotoIDs: []string{uuidString(w.PlantID)}}, CodeValidation},
		{"unknown plant", RequestInput{Kind: "TEXT", PlantID: "11111111-1111-1111-1111-111111111111"}, CodeNotFound},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, probs, err := s.Request(ctx, w.Actor, c.in, nil)
			noProbs(t, nil, err)
			wantCode(t, probs, c.code)
		})
	}
	if n := countRuns(t, ctx, pool, w.WorkspaceID); n != 0 {
		t.Fatalf("invalid requests must persist nothing, got %d runs", n)
	}
}

func TestArchivedPlantRejected(t *testing.T) {
	ctx, pool := scratch(t)
	w := seedWorld(t, ctx, pool)
	s := newSvc(pool, defaultLimits(), newFakePort())
	if _, err := pool.Exec(ctx, `UPDATE plants SET status='ARCHIVED' WHERE id=$1`, w.PlantID); err != nil {
		t.Fatal(err)
	}
	_, probs, err := s.Request(ctx, w.Actor, textReq(w), nil)
	noProbs(t, nil, err)
	wantCode(t, probs, CodeValidation)
}

// ---- happy path: run + job + snapshot ---------------------------------------

func TestRequestQueuesRunAndJob(t *testing.T) {
	ctx, pool := scratch(t)
	w := seedWorld(t, ctx, pool)
	s := newSvc(pool, defaultLimits(), newFakePort())

	run, probs, err := s.Request(ctx, w.Actor, textReq(w), nil)
	noProbs(t, probs, err)
	if run.State != dbgen.AnalysisRunStateQUEUED {
		t.Fatalf("state %s", run.State)
	}
	if !run.CostReservedMicroUsd.Valid || run.CostReservedMicroUsd.Int64 != 4000 {
		t.Fatalf("reservation: %+v", run.CostReservedMicroUsd)
	}
	if run.CostActualMicroUsd.Valid {
		t.Fatal("actual must stay unknown until reconciliation")
	}
	if !strings.HasPrefix(run.InputHash, "sha256:") {
		t.Fatalf("hash %q", run.InputHash)
	}
	if run.SemanticKey != "TEXT:plant:"+uuidString(w.PlantID)+":"+run.InputHash {
		t.Fatalf("semantic key %q", run.SemanticKey)
	}
	snap, err := ParseSnapshot(run.InputSnapshot)
	if err != nil {
		t.Fatalf("stored snapshot: %v", err)
	}
	if snap.Plant.ID != uuidString(w.PlantID) || snap.Kind != "TEXT" {
		t.Fatalf("snapshot target: %+v", snap.Plant)
	}
	if len(snap.MissingFacts) == 0 {
		t.Fatal("empty workspace must record honest missing facts")
	}
	if n := countJobs(t, ctx, pool); n != 1 {
		t.Fatalf("jobs: %d", n)
	}
}

// ---- semantic dedup ----------------------------------------------------------

func TestDuplicateActiveRun(t *testing.T) {
	ctx, pool := scratch(t)
	w := seedWorld(t, ctx, pool)
	s := newSvc(pool, defaultLimits(), newFakePort())

	first, probs, err := s.Request(ctx, w.Actor, textReq(w), nil)
	noProbs(t, probs, err)

	second, probs, err := s.Request(ctx, w.Actor, textReq(w), nil)
	noProbs(t, nil, err)
	p := wantCode(t, probs, CodeIdempotencyConflict)
	if p.Field != "input.plantId" {
		t.Fatalf("field %q", p.Field)
	}
	if p.Message != "An analysis for this plant with unchanged input is already running; follow the existing run or force a rerun." {
		t.Fatalf("message %q", p.Message)
	}
	if uuidString(second.ID) != uuidString(first.ID) {
		t.Fatal("conflict payload must carry the existing run")
	}
	if n := countRuns(t, ctx, pool, w.WorkspaceID); n != 1 {
		t.Fatalf("runs: %d", n)
	}
}

func TestTerminalReuseAndForceRerun(t *testing.T) {
	ctx, pool := scratch(t)
	w := seedWorld(t, ctx, pool)
	s := newSvc(pool, defaultLimits(), newFakePort())

	first, probs, err := s.Request(ctx, w.Actor, textReq(w), nil)
	noProbs(t, probs, err)
	if _, err := pool.Exec(ctx, `UPDATE analysis_runs SET state='SUCCEEDED', result='{}'::jsonb,
		finished_at = now() WHERE id=$1`, first.ID); err != nil {
		t.Fatal(err)
	}

	_, probs, err = s.Request(ctx, w.Actor, textReq(w), nil)
	noProbs(t, nil, err)
	p := wantCode(t, probs, CodeIdempotencyConflict)
	if p.Message != "An analysis for this plant with unchanged input already exists; force a rerun to re-bill." {
		t.Fatalf("message %q", p.Message)
	}

	force := textReq(w)
	force.ForceRerun = true
	rerun, probs, err := s.Request(ctx, w.Actor, force, nil)
	noProbs(t, probs, err)
	if uuidString(rerun.ID) == uuidString(first.ID) {
		t.Fatal("forceRerun must create a new row, never reset in place")
	}
	if n := countRuns(t, ctx, pool, w.WorkspaceID); n != 2 {
		t.Fatalf("runs: %d", n)
	}
}

// ---- budget gates -------------------------------------------------------------

func TestDailyLimitWorkspaceLocalDay(t *testing.T) {
	ctx, pool := scratch(t)
	w := seedWorld(t, ctx, pool)
	lim := defaultLimits()
	lim.DailyRunLimit = 2
	lim.ConcurrentRunLimit = 5
	s := newSvc(pool, lim, newFakePort())

	// three TEXT runs on three distinct plants -> distinct semantic keys
	plants := []pgtype.UUID{w.PlantID}
	for i := 0; i < 2; i++ {
		p, err := dbgen.New(pool).InsertPlant(ctx, dbgen.InsertPlantParams{
			ID: platform.UUIDv4{}.New(), WorkspaceID: w.WorkspaceID, Name: fmt.Sprintf("Plant %d", i),
			Type: dbgen.PlantTypeINDIVIDUAL, Category: dbgen.PlantCategoryOTHER,
		})
		if err != nil {
			t.Fatal(err)
		}
		plants = append(plants, p.ID)
	}
	for i := 0; i < 2; i++ {
		_, probs, err := s.Request(ctx, w.Actor, RequestInput{Kind: "TEXT", PlantID: uuidString(plants[i])}, nil)
		noProbs(t, probs, err)
	}
	_, probs, err := s.Request(ctx, w.Actor, RequestInput{Kind: "TEXT", PlantID: uuidString(plants[2])}, nil)
	noProbs(t, nil, err)
	p := wantCode(t, probs, CodeRateLimited)
	if p.Message != "Daily analysis limit reached (2 of 2 runs used today); try again tomorrow." {
		t.Fatalf("message %q", p.Message)
	}
}

func TestConcurrencyLimitPerCapability(t *testing.T) {
	ctx, pool := scratch(t)
	w := seedWorld(t, ctx, pool)
	lim := defaultLimits() // ConcurrentRunLimit = 1
	s := newSvc(pool, lim, newFakePort())

	_, probs, err := s.Request(ctx, w.Actor, textReq(w), nil)
	noProbs(t, probs, err)

	p2, err := dbgen.New(pool).InsertPlant(ctx, dbgen.InsertPlantParams{
		ID: platform.UUIDv4{}.New(), WorkspaceID: w.WorkspaceID, Name: "Second",
		Type: dbgen.PlantTypeINDIVIDUAL, Category: dbgen.PlantCategoryOTHER,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, probs, err = s.Request(ctx, w.Actor, RequestInput{Kind: "TEXT", PlantID: uuidString(p2.ID)}, nil)
	noProbs(t, nil, err)
	p := wantCode(t, probs, CodeRateLimited)
	if p.Message != "Only one analysis per provider can run at a time; wait for the current run to finish." {
		t.Fatalf("message %q", p.Message)
	}
}

func TestMonthCeilingReservesBeforeDispatch(t *testing.T) {
	ctx, pool := scratch(t)
	w := seedWorld(t, ctx, pool)
	lim := defaultLimits()
	lim.MonthCeilingMicroUSD = 10_000
	lim.AttemptCostMicroUSD = 4_000
	lim.ConcurrentRunLimit = 5
	s := newSvc(pool, lim, newFakePort())

	plants := []pgtype.UUID{w.PlantID}
	for i := 0; i < 2; i++ {
		p, err := dbgen.New(pool).InsertPlant(ctx, dbgen.InsertPlantParams{
			ID: platform.UUIDv4{}.New(), WorkspaceID: w.WorkspaceID, Name: fmt.Sprintf("Plant %d", i),
			Type: dbgen.PlantTypeINDIVIDUAL, Category: dbgen.PlantCategoryOTHER,
		})
		if err != nil {
			t.Fatal(err)
		}
		plants = append(plants, p.ID)
	}
	for i := 0; i < 2; i++ {
		_, probs, err := s.Request(ctx, w.Actor, RequestInput{Kind: "TEXT", PlantID: uuidString(plants[i])}, nil)
		noProbs(t, probs, err)
	}
	// encumbered 8000 + 4000 > 10000: the third request cannot fit.
	_, probs, err := s.Request(ctx, w.Actor, RequestInput{Kind: "TEXT", PlantID: uuidString(plants[2])}, nil)
	noProbs(t, nil, err)
	p := wantCode(t, probs, CodeRateLimited)
	if p.Message != "Monthly external-cost ceiling reached; no further analyses this month." {
		t.Fatalf("message %q", p.Message)
	}
}

// ---- cancel + refund -----------------------------------------------------------

func TestCancelRefundsUndispatchedRun(t *testing.T) {
	ctx, pool := scratch(t)
	w := seedWorld(t, ctx, pool)
	s := newSvc(pool, defaultLimits(), newFakePort())

	run, probs, err := s.Request(ctx, w.Actor, textReq(w), nil)
	noProbs(t, probs, err)

	canceled, probs, err := s.Cancel(ctx, w.Actor, uuidString(run.ID), run.Revision, nil)
	noProbs(t, probs, err)
	if canceled.State != dbgen.AnalysisRunStateCANCELLED {
		t.Fatalf("state %s", canceled.State)
	}
	if !canceled.CostActualMicroUsd.Valid || canceled.CostActualMicroUsd.Int64 != 0 {
		t.Fatalf("refund not recorded: %+v", canceled.CostActualMicroUsd)
	}

	_, probs, err = s.Cancel(ctx, w.Actor, uuidString(run.ID), canceled.Revision, nil)
	noProbs(t, nil, err)
	p := wantCode(t, probs, CodeConflict)
	if p.Message != "This run already finished; it cannot be cancelled." {
		t.Fatalf("message %q", p.Message)
	}
	if p.CurrentRevision == nil || *p.CurrentRevision != canceled.Revision {
		t.Fatalf("currentRevision: %+v", p.CurrentRevision)
	}
}

func TestCancelUnknownRun(t *testing.T) {
	ctx, pool := scratch(t)
	w := seedWorld(t, ctx, pool)
	s := newSvc(pool, defaultLimits(), newFakePort())
	_, probs, err := s.Cancel(ctx, w.Actor, "11111111-1111-1111-1111-111111111111", 1, nil)
	noProbs(t, nil, err)
	wantCode(t, probs, CodeNotFound)
}

// ---- polling read + isolation ---------------------------------------------------

func TestGetRunIsWorkspaceScoped(t *testing.T) {
	ctx, pool := scratch(t)
	w := seedWorld(t, ctx, pool)
	s := newSvc(pool, defaultLimits(), newFakePort())
	run, probs, err := s.Request(ctx, w.Actor, textReq(w), nil)
	noProbs(t, probs, err)

	other := seedWorld(t, ctx, pool)
	got, err := s.GetRun(ctx, other.WorkspaceID, uuidString(run.ID))
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Fatal("cross-workspace read must not see the run")
	}
	got, err = s.GetRun(ctx, w.WorkspaceID, uuidString(run.ID))
	if err != nil || got == nil || uuidString(got.ID) != uuidString(run.ID) {
		t.Fatalf("own-workspace read: %v %+v", err, got)
	}
}

// ---- idempotency receipt ----------------------------------------------------------

func TestClientMutationIdReplayAndConflict(t *testing.T) {
	ctx, pool := scratch(t)
	w := seedWorld(t, ctx, pool)
	s := newSvc(pool, defaultLimits(), newFakePort())
	cmid := "cm-1"

	first, probs, err := s.Request(ctx, w.Actor, textReq(w), &cmid)
	noProbs(t, probs, err)

	replay, probs, err := s.Request(ctx, w.Actor, textReq(w), &cmid)
	noProbs(t, probs, err)
	if uuidString(replay.ID) != uuidString(first.ID) {
		t.Fatal("replay must return the original run")
	}
	if n := countRuns(t, ctx, pool, w.WorkspaceID); n != 1 {
		t.Fatalf("runs: %d", n)
	}

	// same key, different input: honest conflict, no new run
	p2, err := dbgen.New(pool).InsertPlant(ctx, dbgen.InsertPlantParams{
		ID: platform.UUIDv4{}.New(), WorkspaceID: w.WorkspaceID, Name: "Other",
		Type: dbgen.PlantTypeINDIVIDUAL, Category: dbgen.PlantCategoryOTHER,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, probs, err = s.Request(ctx, w.Actor, RequestInput{Kind: "TEXT", PlantID: uuidString(p2.ID)}, &cmid)
	noProbs(t, nil, err)
	p := wantCode(t, probs, CodeIdempotencyConflict)
	if p.Field != "clientMutationId" {
		t.Fatalf("field %q", p.Field)
	}
}

// ---- receipt + vision targets -------------------------------------------------------

func seedAsset(t *testing.T, ctx context.Context, pool *pgxpool.Pool, w world, purpose dbgen.AssetPurpose, state dbgen.AssetState) pgtype.UUID {
	t.Helper()
	ids := platform.UUIDv4{}
	a, err := dbgen.New(pool).InsertAsset(ctx, dbgen.InsertAssetParams{
		ID: ids.New(), WorkspaceID: w.WorkspaceID, Purpose: purpose,
		ByteCount: 100, Sha256: strings.Repeat("a", 64), StorageKey: "k/" + uuidString(ids.New()),
	})
	if err != nil {
		t.Fatalf("asset: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE assets SET state=$2 WHERE id=$1`, a.ID, state); err != nil {
		t.Fatal(err)
	}
	return a.ID
}

func TestReceiptExtractionTarget(t *testing.T) {
	ctx, pool := scratch(t)
	w := seedWorld(t, ctx, pool)
	s := newSvc(pool, defaultLimits(), newFakePort())

	staged := seedAsset(t, ctx, pool, w, dbgen.AssetPurposeRECEIPT, dbgen.AssetStateUPLOADED)
	_, probs, err := s.Request(ctx, w.Actor, RequestInput{Kind: "RECEIPT_EXTRACTION",
		ReceiptAssetID: uuidString(staged)}, nil)
	noProbs(t, nil, err)
	wantCode(t, probs, CodeValidation) // not READY yet

	ready := seedAsset(t, ctx, pool, w, dbgen.AssetPurposeRECEIPT, dbgen.AssetStateREADY)
	run, probs, err := s.Request(ctx, w.Actor, RequestInput{Kind: "RECEIPT_EXTRACTION",
		ReceiptAssetID: uuidString(ready)}, nil)
	noProbs(t, probs, err)
	if !run.ReceiptAssetID.Valid || uuidString(run.ReceiptAssetID) != uuidString(ready) {
		t.Fatalf("receipt target: %+v", run.ReceiptAssetID)
	}
	if !strings.HasPrefix(run.SemanticKey, "RECEIPT_EXTRACTION:receipt:") {
		t.Fatalf("semantic key %q", run.SemanticKey)
	}
}

func TestVisionComparisonPair(t *testing.T) {
	ctx, pool := scratch(t)
	w := seedWorld(t, ctx, pool)
	s := newSvc(pool, defaultLimits(), newFakePort())

	photoIDs := make([]string, 2)
	for i := range photoIDs {
		asset := seedAsset(t, ctx, pool, w, dbgen.AssetPurposePHOTO, dbgen.AssetStateREADY)
		p, err := dbgen.New(pool).InsertPhoto(ctx, dbgen.InsertPhotoParams{
			ID: platform.UUIDv4{}.New(), WorkspaceID: w.WorkspaceID, PlantID: w.PlantID,
			AssetID: asset, TakenAt: pgtype.Timestamptz{Time: time.Now(), Valid: true},
			DateSource: dbgen.PhotoDateSourceUPLOADFALLBACK, Viewpoint: dbgen.PhotoViewpointWHOLE,
		})
		if err != nil {
			t.Fatalf("photo: %v", err)
		}
		photoIDs[i] = uuidString(p.ID)
	}

	// same photo twice: rejected
	_, probs, err := s.Request(ctx, w.Actor, RequestInput{Kind: "VISION",
		PlantID: uuidString(w.PlantID), PhotoIDs: []string{photoIDs[0], photoIDs[0]}}, nil)
	noProbs(t, nil, err)
	wantCode(t, probs, CodeValidation)

	run, probs, err := s.Request(ctx, w.Actor, RequestInput{Kind: "VISION",
		PlantID: uuidString(w.PlantID), PhotoIDs: photoIDs}, nil)
	noProbs(t, probs, err)
	snap, err := ParseSnapshot(run.InputSnapshot)
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Photos) != 2 || snap.Photos[0].ID != photoIDs[0] || snap.Photos[1].ID != photoIDs[1] {
		t.Fatalf("pair not frozen: %+v", snap.Photos)
	}
}

// ---- capabilities + budget view ------------------------------------------------------

func TestSnapshotRowBoundsAreHard(t *testing.T) {
	ctx, pool := scratch(t)
	w := seedWorld(t, ctx, pool)
	// 15 activities and 5 READY photos: the snapshot caps at 12/4 no matter what.
	for i := 0; i < 15; i++ {
		if _, err := pool.Exec(ctx, `INSERT INTO activities (id, workspace_id, plant_id, kind, occurred_at, timezone)
			VALUES ($1, $2, $3, 'WATER', now() - make_interval(mins => $4::int), 'UTC')`,
			platform.UUIDv4{}.New(), w.WorkspaceID, w.PlantID, i); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 5; i++ {
		asset := seedAsset(t, ctx, pool, w, dbgen.AssetPurposePHOTO, dbgen.AssetStateREADY)
		if _, err := dbgen.New(pool).InsertPhoto(ctx, dbgen.InsertPhotoParams{
			ID: platform.UUIDv4{}.New(), WorkspaceID: w.WorkspaceID, PlantID: w.PlantID,
			AssetID: asset, TakenAt: pgtype.Timestamptz{Time: time.Now(), Valid: true},
			DateSource: dbgen.PhotoDateSourceUPLOADFALLBACK, Viewpoint: dbgen.PhotoViewpointWHOLE,
		}); err != nil {
			t.Fatal(err)
		}
	}
	snap, err := BuildSnapshot(ctx, dbgen.New(pool),
		mustRow(dbgen.New(pool).GetWorkspaceByID(ctx, w.WorkspaceID)),
		mustRow(dbgen.New(pool).GetPlant(ctx, dbgen.GetPlantParams{ID: w.PlantID, WorkspaceID: w.WorkspaceID})), false)
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Activities) != SnapMaxActivities || len(snap.Photos) != SnapMaxPhotos {
		t.Fatalf("bounds not enforced: %d activities, %d photos", len(snap.Activities), len(snap.Photos))
	}
}

func mustRow[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}

func TestCapabilitiesBudgetView(t *testing.T) {
	ctx, pool := scratch(t)
	w := seedWorld(t, ctx, pool)
	lim := defaultLimits()
	lim.MonthCeilingMicroUSD = 50_000
	s := newSvc(pool, lim, newFakePort())

	_, probs, err := s.Request(ctx, w.Actor, textReq(w), nil)
	noProbs(t, probs, err)

	caps, view, err := s.Capabilities(ctx, w.WorkspaceID)
	if err != nil {
		t.Fatal(err)
	}
	if !caps[CapTextAnalyzer].Available || caps[CapTextAnalyzer].Model != "fake-model" {
		t.Fatalf("caps: %+v", caps[CapTextAnalyzer])
	}
	if view.DailyRunsUsed != 1 || view.DailyRunLimit != 10 {
		t.Fatalf("daily: %+v", view)
	}
	if view.MonthEncumberedMicroUSD != 4000 || view.MonthSpentMicroUSD != 0 {
		t.Fatalf("ledger: %+v", view)
	}
	if view.MonthCeilingMicroUSD != 50_000 {
		t.Fatalf("ceiling: %+v", view)
	}
}
