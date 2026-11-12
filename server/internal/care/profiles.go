// Reviewed knowledge profiles (T21): draft editing, publish, retire and the
// read assembly (versions → rules → pinned citations).
//
// Lifecycle semantics (care.graphql + ADR-0002):
//   - a draft aggregate owns one DRAFT version; edits replace its rule set.
//   - publish freezes the DRAFT version as REVIEWED (reviewer attribution is
//     required by the DB trigger in the same UPDATE) and marks the aggregate
//     REVIEWED.
//   - editing a published aggregate forks a new DRAFT version (published
//     content is never touched); plans keep pinning the old version until
//     explicit upgrade review.
//   - retire applies to versions; it blocks new selection but pinned plans
//     keep the version inspectable.
//   - draft deletion is permitted only while unpublished and unreferenced.
package care

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	dbgen "canopy/server/db/generated"
)

var categorySet = map[string]bool{"CITRUS": true, "FIG": true, "STRAWBERRY": true, "BLACKBERRY": true, "OTHER": true}

// ---- read views -----------------------------------------------------------

type ProfileView struct {
	ID        string
	Category  string
	Species   *string
	Cultivar  *string
	Status    string
	Revision  int32
	CreatedAt string
	UpdatedAt string
	Versions  []ProfileVersionView
}

type ProfileVersionView struct {
	ID            string
	ProfileID     string
	Version       int32
	Status        string
	Environment   EnvironmentView
	Rules         []RuleView
	ReviewerLabel *string
	ReviewedOn    *string
	PublishedAt   *string
	RetiredAt     *string
	CreatedAt     string
}

type EnvironmentView struct {
	Situations  []string
	Hemispheres []string
}

type RuleView struct {
	RuleKey            string
	Kind               string
	ActivityKind       *string
	Recurrence         json.RawMessage // canonical ADR-0002 shape
	RequiredFacts      []string
	WeatherSensitivity string
	GuidanceText       string
	SourceVersions     []SourceVersionView
}

// ---- inputs ---------------------------------------------------------------

type CreateCareProfileDraftInput struct {
	Category    string
	Species     *string
	Cultivar    *string
	Environment *EnvironmentInput
	Rules       []RuleInput
}

type UpdateCareProfileDraftInput struct {
	ID               string
	ExpectedRevision int32
	Species          *string
	Cultivar         *string
	Environment      *EnvironmentInput
	// Rules replaces the draft rule set when non-nil; nil keeps the current
	// rules (when forking a published profile, they are copied forward).
	Rules []RuleInput
}

type PublishCareProfileInput struct {
	ProfileID        string
	ExpectedRevision int32
	ReviewerLabel    string
	ReviewedOn       string
}

// ---- validation helpers ----------------------------------------------------

// resolveCitationsTx verifies every cited source version lives in this
// workspace and its source is not retired (retired sources are excluded
// from new citations; already-pinned citations stay inspectable).
func (s *Service) resolveCitationsTx(ctx context.Context, q *dbgen.Queries, a Actor, rules []validatedRule) ([]Problem, error) {
	for _, r := range rules {
		for _, idStr := range r.SourceVersionIDs {
			uid, ok := parseID(idStr)
			if !ok {
				return []Problem{problem(CodeValidation, "input.rules.sourceVersionIds",
					"expected a UUID, found "+quote(idStr))}, nil
			}
			_, err := q.GetSourceVersionForCitation(ctx, dbgen.GetSourceVersionForCitationParams{
				ID: uid, WorkspaceID: a.WorkspaceID,
			})
			if errors.Is(err, pgx.ErrNoRows) {
				return []Problem{problem(CodeValidation, "input.rules.sourceVersionIds",
					"citation must reference a source version in this workspace with a non-retired source.")}, nil
			}
			if err != nil {
				return nil, err
			}
		}
	}
	return nil, nil
}

// ---- use cases -------------------------------------------------------------

