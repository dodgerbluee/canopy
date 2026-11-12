// Starter profile import (T21): reviewed knowledge shipped with the product.
//
// The content in starter/starter.json is data work with provenance: every
// rule cites at least one source version, sources carry publisher, license
// note and retrievedOn, and climate-sensitive rules (annual windows, frost
// or rainfall sensitivity) declare REGION so the engine fails closed to
// "needs context" when the region is unknown. Nothing here is scraped and
// nothing is AI-generated fact — summaries are short human-written distillations
// of named extension publications.
//
// Import is idempotent: aggregate ids are derived deterministically from
// (workspace, kind, key), so re-running the import is a no-op. Created
// profiles go through the real lifecycle: DRAFT version + rules, then the
// publish flip with reviewer attribution (the DB guard enforces the shape).
package care

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	dbgen "canopy/server/db/generated"
)

//go:embed starter/starter.json
var starterJSON []byte

type starterData struct {
	ReviewerLabel string           `json:"reviewerLabel"`
	ReviewedOn    string           `json:"reviewedOn"`
	Sources       []starterSource  `json:"sources"`
	Profiles      []starterProfile `json:"profiles"`
}

type starterSource struct {
	Key         string `json:"key"`
	Kind        string `json:"kind"`
	Title       string `json:"title"`
	Publisher   string `json:"publisher"`
	URL         string `json:"url"`
	Region      string `json:"region"`
	License     string `json:"license"`
	Citation    string `json:"citation"`
	Summary     string `json:"summary"`
	RetrievedOn string `json:"retrievedOn"`
}

type starterProfile struct {
	Key         string           `json:"key"`
	Category    string           `json:"category"`
	Species     string           `json:"species"`
	Cultivar    string           `json:"cultivar"`
	Environment EnvironmentInput `json:"environment"`
	Rules       []starterRule    `json:"rules"`
}

type starterRule struct {
	RuleKey            string              `json:"ruleKey"`
	Kind               string              `json:"kind"`
	ActivityKind       string              `json:"activityKind"`
	Recurrence         RecurrenceSpecInput `json:"recurrence"`
	RequiredFacts      []string            `json:"requiredFacts"`
	WeatherSensitivity string              `json:"weatherSensitivity"`
	GuidanceText       string              `json:"guidanceText"`
	Sources            []string            `json:"sources"`
}

// ImportResult reports what the import touched.
type ImportResult struct {
	SourcesCreated  int
	SourcesSkipped  int
	ProfilesCreated int
	ProfilesSkipped int
}

// derivedUUID maps (workspace, kind, key) to a stable UUID (v5-shaped bits
// from sha256) so starter aggregates are idempotent per workspace.
func derivedUUID(ws pgtype.UUID, kind, key string) pgtype.UUID {
	h := sha256.Sum256([]byte(uuidString(ws) + "|" + kind + "|" + key))
	var b [16]byte
	copy(b[:], h[:16])
	b[6] = (b[6] & 0x0f) | 0x50 // version 5 nibble
	b[8] = (b[8] & 0x3f) | 0x80 // variant 10
	return pgtype.UUID{Bytes: b, Valid: true}
}

