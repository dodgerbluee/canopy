package insights

// T33: owner commands over advice rows. Suggestions are advice, never
// logged care: accepting one creates exactly one explicit care task (a
// one-off CHECK occurrence on the plant's plan), dismissal records a
// reason, and every path re-checks the exact suggestion revision so a
// context or source change can never be accepted through silently.

import (
	"canopy/server/internal/agentcontext"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	dbgen "canopy/server/db/generated"
)

// Suggestion list bounds (mirror the journal lane's clamps).
const (
	SuggestDefaultPage = 25
	SuggestMaxPage     = 50
	MaxDismissReason   = 500 // matches the DB check on dismiss_reason
)

// SuggestionQuery is the service-layer shape of the frozen
// SuggestionFilter plus keyset paging.
type SuggestionQuery struct {
	PlantID  *pgtype.UUID
	Statuses []string
	Kinds    []string
	First    int32
	After    *string
}

type SuggestionPage struct {
	Items     []dbgen.Suggestion
	EndCursor *string
	HasNext   bool
}

// ListSuggestions pages (createdAt DESC, id DESC) with keyset cursors
// bound to the filter signature, exactly like the journal lane: a page
// cannot be resumed under a different filter.
func (s *Service) ListSuggestions(ctx context.Context, a Actor, qz SuggestionQuery) (SuggestionPage, []Problem, error) {
	var out SuggestionPage
	first := qz.First
	if first <= 0 {
		first = SuggestDefaultPage
	}
	if first > SuggestMaxPage {
		first = SuggestMaxPage
	}
	params := dbgen.ListSuggestionsPageParams{
		WorkspaceID: a.WorkspaceID,
		Statuses:    qz.Statuses,
		Kinds:       qz.Kinds,
		LimitCount:  first + 1,
	}
	if qz.PlantID != nil {
		params.PlantID = *qz.PlantID
	}
	sig := suggestionFilterSig(qz)
	if qz.After != nil && *qz.After != "" {
		created, id, err := decodeSuggestCursor(*qz.After, sig)
		if err != nil {
			return out, []Problem{problem(CodeValidation, "after", "cursor is not valid for this filter.")}, nil
		}
		params.AfterCreatedAt = pgtype.Timestamptz{Time: created, Valid: true}
		params.AfterID = id
	}
	rows, err := s.q().ListSuggestionsPage(ctx, params)
	if err != nil {
		return out, nil, err
	}
	if int32(len(rows)) > first {
		rows = rows[:first]
		out.HasNext = true
	}
	out.Items = rows
	if len(rows) > 0 {
		last := rows[len(rows)-1]
		cur := encodeSuggestCursor(sig, last.CreatedAt.Time, uuidString(last.ID))
		out.EndCursor = &cur
	}
	return out, nil, nil
}

// RunSuggestions returns the advice rows one run produced (the frozen
// AnalysisRun.suggestions field).
func (s *Service) RunSuggestions(ctx context.Context, a Actor, runID pgtype.UUID) ([]dbgen.Suggestion, error) {
	return s.q().ListSuggestionsByRun(ctx, runID)
}

// AcceptInput is the frozen AcceptSuggestionInput at the service edge.
// DueOn is a workspace-local calendar date; nil defaults to today.
type AcceptInput struct {
	SuggestionID     string
	ExpectedRevision int32
	DueOn            *time.Time
}

