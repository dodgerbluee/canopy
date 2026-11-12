package insights

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/riverqueue/river"

	dbgen "canopy/server/db/generated"
	"canopy/server/internal/agentcontext"
	"canopy/server/internal/aiconnection"
)

// MaxProviderAttempts is the hard bound on provider attempts per run
// (03: timeout/429/5xx retried up to 3 attempts). The single bounded
// schema repair is budgeted separately and always reserves before
// dispatch.
const MaxProviderAttempts = 3

// RunArgs carries only the run id: the worker re-reads the row, so a
// snoozed retry always claims against the CURRENT generation.
type RunArgs struct{ RunID string }

func (RunArgs) Kind() string { return "analysis:run" }

// RunWorker executes one analysis job. River delivers it after the
// enqueue tx committed; every write is generation-fenced.
type RunWorker struct {
	river.WorkerDefaults[RunArgs]
	Svc *Service
}

func (w *RunWorker) Work(ctx context.Context, job *river.Job[RunArgs]) error {
	return w.Svc.ProcessRun(ctx, job)
}

var errTargetGone = errors.New("insights: analysis target changed or gone")

// ProcessRun drives one run through claim → provider → validate →
// commit with the retry/repair/staleness rules of 03. Every terminal
// write is generation-fenced, so a superseded worker can never land a
// late answer.
func (s *Service) ProcessRun(ctx context.Context, job *river.Job[RunArgs]) error {
	log := s.log()
	id, err := parseUUID(job.Args.RunID)
	if err != nil {
		return nil // corrupt args: drop
	}
	run, err := s.q().GetAnalysisRunByID(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil // purged
	}
	if err != nil {
		return err
	}
	if run.State != dbgen.AnalysisRunStateQUEUED && run.State != dbgen.AnalysisRunStateRUNNING {
		return nil // terminal (cancelled/succeeded/failed/stale)
	}

	// Pre-claim validity: a changed/deleted target makes the run STALE
	// before any provider is billed for it.
	snap, err := s.snapshotStillValid(ctx, run)
	if errors.Is(err, errTargetGone) {
		return s.markStale(ctx, id)
	}
	if err != nil {
		return err
	}

	// Claim (generation fence + attempts bound in SQL).
	claimed, err := s.q().ClaimAnalysisRun(ctx, dbgen.ClaimAnalysisRunParams{ID: id, Generation: run.Generation})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil // cancelled, superseded, or attempts exhausted
	}
	if err != nil {
		return err
	}

	if !s.agentJobAllowed(ctx, claimed) {
		return s.failRun(ctx, claimed, dbgen.AnalysisFailureCodePROVIDERUNAVAILABLE, "The initiating agent connection is no longer authorized.", nil)
	}
	// Capability may have vanished since enqueue (operator removed the
	// endpoint): honest failure, retriable by a fresh request later.
	caps := s.Provider.Capabilities(aiconnection.WithWorkspace(ctx, claimed.WorkspaceID))
	if st := caps[Capability(claimed.Capability)]; !st.Available {
		return s.failRun(ctx, claimed, dbgen.AnalysisFailureCodePROVIDERUNAVAILABLE,
			"No provider is configured for this analysis; ask the operator to enable one.", nil)
	}

	// Retry attempts (2nd claim onward) reserve their own max cost
	// before dispatch, under the workspace budget lock.
	if claimed.Attempts > 1 {
		ok, err := s.reserveAttempt(ctx, claimed)
		if errors.Is(err, errTargetGone) {
			return nil // generation bumped mid-reserve: superseded
		}
		if err != nil {
			return err
		}
		if !ok {
			return s.failRun(ctx, claimed, dbgen.AnalysisFailureCodeBUDGETEXCEEDED,
				"Monthly external-cost ceiling reached; the retry was not dispatched.", nil)
		}
	}

	out, perr := s.dispatch(ctx, claimed, claimed.InputSnapshot, "")
	if perr != nil {
		switch perr.Class {
		case ClassTimeout, ClassRetryable:
			if claimed.Attempts < MaxProviderAttempts {
				delay := backoff(claimed.Attempts)
				log.Info("insights: provider attempt failed; snoozing",
					"run", uuidString(claimed.ID), "attempt", claimed.Attempts, "class", perr.Class)
				return river.JobSnooze(delay)
			}
			code := dbgen.AnalysisFailureCodePROVIDERUNAVAILABLERETRYABLE
			if perr.Class == ClassTimeout {
				code = dbgen.AnalysisFailureCodeTIMEOUT
			}
			// Charged timeout: the provider may still bill; the
			// reservation stays encumbered (actual unknown).
			return s.failRun(ctx, claimed, code, safeFailureMessage(perr), nil)
		case ClassInvalidOutput:
			return s.repairOnce(ctx, claimed, nil)
		default: // ClassPermanent, ClassUnavailable
			return s.failRun(ctx, claimed, dbgen.AnalysisFailureCodePROVIDERUNAVAILABLE,
				safeFailureMessage(perr), nil)
		}
	}

	canon, verr := ValidateResult(claimed.Kind, snap, out.Raw, s.MaxResultBytes)
	if verr != nil {
		var ve *ValidationError
		if !errors.As(verr, &ve) {
			return verr // internal, not provider's fault
		}
		log.Info("insights: provider output invalid; attempting one bounded repair",
			"run", uuidString(claimed.ID), "why", ve.Msg)
		return s.repairOnce(ctx, claimed, out.Raw)
	}
	return s.commit(ctx, claimed, snap, out, canon)
}