func (s *Service) CreateCareProfileDraft(ctx context.Context, a Actor, in CreateCareProfileDraftInput, cmid *string) (ProfileView, []Problem, error) {
	var zero ProfileView
	if !categorySet[in.Category] {
		return zero, []Problem{problem(CodeValidation, "input.category",
			"expected CITRUS, FIG, STRAWBERRY, BLACKBERRY or OTHER, found "+quote(in.Category))}, nil
	}
	if charLen(textPtr(in.Species)) > 300 || charLen(textPtr(in.Cultivar)) > 300 {
		return zero, []Problem{problem(CodeValidation, "input.species",
			"species/cultivar accept at most 300 characters")}, nil
	}
	env := EnvironmentInput{}
	if in.Environment != nil {
		env = *in.Environment
	}
	envJSON, probs := canonicalEnvironment("input.environment", env)
	if len(probs) > 0 {
		return zero, probs, nil
	}
	rules, probs := validateRules("input.rules", in.Rules)
	if len(probs) > 0 {
		return zero, probs, nil
	}
	return mutate(s, ctx, a, "createCareProfileDraft", cmid, in,
		func(ctx context.Context, q *dbgen.Queries) (ProfileView, []Problem) {
			if probs, err := s.resolveCitationsTx(ctx, q, a, rules); err != nil {
				return zero, []Problem{dbProblem("", err)}
			} else if len(probs) > 0 {
				return zero, probs
			}
			pid := s.uuid.New()
			prof, err := q.InsertCareProfile(ctx, dbgen.InsertCareProfileParams{
				ID: pid, WorkspaceID: a.WorkspaceID,
				Category: dbgen.PlantCategory(in.Category),
				Species:  ptrText(in.Species), Cultivar: ptrText(in.Cultivar),
				Status: dbgen.ProfileStatusDRAFT,
			})
			if err != nil {
				return zero, []Problem{dbProblem("input", err)}
			}
			if probs := s.insertDraftVersionWithRules(ctx, q, a, pid, 1, envJSON, rules); len(probs) > 0 {
				return zero, probs
			}
			if err := s.audit(ctx, q, a, "create", "care_profile", pid, prof.Revision, nil); err != nil {
				return zero, []Problem{dbProblem("", err)}
			}
			view, err := s.profileView(ctx, q, prof)
			if err != nil {
				return zero, []Problem{dbProblem("", err)}
			}
			return view, nil
		})
}

func (s *Service) insertDraftVersionWithRules(ctx context.Context, q *dbgen.Queries, a Actor,
	profileID pgtype.UUID, version int32, envJSON json.RawMessage, rules []validatedRule,
) []Problem {
	ver, err := q.InsertCareProfileVersion(ctx, dbgen.InsertCareProfileVersionParams{
		ID: s.uuid.New(), ProfileID: profileID, WorkspaceID: a.WorkspaceID,
		Version: version, Status: dbgen.ProfileStatusDRAFT, Environment: envJSON,
	})
	if err != nil {
		return []Problem{dbProblem("input.environment", err)}
	}
	return s.insertRulesForVersion(ctx, q, a.WorkspaceID, ver.ID, rules)
}