// Accept applies the frozen acceptSuggestion contract:
//   - CONFLICT when the suggestion revision moved (the row lock plus the
//     exact-revision guard make check+write atomic);
//   - VALIDATION when EXPIRED, or when the plant has no care plan to
//     host the task (the UI's "create a plan first" path);
//   - the first accept on a needs_review row performs the owner review
//     touch (revision bumps) and returns VALIDATION/REVIEW_REQUIRED so
//     the UI can show the evidence and ask for confirmation; a re-read
//     then accepts cleanly;
//   - success creates exactly one one-off CHECK occurrence (never a
//     completed activity) and stores its id as accepted_task_id; a
//     replay with the same clientMutationId returns the same task from
//     the mutation receipt, and a fresh-key accept on an ACCEPTED row
//     returns CONFLICT with the row whose acceptedTaskId names it.
func (s *Service) Accept(ctx context.Context, a Actor, in AcceptInput, cmid *string) (dbgen.Suggestion, []Problem, error) {
	return s.mutateSuggestion(ctx, a, "acceptSuggestion", cmid, in,
		func(ctx context.Context, q *dbgen.Queries, tx pgx.Tx) (dbgen.Suggestion, []Problem, bool) {
			var zero dbgen.Suggestion
			sid, ok := uuidFrom(in.SuggestionID)
			if !ok {
				return zero, []Problem{problem(CodeValidation, "input.suggestionId", "suggestionId must be a UUID.")}, false
			}
			row, err := q.LockSuggestion(ctx, dbgen.LockSuggestionParams{ID: sid, WorkspaceID: a.WorkspaceID})
			if errors.Is(err, pgx.ErrNoRows) {
				return zero, []Problem{problem(CodeNotFound, "input.suggestionId", "suggestion not found.")}, false
			}
			if err != nil {
				return zero, []Problem{problem(CodeInternal, "", "suggestion read failed.")}, false
			}
			switch row.Status {
			case dbgen.SuggestionStatusACCEPTED:
				// Fresh-key second accept: no second task, ever. The row
				// already names the one that exists.
				return row, []Problem{problemWithRevision(
					problem(CodeConflict, "input.expectedRevision", "suggestion already accepted; its task already exists."),
					row.Revision)}, false
			case dbgen.SuggestionStatusEXPIRED:
				return zero, []Problem{problem(CodeValidation, "input.suggestionId",
					"suggestion is expired and cannot be accepted.")}, false
			case dbgen.SuggestionStatusDISMISSED:
				return row, []Problem{problemWithRevision(
					problem(CodeConflict, "input.expectedRevision", "suggestion was dismissed; dismissal is final."),
					row.Revision)}, false
			}
			// Past expiry but not yet swept is still expired advice: the
			// sweeper is periodic, the guard is not.
			if row.ExpiresAt.Valid && !row.ExpiresAt.Time.After(s.now()) {
				return zero, []Problem{problem(CodeValidation, "input.suggestionId",
					"suggestion is expired and cannot be accepted.")}, false
			}
			if row.Revision != in.ExpectedRevision {
				return zero, []Problem{problemWithRevision(
					problem(CodeConflict, "input.expectedRevision", "suggestion changed since you read it."),
					row.Revision)}, false
			}
			if row.NeedsReview {
				// Owner review touch (the only path back to acceptable):
				// nothing is created; the UI shows the evidence, the owner
				// re-reads at the new revision and confirms. The touch
				// itself persists even though the response is a problem —
				// the revision bump is what makes the confirmation real.
				updated, err := q.ReviewSuggestion(ctx, dbgen.ReviewSuggestionParams{
					ID: sid, WorkspaceID: a.WorkspaceID, Revision: row.Revision,
				})
				if errors.Is(err, pgx.ErrNoRows) {
					return zero, []Problem{problemWithRevision(
						problem(CodeConflict, "input.expectedRevision", "suggestion changed since you read it."),
						row.Revision)}, false
				}
				if err != nil {
					return zero, []Problem{problem(CodeInternal, "", "review touch failed.")}, false
				}
				return updated, []Problem{problemWithRevision(problem(CodeValidation, "input.suggestionId",
					"REVIEW_REQUIRED: a source or the plant context changed; review the evidence, then accept at the current revision."),
					updated.Revision)}, true
			}

			ws, err := q.GetWorkspaceByID(ctx, a.WorkspaceID)
			if err != nil {
				return zero, []Problem{problem(CodeInternal, "", "workspace read failed.")}, false
			}
			due, probs := s.resolveDueOn(ws.Timezone, in.DueOn)
			if probs != nil {
				return zero, probs, false
			}
			plan, err := q.GetCarePlanByPlant(ctx, row.PlantID)
			if errors.Is(err, pgx.ErrNoRows) {
				return zero, []Problem{problem(CodeValidation, "input.suggestionId",
					"plant has no care plan to host the task; create or choose a plan first.")}, false
			}
			if err != nil {
				return zero, []Problem{problem(CodeInternal, "", "care plan read failed.")}, false
			}

			// Exactly one explicit task: a one-off CHECK occurrence keyed
			// to the suggestion id, so even a receipt-loss replay cannot
			// double-create (the DB unique key is the backstop).
			occID := s.uuid.New()
			missing := jsonTextArray(row.MissingFacts)
			notes := []string{}
			if row.Observation.Valid {
				notes = append(notes, row.Observation.String)
			}
			if row.Hypothesis.Valid {
				notes = append(notes, row.Hypothesis.String)
			}
			if _, err := q.InsertCareOccurrenceUpsert(ctx, dbgen.InsertCareOccurrenceUpsertParams{
				ID: occID, WorkspaceID: a.WorkspaceID, PlanID: plan.ID,
				RuleKey: "suggestion", Kind: dbgen.RuleKindCHECK,
				OccurrenceKey:   "suggestion:" + uuidString(row.ID),
				OriginalDueDate: due, CurrentDueDate: due,
				PlanTimezone: plan.Timezone,
				MissingFacts: missing, EvidenceNotes: notes,
			}); err != nil {
				return zero, []Problem{problem(CodeInternal, "", "task creation failed.")}, false
			}

			updated, err := q.AcceptSuggestion(ctx, dbgen.AcceptSuggestionParams{
				ID: sid, WorkspaceID: a.WorkspaceID, AcceptedTaskID: occID, Revision: row.Revision,
			})
			if errors.Is(err, pgx.ErrNoRows) {
				return zero, []Problem{problemWithRevision(
					problem(CodeConflict, "input.expectedRevision", "suggestion changed since you read it."),
					row.Revision)}, false
			}
			if err != nil {
				return zero, []Problem{problem(CodeInternal, "", "acceptance write failed.")}, false
			}

			// Accepted synthesis enters the owner's journey (frozen
			// InsightEntry member): one INSIGHT line per run+plant, upsert
			// so multiple accepted suggestions from one run keep one line.
			if row.RunID.Valid {
				if _, err := q.UpsertJournalEntry(ctx, dbgen.UpsertJournalEntryParams{
					ID: s.uuid.New(), WorkspaceID: a.WorkspaceID, PlantID: row.PlantID,
					EntryType:  dbgen.JournalEntryTypeINSIGHT,
					SourceID:   row.RunID,
					OccurredAt: pgtype.Timestamptz{Time: s.now().UTC(), Valid: true},
					Summary:    truncateRunes(row.Action, 280),
				}); err != nil {
					return zero, []Problem{problem(CodeInternal, "", "journal projection failed.")}, false
				}
			}
			return updated, nil, false
		})
}

