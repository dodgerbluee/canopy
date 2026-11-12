package engine

import (
	"encoding/json"
	"fmt"
	"sort"
	"time"
)

// Form names match the canonical recurrence jsonb (ADR-0002).
type Form string

const (
	FormEveryN  Form = "EVERY_N_DAYS"
	FormWeekly  Form = "WEEKLY_DAYS"
	FormAnnual  Form = "ANNUAL_WINDOW"
	FormOneTime Form = "ONE_TIME"
)

// Anchor values for EVERY_N_DAYS (profile rules explicitly choose anchor
// policy per 03-contracts).
const (
	AnchorPlanStart      = "PLAN_START"
	AnchorLastCompletion = "LAST_QUALIFYING_COMPLETION"
)

// Recurrence is a parsed, validated recurrence spec. Exactly one form.
type Recurrence struct {
	Form Form

	// EVERY_N_DAYS
	Days   int
	Anchor string // PLAN_START | LAST_QUALIFYING_COMPLETION

	// WEEKLY_DAYS
	Weekdays []time.Weekday

	// ANNUAL_WINDOW
	Month      int
	Day        int
	WindowDays int
	EveryYears int

	// ONE_TIME
	When Date
}

// ParseRecurrence decodes the canonical jsonb written by the care domain.
// Anything else is a programming/contract error and fails loudly.
func ParseRecurrence(raw []byte) (*Recurrence, error) {
	var probe struct {
		Form string `json:"form"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		return nil, fmt.Errorf("engine: recurrence json: %w", err)
	}
	switch Form(probe.Form) {
	case FormEveryN:
		var v struct {
			Form   string `json:"form"`
			Days   int    `json:"days"`
			Anchor string `json:"anchor"`
		}
		if err := json.Unmarshal(raw, &v); err != nil {
			return nil, err
		}
		if v.Days < 1 || v.Days > 365 {
			return nil, fmt.Errorf("engine: every_n_days days out of range: %d", v.Days)
		}
		if v.Anchor != AnchorPlanStart && v.Anchor != AnchorLastCompletion {
			return nil, fmt.Errorf("engine: unknown anchor %q", v.Anchor)
		}
		return &Recurrence{Form: FormEveryN, Days: v.Days, Anchor: v.Anchor}, nil
	case FormWeekly:
		var v struct {
			Form     string   `json:"form"`
			Weekdays []string `json:"weekdays"`
		}
		if err := json.Unmarshal(raw, &v); err != nil {
			return nil, err
		}
		if len(v.Weekdays) == 0 || len(v.Weekdays) > 7 {
			return nil, fmt.Errorf("engine: weekly_days needs 1..7 weekdays")
		}
		var out []time.Weekday
		seen := map[time.Weekday]bool{}
		for _, name := range v.Weekdays {
			w, ok := weekdayByName[name]
			if !ok {
				return nil, fmt.Errorf("engine: unknown weekday %q", name)
			}
			if seen[w] {
				return nil, fmt.Errorf("engine: duplicate weekday %q", name)
			}
			seen[w] = true
			out = append(out, w)
		}
		// Canonical chronological order within the Monday-anchored week
		// (Sunday sorts last, matching week-relative chronology), so spec
		// order never affects slot identity and ordinals increase in date
		// order.
		sort.Slice(out, func(i, j int) bool { return (out[i]+6)%7 < (out[j]+6)%7 })
		return &Recurrence{Form: FormWeekly, Weekdays: out}, nil
	case FormAnnual:
		var v struct {
			Form       string `json:"form"`
			Month      int    `json:"month"`
			Day        int    `json:"day"`
			WindowDays int    `json:"window_days"`
			EveryYears int    `json:"every_years"`
		}
		if err := json.Unmarshal(raw, &v); err != nil {
			return nil, err
		}
		if v.Month < 1 || v.Month > 12 || v.Day < 1 || v.Day > 31 {
			return nil, fmt.Errorf("engine: annual_window month/day invalid: %d/%d", v.Month, v.Day)
		}
		// Validate against the real month; Feb 29 stays legal input (the
		// generator maps it to Feb 28 in non-leap years per 03-contracts).
		maxDay := [...]int{31, 29, 31, 30, 31, 30, 31, 31, 30, 31, 30, 31}[v.Month-1]
		if v.Day > maxDay {
			return nil, fmt.Errorf("engine: annual_window day %d invalid for month %d", v.Day, v.Month)
		}
		if v.WindowDays < 1 || v.WindowDays > 366 || v.EveryYears < 1 || v.EveryYears > 50 {
			return nil, fmt.Errorf("engine: annual_window window/every invalid: %d/%d", v.WindowDays, v.EveryYears)
		}
		return &Recurrence{Form: FormAnnual, Month: v.Month, Day: v.Day,
			WindowDays: v.WindowDays, EveryYears: v.EveryYears}, nil
	case FormOneTime:
		var v struct {
			Form string `json:"form"`
			Date string `json:"date"`
		}
		if err := json.Unmarshal(raw, &v); err != nil {
			return nil, err
		}
		d, err := ParseDate(v.Date)
		if err != nil {
			return nil, err
		}
		return &Recurrence{Form: FormOneTime, When: d}, nil
	default:
		return nil, fmt.Errorf("engine: unknown recurrence form %q", probe.Form)
	}
}

var weekdayByName = map[string]time.Weekday{
	"SUNDAY": time.Sunday, "MONDAY": time.Monday, "TUESDAY": time.Tuesday,
	"WEDNESDAY": time.Wednesday, "THURSDAY": time.Thursday, "FRIDAY": time.Friday,
	"SATURDAY": time.Saturday,
}

// Occurrence is one generated due slot for a rule. Key is the stable logical
// key (rule + form + logical ordinal/date): it never changes with snooze,
// skip or completion.
type Occurrence struct {
	DueDate Date
	Ordinal int64
	Key     string
	// Notes are honest reason material, e.g. the Feb-29 mapping.
	Notes []string
	// WindowEnd is set for ANNUAL_WINDOW slots (inclusive).
	WindowEnd *Date
}

// epochWeekMonday anchors WEEKLY_DAYS ordinals so they are stable no matter
// which window generated them.
var epochWeekMonday = Date{1970, 1, 5}

// Generate materializes the slots due in [from, from+horizon] for this
// recurrence. planStart anchors PLAN_START intervals and the every-years
// cadence; cursor is required only for LAST_QUALIFYING_COMPLETION, which
// materializes at most one actionable slot (03: "at most one actionable
// completion-relative occurrence exists").
func (r *Recurrence) Generate(ruleKey string, from Date, planStart Date, cursor *Cursor) ([]Occurrence, error) {
	end := from.AddDays(HorizonDays)
	switch r.Form {
	case FormEveryN:
		if r.Anchor == AnchorLastCompletion {
			if cursor == nil {
				return nil, fmt.Errorf("engine: completion-relative rule needs a cursor")
			}
			nd := cursor.NextDueDate
			if nd.Before(from) || nd.After(end) {
				return nil, nil
			}
			return []Occurrence{{
				DueDate: nd, Ordinal: cursor.LastOrdinal + 1,
				Key: fmt.Sprintf("%s#ENLC:%d", ruleKey, cursor.LastOrdinal+1),
			}}, nil
		}
		if planStart.Year == 0 {
			return nil, fmt.Errorf("engine: plan_start-anchored rule needs a plan start date")
		}
		// First k >= 1 with planStart + k*days >= from (closed form; fuzz
		// can pass millenia-wide spans, a loop would crawl).
		span := planStart.DaysUntil(from)
		k := 1
		if span > 0 {
			k = (span + r.Days - 1) / r.Days
			if k < 1 {
				k = 1
			}
		}
		if planStart.AddDays(k * r.Days).Before(from) {
			k++
		}
		var out []Occurrence
		for ; ; k++ {
			due := planStart.AddDays(k * r.Days)
			if due.After(end) {
				break
			}
			out = append(out, Occurrence{
				DueDate: due, Ordinal: int64(k),
				Key: fmt.Sprintf("%s#ENPS:%d", ruleKey, k),
			})
		}
		return out, nil

	case FormWeekly:
		var out []Occurrence
		for d := from; !d.After(end); d = d.AddDays(1) {
			idx := -1
			for i, w := range r.Weekdays {
				if d.Weekday() == w {
					idx = i
					break
				}
			}
			if idx < 0 {
				continue
			}
			weeks := epochWeekMonday.DaysUntil(d.startOfWeek()) / 7
			ord := int64(weeks)*int64(len(r.Weekdays)) + int64(idx)
			out = append(out, Occurrence{
				DueDate: d, Ordinal: ord,
				Key: fmt.Sprintf("%s#WD:%d", ruleKey, ord),
			})
		}
		return out, nil

	case FormAnnual:
		if planStart.Year == 0 {
			return nil, fmt.Errorf("engine: annual_window rule needs a plan start date")
		}
		var out []Occurrence
		for y := from.Year - 1; y <= end.Year+1; y++ {
			delta := y - planStart.Year
			if delta < 0 || delta%r.EveryYears != 0 {
				continue
			}
			day, note := r.Day, ""
			if r.Month == 2 && r.Day == 29 && !isLeap(y) {
				day = 28
				note = "Feb 29 mapped to Feb 28 in non-leap year " + fmt.Sprintf("%d", y)
			}
			start := Date{y, r.Month, day}
			if start.Before(from) || start.After(end) {
				continue
			}
			we := start.AddDays(r.WindowDays - 1)
			occ := Occurrence{
				DueDate: start, Ordinal: int64(delta / r.EveryYears),
				Key:       fmt.Sprintf("%s#AW:%d", ruleKey, y),
				WindowEnd: &we,
			}
			if note != "" {
				occ.Notes = []string{note}
			}
			out = append(out, occ)
		}
		return out, nil

	case FormOneTime:
		if r.When.Before(from) || r.When.After(end) {
			return nil, nil
		}
		return []Occurrence{{
			DueDate: r.When, Ordinal: 0,
			Key: fmt.Sprintf("%s#OT:%s", ruleKey, r.When),
		}}, nil
	}
	return nil, fmt.Errorf("engine: unreachable recurrence form")
}

// startOfWeek returns the Monday of the week containing d.
func (d Date) startOfWeek() Date {
	w := int(d.Weekday()) // Sunday=0
	if w == 0 {
		w = 7
	}
	return d.AddDays(-(w - 1))
}