// UpdateCareProfileDraft edits a DRAFT aggregate in place; on a published
// aggregate it forks a new DRAFT version carrying the provided content
// (missing fields are copied from the current published version). Published
// content itself is never modified.
func (s *Service) UpdateCareProfileDraft(ctx context.Context, a Actor, in UpdateCareProfileDraftInput, cmid *string) (ProfileView, []Problem, error) {
	var zero ProfileView
	pid, ok := parseID(in.ID)
	if !ok {
		return zero, []Problem{problem(CodeValidation, "input.id", "expected a UUID")}, nil
	}
	var envJSON json.RawMessage
	var probs []Problem
	if in.Environment != nil {
		envJSON, probs = canonicalEnvironment("input.environment", *in.Environment)
		if len(probs) > 0 {
			return zero, probs, nil
		}
	}
	var rules []validatedRule
	if in.Rules != nil {
		rules, probs = validateRules("input.rules", in.Rules)
		if len(probs) > 0 {
			return zero, probs, nil
		}
	}
	return mutate(s, ctx, a, "updateCareProfileDraft", cmid, in,
		func(ctx context.Context, q *dbgen.Queries) (ProfileView, []Problem) {
			prof, err := q.GetCareProfile(ctx, dbgen.GetCareProfileParams{ID: pid, WorkspaceID: a.WorkspaceID})
			if err != nil {
				if errors.Is(err, pgx.ErrNoRows) {
					return zero, []Problem{problem(CodeNotFound, "input.id", "no such profile in this workspace.")}
				}
				return zero, []Problem{dbProblem("", err)}
			}
			if rules != nil {
				if probs, err := s.resolveCitationsTx(ctx, q, a, rules); err != nil {
					return zero, []Problem{dbProblem("", err)}
				} else if len(probs) > 0 {
					return zero, probs
				}
			}
			vers, err := q.ListCareProfileVersions(ctx, pid)
			if err != nil {
				return zero, []Problem{dbProblem("", err)}
			}
			var draft *dbgen.CareProfileVersion
			var current *dbgen.CareProfileVersion
			for i := range vers {
				if vers[i].Status == dbgen.ProfileStatusDRAFT {
					draft = &vers[i]
				}
				if current == nil || vers[i].Version > current.Version {
					current = &vers[i]
				}
			}
			// Revision bump (identity/species/cultivar edits ride along).
			updated, err := q.UpdateCareProfileAggregate(ctx, dbgen.UpdateCareProfileAggregateParams{
				ID: pid, WorkspaceID: a.WorkspaceID, ExpectedRevision: in.ExpectedRevision,
				Species: ptrText(in.Species), Cultivar: ptrText(in.Cultivar),
			})
			if err != nil {
				if errors.Is(err, pgx.ErrNoRows) {
					return zero, []Problem{problemWithRevision(
						problem(CodeConflict, "expectedRevision", "profile was modified concurrently."),
						prof.Revision)}
				}
				return zero, []Problem{dbProblem("input", err)}
			}
			if draft != nil {
				// Edit the draft version in place: replace rules when set.
				if rules != nil {
					if err := q.DeleteCareRulesByVersion(ctx, draft.ID); err != nil {
						return zero, []Problem{dbProblem("", err)}
					}
					env := draft.Environment
					if envJSON != nil {
						env = envJSON
					}
					if probs := s.rewriteDraftRules(ctx, q, a.WorkspaceID, draft.ID, env, rules); len(probs) > 0 {
						return zero, probs
					}
				} else if envJSON != nil {
					// environment-only edit: rules unchanged; environment lives
					// on the version row — DRAFT rows are editable by trigger.
					if _, err := q.UpdateCareProfileDraftEnvironment(ctx, dbgen.UpdateCareProfileDraftEnvironmentParams{
						ID: draft.ID, Environment: envJSON,
					}); err != nil {
						return zero, []Problem{dbProblem("input.environment", err)}
					}
				}
			} else {
				// Published aggregate: fork a new DRAFT version carrying the
				// provided content; missing pieces copy from the newest version.
				maxV, err := q.MaxProfileVersionNumber(ctx, pid)
				if err != nil {
					return zero, []Problem{dbProblem("", err)}
				}
				env := current.Environment
				if envJSON != nil {
					env = envJSON
				}
				forkRules := rules
				if forkRules == nil {
					forkRules, probs = s.rulesAsInputs(ctx, q, current.ID)
					if len(probs) > 0 {
						return zero, probs
					}
				}
				if probs := s.insertDraftVersionWithRules(ctx, q, a, pid, maxV+1, env, forkRules); len(probs) > 0 {
					return zero, probs
				}
				agg, err := q.MarkCareProfileDraft(ctx, dbgen.MarkCareProfileDraftParams{
					ID: pid, WorkspaceID: a.WorkspaceID,
				})
				if err != nil {
					return zero, []Problem{dbProblem("", err)}
				}
				updated = agg // view must reflect the DRAFT flip, not the pre-fork row
			}
			if err := s.audit(ctx, q, a, "update", "care_profile", pid, updated.Revision, prof); err != nil {
				return zero, []Problem{dbProblem("", err)}
			}
			view, err := s.profileView(ctx, q, updated)
			if err != nil {
				return zero, []Problem{dbProblem("", err)}
			}
			return view, nil
		})
}

