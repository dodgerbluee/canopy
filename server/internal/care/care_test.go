package care

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"regexp"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	candb "canopy/server/db"
	dbgen "canopy/server/db/generated"
	"canopy/server/internal/platform"
)

// scratch builds a disposable migrated schema and returns pool + actor.
// Same discipline as the collection/journal suites: throwaway schema per
// test, dropped after use.
func scratch(t *testing.T) (context.Context, *pgxpool.Pool, Actor) {
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
	schema := fmt.Sprintf("t21_%d", time.Now().UnixNano())
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

	ids := platform.UUIDv4{}
	q := dbgen.New(pool)
	owner, err := q.InsertOwner(ctx, dbgen.InsertOwnerParams{ID: ids.New(), PasswordHash: "x"})
	if err != nil {
		t.Fatalf("owner: %v", err)
	}
	ws, err := q.InsertWorkspace(ctx, dbgen.InsertWorkspaceParams{
		ID: ids.New(), OwnerID: owner.ID, DisplayName: "T21", Timezone: "UTC", DefaultCurrency: "USD",
	})
	if err != nil {
		t.Fatalf("workspace: %v", err)
	}
	return ctx, pool, Actor{WorkspaceID: ws.ID, OwnerID: owner.ID, SessionID: ids.New()}
}

func secondWorkspace(t *testing.T, pool *pgxpool.Pool) Actor {
	t.Helper()
	ctx := context.Background()
	ids := platform.UUIDv4{}
	q := dbgen.New(pool)
	owner, err := q.InsertOwner(ctx, dbgen.InsertOwnerParams{ID: ids.New(), PasswordHash: "x"})
	if err != nil {
		t.Fatalf("owner2: %v", err)
	}
	ws, err := q.InsertWorkspace(ctx, dbgen.InsertWorkspaceParams{
		ID: ids.New(), OwnerID: owner.ID, DisplayName: "Other", Timezone: "UTC", DefaultCurrency: "USD",
	})
	if err != nil {
		t.Fatalf("workspace2: %v", err)
	}
	return Actor{WorkspaceID: ws.ID, OwnerID: owner.ID, SessionID: ids.New()}
}

func svc(pool *pgxpool.Pool) *Service {
	return New(pool, slog.New(slog.DiscardHandler))
}

func ptr[T any](v T) *T { return &v }

func hasCode(ps []Problem, code string) bool {
	for _, p := range ps {
		if p.Code == code {
			return true
		}
	}
	return false
}

