package care

// Durable occurrence generation and activity completion (T23).
//
// Contract source: 03-contracts "Scheduling contract". The pure engine
// (internal/care/engine) decides WHEN work exists; this file persists it:
//   - materialization is idempotent: the logical occurrence key is the
//     uniqueness anchor, so duplicate ticks and catch-up replays are
//     harmless (03: "application uniqueness keys make duplicate ticks
//     harmless").
//   - a per-plan watermark (care_plans.last_generated_through) is the
//     persisted scheduler cursor enabling startup catch-up.
//   - outage backlog collapses to ONE overdue actionable check per
//     repeating rule; fixed one-time events stay distinct.
//   - complete/skip/snooze/correct run as single transactions: the linked
//     activity, the occurrence state, the rule cursor, and the
//     regenerated future all commit or roll back together.
//   - plan saves cancel only future uncompleted occurrences and rebuild
//     the future transactionally; history is never rewritten. Timezone
//     edits require an explicit recalculation confirmation (preview is
//     the read-only sibling).

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	dbgen "canopy/server/db/generated"
	"canopy/server/internal/care/engine"
	"canopy/server/internal/journal"
)

var notificationTimeRe = regexp.MustCompile(`^([01][0-9]|2[0-3]):[0-5][0-9]$`)

// ---- date plumbing -------------------------------------------------------

func engToPg(d engine.Date) pgtype.Date {
	return pgtype.Date{Time: time.Date(d.Year, time.Month(d.Month), d.Day, 12, 0, 0, 0, time.UTC), Valid: true}
}

func pgToEng(d pgtype.Date) engine.Date { return engine.DateFrom(d.Time) }

// nonNil keeps text[] columns NOT NULL-safe: a nil Go slice encodes as NULL.
func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// localNoon anchors an activity instant at local noon in the plan zone:
// DST-safe (never lands in a gap) and the date component is exact.
func localNoon(d engine.Date, loc *time.Location) pgtype.Timestamptz {
	return pgtype.Timestamptz{
		Time:  time.Date(d.Year, time.Month(d.Month), d.Day, 12, 0, 0, 0, loc),
		Valid: true,
	}
}

func todayIn(loc *time.Location) engine.Date { return engine.DateFrom(time.Now().In(loc)) }

// ---- plan inputs ---------------------------------------------------------

type PlanOverrideInput struct {
	RuleKey            string
	Enabled            *bool
	RecurrenceOverride *json.RawMessage // canonical frozen jsonb, or nil
}

type CreatePlanInput struct {
	PlantID          string
	ProfileVersionID *string // must be REVIEWED and non-retired
	Timezone         string  // IANA
	NotificationTime *string // HH:MM, default 09:00
	Enabled          *bool
	Overrides        []PlanOverrideInput
}

type UpdatePlanInput struct {
	PlanID               string
	ExpectedRevision     int32
	ProfileVersionID     *string
	ClearProfileVersion  bool // explicit unpin (contract clearProfileVersion)
	Timezone             *string
	NotificationTime     *string
	Enabled              *bool
	Overrides            []PlanOverrideInput
	ConfirmRecalculation bool
}

// ---- plan CRUD + regeneration --------------------------------------------

// CreateCarePlan pins a reviewed profile version to a plant and
// materializes the first horizon transactionally.
func (s *Service) CreateCarePlan(ctx context.Context, a Actor, in CreatePlanInput, cmid *string) (dbgen.CarePlan, []Problem, error) {
	var zero dbgen.CarePlan
	plantID, ok := parseID(in.PlantID)
	if !ok {
		return zero, []Problem{problem(CodeValidation, "input.plantId", "plantId must be a UUID")}, nil
	}
	tz, p := loadZoneProblem(in.Timezone, "input.timezone")
	if p != nil {
		return zero, []Problem{*p}, nil
	}
	notif := "09:00"
	if in.NotificationTime != nil {
		notif = *in.NotificationTime
	}
	if !notificationTimeRe.MatchString(notif) {
		return zero, []Problem{problem(CodeValidation, "input.notificationTime",
			"notificationTime must be HH:MM (00:00..23:59)")}, nil
	}
	enabled := true
	if in.Enabled != nil {
		enabled = *in.Enabled
	}

	return mutate(s, ctx, a, "createCarePlan", cmid, in,
		func(ctx context.Context, q *dbgen.Queries) (dbgen.CarePlan, []Problem) {
			if _, err := q.GetPlant(ctx, dbgen.GetPlantParams{ID: plantID, WorkspaceID: a.WorkspaceID}); err != nil {
				if isNoRows(err) {
					return zero, []Problem{problem(CodeNotFound, "input.plantId", "plant not found")}
				}
				return zero, []Problem{dbProblem("input", err)}
			}
			// One plan per plant; the CONFLICT quotes the existing revision.
			if ex, err := q.GetCarePlanByPlant(ctx, plantID); err == nil {
				return zero, []Problem{problemWithRevision(
					problem(CodeConflict, "input.plantId", "this plant already has a care plan"), ex.Revision)}
			} else if !isNoRows(err) {
				return zero, []Problem{dbProblem("input", err)}
			}

			var pvID pgtype.UUID
			if in.ProfileVersionID != nil {
				id, ok := parseID(*in.ProfileVersionID)
				if !ok {
					return zero, []Problem{problem(CodeValidation, "input.profileVersionId", "profileVersionId must be a UUID")}
				}
				pv, err := q.GetCareProfileVersion(ctx, dbgen.GetCareProfileVersionParams{ID: id, WorkspaceID: a.WorkspaceID})
				if err != nil {
					if isNoRows(err) {
						return zero, []Problem{problem(CodeNotFound, "input.profileVersionId", "profile version not found")}
					}
					return zero, []Problem{dbProblem("input", err)}
				}
				if pv.Status != dbgen.ProfileStatusREVIEWED || pv.RetiredAt.Valid {
					return zero, []Problem{problem(CodeValidation, "input.profileVersionId",
						"plans pin a REVIEWED, non-retired profile version")}
				}
				pvID = pv.ID
			}

			problems := validateOverrides(ctx, q, pvID, in.Overrides)
			if len(problems) > 0 {
				return zero, problems
			}

			plan, err := q.InsertCarePlan(ctx, dbgen.InsertCarePlanParams{
				ID: s.uuid.New(), WorkspaceID: a.WorkspaceID, PlantID: plantID,
				ProfileVersionID: pvID, Timezone: tz.String(), Enabled: enabled, NotificationTime: notif,
			})
			if err != nil {
				if isUniqueViolation(err) {
					return zero, []Problem{problem(CodeConflict, "input.plantId", "this plant already has a care plan")}
				}
				return zero, []Problem{dbProblem("input", err)}
			}
			if problems := insertOverrides(ctx, q, s, plan, in.Overrides); len(problems) > 0 {
				return zero, problems
			}
			if err := s.materializeTx(ctx, q, plan, todayIn(tz), time.Now().UTC()); err != nil {
				return zero, []Problem{dbProblem("input", err)}
			}
			if err := s.audit(ctx, q, a, "createCarePlan", "care_plan", plan.ID, plan.Revision, nil); err != nil {
				return zero, []Problem{dbProblem("input", err)}
			}
			return plan, nil
		})
}