// rewriteDraftRules replaces a DRAFT version's rule set and environment;
// the rules guard allows this because the owning version is still DRAFT.
func (s *Service) rewriteDraftRules(ctx context.Context, q *dbgen.Queries, ws, versionID pgtype.UUID,
	envJSON []byte, rules []validatedRule,
) []Problem {
	if _, err := q.UpdateCareProfileDraftEnvironment(ctx, dbgen.UpdateCareProfileDraftEnvironmentParams{
		ID: versionID, Environment: envJSON,
	}); err != nil {
		return []Problem{dbProblem("input.environment", err)}
	}
	return s.insertRulesForVersion(ctx, q, ws, versionID, rules)
}

func (s *Service) insertRulesForVersion(ctx context.Context, q *dbgen.Queries, ws, versionID pgtype.UUID,
	rules []validatedRule,
) []Problem {
	for _, r := range rules {
		var act dbgen.NullActivityKind
		if r.ActivityKind != nil {
			act = dbgen.NullActivityKind{ActivityKind: dbgen.ActivityKind(*r.ActivityKind), Valid: true}
		}
		facts := r.RequiredFacts
		if facts == nil {
			facts = []string{}
		}
		rule, err := q.InsertCareRule(ctx, dbgen.InsertCareRuleParams{
			ID: s.uuid.New(), ProfileVersionID: versionID, WorkspaceID: ws,
			RuleKey: r.RuleKey, Kind: dbgen.RuleKind(r.Kind), ActivityKind: act,
			Recurrence: r.Recurrence, RequiredFacts: facts,
			WeatherSensitivity: dbgen.WeatherSensitivity(r.WeatherSensitivity),
			GuidanceText:       r.GuidanceText,
		})
		if err != nil {
			return []Problem{dbProblem("input.rules", err)}
		}
		for _, idStr := range r.SourceVersionIDs {
			uid, _ := parseID(idStr) // validated earlier
			if err := q.InsertCareRuleSource(ctx, dbgen.InsertCareRuleSourceParams{
				RuleID: rule.ID, SourceVersionID: uid,
			}); err != nil {
				return []Problem{dbProblem("input.rules.sourceVersionIds", err)}
			}
		}
	}
	return nil
}

// rulesAsInputs converts an existing version's rules back into inputs so a
// fork can copy them forward verbatim.
func (s *Service) rulesAsInputs(ctx context.Context, q *dbgen.Queries, versionID pgtype.UUID) ([]validatedRule, []Problem) {
	rules, err := q.ListCareRules(ctx, versionID)
	if err != nil {
		return nil, []Problem{dbProblem("", err)}
	}
	srcs, err := q.ListRuleSourcesForVersion(ctx, versionID)
	if err != nil {
		return nil, []Problem{dbProblem("", err)}
	}
	byRule := map[string][]string{}
	for _, row := range srcs {
		byRule[uuidString(row.RuleID)] = append(byRule[uuidString(row.RuleID)], uuidString(row.ID))
	}
	out := make([]validatedRule, 0, len(rules))
	for _, r := range rules {
		var act *string
		if r.ActivityKind.Valid {
			v := string(r.ActivityKind.ActivityKind)
			act = &v
		}
		out = append(out, validatedRule{
			RuleKey: r.RuleKey, Kind: string(r.Kind), ActivityKind: act,
			Recurrence: r.Recurrence, RequiredFacts: r.RequiredFacts,
			WeatherSensitivity: string(r.WeatherSensitivity),
			GuidanceText:       r.GuidanceText,
			SourceVersionIDs:   byRule[uuidString(r.ID)],
		})
	}
	return out, nil
}

