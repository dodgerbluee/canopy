// T33 flagging wiring in the care lane: a plant context edit (collection
// hook) and a care profile version retirement must force owner review on
// pending analysis advice, so stale suggestions can never be accepted
// through silently (03 source/context-change rule).
package care

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	dbgen "canopy/server/db/generated"
	"canopy/server/internal/platform"
)

func seedAdvice(t *testing.T, ctx context.Context, pool *pgxpool.Pool, a Actor,
	plantID, runID pgtype.UUID, action string,
) dbgen.Suggestion {
	t.Helper()
	s, err := dbgen.New(pool).InsertSuggestion(ctx, dbgen.InsertSuggestionParams{
		ID: platform.UUIDv4{}.New(), WorkspaceID: a.WorkspaceID, PlantID: plantID, RunID: runID,
		Kind: dbgen.SuggestionKindCARE, Severity: dbgen.SuggestionSeverityMEDIUM, Action: action,
		EvidenceIds: []byte(`[]`), MissingFacts: []byte(`[]`),
		ExpiresAt: pgtype.Timestamptz{Time: time.Now().Add(24 * time.Hour), Valid: true},
	})
	if err != nil {
		t.Fatalf("suggestion: %v", err)
	}
	return s
}

func reloadAdvice(t *testing.T, ctx context.Context, pool *pgxpool.Pool, a Actor, id pgtype.UUID) dbgen.Suggestion {
	t.Helper()
	s, err := dbgen.New(pool).GetSuggestion(ctx, dbgen.GetSuggestionParams{ID: id, WorkspaceID: a.WorkspaceID})
	if err != nil {
		t.Fatalf("get suggestion: %v", err)
	}
	return s
}

func TestT33ContextChangeFlagsAdvice(t *testing.T) {
	ctx, pool, a := scratch(t)
	s := svc(pool)
	plant := mkPlant(t, pool, a)
	sugg := seedAdvice(t, ctx, pool, a, plant.ID, pgtype.UUID{}, "Check the saucer for standing water.")

	h := CollectionHook{Svc: s}
	if err := h.PlantContextChanged(ctx, dbgen.New(pool), a.WorkspaceID, plant.ID, time.Now()); err != nil {
		t.Fatalf("hook: %v", err)
	}
	got := reloadAdvice(t, ctx, pool, a, sugg.ID)
	if !got.NeedsReview || got.Revision != sugg.Revision+1 {
		t.Fatalf("context change must flag with one revision bump: %+v", got)
	}

	// Repeated edits do not inflate the revision or force repeat
	// confirmations on advice already awaiting review.
	if err := h.PlantContextChanged(ctx, dbgen.New(pool), a.WorkspaceID, plant.ID, time.Now()); err != nil {
		t.Fatal(err)
	}
	again := reloadAdvice(t, ctx, pool, a, sugg.ID)
	if again.Revision != got.Revision {
		t.Fatalf("flagging re-bumped an already-flagged row: %d -> %d", got.Revision, again.Revision)
	}
}

func TestT33RetireFlagsCitedAdvice(t *testing.T) {
	ctx, pool, a := scratch(t)
	s := svc(pool)

	// Published (REVIEWED) profile version, via the real publish flow so
	// attribution constraints hold.
	src := mkSource(t, s, a, "Retire-flag source")
	vid := src.Versions[0].ID
	p, probs, err := s.CreateCareProfileDraft(ctx, a, CreateCareProfileDraftInput{
		Category: "FIG", Rules: []RuleInput{waterRule(vid)},
	}, nil)
	noProbs(t, probs, err)
	pub, probs, err := s.PublishCareProfile(ctx, a, PublishCareProfileInput{
		ProfileID: p.ID, ExpectedRevision: p.Revision, ReviewerLabel: "owner", ReviewedOn: "2026-11-02",
	}, nil)
	noProbs(t, probs, err)
	var reviewedID string
	for _, v := range pub.Versions {
		if v.Status == "REVIEWED" {
			reviewedID = v.ID
		}
	}
	if reviewedID == "" {
		t.Fatalf("no reviewed version: %+v", pub.Versions)
	}

	plant := mkPlant(t, pool, a)

	// A SUCCEEDED run whose validated result cites the profile version,
	// and the advice row it produced.
	runID := platform.UUIDv4{}.New()
	result := fmt.Sprintf(
		`{"citations":[{"sourceId":%q,"sourceVersionId":%q,"title":"t","publisher":"p","retrievedOn":"2026-01-01","region":"x","license":"CC"}]}`,
		p.ID, reviewedID)
	if _, err := pool.Exec(ctx,
		`INSERT INTO analysis_runs (id, workspace_id, kind, capability, plant_id, input_snapshot, input_hash,
			semantic_key, state, result, started_at, finished_at)
		 VALUES ($1,$2,'SYNTHESIS','TEXT_ANALYZER',$3,'{}','sha256:`+strings.Repeat("0", 64)+`','sk-$1','SUCCEEDED',$4, now() - interval '1 hour', now())`,
		runID, a.WorkspaceID, plant.ID, result); err != nil {
		t.Fatalf("run: %v", err)
	}
	sugg := seedAdvice(t, ctx, pool, a, plant.ID, runID, "Water every 14 days.")
	// Unrelated advice on the same plant from a non-citing run stays put.
	other := seedAdvice(t, ctx, pool, a, plant.ID, pgtype.UUID{}, "Check drainage.")

	_, probs, err = s.RetireCareProfileVersion(ctx, a, p.ID, reviewedID, pub.Revision, nil)
	noProbs(t, probs, err)

	got := reloadAdvice(t, ctx, pool, a, sugg.ID)
	if !got.NeedsReview || got.Revision != sugg.Revision+1 {
		t.Fatalf("retire must flag cited-run advice: %+v", got)
	}
	if other := reloadAdvice(t, ctx, pool, a, other.ID); other.NeedsReview {
		t.Fatalf("uncited advice was flagged")
	}
}