// UpdateCarePlan bumps the plan revision, replaces overrides, cancels only
// open occurrences, and rebuilds the future in one transaction. A timezone
// change without ConfirmRecalculation is rejected: the preview use case
// exists precisely so the user sees the date shifts first.
func (s *Service) UpdateCarePlan(ctx context.Context, a Actor, in UpdatePlanInput, cmid *string) (dbgen.CarePlan, []Problem, error) {
	var zero dbgen.CarePlan
	planID, ok := parseID(in.PlanID)
	if !ok {
		return zero, []Problem{problem(CodeValidation, "input.planId", "planId must be a UUID")}, nil
	}
	return mutate(s, ctx, a, "updateCarePlan", cmid, in,
		func(ctx context.Context, q *dbgen.Queries) (dbgen.CarePlan, []Problem) {
			cur, err := q.GetCarePlanForUpdate(ctx, planID)
			if err != nil {
				if isNoRows(err) {
					return zero, []Problem{problem(CodeNotFound, "input.planId", "care plan not found")}
				}
				return zero, []Problem{dbProblem("input", err)}
			}
			if cur.WorkspaceID != a.WorkspaceID {
				return zero, []Problem{problem(CodeNotFound, "input.planId", "care plan not found")}
			}
			if cur.Revision != in.ExpectedRevision {
				return zero, []Problem{problemWithRevision(
					problem(CodeConflict, "input.expectedRevision", "revision conflict"), cur.Revision)}
			}
			newTZ := cur.Timezone
			if in.Timezone != nil {
				newTZ = *in.Timezone
			}
			if newTZ != cur.Timezone && !in.ConfirmRecalculation {
				return zero, []Problem{problem(CodeValidation, "input.confirmRecalculation",
					"a timezone change shifts occurrence dates; preview the plan and set confirmRecalculation to apply it")}
			}
			tz, p := loadZoneProblem(newTZ, "input.timezone")
			if p != nil {
				return zero, []Problem{*p}
			}
			notif := cur.NotificationTime
			if in.NotificationTime != nil {
				notif = *in.NotificationTime
			}
			if !notificationTimeRe.MatchString(notif) {
				return zero, []Problem{problem(CodeValidation, "input.notificationTime",
					"notificationTime must be HH:MM (00:00..23:59)")}
			}
			var pvID = cur.ProfileVersionID
			if in.ClearProfileVersion && in.ProfileVersionID != nil {
				return zero, []Problem{problem(CodeValidation, "input.clearProfileVersion",
					"use either clearProfileVersion or profileVersionId, not both")}
			}
			if in.ClearProfileVersion {
				pvID = pgtype.UUID{} // NULL: owner-defined plan only
			} else if in.ProfileVersionID != nil {
				id, ok := parseID(*in.ProfileVersionID)
				if !ok {
					return zero, []Problem{problem(CodeValidation, "input.profileVersionId", "profileVersionId must be a UUID")}
				}
				pv, err := q.GetCareProfileVersion(ctx, dbgen.GetCareProfileVersionParams{ID: id, WorkspaceID: a.WorkspaceID})
				if err != nil {
					if isNoRows(err) {
						return zero, []Problem{problem(CodeNotFound, "input.profileVersionId", "profile version not found")}
					}
					return zero, []Problem{dbProblem("input", err)}
				}
				if pv.Status != dbgen.ProfileStatusREVIEWED || pv.RetiredAt.Valid {
					return zero, []Problem{problem(CodeValidation, "input.profileVersionId",
						"plans pin a REVIEWED, non-retired profile version")}
				}
				pvID = pv.ID
			}
			problems := validateOverrides(ctx, q, pvID, in.Overrides)
			if len(problems) > 0 {
				return zero, problems
			}

			aggTZ := pgtype.Text{String: tz.String(), Valid: true}
			aggNotif := pgtype.Text{String: notif, Valid: true}
			var aggEnabled pgtype.Bool
			if in.Enabled != nil {
				aggEnabled = pgtype.Bool{Bool: *in.Enabled, Valid: true}
			}
			plan, err := q.UpdateCarePlanAggregate(ctx, dbgen.UpdateCarePlanAggregateParams{
				ID: cur.ID, WorkspaceID: a.WorkspaceID, ExpectedRevision: in.ExpectedRevision,
				ProfileVersionID: pvID, Timezone: aggTZ,
				Enabled: aggEnabled, NotificationTime: aggNotif,
			})
			if err != nil {
				if isNoRows(err) {
					return zero, []Problem{problemWithRevision(
						problem(CodeConflict, "input.expectedRevision", "revision conflict"), cur.Revision)}
				}
				return zero, []Problem{dbProblem("input", err)}
			}
			if err := q.DeleteCarePlanOverrides(ctx, plan.ID); err != nil {
				return zero, []Problem{dbProblem("input", err)}
			}
			if problems := insertOverrides(ctx, q, s, plan, in.Overrides); len(problems) > 0 {
				return zero, problems
			}
			// Cancel only open occurrences; history stays intact. The
			// watermark resets so the rebuild regenerates the horizon.
			if _, err := q.CancelAllOpenOccurrences(ctx, plan.ID); err != nil {
				return zero, []Problem{dbProblem("input", err)}
			}
			if err := q.SetPlanWatermark(ctx, dbgen.SetPlanWatermarkParams{
				ID: plan.ID, ThroughDate: pgtype.Date{}, // NULL: rebuild the horizon
			}); err != nil {
				return zero, []Problem{dbProblem("input", err)}
			}
			plan.LastGeneratedThrough = pgtype.Date{} // match the reset row in memory
			if err := s.materializeTx(ctx, q, plan, todayIn(tz), time.Now().UTC()); err != nil {
				return zero, []Problem{dbProblem("input", err)}
			}
			if err := s.audit(ctx, q, a, "updateCarePlan", "care_plan", plan.ID, plan.Revision, cur); err != nil {
				return zero, []Problem{dbProblem("input", err)}
			}
			return plan, nil
		})
}

// ---- preview (read-only dry run) ------------------------------------------

type PreviewPlanInput struct {
	PlantID            string
	ProfileVersionID   *string
	Timezone           string
	NotificationTime   *string
	Overrides          []PlanOverrideInput
	IncludeWouldCancel *bool
}

type PreviewOccurrenceView struct {
	RuleKey       string
	Kind          string
	ActivityKind  string
	Key           string
	DueDate       string // YYYY-MM-DD
	WindowEnd     string // YYYY-MM-DD or ""
	ReasonText    string
	MissingFacts  []string
	EvidenceNotes []string
	// Weather is the exact snapshot the preview consulted (T26); nil
	// when the location has no snapshot — advice then proceeds on
	// calendar cadence alone.
	Weather *dbgen.WeatherSnapshot
}

type PlanPreview struct {
	Occurrences  []PreviewOccurrenceView
	WouldCancel  []dbgen.CareOccurrence
	MissingFacts []string // union across draft rules; abstaining rules list theirs
}

// PreviewCarePlan changes nothing: it generates the draft plan's horizon
// and lists the open occurrences a save would cancel (03: timezone edits
// require an explicit recalculation preview).
func (s *Service) PreviewCarePlan(ctx context.Context, a Actor, in PreviewPlanInput) (PlanPreview, []Problem, error) {
	var out PlanPreview
	plantID, ok := parseID(in.PlantID)
	if !ok {
		return out, []Problem{problem(CodeValidation, "input.plantId", "plantId must be a UUID")}, nil
	}
	tz, p := loadZoneProblem(in.Timezone, "input.timezone")
	if p != nil {
		return out, []Problem{*p}, nil
	}
	q := s.q()
	if _, err := q.GetPlant(ctx, dbgen.GetPlantParams{ID: plantID, WorkspaceID: a.WorkspaceID}); err != nil {
		if isNoRows(err) {
			return out, []Problem{problem(CodeNotFound, "input.plantId", "plant not found")}, nil
		}
		return out, nil, err
	}
	var pvID pgtype.UUID
	if in.ProfileVersionID != nil {
		id, ok := parseID(*in.ProfileVersionID)
		if !ok {
			return out, []Problem{problem(CodeValidation, "input.profileVersionId", "profileVersionId must be a UUID")}, nil
		}
		pv, err := q.GetCareProfileVersion(ctx, dbgen.GetCareProfileVersionParams{ID: id, WorkspaceID: a.WorkspaceID})
		if err != nil {
			if isNoRows(err) {
				return out, []Problem{problem(CodeNotFound, "input.profileVersionId", "profile version not found")}, nil
			}
			return out, nil, err
		}
		pvID = pv.ID
	} else if ex, err := q.GetCarePlanByPlant(ctx, plantID); err == nil {
		// Contract: preview mirrors the saved plan's source when the input
		// does not name a profile version.
		if ex.WorkspaceID == a.WorkspaceID {
			pvID = ex.ProfileVersionID
		}
	} else if !isNoRows(err) {
		return out, nil, err
	}
	if problems := validateOverrides(ctx, q, pvID, in.Overrides); len(problems) > 0 {
		return out, problems, nil
	}

	// Draft plan row for the generator (unsaved; created_at = now anchors
	// plan-start rules at the preview moment).
	draft := dbgen.CarePlan{
		ID: plantID, WorkspaceID: a.WorkspaceID, PlantID: plantID,
		ProfileVersionID: pvID, Timezone: tz.String(), Enabled: true,
		CreatedAt: pgtype.Timestamptz{Time: time.Now().UTC(), Valid: true},
	}
	planStart := todayIn(tz)
	occs, err := s.generateDraft(ctx, q, draft, in.Overrides, planStart, todayIn(tz))
	if err != nil {
		return out, nil, err
	}
	out.Occurrences = occs
	seenFact := map[string]bool{}
	for _, v := range occs {
		for _, f := range v.MissingFacts {
			if !seenFact[f] {
				seenFact[f] = true
				out.MissingFacts = append(out.MissingFacts, f)
			}
		}
	}

	includeCancel := in.IncludeWouldCancel == nil || *in.IncludeWouldCancel
	if includeCancel {
		if ex, err := q.GetCarePlanByPlant(ctx, plantID); err == nil {
			open, err := q.ListPlanOccurrences(ctx, dbgen.ListPlanOccurrencesParams{
				PlanID: ex.ID, Statuses: []string{"DUE"}, LimitCount: 500,
			})
			if err != nil {
				return out, nil, err
			}
			out.WouldCancel = open
		} else if !isNoRows(err) {
			return out, nil, err
		}
	}
	return out, nil, nil
}

// ---- materialization -------------------------------------------------------

// MaterializePlan runs one scheduler tick for one plan in its own
// transaction (plan row locked). Duplicate ticks are harmless.
func (s *Service) MaterializePlan(ctx context.Context, planID pgtype.UUID) error {
	return s.materializePlanAt(ctx, planID, nil)
}