func (s *Service) DeleteCareProfileDraft(ctx context.Context, a Actor, id string, expectedRevision int32, cmid *string) (string, []Problem, error) {
	pid, ok := parseID(id)
	if !ok {
		return "", []Problem{problem(CodeValidation, "input.id", "expected a UUID")}, nil
	}
	return mutate(s, ctx, a, "deleteCareProfileDraft", cmid, map[string]any{"id": id, "expectedRevision": expectedRevision},
		func(ctx context.Context, q *dbgen.Queries) (string, []Problem) {
			prof, err := q.GetCareProfile(ctx, dbgen.GetCareProfileParams{ID: pid, WorkspaceID: a.WorkspaceID})
			if err != nil {
				if errors.Is(err, pgx.ErrNoRows) {
					return "", []Problem{problem(CodeNotFound, "input.id", "no such profile in this workspace.")}
				}
				return "", []Problem{dbProblem("", err)}
			}
			if prof.Status != dbgen.ProfileStatusDRAFT {
				return "", []Problem{problem(CodeConflict, "input.id",
					"only unpublished drafts can be deleted; retire published versions instead.")}
			}
			if prof.Revision != expectedRevision {
				return "", []Problem{problemWithRevision(
					problem(CodeConflict, "expectedRevision", "profile was modified concurrently."), prof.Revision)}
			}
			vers, err := q.ListCareProfileVersions(ctx, pid)
			if err != nil {
				return "", []Problem{dbProblem("", err)}
			}
			for _, v := range vers {
				if v.Status != dbgen.ProfileStatusDRAFT {
					return "", []Problem{problem(CodeConflict, "input.id",
						"only unpublished drafts can be deleted; this profile has published versions — retire them instead.")}
				}
				pinned, err := q.CountPlansPinningVersion(ctx, v.ID)
				if err != nil {
					return "", []Problem{dbProblem("", err)}
				}
				if pinned > 0 {
					return "", []Problem{problem(CodeConflict, "input.id",
						"draft is referenced by care plans; delete the plans or upgrade them first.")}
				}
			}
			if err := q.DeleteCareProfileRow(ctx, dbgen.DeleteCareProfileRowParams{ID: pid, WorkspaceID: a.WorkspaceID}); err != nil {
				return "", []Problem{dbProblem("", err)}
			}
			if err := s.audit(ctx, q, a, "delete", "care_profile", pid, prof.Revision, prof); err != nil {
				return "", []Problem{dbProblem("", err)}
			}
			return uuidString(pid), nil
		})
}