// repairOnce is the single bounded schema-repair request (03): it
// reserves its own max cost before dispatch and re-validates; a second
// invalid answer ends the run REPAIR_FAILED.
func (s *Service) repairOnce(ctx context.Context, claimed dbgen.AnalysisRun, badRaw []byte) error {
	ok, err := s.reserveAttempt(ctx, claimed)
	if errors.Is(err, errTargetGone) {
		return nil
	}
	if err != nil {
		return err
	}
	if !ok {
		return s.failRun(ctx, claimed, dbgen.AnalysisFailureCodeBUDGETEXCEEDED,
			"Monthly external-cost ceiling reached; the repair was not dispatched.", nil)
	}
	repairOf := ""
	if len(badRaw) > 0 {
		repairOf = string(badRaw)
	}
	snap, err := ParseSnapshot(claimed.InputSnapshot)
	if err != nil {
		return s.failRun(ctx, claimed, dbgen.AnalysisFailureCodeINTERNAL,
			"Stored snapshot unreadable.", nil)
	}
	out, perr := s.dispatch(ctx, claimed, claimed.InputSnapshot, repairOf)
	if perr != nil {
		return s.failRun(ctx, claimed, dbgen.AnalysisFailureCodeREPAIRFAILED,
			"The schema repair request failed: "+safeFailureMessage(perr), nil)
	}
	canon, verr := ValidateResult(claimed.Kind, snap, out.Raw, s.MaxResultBytes)
	if verr != nil {
		if s.Log != nil {
			s.Log.Info("insights: repaired output failed validation", "run", uuidString(claimed.ID), "why", verr.Error())
		}
		return s.failRun(ctx, claimed, dbgen.AnalysisFailureCodeREPAIRFAILED,
			"The AI reply could not be verified against this plant’s records after one repair. Try a new review; no care records were changed.", nil)
	}
	return s.commit(ctx, claimed, snap, out, canon)
}

// dispatch performs one provider attempt under the per-attempt timeout.
func (s *Service) dispatch(ctx context.Context, claimed dbgen.AnalysisRun, snapshot []byte, repairOf string) (AnalyzeOutput, *ProviderError) {
	if !s.agentJobAllowed(ctx, claimed) {
		return AnalyzeOutput{}, &ProviderError{Class: ClassUnavailable, Err: errors.New("initiating agent access revoked")}
	}
	actx, cancel := context.WithTimeout(ctx, s.AttemptTimeout)
	defer cancel()
	out, err := s.Provider.Analyze(actx, AnalyzeInput{
		RunID:          uuidString(claimed.ID),
		WorkspaceID:    uuidString(claimed.WorkspaceID),
		Capability:     Capability(claimed.Capability),
		Snapshot:       json.RawMessage(snapshot),
		PromptVersion:  s.PromptVersion,
		RepairOf:       repairOf,
		ReceiptAssetID: uuidString(claimed.ReceiptAssetID),
		PhotoAssetIDs:  targetPhotoAssets(claimed, snapshot),
	})
	if err != nil {
		var pe *ProviderError
		if errors.As(err, &pe) {
			return out, pe
		}
		return out, &ProviderError{Class: ClassPermanent, Err: err}
	}
	return out, nil
}

