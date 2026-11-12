package engine

import (
	"go/parser"
	"go/token"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

func mustParse(t *testing.T, s string) *Recurrence {
	t.Helper()
	r, err := ParseRecurrence([]byte(s))
	if err != nil {
		t.Fatalf("parse %s: %v", s, err)
	}
	return r
}

func D(s string) Date {
	d, err := ParseDate(s)
	if err != nil {
		panic(err)
	}
	return d
}

// ---- dates -----------------------------------------------------------------

func TestParseDateRejectsImpossible(t *testing.T) {
	for _, bad := range []string{"2026-02-30", "2026-13-01", "2026-00-10", "x", "2026-2-3", "26-01-01"} {
		if _, err := ParseDate(bad); err == nil {
			t.Fatalf("%q must fail", bad)
		}
	}
	if got := D("2024-02-29"); got.String() != "2024-02-29" {
		t.Fatalf("leap day: %s", got)
	}
}

func TestDateArithmetic(t *testing.T) {
	if got := D("2026-12-31").AddDays(1); got != D("2027-01-01") {
		t.Fatalf("year rollover: %s", got)
	}
	if got := D("2026-01-01").DaysUntil(D("2026-03-01")); got != 59 {
		t.Fatalf("days until: %d", got)
	}
	if got := D("2026-03-01").DaysUntil(D("2026-01-01")); got != -59 {
		t.Fatalf("negative span: %d", got)
	}
	if D("2026-11-01").Weekday() != time.Sunday {
		t.Fatal("weekday anchor")
	}
	// Ordering sanity: these signs have bitten before.
	if !D("2026-01-01").Before(D("2026-01-08")) || D("2026-01-08").Before(D("2026-01-01")) {
		t.Fatal("Before inverted")
	}
	if !D("2026-01-08").After(D("2026-01-01")) || D("2026-01-01").After(D("2026-01-08")) {
		t.Fatal("After inverted")
	}
	if D("2026-01-01").Before(D("2026-01-01")) || D("2026-01-01").After(D("2026-01-01")) {
		t.Fatal("equal dates must be neither before nor after")
	}
	// Duration-based day math silently overflows past ~292 years; integer
	// day numbers must not. (A fuzz seed with planStart 2825 proved it.)
	if got := D("2026-01-01").DaysUntil(D("2825-06-15")); got <= 0 {
		t.Fatalf("millennia-wide span broke: %d", got)
	}
	if got := D("2825-06-15").AddDays(-292 * 366).AddDays(292 * 366); got != D("2825-06-15") {
		t.Fatalf("far AddDays round trip: %s", got)
	}
}

// ---- recurrence parsing ------------------------------------------------------

func TestParseRecurrenceStrict(t *testing.T) {
	if _, err := ParseRecurrence([]byte(`{"form":"RRULE"}`)); err == nil {
		t.Fatal("unknown form must fail")
	}
	if _, err := ParseRecurrence([]byte(`{"form":"EVERY_N_DAYS","days":0,"anchor":"PLAN_START"}`)); err == nil {
		t.Fatal("days 0 must fail")
	}
	if _, err := ParseRecurrence([]byte(`{"form":"EVERY_N_DAYS","days":366,"anchor":"PLAN_START"}`)); err == nil {
		t.Fatal("days 366 must fail")
	}
	if _, err := ParseRecurrence([]byte(`{"form":"EVERY_N_DAYS","days":7,"anchor":"YESTERDAY"}`)); err == nil {
		t.Fatal("bad anchor must fail")
	}
	if _, err := ParseRecurrence([]byte(`{"form":"WEEKLY_DAYS","weekdays":["MONDAY","MONDAY"]}`)); err == nil {
		t.Fatal("dup weekday must fail")
	}
	if _, err := ParseRecurrence([]byte(`{"form":"ANNUAL_WINDOW","month":2,"day":30,"window_days":1,"every_years":1}`)); err == nil {
		t.Fatal("feb 30 must fail")
	}
	if _, err := ParseRecurrence([]byte(`{"form":"ONE_TIME","date":"2026-02-30"}`)); err == nil {
		t.Fatal("impossible one-time date must fail")
	}
	r := mustParse(t, `{"form":"ANNUAL_WINDOW","month":2,"day":29,"window_days":30,"every_years":2}`)
	if r.Month != 2 || r.Day != 29 || r.WindowDays != 30 || r.EveryYears != 2 {
		t.Fatalf("annual fields: %+v", r)
	}
}

// ---- generation: every-N-days -------------------------------------------------

func TestEveryNPlanStartOrdinalsStableAcrossWindows(t *testing.T) {
	r := mustParse(t, `{"form":"EVERY_N_DAYS","days":7,"anchor":"PLAN_START"}`)
	planStart := D("2026-01-01")

	first, err := r.Generate("water", D("2026-01-01"), planStart, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(first) == 0 || first[0].DueDate != D("2026-01-08") || first[0].Ordinal != 1 {
		t.Fatalf("first window: %+v", first[:min(3, len(first))])
	}
	// Horizon is exactly 60 days: nothing beyond from+60.
	last := first[len(first)-1]
	if last.DueDate.After(D("2026-01-01").AddDays(HorizonDays)) {
		t.Fatalf("past horizon: %s", last.DueDate)
	}
	if len(first) != 8 { // Jan 8,15,22,29, Feb 5,12,19,26 (Mar 5 = day 63)
		t.Fatalf("count: %d", len(first))
	}

	// Regenerating from a later window must reuse identical logical keys.
	second, err := r.Generate("water", D("2026-02-01"), planStart, nil)
	if err != nil {
		t.Fatal(err)
	}
	byKey := map[string]Occurrence{}
	for _, o := range first {
		byKey[o.Key] = o
	}
	for _, o := range second {
		if prev, ok := byKey[o.Key]; ok {
			if prev.DueDate != o.DueDate || prev.Ordinal != o.Ordinal {
				t.Fatalf("key %s drifted: %v vs %v", o.Key, prev, o)
			}
		}
	}
	if _, ok := byKey["water#ENPS:1"]; !ok {
		t.Fatal("expected stable key form")
	}
}

func TestEveryNCompletionRelativeSingleActionable(t *testing.T) {
	r := mustParse(t, `{"form":"EVERY_N_DAYS","days":7,"anchor":"LAST_QUALIFYING_COMPLETION"}`)
	cur := Cursor{LastOrdinal: 4, LastDecision: DecisionSkipped,
		AnchorDate: D("2026-01-08"), NextDueDate: D("2026-01-15")}
	occs, err := r.Generate("feed", D("2026-01-01"), D("2025-12-01"), &cur)
	if err != nil {
		t.Fatal(err)
	}
	if len(occs) != 1 || occs[0].DueDate != D("2026-01-15") || occs[0].Ordinal != 5 {
		t.Fatalf("must materialize exactly one next actionable: %+v", occs)
	}
	// No cursor -> programming error, fail loudly.
	if _, err := r.Generate("feed", D("2026-01-01"), D("2025-12-01"), nil); err == nil {
		t.Fatal("completion-relative rule without cursor must fail")
	}
}

// ---- generation: weekdays -----------------------------------------------------

func TestWeeklyDaysCadence(t *testing.T) {
	// 2026-01-05 is a Monday.
	r := mustParse(t, `{"form":"WEEKLY_DAYS","weekdays":["MONDAY","THURSDAY"]}`)
	occs, err := r.Generate("check", D("2026-01-05"), D("2026-01-05"), nil)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"2026-01-05", "2026-01-08", "2026-01-12", "2026-01-15"}
	for i, w := range want {
		if occs[i].DueDate.String() != w {
			t.Fatalf("slot %d: %s want %s", i, occs[i].DueDate, w)
		}
	}
	// Completion never shifts weekday cadence: generation ignores cursors.
	// Ordinals/keys must be stable across generation windows: overlapping
	// slots carry identical keys, due dates and ordinals.
	later, err := r.Generate("check", D("2026-02-01"), D("2026-01-05"), nil)
	if err != nil {
		t.Fatal(err)
	}
	byKey := map[string]Occurrence{}
	for _, o := range occs {
		byKey[o.Key] = o
	}
	overlap := 0
	for _, o := range later {
		if prev, ok := byKey[o.Key]; ok {
			overlap++
			if prev.DueDate != o.DueDate || prev.Ordinal != o.Ordinal {
				t.Fatalf("key %s drifted: %v vs %v", o.Key, prev, o)
			}
		}
	}
	if overlap == 0 {
		t.Fatal("windows should overlap; test is vacuous otherwise")
	}
	// Ordinal = weeksSinceEpoch*len + index; verify monotonic and dense.
	for i := 1; i < len(occs); i++ {
		if occs[i].Ordinal <= occs[i-1].Ordinal {
			t.Fatalf("ordinals not increasing: %+v", occs)
		}
	}
}

func TestWeeklyOrdinalsIncreaseAcrossSundayBoundary(t *testing.T) {
	// Fuzz-found case: Sunday+Monday pairs. Monday-anchored weeks put
	// Sunday last chronologically; ordinals must still increase in date
	// order, and spec order must not change slot identity.
	a := mustParse(t, `{"form":"WEEKLY_DAYS","weekdays":["SUNDAY","MONDAY"]}`)
	b := mustParse(t, `{"form":"WEEKLY_DAYS","weekdays":["MONDAY","SUNDAY"]}`)
	occs, err := a.Generate("check", D("2026-01-01"), D("2026-01-01"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(occs) < 4 {
		t.Fatalf("expected several slots: %+v", occs)
	}
	for i := 1; i < len(occs); i++ {
		if occs[i].Ordinal <= occs[i-1].Ordinal {
			t.Fatalf("ordinals not increasing across Sunday boundary: %+v", occs)
		}
	}
	occsB, err := b.Generate("check", D("2026-01-01"), D("2026-01-01"), nil)
	if err != nil {
		t.Fatal(err)
	}
	for i := range occs {
		if occs[i].Key != occsB[i].Key || occs[i].DueDate != occsB[i].DueDate {
			t.Fatalf("spec order changed slot identity: %v vs %v", occs[i], occsB[i])
		}
	}
}

// ---- generation: annual windows ------------------------------------------------
func TestAnnualWindowFeb29Mapping(t *testing.T) {
	r := mustParse(t, `{"form":"ANNUAL_WINDOW","month":2,"day":29,"window_days":1,"every_years":1}`)
	// Leap year: exact date, no note.
	occs, err := r.Generate("prune", D("2024-01-01"), D("2024-01-01"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(occs) != 1 || occs[0].DueDate != D("2024-02-29") || len(occs[0].Notes) != 0 {
		t.Fatalf("leap: %+v", occs)
	}
	// Non-leap: Feb 28 + note recorded for the reason.
	occs, err = r.Generate("prune", D("2025-01-01"), D("2024-01-01"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(occs) != 1 || occs[0].DueDate != D("2025-02-28") || len(occs[0].Notes) != 1 {
		t.Fatalf("non-leap: %+v", occs)
	}
	if note := occs[0].Notes[0]; note == "" {
		t.Fatal("feb 29 mapping must be recorded in reason notes")
	}
}

func TestAnnualWindowEveryYearsAndNewYear(t *testing.T) {
	// every 2 years anchored to plan start year 2024: windows in 2024, 2026, ...
	r := mustParse(t, `{"form":"ANNUAL_WINDOW","month":6,"day":15,"window_days":10,"every_years":2}`)
	// 2025 (odd delta) generates nothing near its June:
	occs, err := r.Generate("fertilize", D("2025-05-01"), D("2024-01-01"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(occs) != 0 {
		t.Fatalf("odd year must be skipped: %+v", occs)
	}
	// 2026 (delta 2) generates on schedule:
	occs, err = r.Generate("fertilize", D("2026-05-01"), D("2024-01-01"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(occs) != 1 || occs[0].DueDate != D("2026-06-15") {
		t.Fatalf("even year window missing: %+v", occs)
	}
	// Window spanning New Year: Dec 15 + 45-day window ends Jan 28.
	r2 := mustParse(t, `{"form":"ANNUAL_WINDOW","month":12,"day":15,"window_days":45,"every_years":1}`)
	occs, err = r2.Generate("protect", D("2026-12-01"), D("2026-01-01"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(occs) != 1 || occs[0].DueDate != D("2026-12-15") {
		t.Fatalf("new-year window: %+v", occs)
	}
	if occs[0].WindowEnd == nil || *occs[0].WindowEnd != D("2027-01-28") {
		t.Fatalf("window end must cross the year honestly: %v", occs[0].WindowEnd)
	}
}

func TestOneTimeStaysDistinct(t *testing.T) {
	r := mustParse(t, `{"form":"ONE_TIME","date":"2026-03-20"}`)
	occs, err := r.Generate("repot", D("2026-03-01"), D("2026-01-01"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(occs) != 1 || occs[0].Key != "repot#OT:2026-03-20" {
		t.Fatalf("one-time: %+v", occs)
	}
	// Outside horizon: not generated now, generated on a future tick.
	occs, err = r.Generate("repot", D("2026-06-01"), D("2026-01-01"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(occs) != 0 {
		t.Fatalf("past one-time must not regenerate: %+v", occs)
	}
}

// ---- cursor scenarios straight from 03-contracts --------------------------------

func TestCursorScenariosFromContract(t *testing.T) {
	year := 2026
	jan := func(day int) Date { return Date{year, 1, day} }
	n7 := 7

	// "Jan 1 due -> skip -> Jan 8 due -> skip -> Jan 15 due."
	cur := Cursor{LastOrdinal: 0, NextDueDate: jan(1)}
	cur = cur.Skip(1, jan(1), n7)
	if cur.NextDueDate != jan(8) || cur.LastOrdinal != 1 {
		t.Fatalf("skip 1: %+v", cur)
	}
	cur = cur.Skip(2, jan(8), n7)
	if cur.NextDueDate != jan(15) || cur.LastOrdinal != 2 {
		t.Fatalf("skip 2: %+v", cur)
	}

	// "Complete Jan 15 occurrence on Jan 17 -> Jan 24 next due."
	c2 := cur.Complete(3, jan(17), n7)
	if c2.NextDueDate != jan(24) {
		t.Fatalf("complete on Jan 17: %s", c2.NextDueDate)
	}
	// "...complete it with explicit backdate Jan 14 -> Jan 21 next due."
	c3 := cur.Complete(3, jan(14), n7)
	if c3.NextDueDate != jan(21) {
		t.Fatalf("backdated completion: %s", c3.NextDueDate)
	}

	// "Complete Jan 1 occurrence Jan 2 -> Jan 9 due, skip Jan 9 -> Jan 16 due,
	// then correct earlier completion from Jan 2 to Jan 3: Jan 9 skip remains
	// immutable and Jan 16 stays next."
	c4 := Cursor{LastOrdinal: 0, NextDueDate: jan(1)}
	c4 = c4.Complete(1, jan(2), n7)
	if c4.NextDueDate != jan(9) {
		t.Fatalf("complete Jan 1 on Jan 2: %s", c4.NextDueDate)
	}
	c4 = c4.Skip(2, jan(9), n7)
	if c4.NextDueDate != jan(16) {
		t.Fatalf("skip Jan 9: %s", c4.NextDueDate)
	}
	corrected := c4.CorrectLatestCompletion(jan(3), n7)
	if corrected.NextDueDate != jan(16) || corrected.LastOrdinal != 2 ||
		corrected.LastDecision != DecisionSkipped {
		t.Fatalf("correction of an older completion must not move a newer skip: %+v", corrected)
	}

	// "Snoozing Jan 9 to Jan 12 then skipping still advances to Jan 16."
	c5 := Cursor{LastOrdinal: 1, NextDueDate: jan(9)}
	c5 = c5.Snooze() // snooze never touches cadence
	if c5.NextDueDate != jan(9) {
		t.Fatalf("snooze changed cadence: %+v", c5)
	}
	c5 = c5.Skip(2, jan(9), n7) // skip anchors to ORIGINAL due Jan 9, not snoozed Jan 12
	if c5.NextDueDate != jan(16) {
		t.Fatalf("skip after snooze: %s", c5.NextDueDate)
	}
}

// ---- context: fail-closed + environment ------------------------------------------

func TestMissingFactsAbstain(t *testing.T) {
	ctx := Context{Today: D("2026-01-01"), Situation: "OUTDOOR"}
	r := Rule{RuleKey: "chill", RequiredFacts: []string{"REGION", "HEMISPHERE"},
		WeatherSensitivity: "NONE", GuidanceText: "g"}
	a := Advise(r, ctx)
	if !a.Abstain || len(a.MissingFacts) != 2 {
		t.Fatalf("must abstain: %+v", a)
	}
	ctx.Region = "9b"
	ctx.Hemisphere = "NORTHERN"
	a = Advise(r, ctx)
	if a.Abstain || len(a.MissingFacts) != 0 {
		t.Fatalf("must apply once facts exist: %+v", a)
	}
}

func TestEnvironmentMatches(t *testing.T) {
	env := Environment{Hemispheres: []string{"SOUTHERN"}}
	if ok, missing := env.Matches(Context{Hemisphere: "NORTHERN"}); ok || missing != nil {
		t.Fatalf("hemisphere mismatch must not apply: %v %v", ok, missing)
	}
	if ok, missing := env.Matches(Context{}); ok || len(missing) != 1 || missing[0] != FactHemisphere {
		t.Fatalf("unknown hemisphere on restricted profile must abstain: %v %v", ok, missing)
	}
	if ok, _ := (Environment{}).Matches(Context{}); !ok {
		t.Fatal("empty environment applies to any")
	}
	env2 := Environment{Situations: []string{"OUTDOOR"}}
	if ok, _ := env2.Matches(Context{Situation: "INDOOR"}); ok {
		t.Fatal("situation mismatch must not apply")
	}
}

func TestParseEnvironment(t *testing.T) {
	e, err := ParseEnvironment([]byte(`{"situations":["OUTDOOR"],"hemispheres":["NORTHERN"]}`))
	if err != nil || len(e.Situations) != 1 || len(e.Hemispheres) != 1 {
		t.Fatalf("env: %+v %v", e, err)
	}
	if _, err := ParseEnvironment([]byte(`{`)); err == nil {
		t.Fatal("bad json must fail")
	}
}

// ---- weather advisory --------------------------------------------------------------

func f(v float64) *float64 { return &v }

func TestWeatherAdvisoryRules(t *testing.T) {
	now := time.Date(2026, 1, 10, 12, 0, 0, 0, time.UTC)
	fresh := &Weather{FetchedAt: now.Add(-2 * time.Hour), LowTempC: f(1.5), RainfallMm: f(0.0)}
	stale := &Weather{FetchedAt: now.Add(-10 * time.Hour), LowTempC: f(1.5)}
	gone := &Weather{FetchedAt: now.Add(-30 * time.Hour), LowTempC: f(1.5)}

	if got := fresh.Freshness(now); got != "FRESH" {
		t.Fatalf("fresh: %s", got)
	}
	if got := stale.Freshness(now); got != "STALE" {
		t.Fatalf("stale: %s", got)
	}
	if got := gone.Freshness(now); got != "UNAVAILABLE" {
		t.Fatalf("gone: %s", got)
	}
	var nilW *Weather
	if got := nilW.Freshness(now); got != "UNAVAILABLE" {
		t.Fatalf("nil: %s", got)
	}

	frost := Rule{RuleKey: "frost", WeatherSensitivity: "FROST", GuidanceText: "Protect from frost."}
	a := Advise(frost, Context{Now: now, Weather: fresh})
	if a.Abstain {
		t.Fatal("weather absence never abstains a rule (it is advisory)")
	}
	joined := ""
	for _, n := range a.EvidenceNotes {
		joined += n + "|"
	}
	if !contains(a.EvidenceNotes, "reported low 1.5°C") {
		t.Fatalf("fresh values missing: %v", a.EvidenceNotes)
	}
	if !contains(a.EvidenceNotes, "temperature can only elevate a frost check; it never marks work done or asserts soil moisture") {
		t.Fatalf("frost guardrail note missing: %v", a.EvidenceNotes)
	}

	// Covered/indoor ignores rainfall for water guidance.
	rain := Rule{RuleKey: "water", WeatherSensitivity: "RAINFALL", GuidanceText: "Deep soak."}
	a = Advise(rain, Context{Now: now, Situation: "COVERED", Weather: fresh})
	if !contains(a.EvidenceNotes, "rainfall ignored for water guidance: indoor/covered context") {
		t.Fatalf("covered must ignore rainfall: %v", a.EvidenceNotes)
	}
	for _, n := range a.EvidenceNotes {
		if n == "reported rainfall 0.0mm" {
			t.Fatal("rainfall value must not be surfaced for covered context")
		}
	}
	// Outdoor + stale: shown as stale.
	a = Advise(rain, Context{Now: now, Situation: "OUTDOOR", Weather: stale})
	if !contains(a.EvidenceNotes, "weather snapshot is stale (shown as stale)") {
		t.Fatalf("stale note missing: %v", a.EvidenceNotes)
	}
	// Weather unavailable: proceed on calendar cadence alone.
	a = Advise(rain, Context{Now: now, Situation: "OUTDOOR", Weather: nil})
	if !contains(a.EvidenceNotes, "weather snapshot unavailable; guidance proceeds on calendar cadence alone") {
		t.Fatalf("unavailable note missing: %v", a.EvidenceNotes)
	}
	// NONE: weather plays no part.
	none := Rule{RuleKey: "prune", WeatherSensitivity: "NONE", GuidanceText: "g"}
	a = Advise(none, Context{Now: now, Weather: fresh})
	if len(a.EvidenceNotes) != 0 {
		t.Fatalf("NONE must ignore weather: %v", a.EvidenceNotes)
	}
}

// ---- notification instants (DST) ----------------------------------------------------

func TestNotificationInstantDST(t *testing.T) {
	// Spring-forward gap: 2026-03-08 02:30 America/New_York does not exist.
	inst, notes, err := NotificationInstant(D("2026-03-08"), "02:30", "America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	if len(notes) != 1 {
		t.Fatalf("gap must be noted: %v", notes)
	}
	// Next valid instant = the transition itself: 02:00 EST jumps to 03:00 EDT = 07:00Z.
	want := time.Date(2026, 3, 8, 7, 0, 0, 0, time.UTC)
	if !inst.Equal(want) {
		t.Fatalf("gap target: %s want %s", inst, want)
	}

	// Fall-back overlap: 2026-11-01 01:30 occurs twice; earlier offset (EDT) wins.
	inst, notes, err = NotificationInstant(D("2026-11-01"), "01:30", "America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	if len(notes) != 1 {
		t.Fatalf("ambiguity must be noted: %v", notes)
	}
	want = time.Date(2026, 11, 1, 5, 30, 0, 0, time.UTC) // 01:30 EDT
	if !inst.Equal(want) {
		t.Fatalf("earlier offset: %s want %s", inst, want)
	}

	// Ordinary day, default 09:00, no notes.
	inst, notes, err = NotificationInstant(D("2026-06-15"), "", "America/New_York")
	if err != nil || len(notes) != 0 {
		t.Fatalf("ordinary: %v %v", notes, err)
	}
	want = time.Date(2026, 6, 15, 13, 0, 0, 0, time.UTC) // 09:00 EDT
	if !inst.Equal(want) {
		t.Fatalf("default time: %s", inst)
	}

	// UTC sanity + bad inputs.
	if _, _, err := NotificationInstant(D("2026-01-01"), "25:00", "UTC"); err == nil {
		t.Fatal("bad time-of-day must fail")
	}
	if _, _, err := NotificationInstant(D("2026-01-01"), "09:00", "Mars/Olympus"); err == nil {
		t.Fatal("bad timezone must fail")
	}
}

// ---- purity guard --------------------------------------------------------------------

// The T22 packet forbids DB/network/HTTP imports in the engine. This test
// enforces it from inside the package (go test runs with the package
// directory as cwd).
func TestEnginePurity(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	banned := []string{"github.com/jackc", "canopy/server/db", "net/http", "database/sql",
		"canopy/server/internal/transport", "github.com/riverqueue"}
	checked := 0
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		file, err := parser.ParseFile(token.NewFileSet(), name, src, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		for _, imp := range file.Imports {
			path, _ := strconv.Unquote(imp.Path.Value)
			for _, b := range banned {
				if path == b || strings.HasPrefix(path, b+"/") {
					t.Fatalf("%s imports banned package %q", name, b)
				}
			}
		}
		checked++
	}
	if checked < 4 {
		t.Fatalf("purity scan covered only %d files", checked)
	}
}

// ---- fuzz: generation invariants -------------------------------------------------------

func FuzzRecurrenceGenerate(f *testing.F) {
	seeds := []string{
		`{"form":"EVERY_N_DAYS","days":1,"anchor":"PLAN_START"}`,
		`{"form":"EVERY_N_DAYS","days":365,"anchor":"PLAN_START"}`,
		`{"form":"EVERY_N_DAYS","days":7,"anchor":"LAST_QUALIFYING_COMPLETION"}`,
		`{"form":"WEEKLY_DAYS","weekdays":["MONDAY","SUNDAY"]}`,
		`{"form":"ANNUAL_WINDOW","month":2,"day":29,"window_days":1,"every_years":1}`,
		`{"form":"ANNUAL_WINDOW","month":12,"day":31,"window_days":366,"every_years":50}`,
		`{"form":"ONE_TIME","date":"2026-12-31"}`,
	}
	for _, s := range seeds {
		f.Add(s, "2026-01-01", "2025-06-15")
	}
	f.Fuzz(func(t *testing.T, spec, fromS, planStartS string) {
		r, err := ParseRecurrence([]byte(spec))
		if err != nil {
			t.Skip()
		}
		from, err := ParseDate(fromS)
		if err != nil {
			t.Skip()
		}
		planStart, err := ParseDate(planStartS)
		if err != nil {
			t.Skip()
		}
		cur := &Cursor{LastOrdinal: 3, NextDueDate: from.AddDays(5)}
		occs, err := r.Generate("fuzz", from, planStart, cur)
		if err != nil {
			t.Skip()
		}
		end := from.AddDays(HorizonDays)
		seen := map[string]bool{}
		for i, o := range occs {
			if o.DueDate.Before(from) || o.DueDate.After(end) {
				t.Fatalf("slot outside horizon: %s not in [%s,%s]", o.DueDate, from, end)
			}
			if seen[o.Key] {
				t.Fatalf("duplicate logical key: %s", o.Key)
			}
			seen[o.Key] = true
			if i > 0 && o.DueDate.Before(occs[i-1].DueDate) {
				t.Fatalf("unsorted generation: %s after %s", o.DueDate, occs[i-1].DueDate)
			}
			if r.Form != FormEveryN || r.Anchor == AnchorPlanStart {
				if i > 0 && o.Ordinal <= occs[i-1].Ordinal {
					t.Fatalf("ordinals must increase: %+v", occs)
				}
			}
		}
		if r.Form == FormEveryN && r.Anchor == AnchorLastCompletion && len(occs) > 1 {
			t.Fatal("completion-relative rules materialize at most one actionable slot")
		}
	})
}

func FuzzDateArithmetic(f *testing.F) {
	f.Add("2026-01-01", 0)
	f.Add("2024-02-28", 5)
	f.Add("9999-12-31", -4000)
	f.Fuzz(func(t *testing.T, s string, n int) {
		d, err := ParseDate(s)
		if err != nil {
			t.Skip()
		}
		if n > 100000 || n < -100000 {
			t.Skip()
		}
		got := d.AddDays(n)
		if got.DaysUntil(got) != 0 {
			t.Fatal("self distance")
		}
		if back := got.AddDays(-n); back != d {
			t.Fatalf("round trip: %s +%d -> %s -> %s", d, n, got, back)
		}
		if d.DaysUntil(got) != n {
			t.Fatalf("daysUntil mismatch: %s->%s = %d want %d", d, got, d.DaysUntil(got), n)
		}
	})
}