func (s *Service) PublishCareProfile(ctx context.Context, a Actor, in PublishCareProfileInput, cmid *string) (ProfileView, []Problem, error) {
	var zero ProfileView
	pid, ok := parseID(in.ProfileID)
	if !ok {
		return zero, []Problem{problem(CodeValidation, "input.profileId", "expected a UUID")}, nil
	}
	if charLen(in.ReviewerLabel) < 1 || charLen(in.ReviewerLabel) > 300 {
		return zero, []Problem{problem(CodeValidation, "input.reviewerLabel",
			"expected 1..300 characters; REVIEWED requires owner-visible attribution")}, nil
	}
	if !validDate(in.ReviewedOn) {
		return zero, []Problem{problem(CodeValidation, "input.reviewedOn",
			"expected YYYY-MM-DD, found "+quote(in.ReviewedOn))}, nil
	}
	if in.ReviewedOn > time.Now().UTC().Format("2006-01-02") {
		return zero, []Problem{problem(CodeValidation, "input.reviewedOn",
			"review date cannot be in the future")}, nil
	}
	return mutate(s, ctx, a, "publishCareProfile", cmid, in,
		func(ctx context.Context, q *dbgen.Queries) (ProfileView, []Problem) {
			prof, err := q.GetCareProfile(ctx, dbgen.GetCareProfileParams{ID: pid, WorkspaceID: a.WorkspaceID})
			if err != nil {
				if errors.Is(err, pgx.ErrNoRows) {
					return zero, []Problem{problem(CodeNotFound, "input.profileId", "no such profile in this workspace.")}
				}
				return zero, []Problem{dbProblem("", err)}
			}
			vers, err := q.ListCareProfileVersions(ctx, pid)
			if err != nil {
				return zero, []Problem{dbProblem("", err)}
			}
			var draft *dbgen.CareProfileVersion
			for i := range vers {
				if vers[i].Status == dbgen.ProfileStatusDRAFT {
					draft = &vers[i]
				}
			}
			if draft == nil {
				return zero, []Problem{problem(CodeConflict, "input.profileId",
					"no draft version to publish; edit the profile to fork a draft first.")}
			}
			rules, err := q.ListCareRules(ctx, draft.ID)
			if err != nil {
				return zero, []Problem{dbProblem("", err)}
			}
			if len(rules) == 0 {
				return zero, []Problem{problem(CodeValidation, "input.profileId",
					"a publishable draft needs at least one rule.")}
			}
			if _, err := q.PublishCareProfileVersion(ctx, dbgen.PublishCareProfileVersionParams{
				ID: draft.ID, ReviewerLabel: pgtype.Text{String: in.ReviewerLabel, Valid: true},
				ReviewedOn: ptrDate(&in.ReviewedOn),
			}); err != nil {
				if errors.Is(err, pgx.ErrNoRows) {
					return zero, []Problem{problem(CodeConflict, "input.profileId",
						"draft version changed concurrently; retry.")}
				}
				return zero, []Problem{dbProblem("", err)}
			}
			updated, err := q.MarkCareProfilePublished(ctx, dbgen.MarkCareProfilePublishedParams{
				ID: pid, WorkspaceID: a.WorkspaceID, Revision: in.ExpectedRevision,
			})
			if err != nil {
				if errors.Is(err, pgx.ErrNoRows) {
					return zero, []Problem{problemWithRevision(
						problem(CodeConflict, "expectedRevision", "profile was modified concurrently."),
						prof.Revision)}
				}
				return zero, []Problem{dbProblem("", err)}
			}
			if err := s.audit(ctx, q, a, "publish", "care_profile", pid, updated.Revision, prof); err != nil {
				return zero, []Problem{dbProblem("", err)}
			}
			view, err := s.profileView(ctx, q, updated)
			if err != nil {
				return zero, []Problem{dbProblem("", err)}
			}
			return view, nil
		})
}