// jsonEq compares two JSON documents semantically (jsonb round-trips
// reorder keys and spacing in the view).
func jsonEq(a, b string) bool {
	var av, bv any
	if err := json.Unmarshal([]byte(a), &av); err != nil {
		return false
	}
	if err := json.Unmarshal([]byte(b), &bv); err != nil {
		return false
	}
	x, _ := json.Marshal(av)
	y, _ := json.Marshal(bv)
	return string(x) == string(y)
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

func wantCode(t *testing.T, probs []Problem, err error, code string) {
	t.Helper()
	if err != nil {
		t.Fatalf("transport error: %v", err)
	}
	if !hasCode(probs, code) {
		t.Fatalf("expected %s, got %+v", code, probs)
	}
}

// ---- helpers ---------------------------------------------------------------

func mkSource(t *testing.T, s *Service, a Actor, title string) SourceView {
	t.Helper()
	ctx := context.Background()
	v, probs, err := s.CreateSource(ctx, a, CreateSourceInput{
		Kind: "EXTENSION_PUBLICATION", Title: title,
		Publisher: ptr("UC ANR"), Citation: "UC ANR, " + title,
		Summary: "A short factual summary of " + title + ".",
	}, nil)
	noProbs(t, probs, err)
	return v
}

func waterRule(sourceVersionID string) RuleInput {
	return RuleInput{
		RuleKey: "water-established", Kind: "ACTION", ActivityKind: ptr("WATER"),
		Recurrence:         RecurrenceSpecInput{EveryNDays: &EveryNDaysInput{Days: 14, Anchor: "PLAN_START"}},
		RequiredFacts:      []string{"REGION"},
		WeatherSensitivity: ptr("RAINFALL"),
		GuidanceText:       "Deep soak; let the top few inches dry between waterings.",
		SourceVersionIDs:   []string{sourceVersionID},
	}
}

// ---- sources ---------------------------------------------------------------

func TestSourceLifecycle(t *testing.T) {
	ctx, pool, a := scratch(t)
	s := svc(pool)

	src := mkSource(t, s, a, "Grow Citrus at Home")
	if len(src.Versions) != 1 || src.Versions[0].Version != 1 {
		t.Fatalf("expected one version 1, got %+v", src.Versions)
	}
	if src.Revision != 1 {
		t.Fatalf("revision: %d", src.Revision)
	}

	// Update appends an immutable version; old versions stay inspectable.
	up, probs, err := s.UpdateSource(ctx, a, UpdateSourceInput{
		ID: src.ID, ExpectedRevision: src.Revision,
		Title:    ptr("Grow Citrus at Home (rev 2)"),
		Citation: "UC ANR pub 8157, 2nd printing", Summary: "Updated summary.",
	}, nil)
	noProbs(t, probs, err)
	if len(up.Versions) != 2 {
		t.Fatalf("expected 2 versions, got %d", len(up.Versions))
	}
	// Views list versions newest-first; the old version must stay inspectable
	// and byte-identical.
	if up.Versions[0].Version != 2 || up.Revision != 2 {
		t.Fatalf("append failed: %+v", up)
	}
	if up.Versions[1].Version != 1 ||
		up.Versions[1].Citation != "UC ANR, Grow Citrus at Home" ||
		up.Versions[1].Summary != "A short factual summary of Grow Citrus at Home." {
		t.Fatalf("old version mutated: %+v", up.Versions[1])
	}

	// Stale revision -> CONFLICT.
	_, probs, err = s.UpdateSource(ctx, a, UpdateSourceInput{
		ID: src.ID, ExpectedRevision: 1, Title: ptr("x"),
		Citation: "c", Summary: "s",
	}, nil)
	wantCode(t, probs, err, CodeConflict)

	// Retire is terminal; further edits are RESOURCE_GONE.
	ret, probs, err := s.RetireSource(ctx, a, src.ID, up.Revision, nil)
	noProbs(t, probs, err)
	if ret.RetiredAt == nil {
		t.Fatal("expected retiredAt set")
	}
	_, probs, err = s.UpdateSource(ctx, a, UpdateSourceInput{
		ID: src.ID, ExpectedRevision: ret.Revision, Title: ptr("x"),
		Citation: "c", Summary: "s",
	}, nil)
	wantCode(t, probs, err, CodeResourceGone)

	// Cross-workspace isolation.
	b := secondWorkspace(t, pool)
	_, probs, err = s.GetSource(ctx, b, src.ID)
	wantCode(t, probs, err, CodeNotFound)
}

// ---- profile lifecycle -----------------------------------------------------

func TestProfileDraftPublishForkRetire(t *testing.T) {
	ctx, pool, a := scratch(t)
	s := svc(pool)
	src := mkSource(t, s, a, "OSU Extension, Growing Strawberries")
	vid := src.Versions[0].ID

	// Draft with one rule.
	p, probs, err := s.CreateCareProfileDraft(ctx, a, CreateCareProfileDraftInput{
		Category: "STRAWBERRY", Species: ptr("Fragaria × ananassa"),
		Environment: &EnvironmentInput{Hemispheres: []string{"NORTHERN"}},
		Rules:       []RuleInput{waterRule(vid)},
	}, nil)
	noProbs(t, probs, err)
	if p.Status != "DRAFT" || p.Revision != 1 || len(p.Versions) != 1 {
		t.Fatalf("draft shape: %+v", p)
	}
	if p.Versions[0].Status != "DRAFT" || len(p.Versions[0].Rules) != 1 {
		t.Fatalf("draft version: %+v", p.Versions[0])
	}
	if got := string(p.Versions[0].Rules[0].Recurrence); !jsonEq(got, `{"form":"EVERY_N_DAYS","days":14,"anchor":"PLAN_START"}`) {
		t.Fatalf("canonical recurrence: %s", got)
	}
	if len(p.Versions[0].Rules[0].SourceVersions) != 1 {
		t.Fatal("rule citation missing from view")
	}

	// Publish without attribution -> VALIDATION (owner-visible attribution required).
	_, probs, err = s.PublishCareProfile(ctx, a, PublishCareProfileInput{
		ProfileID: p.ID, ExpectedRevision: p.Revision, ReviewerLabel: "", ReviewedOn: "2026-11-02",
	}, nil)
	wantCode(t, probs, err, CodeValidation)
	// Future review date -> VALIDATION.
	_, probs, err = s.PublishCareProfile(ctx, a, PublishCareProfileInput{
		ProfileID: p.ID, ExpectedRevision: p.Revision, ReviewerLabel: "me", ReviewedOn: "2099-01-01",
	}, nil)
	wantCode(t, probs, err, CodeValidation)

	// Publish with attribution.
	pub, probs, err := s.PublishCareProfile(ctx, a, PublishCareProfileInput{
		ProfileID: p.ID, ExpectedRevision: p.Revision,
		ReviewerLabel: "Owner review vs OSU Extension", ReviewedOn: "2026-11-02",
	}, nil)
	noProbs(t, probs, err)
	if pub.Status != "REVIEWED" || pub.Versions[0].Status != "REVIEWED" {
		t.Fatalf("publish: %+v", pub)
	}
	v0 := pub.Versions[0]
	if v0.ReviewerLabel == nil || *v0.ReviewerLabel != "Owner review vs OSU Extension" ||
		v0.ReviewedOn == nil || *v0.ReviewedOn != "2026-11-02" || v0.PublishedAt == nil {
		t.Fatalf("attribution: %+v", v0)
	}

	// Editing a published profile forks a new DRAFT version (v1 stays frozen).
	forked, probs, err := s.UpdateCareProfileDraft(ctx, a, UpdateCareProfileDraftInput{
		ID: p.ID, ExpectedRevision: pub.Revision,
		Rules: []RuleInput{waterRule(vid), {
			RuleKey: "check-snow", Kind: "CHECK",
			Recurrence:       RecurrenceSpecInput{OneTime: &OneTimeInput{Date: "2027-01-15"}},
			GuidanceText:     "Cover if a hard freeze is forecast.",
			SourceVersionIDs: []string{vid},
		}},
	}, nil)
	noProbs(t, probs, err)
	if forked.Status != "DRAFT" || len(forked.Versions) != 2 {
		t.Fatalf("fork: %+v", forked)
	}
	var v1 ProfileVersionView
	for _, v := range forked.Versions {
		if v.Version == 1 {
			v1 = v
		}
	}
	if v1.Status != "REVIEWED" || len(v1.Rules) != 1 {
		t.Fatalf("v1 must stay frozen REVIEWED: %+v", v1)
	}

	// Retire the published v1 (allowed: status + retired_at only).
	ret, probs, err := s.RetireCareProfileVersion(ctx, a, p.ID, v1.ID, forked.Revision, nil)
	noProbs(t, probs, err)
	var retired ProfileVersionView
	for _, v := range ret.Versions {
		if v.Version == 1 {
			retired = v
		}
	}
	if retired.Status != "RETIRED" || retired.RetiredAt == nil {
		t.Fatalf("retire: %+v", retired)
	}
	// Retiring twice -> CONFLICT (guard: retired rows immutable).
	_, probs, err = s.RetireCareProfileVersion(ctx, a, p.ID, v1.ID, ret.Revision, nil)
	wantCode(t, probs, err, CodeConflict)
}

func TestDraftDeleteGuards(t *testing.T) {
	ctx, pool, a := scratch(t)
	s := svc(pool)
	src := mkSource(t, s, a, "WSU cane notes")
	vid := src.Versions[0].ID

	// Plain draft deletes.
	p, probs, err := s.CreateCareProfileDraft(ctx, a, CreateCareProfileDraftInput{
		Category: "BLACKBERRY", Rules: []RuleInput{waterRule(vid)},
	}, nil)
	noProbs(t, probs, err)
	id, probs, err := s.DeleteCareProfileDraft(ctx, a, p.ID, p.Revision, nil)
	noProbs(t, probs, err)
	if id != p.ID {
		t.Fatalf("deleted id: %s", id)
	}
	_, probs, err = s.GetCareProfile(ctx, a, p.ID)
	wantCode(t, probs, err, CodeNotFound)

	// Published profile cannot be deleted.
	q2, probs, err := s.CreateCareProfileDraft(ctx, a, CreateCareProfileDraftInput{
		Category: "FIG", Rules: []RuleInput{waterRule(vid)},
	}, nil)
	noProbs(t, probs, err)
	pub, probs, err := s.PublishCareProfile(ctx, a, PublishCareProfileInput{
		ProfileID: q2.ID, ExpectedRevision: q2.Revision, ReviewerLabel: "owner", ReviewedOn: "2026-11-02",
	}, nil)
	noProbs(t, probs, err)
	_, probs, err = s.DeleteCareProfileDraft(ctx, a, q2.ID, pub.Revision, nil)
	wantCode(t, probs, err, CodeConflict)

	// Forked profile (DRAFT aggregate, frozen REVIEWED v1) also cannot be deleted.
	_, probs, err = s.UpdateCareProfileDraft(ctx, a, UpdateCareProfileDraftInput{
		ID: q2.ID, ExpectedRevision: pub.Revision,
	}, nil)
	noProbs(t, probs, err)
	_, probs, err = s.DeleteCareProfileDraft(ctx, a, q2.ID, pub.Revision+1, nil)
	wantCode(t, probs, err, CodeConflict)
}

// ---- validation ------------------------------------------------------------

func TestRecurrenceExactlyOne(t *testing.T) {
	if _, probs := canonicalRecurrence("r", RecurrenceSpecInput{}); !hasCode(probs, CodeValidation) {
		t.Fatalf("zero forms: %+v", probs)
	}
	two := RecurrenceSpecInput{
		EveryNDays: &EveryNDaysInput{Days: 7, Anchor: "PLAN_START"},
		OneTime:    &OneTimeInput{Date: "2027-01-01"},
	}
	if _, probs := canonicalRecurrence("r", two); !hasCode(probs, CodeValidation) {
		t.Fatalf("two forms: %+v", probs)
	}
	if _, probs := canonicalRecurrence("r", RecurrenceSpecInput{
		EveryNDays: &EveryNDaysInput{Days: 0, Anchor: "PLAN_START"},
	}); !hasCode(probs, CodeValidation) {
		t.Fatalf("days 0: %+v", probs)
	}
	if _, probs := canonicalRecurrence("r", RecurrenceSpecInput{
		EveryNDays: &EveryNDaysInput{Days: 400, Anchor: "PLAN_START"},
	}); !hasCode(probs, CodeValidation) {
		t.Fatalf("days 400: %+v", probs)
	}
	if _, probs := canonicalRecurrence("r", RecurrenceSpecInput{
		EveryNDays: &EveryNDaysInput{Days: 7, Anchor: "YESTERDAY"},
	}); !hasCode(probs, CodeValidation) {
		t.Fatalf("bad anchor: %+v", probs)
	}
	if _, probs := canonicalRecurrence("r", RecurrenceSpecInput{
		AnnualWindow: &AnnualWindowInput{Month: 2, Day: 30},
	}); !hasCode(probs, CodeValidation) {
		t.Fatalf("feb 30: %+v", probs)
	}
	if _, probs := canonicalRecurrence("r", RecurrenceSpecInput{
		WeeklyDays: &WeeklyDaysInput{Weekdays: []string{"MONDAY", "MONDAY"}},
	}); !hasCode(probs, CodeValidation) {
		t.Fatalf("dup weekday: %+v", probs)
	}
	// Feb 29 is valid input (engine maps it in non-leap years).
	if _, probs := canonicalRecurrence("r", RecurrenceSpecInput{
		AnnualWindow: &AnnualWindowInput{Month: 2, Day: 29},
	}); len(probs) > 0 {
		t.Fatalf("feb 29 must be valid: %+v", probs)
	}
}

func TestRuleValidationCases(t *testing.T) {
	base := RuleInput{
		RuleKey: "a-rule", Kind: "ACTION", ActivityKind: ptr("WATER"),
		Recurrence:   RecurrenceSpecInput{EveryNDays: &EveryNDaysInput{Days: 3, Anchor: "PLAN_START"}},
		GuidanceText: "g",
	}
	if _, probs := validateRules("rules", []RuleInput{base, base}); !hasCode(probs, CodeValidation) {
		t.Fatalf("duplicate ruleKey: %+v", probs)
	}
	noAct := base
	noAct.ActivityKind = nil
	if _, probs := validateRules("rules", []RuleInput{noAct}); !hasCode(probs, CodeValidation) {
		t.Fatalf("ACTION without activityKind: %+v", probs)
	}
	checkAct := base
	checkAct.Kind = "CHECK"
	if _, probs := validateRules("rules", []RuleInput{checkAct}); !hasCode(probs, CodeValidation) {
		t.Fatalf("CHECK with activityKind: %+v", probs)
	}
	badFact := base
	badFact.RequiredFacts = []string{"MOON_PHASE"}
	if _, probs := validateRules("rules", []RuleInput{badFact}); !hasCode(probs, CodeValidation) {
		t.Fatalf("bad fact: %+v", probs)
	}
	if _, probs := validateRules("rules", nil); !hasCode(probs, CodeValidation) {
		t.Fatalf("empty rules: %+v", probs)
	}
}

func TestCitationGuards(t *testing.T) {
	ctx, pool, a := scratch(t)
	s := svc(pool)
	src := mkSource(t, s, a, "UC IPM Home Garden")
	vid := src.Versions[0].ID

	// Unknown citation -> VALIDATION.
	r := waterRule("11111111-1111-1111-1111-111111111111")
	_, probs, err := s.CreateCareProfileDraft(ctx, a, CreateCareProfileDraftInput{
		Category: "OTHER", Rules: []RuleInput{r},
	}, nil)
	wantCode(t, probs, err, CodeValidation)

	// Citation to a retired source is rejected for new rules.
	_, probs, err = s.RetireSource(ctx, a, src.ID, src.Revision, nil)
	noProbs(t, probs, err)
	r2 := waterRule(vid)
	_, probs, err = s.CreateCareProfileDraft(ctx, a, CreateCareProfileDraftInput{
		Category: "OTHER", Rules: []RuleInput{r2},
	}, nil)
	wantCode(t, probs, err, CodeValidation)
}

// ---- starter import ---------------------------------------------------------

var defoliationRe = regexp.MustCompile(`(?i)(strip|remove|pull)\s+(all|every|the)\s+(the\s+)?leaves|defoliat`)

func TestStarterImport(t *testing.T) {
	ctx, pool, a := scratch(t)
	s := svc(pool)

	res, err := s.ImportStarterProfiles(ctx, a)
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if res.SourcesCreated != 5 || res.ProfilesCreated != 7 {
		t.Fatalf("import counts: %+v", res)
	}

	// Idempotent re-import: nothing new, everything skipped.
	res2, err := s.ImportStarterProfiles(ctx, a)
	if err != nil {
		t.Fatalf("re-import: %v", err)
	}
	if res2.SourcesCreated != 0 || res2.ProfilesCreated != 0 ||
		res2.SourcesSkipped != 5 || res2.ProfilesSkipped != 7 {
		t.Fatalf("re-import counts: %+v", res2)
	}

	// Coverage expectations from the acceptance criteria, straight from the data.
	d, err := parseStarter()
	if err != nil {
		t.Fatalf("parseStarter: %v", err)
	}
	byCat := map[string][]starterProfile{}
	for _, p := range d.Profiles {
		byCat[p.Category] = append(byCat[p.Category], p)
	}
	// citrus: species AND cultivar distinctions (Meyer vs Eureka)
	if len(byCat["CITRUS"]) != 2 || byCat["CITRUS"][0].Species == byCat["CITRUS"][1].Species {
		t.Fatalf("citrus profiles must differ by species: %+v", byCat["CITRUS"])
	}
	if len(byCat["FIG"]) != 1 || len(byCat["STRAWBERRY"]) != 2 || len(byCat["BLACKBERRY"]) != 2 {
		t.Fatalf("coverage: %+v", byCat)
	}
	// strawberry bearing types and blackberry cane types are distinct cultivar groups
	if byCat["STRAWBERRY"][0].Cultivar == byCat["STRAWBERRY"][1].Cultivar ||
		byCat["BLACKBERRY"][0].Cultivar == byCat["BLACKBERRY"][1].Cultivar {
		t.Fatal("bearing/cane type groups must be distinct profiles")
	}

	// Content discipline: provenance on every rule, REGION on climate-sensitive
	// rules, no "strip all leaves" folklore anywhere.
	for _, p := range d.Profiles {
		for _, r := range p.Rules {
			if len(r.Sources) == 0 {
				t.Fatalf("rule %s/%s has no citation", p.Key, r.RuleKey)
			}
			if defoliationRe.MatchString(r.GuidanceText) {
				t.Fatalf("rule %s/%s carries defoliation folklore", p.Key, r.RuleKey)
			}
			sensitive := r.Recurrence.AnnualWindow != nil ||
				r.WeatherSensitivity == "FROST" || r.WeatherSensitivity == "RAINFALL"
			if sensitive {
				hasRegion := false
				for _, f := range r.RequiredFacts {
					if f == "REGION" {
						hasRegion = true
					}
				}
				if !hasRegion {
					t.Fatalf("climate-sensitive rule %s/%s must require REGION (fail-closed to needs-context)", p.Key, r.RuleKey)
				}
			}
		}
	}
	for _, src := range d.Sources {
		if src.Citation == "" || src.Summary == "" || src.RetrievedOn == "" || src.License == "" || src.Publisher == "" {
			t.Fatalf("source %s lacks provenance fields", src.Key)
		}
	}

	// DB state: every starter profile is REVIEWED with attribution, and every
	// rule row carries at least one citation.
	profiles, probs, err := s.ListCareProfiles(ctx, a, ProfileFilter{}, 50, nil)
	noProbs(t, probs, err)
	if len(profiles.Items) != 7 {
		t.Fatalf("list: %d", len(profiles.Items))
	}
	totalRules := 0
	for _, e := range profiles.Items {
		pv, probs, err := s.GetCareProfile(ctx, a, e.ID)
		noProbs(t, probs, err)
		if pv.Status != "REVIEWED" || len(pv.Versions) != 1 {
			t.Fatalf("starter %s: %+v", pv.ID, pv)
		}
		v := pv.Versions[0]
		if v.Status != "REVIEWED" || v.ReviewerLabel == nil || v.ReviewedOn == nil || v.PublishedAt == nil {
			t.Fatalf("starter version not published with attribution: %+v", v)
		}
		for _, r := range v.Rules {
			totalRules++
			if len(r.SourceVersions) == 0 {
				t.Fatalf("rule %s has no citations in DB", r.RuleKey)
			}
			for _, sv := range r.SourceVersions {
				if sv.Citation == "" || sv.Summary == "" {
					t.Fatalf("citation row empty on %s", r.RuleKey)
				}
			}
		}
	}
	if totalRules < 20 {
		t.Fatalf("starter content too thin: %d rules", totalRules)
	}
}

func TestStarterOwnerEditsSurviveReimport(t *testing.T) {
	ctx, pool, a := scratch(t)
	s := svc(pool)
	if _, err := s.ImportStarterProfiles(ctx, a); err != nil {
		t.Fatalf("import: %v", err)
	}
	profiles, probs, err := s.ListCareProfiles(ctx, a, ProfileFilter{}, 50, nil)
	noProbs(t, probs, err)

	// Owner retires one starter profile's published version, then re-imports:
	// the retirement must survive (import skips existing aggregates).
	target := profiles.Items[0]
	_, probs, err = s.RetireCareProfileVersion(ctx, a, target.ID, target.Versions[0].ID, target.Revision, nil)
	noProbs(t, probs, err)

	if _, err := s.ImportStarterProfiles(ctx, a); err != nil {
		t.Fatalf("re-import: %v", err)
	}
	after, probs, err := s.GetCareProfile(ctx, a, target.ID)
	noProbs(t, probs, err)
	if after.Versions[0].Status != "RETIRED" {
		t.Fatalf("re-import clobbered owner retirement: %+v", after.Versions[0])
	}
}

func TestStarterImportPerWorkspace(t *testing.T) {
	ctx, pool, a := scratch(t)
	s := svc(pool)
	b := secondWorkspace(t, pool)
	if _, err := s.ImportStarterProfiles(ctx, a); err != nil {
		t.Fatalf("import a: %v", err)
	}
	res, err := s.ImportStarterProfiles(ctx, b)
	if err != nil {
		t.Fatalf("import b: %v", err)
	}
	if res.ProfilesCreated != 7 {
		t.Fatalf("workspace b must get its own copies: %+v", res)
	}
	// Deterministic ids are workspace-scoped: b cannot see a's rows and vice versa.
	pa, probs, err := s.ListCareProfiles(ctx, a, ProfileFilter{}, 50, nil)
	noProbs(t, probs, err)
	pb, probs, err := s.ListCareProfiles(ctx, b, ProfileFilter{}, 50, nil)
	noProbs(t, probs, err)
	seen := map[string]bool{}
	for _, e := range pa.Items {
		seen[e.ID] = true
	}
	for _, e := range pb.Items {
		if seen[e.ID] {
			t.Fatal("starter ids leaked across workspaces")
		}
	}
}

// starter json must never contain the "strip all trees" general-purpose myth
// even as guidance wording, and must be valid through the owner-input path.
func TestStarterDataSelfCheck(t *testing.T) {
	d, err := parseStarter()
	if err != nil {
		t.Fatalf("parseStarter: %v", err)
	}
	if len(d.Profiles) != 7 || len(d.Sources) != 5 {
		t.Fatalf("starter shape: %d profiles, %d sources", len(d.Profiles), len(d.Sources))
	}
	// Environment jsonb round-trips through the canonical path.
	for _, p := range d.Profiles {
		raw, probs := canonicalEnvironment(p.Key, p.Environment)
		if len(probs) > 0 {
			t.Fatalf("%s: %+v", p.Key, probs)
		}
		var env struct {
			Situations  []string `json:"situations"`
			Hemispheres []string `json:"hemispheres"`
		}
		if err := json.Unmarshal(raw, &env); err != nil {
			t.Fatalf("%s env: %v", p.Key, err)
		}
	}
}