// materializePlanAt is the seam tests use to run a tick "as of" a given
// local date (outage catch-up, restart replay) without moving the clock.
func (s *Service) materializePlanAt(ctx context.Context, planID pgtype.UUID, asOf *engine.Date) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := dbgen.New(tx)
	plan, err := q.GetCarePlanForUpdate(ctx, planID)
	if err != nil {
		if isNoRows(err) {
			return nil
		}
		return err
	}
	if !plan.Enabled {
		return nil
	}
	tz, err := time.LoadLocation(plan.Timezone)
	if err != nil {
		return fmt.Errorf("care plan %s: bad timezone %q: %w", uuidString(plan.ID), plan.Timezone, err)
	}
	today := todayIn(tz)
	if asOf != nil {
		today = *asOf
	}
	if err := s.materializeTx(ctx, q, plan, today, time.Now().UTC()); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// RunCareCatchUp is the startup catch-up: one tick per enabled plan, each
// in its own transaction so one bad plan cannot block the agenda.
func (s *Service) RunCareCatchUp(ctx context.Context) error {
	plans, err := s.q().ListEnabledCarePlans(ctx)
	if err != nil {
		return err
	}
	var firstErr error
	for _, p := range plans {
		if err := s.MaterializePlan(ctx, p.ID); err != nil {
			s.Log.Error("care catch-up tick failed", "plan", uuidString(p.ID), "err", err)
			if firstErr == nil {
				firstErr = err
			}
		}
	}
	// Daily digest intents ride the same full-scan tick (T27): one per
	// owner per local date, deduped, gated at the plan notification time.
	if err := s.EnsureDigests(ctx, s.clock.Now().UTC()); err != nil {
		s.Log.Error("digest composition failed", "err", err)
		if firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// materializeTx generates the 60-day horizon for every enabled rule of a
// locked plan, collapses outage backlog to one overdue actionable check
// per repeating rule, and advances the watermark.
func (s *Service) materializeTx(ctx context.Context, q *dbgen.Queries, plan dbgen.CarePlan, today engine.Date, now time.Time) error {
	watermarkCovers := planThroughOrBeyond(plan, today)

	plant, ws, ec, planStart, snap, err := s.buildContext(ctx, q, plan, today, now)
	if err != nil {
		return err
	}
	if plant.Status != dbgen.PlantStatusACTIVE {
		// Archived/dead plants never gain new work or notifications;
		// archive itself cancelled the open slots (T24 lifecycle hook).
		return nil
	}

	var rules []dbgen.CareRule
	var envMissing []string
	if plan.ProfileVersionID.Valid {
		rules, err = q.ListCareRules(ctx, plan.ProfileVersionID)
		if err != nil {
			return err
		}
		pv, err := q.GetCareProfileVersion(ctx, dbgen.GetCareProfileVersionParams{
			ID: plan.ProfileVersionID, WorkspaceID: plan.WorkspaceID})
		if err != nil {
			return err
		}
		env, err := engine.ParseEnvironment(pv.Environment)
		if err != nil {
			return err
		}
		ok, missing := env.Matches(ec)
		if !ok {
			if len(missing) == 0 {
				return nil // profile does not apply to this plant's environment
			}
			envMissing = missing // fail closed per rule, never silently pass
		}
	}

	ovs, err := q.ListCarePlanOverrides(ctx, plan.ID)
	if err != nil {
		return err
	}
	ovMap := map[string]dbgen.CarePlanOverride{}
	for _, o := range ovs {
		ovMap[o.RuleKey] = o
	}

	for _, rule := range rules {
		ov, has := ovMap[rule.RuleKey]
		if has && !ov.Enabled {
			continue
		}
		raw := rule.Recurrence
		if has && ov.RecurrenceOverride != nil {
			raw = ov.RecurrenceOverride
		}
		rec, err := engine.ParseRecurrence(raw)
		if err != nil {
			return fmt.Errorf("rule %s: %w", rule.RuleKey, err)
		}

		if !watermarkCovers {
			if err := s.generateRule(ctx, q, plan, rule, rec, ec, envMissing, planStart, today, snap.ID); err != nil {
				return err
			}
		}
		// Outage collapse runs every tick: missed repeating checks reduce
		// to one overdue actionable check; one-time events stay distinct.
		if rec.Form != engine.FormOneTime {
			if _, err := q.CancelStaleBacklogKeepEarliest(ctx, dbgen.CancelStaleBacklogKeepEarliestParams{
				PlanID: plan.ID, RuleKey: rule.RuleKey, TodayDate: engToPg(today),
			}); err != nil {
				return err
			}
		}
	}

	// Notification intents ride the same tick: per-occurrence dedup keys
	// keep restarts and repeated ticks from duplicating a day's nag.
	if err := s.generateNotifications(ctx, q, plan, plant.Name, ws.OwnerID, today); err != nil {
		return err
	}

	return q.SetPlanWatermark(ctx, dbgen.SetPlanWatermarkParams{
		ID: plan.ID, ThroughDate: engToPg(today.AddDays(engine.HorizonDays)),
	})
}

// planThroughOrBeyond reports whether the watermark already covers the
// full horizon from today.
func planThroughOrBeyond(plan dbgen.CarePlan, today engine.Date) bool {
	if !plan.LastGeneratedThrough.Valid {
		return false
	}
	wm := pgToEng(plan.LastGeneratedThrough)
	return !wm.Before(today.AddDays(engine.HorizonDays))
}

// generateRule materializes one rule's slots for [today, today+60]
// (completion-relative rules also backfill an overdue cursor slot).
// snapID is the exact weather snapshot the advice was computed from
// (zero value = none); it rides onto every slot as typed evidence.
func (s *Service) generateRule(ctx context.Context, q *dbgen.Queries, plan dbgen.CarePlan,
	rule dbgen.CareRule, rec *engine.Recurrence, ec engine.Context, envMissing []string,
	planStart, today engine.Date, snapID pgtype.UUID) error {

	var cursor *engine.Cursor
	from := today
	if rec.Form == engine.FormEveryN && rec.Anchor == engine.AnchorLastCompletion {
		row, err := q.GetCareRuleCursor(ctx, dbgen.GetCareRuleCursorParams{PlanID: plan.ID, RuleKey: rule.RuleKey})
		switch {
		case err == nil:
			c := engine.Cursor{
				LastOrdinal: row.LastResolvedOrdinal.Int64,
				AnchorDate:  pgToEng(row.AnchorDate), NextDueDate: pgToEng(row.NextDueDate),
			}
			if row.Decision.Valid {
				c.LastDecision = engine.Decision(string(row.Decision.OccurrenceStatus))
			}
			cursor = &c
		case isNoRows(err):
			// No decision yet: the first completion-relative check is due
			// at plan start.
			cursor = &engine.Cursor{AnchorDate: planStart, NextDueDate: planStart}
		default:
			return err
		}
		if cursor.NextDueDate.Before(today) {
			from = cursor.NextDueDate // backfill the overdue slot; collapse keeps one
		}
	}

	slots, err := rec.Generate(rule.RuleKey, from, planStart, cursor)
	if err != nil {
		return err
	}

	adv := engine.Advise(engine.Rule{
		RuleKey: rule.RuleKey, Kind: string(rule.Kind), ActivityKind: activityKindText(rule.ActivityKind),
		RequiredFacts: rule.RequiredFacts, WeatherSensitivity: string(rule.WeatherSensitivity),
		GuidanceText: rule.GuidanceText,
	}, ec)
	missing := mergeFacts(adv.MissingFacts, envMissing)

	for _, slot := range slots {
		var windowEnd pgtype.Date
		if slot.WindowEnd != nil {
			windowEnd = engToPg(*slot.WindowEnd)
		}
		if _, err := q.InsertCareOccurrenceUpsert(ctx, dbgen.InsertCareOccurrenceUpsertParams{
			ID: s.uuid.New(), WorkspaceID: plan.WorkspaceID, PlanID: plan.ID,
			RuleKey: rule.RuleKey, Kind: rule.Kind, ActivityKind: rule.ActivityKind,
			OccurrenceKey:   slot.Key,
			OriginalDueDate: engToPg(slot.DueDate), CurrentDueDate: engToPg(slot.DueDate),
			PlanTimezone:      plan.Timezone,
			Ordinal:           pgtype.Int8{Int64: slot.Ordinal, Valid: true},
			ReasonText:        pgtype.Text{String: adv.ReasonText, Valid: true},
			MissingFacts:      nonNil(missing),
			EvidenceNotes:     nonNil(adv.EvidenceNotes),
			WindowEnd:         windowEnd,
			WeatherSnapshotID: snapID,
		}); err != nil && !isNoRows(err) {
			// ErrNoRows here means "conflicted with a non-cancelled row":
			// the slot already exists in its final state, nothing to do.
			return err
		}
	}
	return nil
}

// buildContext assembles the engine Context (plant facts, workspace
// hemisphere, current placement, latest weather) for materialization,
// and hands back the plant and workspace rows so callers can gate on
// plant status and address the owner (notification intents). The latest
// snapshot row is returned too (zero value when absent): materialization
// records its id so every advisory result references the EXACT snapshot
// that backed it (T26).
func (s *Service) buildContext(ctx context.Context, q *dbgen.Queries, plan dbgen.CarePlan,
	today engine.Date, now time.Time) (dbgen.Plant, dbgen.Workspace, engine.Context, engine.Date, dbgen.WeatherSnapshot, error) {

	plant, err := q.GetPlant(ctx, dbgen.GetPlantParams{ID: plan.PlantID, WorkspaceID: plan.WorkspaceID})
	if err != nil {
		return dbgen.Plant{}, dbgen.Workspace{}, engine.Context{}, engine.Date{}, dbgen.WeatherSnapshot{}, err
	}
	ws, err := q.GetWorkspaceByID(ctx, plan.WorkspaceID)
	if err != nil {
		return dbgen.Plant{}, dbgen.Workspace{}, engine.Context{}, engine.Date{}, dbgen.WeatherSnapshot{}, err
	}
	ec := engine.Context{
		Today: today, Now: now,
		Cultivar:       plant.Cultivar.String,
		ScientificName: plant.ScientificName.String,
	}
	if ws.Latitude != nil && ws.Latitude.Valid {
		if f, err := ws.Latitude.Float64Value(); err == nil && f.Valid {
			if f.Float64 < 0 {
				ec.Hemisphere = "SOUTHERN"
			} else {
				ec.Hemisphere = "NORTHERN"
			}
		}
	}
	tz, err := time.LoadLocation(plan.Timezone)
	if err != nil {
		return dbgen.Plant{}, dbgen.Workspace{}, engine.Context{}, engine.Date{}, dbgen.WeatherSnapshot{}, err
	}
	planStart := engine.DateFrom(plan.CreatedAt.Time.In(tz))

	var snap dbgen.WeatherSnapshot
	pctx, err := q.GetCurrentContext(ctx, plan.PlantID)
	switch {
	case err == nil:
		ec.Placement = string(pctx.Placement)
		// nullable numeric arrives as *pgtype.Numeric: NULL = no pot volume
		ec.PotVolumeSet = pctx.PotVolumeLiters != nil && pctx.PotVolumeLiters.Valid
		if pctx.LocationID.Valid {
			loc, err := q.GetLocation(ctx, dbgen.GetLocationParams{ID: pctx.LocationID, WorkspaceID: plan.WorkspaceID})
			if err == nil {
				ec.Situation = string(loc.Situation)
				s, err := q.LatestWeatherSnapshot(ctx, dbgen.LatestWeatherSnapshotParams{
					WorkspaceID: plan.WorkspaceID, LocationID: pctx.LocationID})
				if err == nil {
					snap = s
					ec.Weather = &engine.Weather{
						FetchedAt: snap.FetchedAt.Time, ValidFrom: snap.ValidFrom.Time, ValidUntil: snap.ValidUntil.Time,
						HighTempC: numericFloat(snap.HighTempC), LowTempC: numericFloat(snap.LowTempC),
						RainfallMm: numericFloat(snap.RainfallMm), Et0Mm: numericFloat(snap.Et0Mm),
					}
				} else if !isNoRows(err) {
					return dbgen.Plant{}, dbgen.Workspace{}, engine.Context{}, engine.Date{}, dbgen.WeatherSnapshot{}, err
				}
			} else if !isNoRows(err) {
				return dbgen.Plant{}, dbgen.Workspace{}, engine.Context{}, engine.Date{}, dbgen.WeatherSnapshot{}, err
			}
		}
	case isNoRows(err):
		// no current context: facts stay unknown, engine fails closed
	default:
		return dbgen.Plant{}, dbgen.Workspace{}, engine.Context{}, engine.Date{}, dbgen.WeatherSnapshot{}, err
	}
	return plant, ws, ec, planStart, snap, nil
}

// generateDraft is the preview path: same generation math, nothing written.
func (s *Service) generateDraft(ctx context.Context, q *dbgen.Queries, draft dbgen.CarePlan,
	overrides []PlanOverrideInput, planStart, today engine.Date) ([]PreviewOccurrenceView, error) {

	_, _, ec, _, snap, err := s.buildContext(ctx, q, draft, today, time.Now().UTC())
	if err != nil {
		return nil, err
	}
	var snapPtr *dbgen.WeatherSnapshot
	if snap.ID.Valid {
		snapPtr = &snap // preview evidence references the exact snapshot consulted now
	}
	var rules []dbgen.CareRule
	var envMissing []string
	if draft.ProfileVersionID.Valid {
		var err error
		rules, err = q.ListCareRules(ctx, draft.ProfileVersionID)
		if err != nil {
			return nil, err
		}
		pv, err := q.GetCareProfileVersion(ctx, dbgen.GetCareProfileVersionParams{
			ID: draft.ProfileVersionID, WorkspaceID: draft.WorkspaceID})
		if err != nil {
			return nil, err
		}
		env, err := engine.ParseEnvironment(pv.Environment)
		if err != nil {
			return nil, err
		}
		if ok, missing := env.Matches(ec); !ok {
			if len(missing) == 0 {
				return nil, nil // profile does not apply; preview shows nothing
			}
			envMissing = missing
		}
	}
	ovMap := map[string]PlanOverrideInput{}
	for _, o := range overrides {
		ovMap[o.RuleKey] = o
	}
	var out []PreviewOccurrenceView
	for _, rule := range rules {
		ov, has := ovMap[rule.RuleKey]
		if has && ov.Enabled != nil && !*ov.Enabled {
			continue
		}
		raw := rule.Recurrence
		if has && ov.RecurrenceOverride != nil {
			raw = *ov.RecurrenceOverride
		}
		rec, err := engine.ParseRecurrence(raw)
		if err != nil {
			return nil, err
		}
		var cursor *engine.Cursor
		if rec.Form == engine.FormEveryN && rec.Anchor == engine.AnchorLastCompletion {
			// Preview assumes no decisions yet: first check due at plan start.
			cursor = &engine.Cursor{AnchorDate: planStart, NextDueDate: planStart}
		}
		slots, err := rec.Generate(rule.RuleKey, today, planStart, cursor)
		if err != nil {
			return nil, err
		}
		adv := engine.Advise(engine.Rule{
			RuleKey: rule.RuleKey, Kind: string(rule.Kind), ActivityKind: activityKindText(rule.ActivityKind),
			RequiredFacts: rule.RequiredFacts, WeatherSensitivity: string(rule.WeatherSensitivity),
			GuidanceText: rule.GuidanceText,
		}, ec)
		missing := mergeFacts(adv.MissingFacts, envMissing)
		for _, slot := range slots {
			v := PreviewOccurrenceView{
				RuleKey: rule.RuleKey, Kind: string(rule.Kind),
				ActivityKind: activityKindText(rule.ActivityKind),
				Key:          slot.Key, DueDate: slot.DueDate.String(),
				ReasonText:    adv.ReasonText,
				MissingFacts:  missing,
				EvidenceNotes: adv.EvidenceNotes,
				Weather:       snapPtr,
			}
			if slot.WindowEnd != nil {
				v.WindowEnd = slot.WindowEnd.String()
			}
			out = append(out, v)
		}
	}
	return out, nil
}

// ---- resolution flows ------------------------------------------------------

type CompleteOccurrenceInput struct {
	OccurrenceID     string
	ExpectedRevision int32
	OccurredOn       *string // backdated local date in plan tz; default today
	Note             *string
	Quantity         *string
	Unit             *string
	Product          *string
}

// CompleteOccurrence resolves one DUE occurrence: the linked activity (for
// ACTION occurrences), the occurrence state, the completion-relative rule
// cursor, and the regenerated next check all commit atomically.
func (s *Service) CompleteOccurrence(ctx context.Context, a Actor, in CompleteOccurrenceInput, cmid *string) (dbgen.CareOccurrence, []Problem, error) {
	var zero dbgen.CareOccurrence
	occID, ok := parseID(in.OccurrenceID)
	if !ok {
		return zero, []Problem{problem(CodeValidation, "input.occurrenceId", "occurrenceId must be a UUID")}, nil
	}
	return mutate(s, ctx, a, "completeOccurrence", cmid, in,
		func(ctx context.Context, q *dbgen.Queries) (dbgen.CareOccurrence, []Problem) {
			occ, problems := lockOccurrence(ctx, q, a, occID, in.ExpectedRevision)
			if problems != nil {
				return zero, problems
			}
			if occ.Status != dbgen.OccurrenceStatusDUE {
				return zero, []Problem{problemWithRevision(
					problem(CodeConflict, "input.occurrenceId",
						fmt.Sprintf("occurrence already resolved as %s", occ.Status)), occ.Revision)}
			}
			plan, tz, problems := lockPlanForOccurrence(ctx, q, occ)
			if problems != nil {
				return zero, problems
			}
			today := todayIn(tz)
			occurredOn := today
			if in.OccurredOn != nil {
				d, err := engine.ParseDate(*in.OccurredOn)
				if err != nil {
					return zero, []Problem{problem(CodeValidation, "input.occurredOn", "occurredOn must be YYYY-MM-DD")}
				}
				if occurredOnIsFuture(d, today) {
					return zero, []Problem{problem(CodeValidation, "input.occurredOn", "occurredOn cannot be in the future")}
				}
				occurredOn = d
			}
			eff, problems := effectiveRule(ctx, q, plan, occ.RuleKey)
			if problems != nil {
				return zero, problems
			}

			var actID pgtype.UUID
			// The linked activity rides this same transaction: the
			// contract demands completion+activity atomicity (03: completing
			// a check "creates one activity and one next occurrence"), and
			// the schema requires a completion link. CHECK completions
			// link an OBSERVE activity; their timeline projection is the
			// CARE entry, not an ActivityEntry (care.graphql union note).
			actKind := activityKindText(occ.ActivityKind)
			if occ.Kind == dbgen.RuleKindCHECK {
				actKind = "OBSERVE"
			}
			num, unit, p := validateCompletionQuantity(actKind, in.Quantity, in.Unit)
			if p != nil {
				return zero, []Problem{*p}
			}
			act, err := q.InsertActivity(ctx, dbgen.InsertActivityParams{
				ID: s.uuid.New(), WorkspaceID: a.WorkspaceID, PlantID: plan.PlantID,
				Kind:       dbgen.ActivityKind(actKind),
				OccurredAt: localNoon(occurredOn, tz), Timezone: plan.Timezone,
				Note:     pgtype.Text{String: textPtr(in.Note), Valid: in.Note != nil},
				Quantity: num, Unit: nullUnit(unit), Product: pgtype.Text{String: textPtr(in.Product), Valid: in.Product != nil},
			})
			if err != nil {
				return zero, []Problem{dbProblem("input", err)}
			}
			actID = act.ID
			if occ.Kind != dbgen.RuleKindCHECK {
				// ACTION completions project an ActivityEntry exactly as
				// journal.Service would for a direct activity write.
				if err := journal.ProjectActivity(ctx, q, s.uuid.New(), act); err != nil {
					return zero, []Problem{dbProblem("input", err)}
				}
			}

			resolved, problems := resolveOccurrence(ctx, q, occ, dbgen.OccurrenceStatusCOMPLETED, actID)
			if problems != nil {
				return zero, problems
			}
			if occ.Kind == dbgen.RuleKindCHECK {
				// The CHECK's timeline record is the CARE entry (its linked
				// OBSERVE activity stays behind the completion link, never
				// projected as an ActivityEntry). Upsert semantics revive a
				// tombstoned entry on re-completion.
				if err := s.projectCheckCompletion(ctx, q, a.WorkspaceID, plan.PlantID, resolved, occurredOn, tz); err != nil {
					return zero, []Problem{dbProblem("input", err)}
				}
			}
			if problems := s.advanceCursorAndRegenerate(ctx, q, plan, eff, occ,
				func(c engine.Cursor) engine.Cursor {
					return c.Complete(occ.Ordinal.Int64, occurredOn, eff.rec.Days)
				}); problems != nil {
				return zero, problems
			}
			if err := s.audit(ctx, q, a, "completeOccurrence", "care_occurrence", resolved.ID, resolved.Revision, occ); err != nil {
				return zero, []Problem{dbProblem("input", err)}
			}
			return resolved, nil
		})
}

type SkipOccurrenceInput struct {
	OccurrenceID     string
	ExpectedRevision int32
}

// SkipOccurrence resolves one DUE occurrence with no activity. Cadence
// anchors on the ORIGINAL scheduled due date + N — never the click date
// and never a snoozed date (03, encoded verbatim in the T22 cursor tests).
func (s *Service) SkipOccurrence(ctx context.Context, a Actor, in SkipOccurrenceInput, cmid *string) (dbgen.CareOccurrence, []Problem, error) {
	var zero dbgen.CareOccurrence
	occID, ok := parseID(in.OccurrenceID)
	if !ok {
		return zero, []Problem{problem(CodeValidation, "input.occurrenceId", "occurrenceId must be a UUID")}, nil
	}
	return mutate(s, ctx, a, "skipOccurrence", cmid, in,
		func(ctx context.Context, q *dbgen.Queries) (dbgen.CareOccurrence, []Problem) {
			occ, problems := lockOccurrence(ctx, q, a, occID, in.ExpectedRevision)
			if problems != nil {
				return zero, problems
			}
			if occ.Status != dbgen.OccurrenceStatusDUE {
				return zero, []Problem{problemWithRevision(
					problem(CodeConflict, "input.occurrenceId",
						fmt.Sprintf("occurrence already resolved as %s", occ.Status)), occ.Revision)}
			}
			plan, _, problems := lockPlanForOccurrence(ctx, q, occ)
			if problems != nil {
				return zero, problems
			}
			eff, problems := effectiveRule(ctx, q, plan, occ.RuleKey)
			if problems != nil {
				return zero, problems
			}
			resolved, problems := resolveOccurrence(ctx, q, occ, dbgen.OccurrenceStatusSKIPPED, pgtype.UUID{})
			if problems != nil {
				return zero, problems
			}
			originalDue := pgToEng(occ.OriginalDueDate)
			if problems := s.advanceCursorAndRegenerate(ctx, q, plan, eff, occ,
				func(c engine.Cursor) engine.Cursor {
					return c.Skip(occ.Ordinal.Int64, originalDue, eff.rec.Days)
				}); problems != nil {
				return zero, problems
			}
			if err := s.audit(ctx, q, a, "skipOccurrence", "care_occurrence", resolved.ID, resolved.Revision, occ); err != nil {
				return zero, []Problem{dbProblem("input", err)}
			}
			return resolved, nil
		})
}

type SnoozeOccurrenceInput struct {
	OccurrenceID     string
	ExpectedRevision int32
	Until            string // YYYY-MM-DD local in plan tz
}

// SnoozeOccurrence moves the effective due date (a field on the same
// occurrence; the logical key and the cadence cursor never change).
func (s *Service) SnoozeOccurrence(ctx context.Context, a Actor, in SnoozeOccurrenceInput, cmid *string) (dbgen.CareOccurrence, []Problem, error) {
	var zero dbgen.CareOccurrence
	occID, ok := parseID(in.OccurrenceID)
	if !ok {
		return zero, []Problem{problem(CodeValidation, "input.occurrenceId", "occurrenceId must be a UUID")}, nil
	}
	return mutate(s, ctx, a, "snoozeOccurrence", cmid, in,
		func(ctx context.Context, q *dbgen.Queries) (dbgen.CareOccurrence, []Problem) {
			occ, problems := lockOccurrence(ctx, q, a, occID, in.ExpectedRevision)
			if problems != nil {
				return zero, problems
			}
			if occ.Status != dbgen.OccurrenceStatusDUE {
				return zero, []Problem{problemWithRevision(
					problem(CodeConflict, "input.occurrenceId",
						fmt.Sprintf("occurrence already resolved as %s", occ.Status)), occ.Revision)}
			}
			until, err := engine.ParseDate(in.Until)
			if err != nil {
				return zero, []Problem{problem(CodeValidation, "input.until", "until must be YYYY-MM-DD")}
			}
			if until.Before(pgToEng(occ.CurrentDueDate)) {
				return zero, []Problem{problem(CodeValidation, "input.until",
					"until must not precede the current due date")}
			}
			row, err := q.SnoozeOccurrenceRow(ctx, dbgen.SnoozeOccurrenceRowParams{
				ID: occ.ID, WorkspaceID: a.WorkspaceID, ExpectedRevision: in.ExpectedRevision,
				UntilDate: engToPg(until),
			})
			if err != nil {
				if isNoRows(err) {
					return zero, []Problem{problemWithRevision(
						problem(CodeConflict, "input.expectedRevision", "revision conflict"), occ.Revision)}
				}
				return zero, []Problem{dbProblem("input", err)}
			}
			if err := s.audit(ctx, q, a, "snoozeOccurrence", "care_occurrence", row.ID, row.Revision, occ); err != nil {
				return zero, []Problem{dbProblem("input", err)}
			}
			return row, nil
		})
}

type CorrectCompletionInput struct {
	OccurrenceID     string
	ExpectedRevision int32
	// Exactly one of Undo or field edits (contract: care.graphql).
	Undo       bool
	OccurredOn *string
	Note       *string
	Quantity   *string
	Unit       *string
	Product    *string
}

// CorrectCompletion is the only path to edit or undo a completion
// (general activity commands reject linked activities). Undo voids the
// linked activity and either reopens the occurrence as one overdue check
// (no later decision) or marks it COMPLETION_RETRACTED (newer history is
// authoritative). occurredAt edits recalculate completion-relative next
// work only when this completion is the latest decision; note/quantity
// edits leave cadence untouched.
func (s *Service) CorrectCompletion(ctx context.Context, a Actor, in CorrectCompletionInput, cmid *string) (dbgen.CareOccurrence, []Problem, error) {
	var zero dbgen.CareOccurrence
	occID, ok := parseID(in.OccurrenceID)
	if !ok {
		return zero, []Problem{problem(CodeValidation, "input.occurrenceId", "occurrenceId must be a UUID")}, nil
	}
	hasEdits := in.OccurredOn != nil || in.Note != nil || in.Quantity != nil || in.Unit != nil || in.Product != nil
	if in.Undo == hasEdits {
		return zero, []Problem{problem(CodeValidation, "input.undo",
			"exactly one of undo or field edits must be provided")}, nil
	}
	return mutate(s, ctx, a, "correctOccurrenceCompletion", cmid, in,
		func(ctx context.Context, q *dbgen.Queries) (dbgen.CareOccurrence, []Problem) {
			occ, problems := lockOccurrence(ctx, q, a, occID, in.ExpectedRevision)
			if problems != nil {
				return zero, problems
			}
			if occ.Status != dbgen.OccurrenceStatusCOMPLETED {
				return zero, []Problem{problemWithRevision(
					problem(CodeConflict, "input.occurrenceId",
						fmt.Sprintf("occurrence is %s, not a completion", occ.Status)), occ.Revision)}
			}
			plan, tz, problems := lockPlanForOccurrence(ctx, q, occ)
			if problems != nil {
				return zero, problems
			}
			eff, problems := effectiveRule(ctx, q, plan, occ.RuleKey)
			if problems != nil {
				return zero, problems
			}
			resolved, err := q.ListResolvedOccurrencesByRule(ctx, dbgen.ListResolvedOccurrencesByRuleParams{
				PlanID: occ.PlanID, RuleKey: occ.RuleKey})
			if err != nil {
				return zero, []Problem{dbProblem("input", err)}
			}
			hasLater := false
			for _, r := range resolved {
				if r.Ordinal.Valid && r.Ordinal.Int64 > occ.Ordinal.Int64 {
					hasLater = true
					break
				}
			}

			if in.Undo {
				// Void the linked activity (the event stays attributable).
				if occ.CompletedActivityID.Valid {
					act, err := q.GetActivity(ctx, dbgen.GetActivityParams{ID: occ.CompletedActivityID, WorkspaceID: a.WorkspaceID})
					if err != nil && !isNoRows(err) {
						return zero, []Problem{dbProblem("input", err)}
					}
					if err == nil && !act.VoidedAt.Valid {
						if _, err := q.VoidActivity(ctx, dbgen.VoidActivityParams{
							ID: act.ID, WorkspaceID: a.WorkspaceID, Revision: act.Revision}); err != nil && !isNoRows(err) {
							return zero, []Problem{dbProblem("input", err)}
						}
					}
					// Its timeline entry leaves with it (the CARE tombstone
					// below only covers logged checks).
					if err := journal.TombstoneActivity(ctx, q, a.WorkspaceID, occ.CompletedActivityID); err != nil {
						return zero, []Problem{dbProblem("input", err)}
					}
				}
				// The logged check leaves the timeline too (no-op for
				// ACTION completions, whose entry is the voided activity's).
				if _, err := q.TombstoneJournalEntry(ctx, dbgen.TombstoneJournalEntryParams{
					WorkspaceID: a.WorkspaceID, EntryType: dbgen.JournalEntryTypeCARE, SourceID: occ.ID}); err != nil {
					return zero, []Problem{dbProblem("input", err)}
				}
				var row dbgen.CareOccurrence
				if hasLater {
					row, err = q.RetractOccurrenceRow(ctx, dbgen.RetractOccurrenceRowParams{
						ID: occ.ID, WorkspaceID: a.WorkspaceID, ExpectedRevision: in.ExpectedRevision})
					if err == nil {
						// Newer decisions are authoritative: cursor untouched.
						if err := s.audit(ctx, q, a, "correctOccurrenceCompletion", "care_occurrence", row.ID, row.Revision, occ); err != nil {
							return zero, []Problem{dbProblem("input", err)}
						}
						return row, nil
					}
				} else {
					row, err = q.ReopenOccurrenceRow(ctx, dbgen.ReopenOccurrenceRowParams{
						ID: occ.ID, WorkspaceID: a.WorkspaceID, ExpectedRevision: in.ExpectedRevision})
					if err == nil {
						if problems := s.replayCursorAndRegenerate(ctx, q, plan, eff, resolved, occ.ID, tz); problems != nil {
							return zero, problems
						}
						if err := s.audit(ctx, q, a, "correctOccurrenceCompletion", "care_occurrence", row.ID, row.Revision, occ); err != nil {
							return zero, []Problem{dbProblem("input", err)}
						}
						return row, nil
					}
				}
				if isNoRows(err) {
					return zero, []Problem{problemWithRevision(
						problem(CodeConflict, "input.expectedRevision", "revision conflict"), occ.Revision)}
				}
				return zero, []Problem{dbProblem("input", err)}
			}

			// Field edits ride the linked activity.
			if !occ.CompletedActivityID.Valid {
				return zero, []Problem{problem(CodeConflict, "input.occurrenceId", "completion has no linked activity")}
			}
			act, err := q.GetActivity(ctx, dbgen.GetActivityParams{ID: occ.CompletedActivityID, WorkspaceID: a.WorkspaceID})
			if err != nil {
				if isNoRows(err) {
					return zero, []Problem{problem(CodeNotFound, "input.occurrenceId", "linked activity not found")}
				}
				return zero, []Problem{dbProblem("input", err)}
			}
			if act.VoidedAt.Valid {
				return zero, []Problem{problem(CodeConflict, "input.occurrenceId",
					"linked activity is voided; undo the completion instead")}
			}
			newOccurredAt := act.OccurredAt
			occurredOnChanged := false
			var newDate engine.Date
			if in.OccurredOn != nil {
				d, err := engine.ParseDate(*in.OccurredOn)
				if err != nil {
					return zero, []Problem{problem(CodeValidation, "input.occurredOn", "occurredOn must be YYYY-MM-DD")}
				}
				if occurredOnIsFuture(d, todayIn(tz)) {
					return zero, []Problem{problem(CodeValidation, "input.occurredOn", "occurredOn cannot be in the future")}
				}
				newOccurredAt = localNoon(d, tz)
				newDate = d
				occurredOnChanged = true
			}
			num, unit, p := editQuantity(act, in.Quantity, in.Unit)
			if p != nil {
				return zero, []Problem{*p}
			}
			edited, err := q.UpdateActivity(ctx, dbgen.UpdateActivityParams{
				ID: act.ID, WorkspaceID: a.WorkspaceID, ExpectedRevision: act.Revision,
				OccurredAt: newOccurredAt,
				Timezone:   pgtype.Text{}, // unchanged
				Note:       pgtype.Text{String: textPtr(in.Note), Valid: in.Note != nil},
				Quantity:   num, Unit: nullUnit(unit),
				Product: pgtype.Text{String: textPtr(in.Product), Valid: in.Product != nil},
			})
			if err != nil {
				if isNoRows(err) {
					return zero, []Problem{problem(CodeConflict, "input.occurrenceId", "activity revision conflict")}
				}
				return zero, []Problem{dbProblem("input", err)}
			}
			// The timeline entry follows the edited event. CHECK
			// completions refresh the CARE projection (their OBSERVE link
			// has no ActivityEntry); ACTION completions re-project the
			// ActivityEntry in place via the shared conflict target.
			if occ.Kind == dbgen.RuleKindCHECK {
				on := engine.DateFrom(act.OccurredAt.Time.In(tz))
				if occurredOnChanged {
					on = newDate
				}
				if err := s.projectCheckCompletion(ctx, q, a.WorkspaceID, plan.PlantID, occ, on, tz); err != nil {
					return zero, []Problem{dbProblem("input", err)}
				}
			} else if err := journal.ProjectActivity(ctx, q, s.uuid.New(), edited); err != nil {
				return zero, []Problem{dbProblem("input", err)}
			}
			// occurredAt recalculates completion-relative next work only
			// when this completion is the latest decision.
			if occurredOnChanged && eff.isCompletionRelative() && !hasLater {
				cursor, problems := s.loadCursor(ctx, q, plan, occ.RuleKey, eff)
				if problems != nil {
					return zero, problems
				}
				cursor = cursor.CorrectLatestCompletion(newDate, eff.rec.Days)
				if problems := s.storeCursorAndRegenerate(ctx, q, plan, eff, cursor); problems != nil {
					return zero, problems
				}
			}
			updated, err := q.GetCareOccurrence(ctx, occ.ID)
			if err != nil {
				return zero, []Problem{dbProblem("input", err)}
			}
			if err := s.audit(ctx, q, a, "correctOccurrenceCompletion", "care_occurrence", updated.ID, updated.Revision, occ); err != nil {
				return zero, []Problem{dbProblem("input", err)}
			}
			return updated, nil
		})
}

// ---- resolution helpers ----------------------------------------------------

func lockOccurrence(ctx context.Context, q *dbgen.Queries, a Actor, occID pgtype.UUID, expectedRevision int32) (dbgen.CareOccurrence, []Problem) {
	occ, err := q.LockCareOccurrence(ctx, dbgen.LockCareOccurrenceParams{ID: occID, WorkspaceID: a.WorkspaceID})
	if err != nil {
		if isNoRows(err) {
			return dbgen.CareOccurrence{}, []Problem{problem(CodeNotFound, "input.occurrenceId", "occurrence not found")}
		}
		return dbgen.CareOccurrence{}, []Problem{dbProblem("input", err)}
	}
	// Optimistic concurrency per the contract inputs: a stale client edit
	// loses against the locked row, whatever the resolution path is.
	if occ.Revision != expectedRevision {
		return dbgen.CareOccurrence{}, []Problem{problemWithRevision(
			problem(CodeConflict, "input.expectedRevision", "revision conflict"), occ.Revision)}
	}
	return occ, nil
}

func lockPlanForOccurrence(ctx context.Context, q *dbgen.Queries, occ dbgen.CareOccurrence) (dbgen.CarePlan, *time.Location, []Problem) {
	plan, err := q.GetCarePlanForUpdate(ctx, occ.PlanID)
	if err != nil {
		if isNoRows(err) {
			return dbgen.CarePlan{}, nil, []Problem{problem(CodeNotFound, "input.occurrenceId", "care plan not found")}
		}
		return dbgen.CarePlan{}, nil, []Problem{dbProblem("input", err)}
	}
	tz, err := time.LoadLocation(occ.PlanTimezone)
	if err != nil {
		return dbgen.CarePlan{}, nil, []Problem{problem(CodeConflict, "input.occurrenceId", "plan timezone is invalid")}
	}
	return plan, tz, nil
}

func resolveOccurrence(ctx context.Context, q *dbgen.Queries, occ dbgen.CareOccurrence,
	status dbgen.OccurrenceStatus, actID pgtype.UUID) (dbgen.CareOccurrence, []Problem) {
	row, err := q.ResolveOccurrence(ctx, dbgen.ResolveOccurrenceParams{
		ID: occ.ID, WorkspaceID: occ.WorkspaceID, ExpectedRevision: occ.Revision,
		Status: status, CompletedActivityID: actID,
	})
	if err != nil {
		if isNoRows(err) {
			return dbgen.CareOccurrence{}, []Problem{problemWithRevision(
				problem(CodeConflict, "input.expectedRevision", "revision conflict"), occ.Revision)}
		}
		return dbgen.CareOccurrence{}, []Problem{dbProblem("input", err)}
	}
	return row, nil
}

// effectiveRule is the plan's live view of a rule: pinned-version rule +
// override (enabled flag and recurrence).
type effectiveRuleView struct {
	rule dbgen.CareRule
	rec  *engine.Recurrence
	has  bool
}

func (e effectiveRuleView) isCompletionRelative() bool {
	return e.has && e.rec != nil && e.rec.Form == engine.FormEveryN && e.rec.Anchor == engine.AnchorLastCompletion
}

func effectiveRule(ctx context.Context, q *dbgen.Queries, plan dbgen.CarePlan, ruleKey string) (effectiveRuleView, []Problem) {
	view := effectiveRuleView{}
	if !plan.ProfileVersionID.Valid {
		return view, nil
	}
	rules, err := q.ListCareRules(ctx, plan.ProfileVersionID)
	if err != nil {
		return view, []Problem{dbProblem("input", err)}
	}
	for _, r := range rules {
		if r.RuleKey != ruleKey {
			continue
		}
		view.rule = r
		view.has = true
		raw := r.Recurrence
		ovs, err := q.ListCarePlanOverrides(ctx, plan.ID)
		if err != nil {
			return view, []Problem{dbProblem("input", err)}
		}
		for _, o := range ovs {
			if o.RuleKey == ruleKey && o.RecurrenceOverride != nil {
				raw = o.RecurrenceOverride
			}
		}
		rec, err := engine.ParseRecurrence(raw)
		if err != nil {
			return view, []Problem{problem(CodeConflict, "input.occurrenceId", "rule recurrence is not parseable")}
		}
		view.rec = rec
		return view, nil
	}
	return view, nil
}

// advanceCursorAndRegenerate updates the completion-relative cursor after
// a decision and materializes the next check; calendar-cadence rules need
// nothing (their future slots already exist and completion never shifts
// them).
func (s *Service) advanceCursorAndRegenerate(ctx context.Context, q *dbgen.Queries, plan dbgen.CarePlan,
	eff effectiveRuleView, occ dbgen.CareOccurrence, advance func(engine.Cursor) engine.Cursor) []Problem {
	if !eff.isCompletionRelative() {
		return nil
	}
	cursor, problems := s.loadCursor(ctx, q, plan, occ.RuleKey, eff)
	if problems != nil {
		return problems
	}
	cursor = advance(cursor)
	return s.storeCursorAndRegenerate(ctx, q, plan, eff, cursor)
}

func (s *Service) loadCursor(ctx context.Context, q *dbgen.Queries, plan dbgen.CarePlan,
	ruleKey string, eff effectiveRuleView) (engine.Cursor, []Problem) {
	tz, err := time.LoadLocation(plan.Timezone)
	if err != nil {
		return engine.Cursor{}, []Problem{problem(CodeConflict, "input.occurrenceId", "plan timezone is invalid")}
	}
	planStart := engine.DateFrom(plan.CreatedAt.Time.In(tz))
	row, err := q.GetCareRuleCursor(ctx, dbgen.GetCareRuleCursorParams{PlanID: plan.ID, RuleKey: ruleKey})
	switch {
	case err == nil:
		c := engine.Cursor{LastOrdinal: row.LastResolvedOrdinal.Int64,
			AnchorDate: pgToEng(row.AnchorDate), NextDueDate: pgToEng(row.NextDueDate)}
		if row.Decision.Valid {
			c.LastDecision = engine.Decision(string(row.Decision.OccurrenceStatus))
		}
		return c, nil
	case isNoRows(err):
		return engine.Cursor{AnchorDate: planStart, NextDueDate: planStart}, nil
	default:
		return engine.Cursor{}, []Problem{dbProblem("input", err)}
	}
}

// storeCursorAndRegenerate persists the cursor, cancels stale open slots
// for the rule, and materializes the next actionable check.
func (s *Service) storeCursorAndRegenerate(ctx context.Context, q *dbgen.Queries, plan dbgen.CarePlan,
	eff effectiveRuleView, cursor engine.Cursor) []Problem {
	if _, err := q.UpsertCareRuleCursor(ctx, dbgen.UpsertCareRuleCursorParams{
		PlanID: plan.ID, WorkspaceID: plan.WorkspaceID, RuleKey: eff.rule.RuleKey,
		LastResolvedOrdinal: pgtype.Int8{Int64: cursor.LastOrdinal, Valid: true},
		Decision:            dbgen.NullOccurrenceStatus{OccurrenceStatus: dbgen.OccurrenceStatus(cursor.LastDecision), Valid: cursor.LastDecision != ""},
		AnchorDate:          engToPg(cursor.AnchorDate), NextDueDate: engToPg(cursor.NextDueDate),
	}); err != nil {
		return []Problem{dbProblem("input", err)}
	}
	tz, err := time.LoadLocation(plan.Timezone)
	if err != nil {
		return []Problem{problem(CodeConflict, "input.occurrenceId", "plan timezone is invalid")}
	}
	planStart := engine.DateFrom(plan.CreatedAt.Time.In(tz))
	today := todayIn(tz)
	if _, err := q.CancelOpenOccurrencesAfter(ctx, dbgen.CancelOpenOccurrencesAfterParams{
		PlanID: plan.ID, RuleKey: eff.rule.RuleKey, CutoffDate: engToPg(cursor.NextDueDate),
	}); err != nil {
		return []Problem{dbProblem("input", err)}
	}
	from := today
	if cursor.NextDueDate.Before(today) {
		from = cursor.NextDueDate
	}
	slots, err := eff.rec.Generate(eff.rule.RuleKey, from, planStart, &cursor)
	if err != nil {
		return []Problem{dbProblem("input", err)}
	}
	adv := engine.Advise(engine.Rule{
		RuleKey: eff.rule.RuleKey, Kind: string(eff.rule.Kind),
		ActivityKind:  activityKindText(eff.rule.ActivityKind),
		RequiredFacts: eff.rule.RequiredFacts, WeatherSensitivity: string(eff.rule.WeatherSensitivity),
		GuidanceText: eff.rule.GuidanceText,
	}, engine.Context{Today: today, Now: time.Now().UTC()})
	for _, slot := range slots {
		if _, err := q.InsertCareOccurrenceUpsert(ctx, dbgen.InsertCareOccurrenceUpsertParams{
			ID: s.uuid.New(), WorkspaceID: plan.WorkspaceID, PlanID: plan.ID,
			RuleKey: eff.rule.RuleKey, Kind: eff.rule.Kind, ActivityKind: eff.rule.ActivityKind,
			OccurrenceKey:   slot.Key,
			OriginalDueDate: engToPg(slot.DueDate), CurrentDueDate: engToPg(slot.DueDate),
			PlanTimezone: plan.Timezone,
			Ordinal:      pgtype.Int8{Int64: slot.Ordinal, Valid: true},
			ReasonText:   pgtype.Text{String: adv.ReasonText, Valid: true},
			MissingFacts: nonNil(adv.MissingFacts), EvidenceNotes: nonNil(adv.EvidenceNotes),
		}); err != nil && !isNoRows(err) {
			return []Problem{dbProblem("input", err)}
		}
	}
	return nil
}

// replayCursorAndRegenerate rebuilds a completion-relative cursor by
// replaying the surviving resolved decisions in logical order — the
// authoritative state after an undo.
func (s *Service) replayCursorAndRegenerate(ctx context.Context, q *dbgen.Queries, plan dbgen.CarePlan,
	eff effectiveRuleView, resolved []dbgen.CareOccurrence, undoneID pgtype.UUID, tz *time.Location) []Problem {
	if !eff.isCompletionRelative() {
		return nil
	}
	planStart := engine.DateFrom(plan.CreatedAt.Time.In(tz))
	c := engine.Cursor{AnchorDate: planStart, NextDueDate: planStart}
	for _, r := range resolved {
		if r.ID == undoneID {
			continue
		}
		switch r.Status {
		case dbgen.OccurrenceStatusCOMPLETED:
			act, err := q.GetActivity(ctx, dbgen.GetActivityParams{ID: r.CompletedActivityID, WorkspaceID: plan.WorkspaceID})
			if err != nil {
				if isNoRows(err) {
					continue
				}
				return []Problem{dbProblem("input", err)}
			}
			c = c.Complete(r.Ordinal.Int64, engine.DateFrom(act.OccurredAt.Time.In(tz)), eff.rec.Days)
		case dbgen.OccurrenceStatusSKIPPED:
			c = c.Skip(r.Ordinal.Int64, pgToEng(r.OriginalDueDate), eff.rec.Days)
		}
	}
	return s.storeCursorAndRegenerate(ctx, q, plan, eff, c)
}

// ---- shared validation helpers ---------------------------------------------

func loadZoneProblem(name, field string) (*time.Location, *Problem) {
	loc, err := time.LoadLocation(name)
	if err != nil || name == "" {
		p := problem(CodeValidation, field, "timezone must be an IANA zone, e.g. America/Los_Angeles")
		return nil, &p
	}
	return loc, nil
}

func validateOverrides(ctx context.Context, q *dbgen.Queries, pvID pgtype.UUID, ovs []PlanOverrideInput) []Problem {
	if len(ovs) == 0 {
		return nil
	}
	if !pvID.Valid {
		return []Problem{problem(CodeValidation, "input.overrides",
			"rule overrides require a pinned profile version")}
	}
	rules, err := q.ListCareRules(ctx, pvID)
	if err != nil {
		return []Problem{dbProblem("input", err)}
	}
	known := map[string]bool{}
	for _, r := range rules {
		known[r.RuleKey] = true
	}
	for _, o := range ovs {
		if !known[o.RuleKey] {
			return []Problem{problem(CodeValidation, "input.overrides.ruleKey",
				fmt.Sprintf("unknown rule key %q for the pinned profile version", o.RuleKey))}
		}
		if o.RecurrenceOverride != nil {
			if _, err := engine.ParseRecurrence(*o.RecurrenceOverride); err != nil {
				return []Problem{problem(CodeValidation, "input.overrides.recurrenceOverride",
					fmt.Sprintf("recurrenceOverride: %v", err))}
			}
		}
	}
	return nil
}

func insertOverrides(ctx context.Context, q *dbgen.Queries, s *Service, plan dbgen.CarePlan, ovs []PlanOverrideInput) []Problem {
	for _, o := range ovs {
		enabled := true
		if o.Enabled != nil {
			enabled = *o.Enabled
		}
		var raw []byte
		if o.RecurrenceOverride != nil {
			raw = *o.RecurrenceOverride
		}
		if err := q.InsertCarePlanOverride(ctx, dbgen.InsertCarePlanOverrideParams{
			ID: s.uuid.New(), PlanID: plan.ID, WorkspaceID: plan.WorkspaceID,
			RuleKey: o.RuleKey, Enabled: enabled, RecurrenceOverride: raw,
		}); err != nil {
			return []Problem{dbProblem("input", err)}
		}
	}
	return nil
}

// completionQuantityRules mirrors the journal kind/unit pairing so linked
// activities are always valid journal rows.
var completionQuantityRules = map[string]map[string]bool{
	"WATER":     {"ML": true, "L": true},
	"FERTILIZE": {"G": true, "KG": true, "ML": true, "L": true},
}

func validateCompletionQuantity(kind string, quantity, unit *string) (*pgtype.Numeric, *string, *Problem) {
	allowed, carries := completionQuantityRules[kind]
	if quantity == nil && unit == nil {
		if carries {
			p := problem(CodeValidation, "input.quantity", fmt.Sprintf("%s requires quantity and unit", kind))
			return nil, nil, &p
		}
		return nil, nil, nil
	}
	if !carries {
		p := problem(CodeValidation, "input.unit", fmt.Sprintf("%s carries no quantity or unit", kind))
		return nil, nil, &p
	}
	if quantity == nil || unit == nil {
		p := problem(CodeValidation, "input.quantity", "quantity and unit travel together")
		return nil, nil, &p
	}
	if !allowed[*unit] {
		keys := make([]string, 0, len(allowed))
		for k := range allowed {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		p := problem(CodeValidation, "input.unit",
			fmt.Sprintf("%s accepts unit %s, got %s", kind, strings.Join(keys, " or "), *unit))
		return nil, nil, &p
	}
	var n pgtype.Numeric
	if err := n.Scan(*quantity); err != nil || !n.Valid {
		p := problem(CodeValidation, "input.quantity", "quantity must be a decimal number")
		return nil, nil, &p
	}
	if f, err := n.Float64Value(); err != nil || !f.Valid || f.Float64 <= 0 {
		p := problem(CodeValidation, "input.quantity", "quantity must be greater than zero")
		return nil, nil, &p
	}
	return &n, unit, nil
}

// editQuantity applies COALESCE-style edits: nil keeps the current value.
// The effective (stored ⊕ edit) pair must satisfy the kind/unit rules.
func editQuantity(act dbgen.Activity, quantity, unit *string) (*pgtype.Numeric, *string, *Problem) {
	if quantity == nil && unit == nil {
		return nil, nil, nil
	}
	kind := string(act.Kind)
	allowed, carries := completionQuantityRules[kind]
	if !carries {
		p := problem(CodeValidation, "input.unit", fmt.Sprintf("%s carries no quantity or unit", kind))
		return nil, nil, &p
	}
	effUnit := unit
	if effUnit == nil && act.Unit.Valid {
		u := string(act.Unit.MeasurementUnit)
		effUnit = &u
	}
	if effUnit == nil {
		p := problem(CodeValidation, "input.unit", "quantity and unit travel together")
		return nil, nil, &p
	}
	if !allowed[*effUnit] {
		p := problem(CodeValidation, "input.unit",
			fmt.Sprintf("%s does not accept unit %s", kind, *effUnit))
		return nil, nil, &p
	}
	if quantity == nil {
		// keep the stored quantity (validated positive at creation)
		if act.Quantity == nil || !act.Quantity.Valid {
			p := problem(CodeValidation, "input.quantity", "quantity and unit travel together")
			return nil, nil, &p
		}
		return act.Quantity, effUnit, nil
	}
	var n pgtype.Numeric
	if err := n.Scan(*quantity); err != nil || !n.Valid {
		p := problem(CodeValidation, "input.quantity", "quantity must be a decimal number")
		return nil, nil, &p
	}
	if f, err := n.Float64Value(); err != nil || !f.Valid || f.Float64 <= 0 {
		p := problem(CodeValidation, "input.quantity", "quantity must be greater than zero")
		return nil, nil, &p
	}
	return &n, effUnit, nil
}

func nullUnit(u *string) dbgen.NullMeasurementUnit {
	if u == nil {
		return dbgen.NullMeasurementUnit{}
	}
	return dbgen.NullMeasurementUnit{MeasurementUnit: dbgen.MeasurementUnit(*u), Valid: true}
}

func activityKindText(k dbgen.NullActivityKind) string {
	if !k.Valid {
		return ""
	}
	return string(k.ActivityKind)
}

func numericFloat(n *pgtype.Numeric) *float64 {
	if n == nil || !n.Valid {
		return nil
	}
	f, err := n.Float64Value()
	if err != nil || !f.Valid {
		return nil
	}
	return &f.Float64
}

func mergeFacts(a, b []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, x := range append(append([]string{}, a...), b...) {
		if !seen[x] {
			seen[x] = true
			out = append(out, x)
		}
	}
	return out
}

func occurredOnIsFuture(d engine.Date, today engine.Date) bool { return d.After(today) }

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

func isNoRows(err error) bool { return errors.Is(err, pgx.ErrNoRows) }