// parseStarter decodes and fully validates the embedded content. Any shape
// problem is a programming error surfaced loudly, not a silent partial write.
func parseStarter() (*starterData, error) {
	var d starterData
	if err := json.Unmarshal(starterJSON, &d); err != nil {
		return nil, fmt.Errorf("starter json: %w", err)
	}
	if charLen(d.ReviewerLabel) < 1 || charLen(d.ReviewerLabel) > 300 || !validDate(d.ReviewedOn) {
		return nil, errors.New("starter json: reviewer attribution invalid")
	}
	seen := map[string]bool{}
	for _, s := range d.Sources {
		if seen["s:"+s.Key] {
			return nil, fmt.Errorf("starter json: duplicate source key %q", s.Key)
		}
		seen["s:"+s.Key] = true
		if !sourceKindSet[s.Kind] || charLen(s.Title) == 0 || charLen(s.Citation) == 0 || charLen(s.Summary) == 0 {
			return nil, fmt.Errorf("starter json: source %q invalid", s.Key)
		}
	}
	for _, p := range d.Profiles {
		if seen["p:"+p.Key] {
			return nil, fmt.Errorf("starter json: duplicate profile key %q", p.Key)
		}
		seen["p:"+p.Key] = true
		if !categorySet[p.Category] {
			return nil, fmt.Errorf("starter json: profile %q: bad category", p.Key)
		}
		for _, r := range p.Rules {
			if len(r.Sources) == 0 {
				return nil, fmt.Errorf("starter json: rule %q/%q has no source citation", p.Key, r.RuleKey)
			}
			for _, k := range r.Sources {
				if !seen["s:"+k] {
					return nil, fmt.Errorf("starter json: rule %q/%q cites unknown source %q", p.Key, r.RuleKey, k)
				}
			}
			// Full shape validation through the same path owner input takes.
			var act *string
			if r.ActivityKind != "" {
				v := r.ActivityKind
				act = &v
			}
			var weather *string
			if r.WeatherSensitivity != "" {
				v := r.WeatherSensitivity
				weather = &v
			}
			if _, probs := validateRules("starter."+p.Key, []RuleInput{{
				RuleKey: r.RuleKey, Kind: r.Kind, ActivityKind: act, Recurrence: r.Recurrence,
				RequiredFacts: r.RequiredFacts, WeatherSensitivity: weather,
				GuidanceText: r.GuidanceText, SourceVersionIDs: r.Sources,
			}}); len(probs) > 0 {
				return nil, fmt.Errorf("starter json: rule %q/%q invalid: %s", p.Key, r.RuleKey, probs[0].Message)
			}
		}
	}
	return &d, nil
}

