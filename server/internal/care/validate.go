// Validation for care inputs (T21): the four frozen recurrence forms, rule
// shape, environment predicates and citation resolution. The canonical
// recurrence jsonb shape is documented in ADR-0002; the DB CHECK
// (valid_recurrence) enforces the numeric ranges, but problems are produced
// here first so they quote exact expected/found values.
package care

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// RecurrenceSpecInput mirrors the frozen input: exactly one member must be set.
type RecurrenceSpecInput struct {
	EveryNDays   *EveryNDaysInput
	WeeklyDays   *WeeklyDaysInput
	AnnualWindow *AnnualWindowInput
	OneTime      *OneTimeInput
}

type EveryNDaysInput struct {
	Days   int32
	Anchor string // PLAN_START | LAST_QUALIFYING_COMPLETION
}

type WeeklyDaysInput struct {
	Weekdays []string // MONDAY..SUNDAY
}

type AnnualWindowInput struct {
	Month      int32
	Day        int32
	WindowDays *int32 // default 1
	EveryYears *int32 // default 1
}

type OneTimeInput struct {
	Date string // YYYY-MM-DD
}

var (
	anchorSet    = map[string]bool{"PLAN_START": true, "LAST_QUALIFYING_COMPLETION": true}
	weekdaySet   = map[string]bool{"MONDAY": true, "TUESDAY": true, "WEDNESDAY": true, "THURSDAY": true, "FRIDAY": true, "SATURDAY": true, "SUNDAY": true}
	factSet      = map[string]bool{"CULTIVAR": true, "SCIENTIFIC_NAME": true, "PLACEMENT": true, "LOCATION_SITUATION": true, "POT_VOLUME": true, "REGION": true, "HEMISPHERE": true}
	situationSet = map[string]bool{"INDOOR": true, "OUTDOOR": true, "COVERED": true}
	hemiSet      = map[string]bool{"NORTHERN": true, "SOUTHERN": true}
	weatherSet   = map[string]bool{"NONE": true, "RAINFALL": true, "FROST": true}
	ruleKeyRe    = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,99}$`)
	dateRe       = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)
)

// CanonicalRecurrence validates a recurrence union input and renders the
// frozen jsonb form; exported for the transport layer's plan overrides.
func CanonicalRecurrence(field string, r RecurrenceSpecInput) (json.RawMessage, []Problem) {
	return canonicalRecurrence(field, r)
}

// canonicalRecurrence validates and renders the ADR-0002 jsonb form.
func canonicalRecurrence(field string, r RecurrenceSpecInput) (json.RawMessage, []Problem) {
	set := 0
	if r.EveryNDays != nil {
		set++
	}
	if r.WeeklyDays != nil {
		set++
	}
	if r.AnnualWindow != nil {
		set++
	}
	if r.OneTime != nil {
		set++
	}
	if set == 0 {
		return nil, []Problem{problem(CodeValidation, field,
			"exactly one recurrence form must be set (everyNDays, weeklyDays, annualWindow, oneTime).")}
	}
	if set > 1 {
		return nil, []Problem{problem(CodeValidation, field,
			"exactly one recurrence form must be set; several were provided.")}
	}
	if e := r.EveryNDays; e != nil {
		if e.Days < 1 || e.Days > 365 {
			return nil, []Problem{problem(CodeValidation, field+".everyNDays.days",
				fmt.Sprintf("expected 1..365, found %d", e.Days))}
		}
		if !anchorSet[e.Anchor] {
			return nil, []Problem{problem(CodeValidation, field+".everyNDays.anchor",
				"expected PLAN_START or LAST_QUALIFYING_COMPLETION, found "+quote(e.Anchor))}
		}
		return mustJSON(map[string]any{
			"form": "EVERY_N_DAYS", "days": e.Days, "anchor": e.Anchor,
		}), nil
	}
	if w := r.WeeklyDays; w != nil {
		if len(w.Weekdays) == 0 || len(w.Weekdays) > 7 {
			return nil, []Problem{problem(CodeValidation, field+".weeklyDays.weekdays",
				fmt.Sprintf("expected 1..7 weekdays, found %d", len(w.Weekdays)))}
		}
		seen := map[string]bool{}
		for _, d := range w.Weekdays {
			if !weekdaySet[d] {
				return nil, []Problem{problem(CodeValidation, field+".weeklyDays.weekdays",
					"expected MONDAY..SUNDAY, found "+quote(d))}
			}
			if seen[d] {
				return nil, []Problem{problem(CodeValidation, field+".weeklyDays.weekdays",
					"duplicate weekday "+quote(d))}
			}
			seen[d] = true
		}
		return mustJSON(map[string]any{
			"form": "WEEKLY_DAYS", "weekdays": w.Weekdays,
		}), nil
	}
	if a := r.AnnualWindow; a != nil {
		if a.Month < 1 || a.Month > 12 {
			return nil, []Problem{problem(CodeValidation, field+".annualWindow.month",
				fmt.Sprintf("expected 1..12, found %d", a.Month))}
		}
		// Day validated against the month (Feb 29 stays valid input: the
		// engine maps it to Feb 28 in non-leap years and records that).
		maxDay := []int32{31, 29, 31, 30, 31, 30, 31, 31, 30, 31, 30, 31}[a.Month-1]
		if a.Day < 1 || a.Day > maxDay {
			return nil, []Problem{problem(CodeValidation, field+".annualWindow.day",
				fmt.Sprintf("expected 1..%d for month %d, found %d", maxDay, a.Month, a.Day))}
		}
		window, every := int32(1), int32(1)
		if a.WindowDays != nil {
			window = *a.WindowDays
		}
		if a.EveryYears != nil {
			every = *a.EveryYears
		}
		if window < 1 || window > 366 {
			return nil, []Problem{problem(CodeValidation, field+".annualWindow.windowDays",
				fmt.Sprintf("expected 1..366, found %d", window))}
		}
		if every < 1 || every > 50 {
			return nil, []Problem{problem(CodeValidation, field+".annualWindow.everyYears",
				fmt.Sprintf("expected 1..50, found %d", every))}
		}
		return mustJSON(map[string]any{
			"form": "ANNUAL_WINDOW", "month": a.Month, "day": a.Day,
			"window_days": window, "every_years": every,
		}), nil
	}
	if o := r.OneTime; o != nil {
		if !dateRe.MatchString(o.Date) {
			return nil, []Problem{problem(CodeValidation, field+".oneTime.date",
				"expected YYYY-MM-DD, found "+quote(o.Date))}
		}
		if _, err := time.Parse("2006-01-02", o.Date); err != nil {
			return nil, []Problem{problem(CodeValidation, field+".oneTime.date",
				"not a real calendar date: "+quote(o.Date))}
		}
		return mustJSON(map[string]any{"form": "ONE_TIME", "date": o.Date}), nil
	}
	return nil, []Problem{problem(CodeValidation, field, "unreachable recurrence branch")}
}

// RuleInput mirrors CareRuleInput from the frozen SDL.
type RuleInput struct {
	RuleKey            string
	Kind               string // CHECK | ACTION
	ActivityKind       *string
	Recurrence         RecurrenceSpecInput
	RequiredFacts      []string
	WeatherSensitivity *string
	GuidanceText       string
	SourceVersionIDs   []string
}

// validatedRule is a rule after validation, ready for insert.
type validatedRule struct {
	RuleKey            string
	Kind               string
	ActivityKind       *string
	Recurrence         json.RawMessage
	RequiredFacts      []string
	WeatherSensitivity string
	GuidanceText       string
	SourceVersionIDs   []string
}

var activityKindSet = map[string]bool{"WATER": true, "FERTILIZE": true, "PRUNE": true, "REPOT": true, "HARVEST": true, "OBSERVE": true, "NOTE": true}

func validateRules(field string, rules []RuleInput) ([]validatedRule, []Problem) {
	if len(rules) == 0 {
		return nil, []Problem{problem(CodeValidation, field, "at least one rule is required.")}
	}
	out := make([]validatedRule, 0, len(rules))
	seenKey := map[string]bool{}
	for i, r := range rules {
		at := fmt.Sprintf("%s[%d]", field, i)
		if !ruleKeyRe.MatchString(r.RuleKey) {
			return nil, []Problem{problem(CodeValidation, at+".ruleKey",
				"expected lowercase letters/digits/underscore/hyphen starting with a letter or digit, found "+quote(r.RuleKey))}
		}
		if seenKey[r.RuleKey] {
			return nil, []Problem{problem(CodeValidation, at+".ruleKey",
				"duplicate ruleKey "+quote(r.RuleKey))}
		}
		seenKey[r.RuleKey] = true
		if r.Kind != "CHECK" && r.Kind != "ACTION" {
			return nil, []Problem{problem(CodeValidation, at+".kind",
				"expected CHECK or ACTION, found "+quote(r.Kind))}
		}
		var act *string
		if r.Kind == "ACTION" {
			if r.ActivityKind == nil || !activityKindSet[*r.ActivityKind] {
				return nil, []Problem{problem(CodeValidation, at+".activityKind",
					"required exactly when kind is ACTION (WATER, FERTILIZE, PRUNE, REPOT, HARVEST, OBSERVE, NOTE).")}
			}
			act = r.ActivityKind
		} else if r.ActivityKind != nil {
			return nil, []Problem{problem(CodeValidation, at+".activityKind",
				"must be null when kind is CHECK; CHECK rules never record an activity.")}
		}
		rec, probs := canonicalRecurrence(at+".recurrence", r.Recurrence)
		if len(probs) > 0 {
			return nil, probs
		}
		for _, f := range r.RequiredFacts {
			if !factSet[f] {
				return nil, []Problem{problem(CodeValidation, at+".requiredFacts",
					"expected CULTIVAR, SCIENTIFIC_NAME, PLACEMENT, LOCATION_SITUATION, POT_VOLUME, REGION or HEMISPHERE, found "+quote(f))}
			}
		}
		weather := "NONE"
		if r.WeatherSensitivity != nil {
			weather = *r.WeatherSensitivity
			if !weatherSet[weather] {
				return nil, []Problem{problem(CodeValidation, at+".weatherSensitivity",
					"expected NONE, RAINFALL or FROST, found "+quote(weather))}
			}
		}
		if charLen(r.GuidanceText) < 1 || charLen(r.GuidanceText) > 4000 {
			return nil, []Problem{problem(CodeValidation, at+".guidanceText",
				"expected 1..4000 characters")}
		}
		out = append(out, validatedRule{
			RuleKey: r.RuleKey, Kind: r.Kind, ActivityKind: act,
			Recurrence: rec, RequiredFacts: r.RequiredFacts,
			WeatherSensitivity: weather, GuidanceText: r.GuidanceText,
			SourceVersionIDs: r.SourceVersionIDs,
		})
	}
	return out, nil
}

// EnvironmentInput mirrors ProfileEnvironmentInput; empty lists mean "any".
type EnvironmentInput struct {
	Situations  []string
	Hemispheres []string
}

// canonicalEnvironment renders the DB jsonb predicate object.
func canonicalEnvironment(field string, e EnvironmentInput) (json.RawMessage, []Problem) {
	for _, s := range e.Situations {
		if !situationSet[s] {
			return nil, []Problem{problem(CodeValidation, field+".situations",
				"expected INDOOR, OUTDOOR or COVERED, found "+quote(s))}
		}
	}
	for _, h := range e.Hemispheres {
		if !hemiSet[h] {
			return nil, []Problem{problem(CodeValidation, field+".hemispheres",
				"expected NORTHERN or SOUTHERN, found "+quote(h))}
		}
	}
	sits, hemis := e.Situations, e.Hemispheres
	if sits == nil {
		sits = []string{}
	}
	if hemis == nil {
		hemis = []string{}
	}
	return mustJSON(map[string]any{"situations": sits, "hemispheres": hemis}), nil
}

func quote(s string) string { return `"` + strings.ReplaceAll(s, `"`, `\"`) + `"` }

func mustJSON(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		panic("care: canonical json encoding: " + err.Error())
	}
	return b
}