func (s *Service) RetireCareProfileVersion(ctx context.Context, a Actor, profileID, profileVersionID string, expectedRevision int32, cmid *string) (ProfileView, []Problem, error) {
	var zero ProfileView
	pid, ok := parseID(profileID)
	if !ok {
		return zero, []Problem{problem(CodeValidation, "input.profileId", "expected a UUID")}, nil
	}
	vid, ok := parseID(profileVersionID)
	if !ok {
		return zero, []Problem{problem(CodeValidation, "input.profileVersionId", "expected a UUID")}, nil
	}
	return mutate(s, ctx, a, "retireCareProfileVersion", cmid,
		map[string]any{"profileId": profileID, "profileVersionId": profileVersionID, "expectedRevision": expectedRevision},
		func(ctx context.Context, q *dbgen.Queries) (ProfileView, []Problem) {
			prof, err := q.GetCareProfile(ctx, dbgen.GetCareProfileParams{ID: pid, WorkspaceID: a.WorkspaceID})
			if err != nil {
				if errors.Is(err, pgx.ErrNoRows) {
					return zero, []Problem{problem(CodeNotFound, "input.profileId", "no such profile in this workspace.")}
				}
				return zero, []Problem{dbProblem("", err)}
			}
			ver, err := q.GetCareProfileVersion(ctx, dbgen.GetCareProfileVersionParams{ID: vid, WorkspaceID: a.WorkspaceID})
			if err != nil {
				if errors.Is(err, pgx.ErrNoRows) {
					return zero, []Problem{problem(CodeNotFound, "input.profileVersionId", "no such version in this workspace.")}
				}
				return zero, []Problem{dbProblem("", err)}
			}
			if ver.ProfileID != pid {
				return zero, []Problem{problem(CodeValidation, "input.profileVersionId",
					"version does not belong to this profile.")}
			}
			if ver.Status != dbgen.ProfileStatusREVIEWED {
				return zero, []Problem{problem(CodeConflict, "input.profileVersionId",
					"only REVIEWED versions can be retired (status is "+string(ver.Status)+").")}
			}
			if _, err := q.RetireCareProfileVersionRow(ctx, vid); err != nil {
				if errors.Is(err, pgx.ErrNoRows) {
					return zero, []Problem{problem(CodeConflict, "input.profileVersionId",
						"version changed concurrently; retry.")}
				}
				return zero, []Problem{dbProblem("", err)}
			}
			bumped, err := q.BumpCareProfileRevision(ctx, dbgen.BumpCareProfileRevisionParams{
				ID: pid, WorkspaceID: a.WorkspaceID, Revision: expectedRevision,
			})
			if err != nil {
				if errors.Is(err, pgx.ErrNoRows) {
					return zero, []Problem{problemWithRevision(
						problem(CodeConflict, "expectedRevision", "profile was modified concurrently."),
						prof.Revision)}
				}
				return zero, []Problem{dbProblem("", err)}
			}
			if err := s.audit(ctx, q, a, "retire_version", "care_profile", pid, bumped.Revision, prof); err != nil {
				return zero, []Problem{dbProblem("", err)}
			}
			// T33: pending advice that cited this version needs an owner
			// review touch before it can be accepted again; the revision
			// bump makes in-flight accepts fail CONFLICT, not staleness.
			if err := flagReviewForRetiredVersion(ctx, q, a.WorkspaceID, pid, vid); err != nil {
				return zero, []Problem{dbProblem("", err)}
			}
			view, err := s.profileView(ctx, q, bumped)
			if err != nil {
				return zero, []Problem{dbProblem("", err)}
			}
			return view, nil
		})
}

// ---- reads ---------------------------------------------------------------

func (s *Service) GetCareProfile(ctx context.Context, a Actor, id string) (ProfileView, []Problem, error) {
	pid, ok := parseID(id)
	if !ok {
		return ProfileView{}, []Problem{problem(CodeValidation, "id", "expected a UUID")}, nil
	}
	prof, err := s.q().GetCareProfile(ctx, dbgen.GetCareProfileParams{ID: pid, WorkspaceID: a.WorkspaceID})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ProfileView{}, []Problem{problem(CodeNotFound, "id", "no such profile in this workspace.")}, nil
		}
		return ProfileView{}, nil, err
	}
	view, err := s.profileView(ctx, s.q(), prof)
	if err != nil {
		return ProfileView{}, nil, err
	}
	return view, nil, nil
}

type ProfileFilter struct {
	Category      *string
	Query         *string
	IncludeDrafts *bool
}

type ProfileConnection struct {
	Items      []ProfileView
	NextCursor *string
}

func (s *Service) ListCareProfiles(ctx context.Context, a Actor, f ProfileFilter, first int32, after *string) (ProfileConnection, []Problem, error) {
	if first < 1 || first > 100 {
		return ProfileConnection{}, []Problem{problem(CodeValidation, "first", "expected 1..100")}, nil
	}
	if f.Category != nil && !categorySet[*f.Category] {
		return ProfileConnection{}, []Problem{problem(CodeValidation, "filter.category", "unknown PlantCategory")}, nil
	}
	afterTS, afterID, probs := decodeDateCursor(after)
	if len(probs) > 0 {
		return ProfileConnection{}, probs, nil
	}
	rows, err := s.q().ListCareProfiles(ctx, dbgen.ListCareProfilesParams{
		WorkspaceID: a.WorkspaceID, Category: ptrText(f.Category), Query: ptrText(f.Query),
		IncludeDrafts: f.IncludeDrafts, AfterCreatedAt: afterTS, AfterID: afterID,
		LimitCount: first + 1,
	})
	if err != nil {
		return ProfileConnection{}, nil, err
	}
	hasMore := len(rows) > int(first)
	if hasMore {
		rows = rows[:first]
	}
	out := ProfileConnection{Items: make([]ProfileView, 0, len(rows))}
	for _, r := range rows {
		v, err := s.profileView(ctx, s.q(), r)
		if err != nil {
			return ProfileConnection{}, nil, err
		}
		out.Items = append(out.Items, v)
	}
	if hasMore && len(rows) > 0 {
		last := rows[len(rows)-1]
		c := encodeDateCursor(last.CreatedAt, uuidString(last.ID))
		out.NextCursor = &c
	}
	return out, nil, nil
}