// ImportStarterProfiles materializes the reviewed starter content into the
// actor's workspace. Idempotent: existing starter aggregates are left alone
// (owner edits and retirements survive re-import).
func (s *Service) ImportStarterProfiles(ctx context.Context, a Actor) (ImportResult, error) {
	var out ImportResult
	d, err := parseStarter()
	if err != nil {
		return out, err
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return out, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := dbgen.New(tx)

	// Sources first: citations resolve against deterministic version ids.
	for _, src := range d.Sources {
		sid := derivedUUID(a.WorkspaceID, "source", src.Key)
		vid := derivedUUID(a.WorkspaceID, "source_version", src.Key)
		if _, err := q.GetSource(ctx, dbgen.GetSourceParams{ID: sid, WorkspaceID: a.WorkspaceID}); err == nil {
			out.SourcesSkipped++
			continue
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return out, err
		}
		if _, err := q.InsertSource(ctx, dbgen.InsertSourceParams{
			ID: sid, WorkspaceID: a.WorkspaceID, Kind: dbgen.SourceKind(src.Kind),
			Title:     src.Title,
			Publisher: pgtype.Text{String: src.Publisher, Valid: true},
			Url:       pgtype.Text{String: src.URL, Valid: true},
			Region:    pgtype.Text{String: src.Region, Valid: true},
			License:   pgtype.Text{String: src.License, Valid: true},
		}); err != nil {
			return out, fmt.Errorf("starter source %q: %w", src.Key, err)
		}
		if _, err := q.InsertSourceVersion(ctx, dbgen.InsertSourceVersionParams{
			ID: vid, SourceID: sid, WorkspaceID: a.WorkspaceID, Version: 1,
			Citation: src.Citation, Summary: src.Summary,
			RetrievedOn: ptrDate(&src.RetrievedOn),
		}); err != nil {
			return out, fmt.Errorf("starter source_version %q: %w", src.Key, err)
		}
		if err := s.audit(ctx, q, a, "import", "source", sid, 1, nil); err != nil {
			return out, err
		}
		out.SourcesCreated++
	}

	for _, p := range d.Profiles {
		pid := derivedUUID(a.WorkspaceID, "profile", p.Key)
		if _, err := q.GetCareProfile(ctx, dbgen.GetCareProfileParams{ID: pid, WorkspaceID: a.WorkspaceID}); err == nil {
			out.ProfilesSkipped++
			continue
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return out, err
		}
		envJSON, probs := canonicalEnvironment("starter."+p.Key+".environment", p.Environment)
		if len(probs) > 0 {
			return out, fmt.Errorf("starter profile %q: %s", p.Key, probs[0].Message)
		}
		rules := make([]validatedRule, 0, len(p.Rules))
		for _, r := range p.Rules {
			rec, probs := canonicalRecurrence("starter."+p.Key+"."+r.RuleKey, r.Recurrence)
			if len(probs) > 0 {
				return out, fmt.Errorf("starter profile %q: %s", p.Key, probs[0].Message)
			}
			var act *string
			if r.ActivityKind != "" {
				v := r.ActivityKind
				act = &v
			}
			weather := r.WeatherSensitivity
			if weather == "" {
				weather = "NONE"
			}
			citations := make([]string, 0, len(r.Sources))
			for _, k := range r.Sources {
				citations = append(citations, uuidString(derivedUUID(a.WorkspaceID, "source_version", k)))
			}
			rules = append(rules, validatedRule{
				RuleKey: r.RuleKey, Kind: r.Kind, ActivityKind: act, Recurrence: rec,
				RequiredFacts: r.RequiredFacts, WeatherSensitivity: weather,
				GuidanceText: r.GuidanceText, SourceVersionIDs: citations,
			})
		}
		if _, err := q.InsertCareProfile(ctx, dbgen.InsertCareProfileParams{
			ID: pid, WorkspaceID: a.WorkspaceID, Category: dbgen.PlantCategory(p.Category),
			Species: ptrText(orNil(p.Species)), Cultivar: ptrText(orNil(p.Cultivar)),
			Status: dbgen.ProfileStatusDRAFT,
		}); err != nil {
			return out, fmt.Errorf("starter profile %q: %w", p.Key, err)
		}
		vid := derivedUUID(a.WorkspaceID, "profile_version", p.Key)
		if _, err := q.InsertCareProfileVersion(ctx, dbgen.InsertCareProfileVersionParams{
			ID: vid, ProfileID: pid, WorkspaceID: a.WorkspaceID,
			Version: 1, Status: dbgen.ProfileStatusDRAFT, Environment: envJSON,
		}); err != nil {
			return out, fmt.Errorf("starter profile_version %q: %w", p.Key, err)
		}
		if probs := s.insertRulesForVersion(ctx, q, a.WorkspaceID, vid, rules); len(probs) > 0 {
			return out, fmt.Errorf("starter profile %q: %s", p.Key, probs[0].Message)
		}
		// Publish through the guarded lifecycle: reviewer attribution in the
		// same UPDATE the trigger requires.
		if _, err := q.PublishCareProfileVersion(ctx, dbgen.PublishCareProfileVersionParams{
			ID: vid, ReviewerLabel: pgtype.Text{String: d.ReviewerLabel, Valid: true},
			ReviewedOn: ptrDate(&d.ReviewedOn),
		}); err != nil {
			return out, fmt.Errorf("starter publish %q: %w", p.Key, err)
		}
		if _, err := q.MarkCareProfilePublished(ctx, dbgen.MarkCareProfilePublishedParams{
			ID: pid, WorkspaceID: a.WorkspaceID, Revision: 1,
		}); err != nil {
			return out, fmt.Errorf("starter profile status %q: %w", p.Key, err)
		}
		if err := s.audit(ctx, q, a, "import", "care_profile", pid, 2, nil); err != nil {
			return out, err
		}
		out.ProfilesCreated++
	}

	if err := tx.Commit(ctx); err != nil {
		return out, err
	}
	return out, nil
}

func orNil(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