// targetPhotoAssets resolves photo asset ids for byte-level photo
// adapters: the VISION comparison pair from the run row (whose
// photo_id_a/b columns are pinned to that purpose), or, for
// IDENTIFICATION runs, the plant's snapshot photos.
func targetPhotoAssets(run dbgen.AnalysisRun, snapshot []byte) []string {
	var ids []string
	if run.PhotoIDA.Valid || run.PhotoIDB.Valid {
		for _, u := range []pgtype.UUID{run.PhotoIDA, run.PhotoIDB} {
			if u.Valid {
				ids = append(ids, uuidString(u))
			}
		}
		return ids
	}
	if run.Kind == dbgen.AnalysisKindIDENTIFICATION {
		if snap, err := ParseSnapshot(snapshot); err == nil {
			for _, p := range snap.Photos {
				if len(ids) >= 2 {
					break
				}
				ids = append(ids, p.AssetID)
			}
		}
	}
	return ids
}

// reserveAttempt atomically (workspace budget lock + ledger check +
// reservation update) encumbers one more attempt's max cost. false means
// no headroom; errTargetGone means the row was superseded.
func (s *Service) reserveAttempt(ctx context.Context, claimed dbgen.AnalysisRun) (bool, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := dbgen.New(tx)
	if err := q.LockWorkspaceBudget(ctx, budgetLockID(claimed.WorkspaceID)); err != nil {
		return false, err
	}
	if s.Limit.MonthCeilingMicroUSD > 0 {
		mStart, mEnd := utcMonth(claimed.RequestedAt.Time)
		led, err := q.BudgetMonthLedger(ctx, dbgen.BudgetMonthLedgerParams{
			WorkspaceID:   claimed.WorkspaceID,
			RequestedAt:   pgtype.Timestamptz{Time: mStart, Valid: true},
			RequestedAt_2: pgtype.Timestamptz{Time: mEnd, Valid: true},
		})
		if err != nil {
			return false, err
		}
		if led.EncumberedMicroUsd+led.SpentMicroUsd+s.Limit.AttemptCostMicroUSD > s.Limit.MonthCeilingMicroUSD {
			return false, nil
		}
	}
	_, err = q.ReserveAttemptCost(ctx, dbgen.ReserveAttemptCostParams{
		ExtraMicroUsd: pgtype.Int8{Int64: s.Limit.AttemptCostMicroUSD, Valid: true},
		ID:            claimed.ID,
		Generation:    claimed.Generation,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return false, errTargetGone
	}
	if err != nil {
		return false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return false, err
	}
	return true, nil
}

// commit writes the validated result, generation-fenced. A bumped
// generation (target change, supersede, cancel during the provider
// call) means the late answer is dropped and the run ends STALE —
// never resurrected. For SYNTHESIS the suggestion projection lands in
// the same tx: a SUCCEEDED synthesis either owns its advice rows or
// nothing persisted (T33).
func (s *Service) commit(ctx context.Context, claimed dbgen.AnalysisRun, snap *Snapshot, out AnalyzeOutput, canon json.RawMessage) error {
	// Change during the provider call: drop before writing.
	if _, err := s.snapshotStillValid(ctx, claimed); errors.Is(err, errTargetGone) {
		return s.markStale(ctx, claimed.ID)
	}
	actual := pgtype.Int8{}
	if out.UsageCostMicroUSD != nil {
		actual = pgtype.Int8{Int64: *out.UsageCostMicroUSD, Valid: true}
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := dbgen.New(tx)
	_, err = q.CommitAnalysisRun(ctx, dbgen.CommitAnalysisRunParams{
		ID: claimed.ID, Generation: claimed.Generation, Result: canon,
		Provider: pgTextOf(out.Provider), Model: pgTextOf(out.Model),
		PromptVersion: pgTextOf(s.PromptVersion), ResultSchemaVersion: pgTextOf(s.SchemaVersion),
		CostActualMicroUsd: actual,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		// Generation fence: a late answer cannot write.
		return s.markStale(ctx, claimed.ID)
	}
	if err != nil {
		return err
	}
	if claimed.Kind == dbgen.AnalysisKindSYNTHESIS {
		if err := s.projectSuggestions(ctx, q, claimed, snap, canon); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// Suggestion projection bounds (T33): synthesis output becomes a small
// number of owner-actionable advice rows, never a wall of text. Cost
// figures come from the snapshot's ledger-derived section, never from
// model text, so "costs match purchases" holds by construction.
const (
	SuggestMaxFollowUps = 5
	SuggestMaxTotal     = 8
	SuggestExpiresDays  = 30
)

// projectSuggestions turns one validated SYNTHESIS result into advice
// rows: each follow-up check becomes a CARE suggestion carrying the
// run's structured parts, and each per-currency cost total becomes a
// COST suggestion quoting the exact ledger aggregate.
func (s *Service) projectSuggestions(ctx context.Context, q *dbgen.Queries, claimed dbgen.AnalysisRun,
	snap *Snapshot, canon json.RawMessage,
) error {
	var res ObsResult
	if err := json.Unmarshal(canon, &res); err != nil {
		return fmt.Errorf("insights: synthesis result decode: %w", err)
	}
	expires := pgtype.Timestamptz{Time: s.now().UTC().Add(SuggestExpiresDays * 24 * time.Hour), Valid: true}

	obsText, hypText := "", ""
	if len(res.VisibleObservations) > 0 {
		obsText = res.VisibleObservations[0].Text
	}
	if len(res.Hypotheses) > 0 {
		hypText = res.Hypotheses[0].Text
	}
	// Evidence union of the validated parts; the validator already closed
	// these against the snapshot, so suggestion evidence stays traceable.
	evSeen := map[string]bool{}
	ev := []string{}
	for _, o := range res.VisibleObservations {
		for _, id := range o.EvidenceIDs {
			if !evSeen[id] {
				evSeen[id] = true
				ev = append(ev, id)
			}
		}
	}
	for _, h := range res.Hypotheses {
		for _, id := range h.EvidenceIDs {
			if !evSeen[id] {
				evSeen[id] = true
				ev = append(ev, id)
			}
		}
	}
	evJSON, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	missing := res.MissingInformation
	if len(missing) > MaxInfoStrings {
		missing = missing[:MaxInfoStrings]
	}
	missingJSON, err := json.Marshal(missing)
	if err != nil {
		return err
	}

	n := 0
	for _, check := range res.FollowUpChecks {
		if n >= SuggestMaxFollowUps || n >= SuggestMaxTotal {
			break
		}
		if _, err := q.InsertSuggestion(ctx, dbgen.InsertSuggestionParams{
			ID: s.uuid.New(), WorkspaceID: claimed.WorkspaceID, PlantID: claimed.PlantID,
			RunID: claimed.ID, Kind: dbgen.SuggestionKindCARE, Severity: dbgen.SuggestionSeverityMEDIUM,
			Observation: pgTextOf(obsText), Hypothesis: pgTextOf(hypText), Action: check,
			EvidenceIds: evJSON, MissingFacts: missingJSON, ExpiresAt: expires,
		}); err != nil {
			return fmt.Errorf("insights: project follow-up suggestion: %w", err)
		}
		n++
	}
	for _, ct := range snap.CostTotals {
		if n >= SuggestMaxTotal {
			break
		}
		purchaseIDs := []string{}
		for _, c := range snap.Costs {
			if c.Currency == ct.Currency {
				purchaseIDs = append(purchaseIDs, c.ID)
			}
		}
		pj, err := json.Marshal(purchaseIDs)
		if err != nil {
			return err
		}
		obs := fmt.Sprintf(
			"Recorded purchases allocated to this plant total %s %s minor units across %d purchase(s). "+
				"These figures are the exact money-ledger totals, not model estimates.",
			ct.TotalMinor, ct.Currency, ct.PurchaseCount)
		act := "Review these purchase records if the figures look wrong; correct or void the purchase before acting on any cost-based advice."
		if _, err := q.InsertSuggestion(ctx, dbgen.InsertSuggestionParams{
			ID: s.uuid.New(), WorkspaceID: claimed.WorkspaceID, PlantID: claimed.PlantID,
			RunID: claimed.ID, Kind: dbgen.SuggestionKindCOST, Severity: dbgen.SuggestionSeverityLOW,
			Observation: pgTextOf(obs), Hypothesis: pgtype.Text{}, Action: act,
			EvidenceIds: pj, MissingFacts: []byte(`[]`), ExpiresAt: expires,
		}); err != nil {
			return fmt.Errorf("insights: project cost suggestion: %w", err)
		}
		n++
	}
	return nil
}

func (s *Service) failRun(ctx context.Context, claimed dbgen.AnalysisRun, code dbgen.AnalysisFailureCode, msg string, actual *int64) error {
	act := pgtype.Int8{}
	if actual != nil {
		act = pgtype.Int8{Int64: *actual, Valid: true}
	}
	_, err := s.q().FailAnalysisRun(ctx, dbgen.FailAnalysisRunParams{
		ID: claimed.ID, Generation: claimed.Generation,
		FailureCode:        dbgen.NullAnalysisFailureCode{AnalysisFailureCode: code, Valid: true},
		FailureMessage:     pgTextOf(truncate(msg, 300)),
		CostActualMicroUsd: act,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil // superseded mid-failure: newer writer owns the row
	}
	return err
}

func (s *Service) markStale(ctx context.Context, id pgtype.UUID) error {
	_, err := s.q().MarkAnalysisRunStale(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil // already terminal (e.g. cancelled first)
	}
	return err
}

// snapshotStillValid re-checks the frozen snapshot against live rows:
// plant revision/status drift or a receipt asset that left READY means
// the stored input no longer describes the target (03: STALE, never a
// silent re-snapshot).
func (s *Service) snapshotStillValid(ctx context.Context, run dbgen.AnalysisRun) (*Snapshot, error) {
	snap, err := ParseSnapshot(run.InputSnapshot)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", errTargetGone, err)
	}
	q := s.q()
	if snap.Plant.ID != "" {
		plant, err := q.GetPlant(ctx, dbgen.GetPlantParams{ID: pgUUID(snap.Plant.ID), WorkspaceID: run.WorkspaceID})
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, errTargetGone
		}
		if err != nil {
			return nil, err
		}
		if plant.Status != dbgen.PlantStatusACTIVE || plant.Revision != snap.Plant.Revision {
			return nil, errTargetGone
		}
	}
	if run.ReceiptAssetID.Valid {
		if _, err := q.GetReadyReceiptForRun(ctx, dbgen.GetReadyReceiptForRunParams{
			ID: run.ReceiptAssetID, WorkspaceID: run.WorkspaceID,
		}); errors.Is(err, pgx.ErrNoRows) {
			return nil, errTargetGone
		} else if err != nil {
			return nil, err
		}
	}
	return snap, nil
}

// backoff is bounded exponential with jitter (03: "bounded exponential
// jitter"): 5s, 10s, 20s … capped at 5 minutes, plus up to 25% jitter.
func backoff(attempt int32) time.Duration {
	base := 5 * time.Second * time.Duration(int64(1)<<uint(attempt-1))
	if base > 5*time.Minute {
		base = 5 * time.Minute
	}
	return base + time.Duration(rand.Int64N(int64(base/4)+1))
}

// safeFailureMessage keeps failure text safe for the UI: no provider
// bodies, no secrets, bounded length.
func safeFailureMessage(pe *ProviderError) string {
	switch pe.Class {
	case ClassTimeout:
		return "The provider request timed out."
	case ClassRetryable:
		return fmt.Sprintf("The provider is busy or unavailable (status %d).", pe.Status)
	case ClassInvalidOutput:
		return "The provider returned an unreadable response."
	default:
		return "The provider could not be reached."
	}
}

func (s *Service) log() *slog.Logger {
	if s.Log == nil {
		return slog.New(slog.DiscardHandler)
	}
	return s.Log
}

func pgTextOf(s string) pgtype.Text {
	if s == "" {
		return pgtype.Text{}
	}
	return pgtype.Text{String: s, Valid: true}
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

func (s *Service) agentJobAllowed(ctx context.Context, run dbgen.AnalysisRun) bool {
	check := func(scope string) bool {
		return agentcontext.JobAllowed(ctx, s.Pool, uuidString(run.AgentGrantID), uuidString(run.AgentCredentialID), uuidString(run.WorkspaceID), scope, uuidString(run.PlantID))
	}
	if !check("analysis:run") {
		return false
	}
	switch run.Kind {
	case dbgen.AnalysisKindRECEIPTEXTRACTION:
		return check("receipts:read") && check("purchases:read")
	case dbgen.AnalysisKindSYNTHESIS:
		return check("garden:read") && check("purchases:read")
	case dbgen.AnalysisKindVISION, dbgen.AnalysisKindIDENTIFICATION:
		return check("garden:read") && check("photos:read")
	default:
		return check("garden:read")
	}
}