// DismissInput is the frozen DismissSuggestionInput at the service edge.
type DismissInput struct {
	SuggestionID     string
	ExpectedRevision int32
	Reason           *string
}

// Dismiss applies the frozen dismissSuggestion contract: PROPOSED only,
// exact revision, optional bounded reason.
func (s *Service) Dismiss(ctx context.Context, a Actor, in DismissInput, cmid *string) (dbgen.Suggestion, []Problem, error) {
	return s.mutateSuggestion(ctx, a, "dismissSuggestion", cmid, in,
		func(ctx context.Context, q *dbgen.Queries, tx pgx.Tx) (dbgen.Suggestion, []Problem, bool) {
			var zero dbgen.Suggestion
			sid, ok := uuidFrom(in.SuggestionID)
			if !ok {
				return zero, []Problem{problem(CodeValidation, "input.suggestionId", "suggestionId must be a UUID.")}, false
			}
			reason := ""
			if in.Reason != nil {
				reason = strings.TrimSpace(*in.Reason)
				if utf8Len(reason) > MaxDismissReason {
					return zero, []Problem{problem(CodeValidation, "input.reason",
						fmt.Sprintf("reason must stay within %d characters.", MaxDismissReason))}, false
				}
			}
			row, err := q.LockSuggestion(ctx, dbgen.LockSuggestionParams{ID: sid, WorkspaceID: a.WorkspaceID})
			if errors.Is(err, pgx.ErrNoRows) {
				return zero, []Problem{problem(CodeNotFound, "input.suggestionId", "suggestion not found.")}, false
			}
			if err != nil {
				return zero, []Problem{problem(CodeInternal, "", "suggestion read failed.")}, false
			}
			if row.Status != dbgen.SuggestionStatusPROPOSED {
				code := CodeConflict
				msg := "suggestion is no longer open; dismissal needs a PROPOSED suggestion."
				if row.Status == dbgen.SuggestionStatusEXPIRED {
					code = CodeValidation
					msg = "suggestion is expired; expired advice needs no dismissal."
				}
				return row, []Problem{problemWithRevision(problem(code, "input.expectedRevision", msg), row.Revision)}, false
			}
			// Past expiry but not yet swept: expired advice needs no
			// dismissal either.
			if row.ExpiresAt.Valid && !row.ExpiresAt.Time.After(s.now()) {
				return row, []Problem{problemWithRevision(problem(CodeValidation, "input.expectedRevision",
					"suggestion is expired; expired advice needs no dismissal."), row.Revision)}, false
			}
			if row.Revision != in.ExpectedRevision {
				return zero, []Problem{problemWithRevision(
					problem(CodeConflict, "input.expectedRevision", "suggestion changed since you read it."),
					row.Revision)}, false
			}
			updated, err := q.DismissSuggestion(ctx, dbgen.DismissSuggestionParams{
				ID: sid, WorkspaceID: a.WorkspaceID,
				DismissReason: pgTextOf(reason), Revision: row.Revision,
			})
			if errors.Is(err, pgx.ErrNoRows) {
				return zero, []Problem{problemWithRevision(
					problem(CodeConflict, "input.expectedRevision", "suggestion changed since you read it."),
					row.Revision)}, false
			}
			if err != nil {
				return zero, []Problem{problem(CodeInternal, "", "dismissal write failed.")}, false
			}
			return updated, nil, false
		})
}