// profileView assembles versions → rules → pinned citations.
func (s *Service) profileView(ctx context.Context, q *dbgen.Queries, prof dbgen.CareProfile) (ProfileView, error) {
	vers, err := q.ListCareProfileVersions(ctx, prof.ID)
	if err != nil {
		return ProfileView{}, err
	}
	v := ProfileView{
		ID: uuidString(prof.ID), Category: string(prof.Category),
		Species: optText(prof.Species), Cultivar: optText(prof.Cultivar),
		Status: string(prof.Status), Revision: prof.Revision,
		CreatedAt: stamp(prof.CreatedAt), UpdatedAt: stamp(prof.UpdatedAt),
		Versions: make([]ProfileVersionView, 0, len(vers)),
	}
	for _, ver := range vers {
		env := EnvironmentView{Situations: []string{}, Hemispheres: []string{}}
		var raw struct {
			Situations  []string `json:"situations"`
			Hemispheres []string `json:"hemispheres"`
		}
		if len(ver.Environment) > 0 {
			if err := json.Unmarshal(ver.Environment, &raw); err == nil {
				if raw.Situations != nil {
					env.Situations = raw.Situations
				}
				if raw.Hemispheres != nil {
					env.Hemispheres = raw.Hemispheres
				}
			}
		}
		rules, err := q.ListCareRules(ctx, ver.ID)
		if err != nil {
			return ProfileView{}, err
		}
		srcs, err := q.ListRuleSourcesForVersion(ctx, ver.ID)
		if err != nil {
			return ProfileView{}, err
		}
		byRule := map[string][]SourceVersionView{}
		for _, row := range srcs {
			byRule[uuidString(row.RuleID)] = append(byRule[uuidString(row.RuleID)], SourceVersionView{
				ID: uuidString(row.ID), SourceID: uuidString(row.SourceID), Version: row.Version,
				Citation: row.Citation, Summary: row.Summary,
				RetrievedOn:   optDate(row.RetrievedOn),
				ContentSha256: optText(row.ContentSha256),
				CreatedAt:     stamp(row.CreatedAt),
			})
		}
		pv := ProfileVersionView{
			ID: uuidString(ver.ID), ProfileID: uuidString(ver.ProfileID),
			Version: ver.Version, Status: string(ver.Status), Environment: env,
			Rules:         make([]RuleView, 0, len(rules)),
			ReviewerLabel: optText(ver.ReviewerLabel),
			ReviewedOn:    optDate(ver.ReviewedOn),
			PublishedAt:   optTS(ver.PublishedAt), RetiredAt: optTS(ver.RetiredAt),
			CreatedAt: stamp(ver.CreatedAt),
		}
		for _, r := range rules {
			rv := RuleView{
				RuleKey: r.RuleKey, Kind: string(r.Kind),
				Recurrence:         r.Recurrence,
				RequiredFacts:      r.RequiredFacts,
				WeatherSensitivity: string(r.WeatherSensitivity),
				GuidanceText:       r.GuidanceText,
				SourceVersions:     byRule[uuidString(r.ID)],
			}
			if rv.SourceVersions == nil {
				rv.SourceVersions = []SourceVersionView{}
			}
			if r.ActivityKind.Valid {
				ak := string(r.ActivityKind.ActivityKind)
				rv.ActivityKind = &ak
			}
			pv.Rules = append(pv.Rules, rv)
		}
		v.Versions = append(v.Versions, pv)
	}
	return v, nil
}