// mutateSuggestion mirrors mutateRun (same receipt replay + request-hash
// idempotency), typed for suggestion outputs. The persist flag lets a
// mutation commit its touch even while returning a problem (the review
// confirmation is the one path where the response fails but the
// revision bump must survive); touch paths deliberately skip the receipt
// so a replay re-runs the guard instead of replaying a stale problem.
func (s *Service) mutateSuggestion(ctx context.Context, a Actor, op string, cmid *string, input any,
	fn func(ctx context.Context, q *dbgen.Queries, tx pgx.Tx) (dbgen.Suggestion, []Problem, bool),
) (dbgen.Suggestion, []Problem, error) {
	var zero dbgen.Suggestion
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return zero, nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := dbgen.New(tx)
	if err := agentcontext.Enter(ctx, tx); err != nil {
		return zero, nil, err
	}

	key := ""
	if cmid != nil {
		key = strings.TrimSpace(*cmid)
	}
	if key != "" {
		lockID := uuidString(a.WorkspaceID) + "|" + op + "|" + key
		if err := q.LockMutationKey(ctx, lockID); err != nil {
			return zero, nil, err
		}
		rec, err := q.GetMutationReceipt(ctx, dbgen.GetMutationReceiptParams{
			WorkspaceID: a.WorkspaceID, Operation: op, IdempotencyKey: key,
		})
		switch {
		case err == nil:
			if rec.RequestHash != requestHash(input) {
				return zero, []Problem{problem(CodeIdempotencyConflict, "clientMutationId",
					"clientMutationId was already used for a different request.")}, nil
			}
			var out dbgen.Suggestion
			if err := json.Unmarshal(rec.Result, &out); err != nil {
				return zero, nil, fmt.Errorf("insights: receipt decode: %w", err)
			}
			return out, nil, nil
		case !errors.Is(err, pgx.ErrNoRows):
			return zero, nil, err
		}
	}

	sugg, probs, persist := fn(ctx, q, tx)
	if len(probs) > 0 && !persist {
		// Problem paths persist nothing.
		return sugg, probs, nil
	}
	if key != "" && len(probs) == 0 {
		raw, err := json.Marshal(sugg)
		if err != nil {
			return zero, nil, err
		}
		if _, err := q.InsertMutationReceipt(ctx, dbgen.InsertMutationReceiptParams{
			ID: s.uuid.New(), WorkspaceID: a.WorkspaceID, Operation: op, IdempotencyKey: key,
			RequestHash: requestHash(input), Result: raw,
			ExpiresAt: pgtype.Timestamptz{Time: s.now().Add(ReceiptRetention), Valid: true},
		}); err != nil {
			return zero, nil, err
		}
	}
	if len(probs) == 0 {
		if err := agentcontext.Complete(ctx, tx, sugg); err != nil {
			return zero, nil, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return zero, nil, err
	}
	return sugg, probs, nil
}

// resolveDueOn clamps the created task's due date: default today in the
// workspace timezone; a past local date is a validation failure (03:
// tasks live in the owner's future).
func (s *Service) resolveDueOn(tz string, dueOn *time.Time) (pgtype.Date, []Problem) {
	today := localDate(tz, s.now())
	if dueOn == nil {
		return pgtype.Date{Time: today, Valid: true}, nil
	}
	d := dueOn.UTC().Truncate(24 * time.Hour)
	if d.Before(today) {
		return pgtype.Date{}, []Problem{problem(CodeValidation, "input.dueOn",
			"dueOn cannot be in the past; the task must live in the owner's future.")}
	}
	return pgtype.Date{Time: d, Valid: true}, nil
}

// localDate is today's calendar date in an IANA timezone.
func localDate(tz string, now time.Time) time.Time {
	loc, err := time.LoadLocation(tz)
	if err != nil {
		loc = time.UTC
	}
	y, m, d := now.In(loc).Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

func suggestionFilterSig(qz SuggestionQuery) string {
	var b strings.Builder
	if qz.PlantID != nil {
		b.WriteString(uuidString(*qz.PlantID))
	}
	b.WriteByte('|')
	st := append([]string(nil), qz.Statuses...)
	sort.Strings(st)
	b.WriteString(strings.Join(st, ","))
	b.WriteByte('|')
	ki := append([]string(nil), qz.Kinds...)
	sort.Strings(ki)
	b.WriteString(strings.Join(ki, ","))
	sum := sha256.Sum256([]byte(b.String()))
	return hex.EncodeToString(sum[:8])
}

func encodeSuggestCursor(sig string, createdAt time.Time, id string) string {
	payload := sig + "|" + createdAt.UTC().Format(time.RFC3339Nano) + "|" + id
	return base64.RawURLEncoding.EncodeToString([]byte(payload))
}

func decodeSuggestCursor(cur, sig string) (time.Time, pgtype.UUID, error) {
	raw, err := base64.RawURLEncoding.DecodeString(cur)
	if err != nil {
		return time.Time{}, pgtype.UUID{}, errors.New("cursor encoding")
	}
	parts := strings.SplitN(string(raw), "|", 3)
	if len(parts) != 3 || parts[0] != sig {
		return time.Time{}, pgtype.UUID{}, errors.New("cursor filter mismatch")
	}
	created, err := time.Parse(time.RFC3339Nano, parts[1])
	if err != nil {
		return time.Time{}, pgtype.UUID{}, errors.New("cursor time")
	}
	id, ok := uuidFrom(parts[2])
	if !ok {
		return time.Time{}, pgtype.UUID{}, errors.New("cursor id")
	}
	return created.UTC(), id, nil
}

// jsonTextArray decodes a jsonb text array into a Go slice for the
// occurrence missing_facts column; malformed input degrades to empty
// rather than failing the acceptance (the facts stay on the suggestion).
func jsonTextArray(raw []byte) []string {
	out := []string{}
	if len(raw) == 0 {
		return out
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return []string{}
	}
	return out
}

func uuidFrom(s string) (pgtype.UUID, bool) {
	u, err := parseUUID(s)
	return u, err == nil
}

func utf8Len(s string) int { return utf8.RuneCountInString(s) }

func truncateRunes(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max])
}

// ExpireDueSuggestions flips PROPOSED rows past their expiry to EXPIRED
// (sweeper). Returns the affected count.
func (s *Service) ExpireDueSuggestions(ctx context.Context) (int, error) {
	ids, err := s.q().ExpireDueSuggestions(ctx)
	if err != nil {
		return 0, err
	}
	return len(ids), nil
}
