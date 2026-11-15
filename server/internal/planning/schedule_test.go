package planning

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"
)

func scheduleString(v string) *string { return &v }
func scheduleInt(v int) *int          { return &v }
func scheduleFact(windows ...GuidanceWindow) GuidanceFact {
	return GuidanceFact{ID: "source-planting", Phase: "PLANT_OUT", Label: "Planting", Summary: "Supported regional planting guidance.", Conditions: []string{}, Windows: windows, SourceTitle: "Regional guide", SourceURL: "https://extension.example/guide", Publisher: "Extension", SourceRegion: "Example region", RetrievedAt: time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC), ContentSHA256: strings.Repeat("a", 64), SourceID: "source", ExtractionVersion: 1, Evidence: "Plant outside from March through April.", TimingText: "March through April", ExtractionModel: strings.Repeat("b", 64)}
}
func annualMonthWindow(from, to int) GuidanceWindow {
	return GuidanceWindow{Precision: "ANNUAL", Label: "Source months", Annual: &AnnualPeriod{Precision: "MONTH_RANGE", Months: &AnnualMonthRange{From: from, To: to}}}
}
func scheduleFixture(facts ...GuidanceFact) ScheduleInput {
	return ScheduleInput{Range: MonthRange{From: "2027-01", To: "2027-12"}, Today: "2027-03-15", Method: "NURSERY_PLANT", Facts: facts}
}
func projectTest(t *testing.T, in ScheduleInput) ScheduleProjection {
	t.Helper()
	out, err := ProjectSchedule(in)
	if err != nil {
		t.Fatal(err)
	}
	return out
}
func TestScheduleAnnualWindowsRemainDistinctAndInputImmutable(t *testing.T) {
	in := scheduleFixture(scheduleFact(annualMonthWindow(3, 4), annualMonthWindow(9, 10)))
	in.Target = &Period{Precision: YearPrecision, Year: scheduleInt(2027)}
	before, _ := json.Marshal(in)
	out := projectTest(t, in)
	if len(out.Windows) != 2 || out.Windows[0].Period.Precision != MonthRangePrecision || *out.Windows[0].Period.Months != (MonthRange{"2027-03", "2027-04"}) || *out.Windows[1].Period.Months != (MonthRange{"2027-09", "2027-10"}) {
		t.Fatal(out)
	}
	if out.Windows[0].Status != "IN_WINDOW" || out.Windows[1].Status != "UPCOMING" || !out.Windows[0].Selectable || out.TargetStatus != "IN_WINDOW" {
		t.Fatal(out)
	}
	if out.Windows[0].Period.Dates != nil || in.Target.Dates != nil || in.Target.Months != nil {
		t.Fatal("coarse source/target became concrete")
	}
	after, _ := json.Marshal(in)
	if string(before) != string(after) || !reflect.DeepEqual(out, projectTest(t, in)) {
		t.Fatal("projection changed inputs or was nondeterministic")
	}
	out.Windows[0].Period.Months.From = "2000-01"
	if in.Facts[0].Windows[0].Annual.Months.From != 3 {
		t.Fatal("output aliases source")
	}
}
func TestScheduleOverlapIncludesPreviousStartAndClipsOnlyDisplayBand(t *testing.T) {
	in := scheduleFixture(scheduleFact(annualMonthWindow(12, 2)))
	in.Range = MonthRange{"2027-01", "2027-12"}
	out := projectTest(t, in)
	if len(out.Windows) != 2 || out.Windows[0].Year != 2026 || *out.Windows[0].Period.Months != (MonthRange{"2026-12", "2027-02"}) || *out.Windows[0].DisplayMonths != (MonthRange{"2027-01", "2027-02"}) || !out.Windows[0].ContinuesBefore || !out.Windows[1].ContinuesAfter {
		t.Fatal(out)
	}
	in.Range = MonthRange{"2027-04", "2027-10"}
	if len(projectTest(t, in).Windows) != 0 {
		t.Fatal("off-screen resolved windows remained")
	}
}
func TestScheduleSeasonsKeepPrecisionAndUseStartingYear(t *testing.T) {
	for _, tc := range []struct {
		hemi     string
		season   Season
		from, to string
	}{
		{"NORTHERN", Winter, "2027-12", "2028-02"},
		{"SOUTHERN", Summer, "2027-12", "2028-02"},
		{"SOUTHERN", Winter, "2027-06", "2027-08"},
		{"NORTHERN", Spring, "2027-03", "2027-05"},
	} {
		t.Run(tc.hemi+string(tc.season), func(t *testing.T) {
			w := GuidanceWindow{Precision: "ANNUAL", Label: "Source season", Annual: &AnnualPeriod{Precision: "SEASON", Season: &tc.season}, Hemisphere: &tc.hemi}
			in := scheduleFixture(scheduleFact(w))
			in.Range = MonthRange{"2027-03", "2028-02"}
			out := projectTest(t, in)
			if len(out.Windows) != 1 || out.Windows[0].Period.Precision != SeasonPrecision || out.Windows[0].Period.Season.Year != 2027 || *out.Windows[0].DisplayMonths != (MonthRange{tc.from, tc.to}) || out.Windows[0].Period.Dates != nil {
				t.Fatal(out)
			}
		})
	}
}
func TestScheduleMissingOrConflictingHemisphereDoesNotGuessFromSeason(t *testing.T) {
	season := Winter
	fact := scheduleFact(GuidanceWindow{Precision: "ANNUAL", Label: "Winter", Annual: &AnnualPeriod{Precision: "SEASON", Season: &season}})
	in := scheduleFixture(fact)
	in.Target = &Period{Precision: SeasonPrecision, Season: &SeasonPeriod{Season: Winter, Year: 2027}}
	out := projectTest(t, in)
	if out.TargetStatus != "NEEDS_REVIEW" || out.Windows[0].Resolution != "NEEDS_HEMISPHERE" || out.Windows[0].Period != nil || out.Windows[0].Selectable {
		t.Fatal(out)
	}
	in.Facts[0].Windows[0].Hemisphere = scheduleString("NORTHERN")
	in.Assumptions.Hemisphere = scheduleString("SOUTHERN")
	in.Range = MonthRange{"2027-03", "2027-03"}
	out = projectTest(t, in)
	if len(out.Windows) != 1 || out.Windows[0].Resolution != "HEMISPHERE_MISMATCH" {
		t.Fatal(out)
	}
	// Historical accepted season keeps its original hemisphere even when the
	// intended site changes. Source guidance can still be flagged incompatible.
	in.Range = MonthRange{"2027-03", "2028-02"}
	in.TargetHemisphere = scheduleString("NORTHERN")
	out = projectTest(t, in)
	if *out.TargetDisplayMonths != (MonthRange{"2027-12", "2028-02"}) {
		t.Fatal("accepted season moved after site change", out)
	}
}
func TestScheduleLeapDatesRequireReviewWithoutClamping(t *testing.T) {
	w := GuidanceWindow{Precision: "ANNUAL", Label: "Leap day", Annual: &AnnualPeriod{Precision: "DATE_RANGE", Dates: &AnnualDateRange{From: &MonthDay{2, 29}, To: &MonthDay{3, 1}}}}
	in := scheduleFixture(scheduleFact(w))
	out := projectTest(t, in)
	if len(out.Windows) != 1 || out.Windows[0].Resolution != "INVALID_DATE" || out.Windows[0].Period != nil {
		t.Fatal(out)
	}
	in.Range = MonthRange{"2027-11", "2028-10"}
	out = projectTest(t, in)
	if len(out.Windows) != 1 || out.Windows[0].Year != 2028 || out.Windows[0].Period.Dates.From != "2028-02-29" {
		t.Fatal(out)
	}
}
func TestScheduleRelativeUsesNamedAnchorSignedCivilDaysAndUnknownOffsets(t *testing.T) {
	w := GuidanceWindow{Precision: "RELATIVE", Label: "Before frost", Relative: &RelativeWindow{Anchor: "LAST_FROST", FromDays: scheduleInt(-42), ToDays: scheduleInt(-28)}}
	in := scheduleFixture(scheduleFact(w))
	in.Assumptions.Anchors = []ScheduleAnchor{{Kind: "SOWING", On: "2027-04-01"}}
	if got := projectTest(t, in).Windows[0]; got.Resolution != "NEEDS_ANCHOR" || got.Period != nil {
		t.Fatal(got)
	}
	in.Assumptions.Anchors = []ScheduleAnchor{{Kind: "LAST_FROST", On: "2027-04-01"}}
	got := projectTest(t, in).Windows[0]
	if got.Resolution != "RESOLVED" || !got.Estimated || *got.Period.Dates != (DateRange{"2027-02-18", "2027-03-04"}) {
		t.Fatal(got)
	}
	in.Facts[0].Windows[0].Relative.FromDays = nil
	if got := projectTest(t, in).Windows[0]; got.Resolution != "NEEDS_OFFSET" || got.Period != nil {
		t.Fatal("null offset became zero", got)
	}
	in.Facts[0].Windows[0].Relative.FromDays = scheduleInt(0)
	in.Facts[0].Windows[0].Relative.ToDays = scheduleInt(0)
	if got := projectTest(t, in).Windows[0]; got.Period.Dates.From != "2027-04-01" || got.Period.Dates.To != "2027-04-01" {
		t.Fatal("explicit zero was not retained", got)
	}
}
func TestScheduleHarvestAdviceDoesNotGuaranteeFirstYearFruit(t *testing.T) {
	fact := scheduleFact(annualMonthWindow(8, 10))
	fact.Phase = "HARVEST"
	in := scheduleFixture(fact)
	got := projectTest(t, in).Windows[0]
	if !got.FirstHarvestUnknown || got.Estimated || got.Selectable {
		t.Fatal(got)
	}
	in.Facts[0].Windows = []GuidanceWindow{{Precision: "RELATIVE", Label: "Maturity from transplanting", Relative: &RelativeWindow{Anchor: "TRANSPLANTING", FromDays: scheduleInt(60), ToDays: scheduleInt(80)}}}
	in.Assumptions.Anchors = []ScheduleAnchor{{Kind: "SOWING", On: "2027-03-01"}}
	if got := projectTest(t, in).Windows[0]; got.Resolution != "NEEDS_ANCHOR" || !got.FirstHarvestUnknown {
		t.Fatal("sowing stood in for transplanting", got)
	}
	in.Assumptions.Anchors = []ScheduleAnchor{{Kind: "TRANSPLANTING", On: "2027-05-01"}}
	got = projectTest(t, in).Windows[0]
	if got.FirstHarvestUnknown || !got.Estimated || got.Period.Dates.From != "2027-06-30" || got.Period.Dates.To != "2027-07-20" {
		t.Fatal(got)
	}
}
func TestScheduleYearBoundsAndPeriodContainment(t *testing.T) {
	in := scheduleFixture(scheduleFact(annualMonthWindow(12, 2)))
	in.Range = MonthRange{"9999-12", "9999-12"}
	if got := projectTest(t, in).Windows[0]; got.Resolution != "OUT_OF_RANGE" || got.Period != nil {
		t.Fatal(got)
	}
	in.Facts[0].Windows = []GuidanceWindow{{Precision: "RELATIVE", Label: "Overflow", Relative: &RelativeWindow{Anchor: "PLANTING", FromDays: scheduleInt(1), ToDays: scheduleInt(2147483647)}}}
	in.Assumptions.Anchors = []ScheduleAnchor{{Kind: "PLANTING", On: "9999-12-31"}}
	if got := projectTest(t, in).Windows[0]; got.Resolution != "OUT_OF_RANGE" {
		t.Fatal(got)
	}
	source := &Period{Precision: MonthRangePrecision, Months: &MonthRange{"2027-03", "2027-04"}}
	for _, tc := range []struct {
		chosen *Period
		ok     bool
	}{
		{&Period{Precision: DateRangePrecision, Dates: &DateRange{"2027-03-01", "2027-04-30"}}, true},
		{&Period{Precision: DateRangePrecision, Dates: &DateRange{"2027-02-28", "2027-03-01"}}, false},
		{&Period{Precision: MonthRangePrecision, Months: &MonthRange{"2027-04", "2027-04"}}, true},
		{&Period{Precision: YearPrecision, Year: scheduleInt(2027)}, false},
		{nil, false},
	} {
		if periodContained(tc.chosen, source, "NORTHERN") != tc.ok {
			t.Fatal(tc)
		}
	}
}
func TestScheduleRejectsMalformedRangeAndAssumptions(t *testing.T) {
	for _, r := range []MonthRange{{"2027-01", "2028-01"}, {"0000-01", "0000-12"}, {"2027-02", "2027-01"}, {"2027-1", "2027-12"}} {
		in := scheduleFixture()
		in.Range = r
		if _, err := ProjectSchedule(in); err == nil {
			t.Fatal("invalid range accepted", r)
		}
	}
	in := scheduleFixture()
	in.Assumptions.Anchors = []ScheduleAnchor{{"SOWING", "2027-03-01"}, {"SOWING", "2027-04-01"}}
	if _, err := ProjectSchedule(in); err == nil {
		t.Fatal("ambiguous anchor accepted")
	}
	in.Assumptions.Anchors = nil
	in.Today = "2027-02-30"
	if _, err := ProjectSchedule(in); err == nil {
		t.Fatal("invalid local day accepted")
	}
}

func TestScheduleLeapDayAcrossCompleteGregorianCycle(t *testing.T) {
	fact := scheduleFact(GuidanceWindow{Precision: "ANNUAL", Label: "Leap date", Annual: &AnnualPeriod{Precision: "DATE_RANGE", Dates: &AnnualDateRange{From: &MonthDay{2, 29}, To: &MonthDay{2, 29}}}})
	resolved, review := 0, 0
	for year := 2000; year < 2400; year++ {
		in := scheduleFixture(fact)
		month := fmt.Sprintf("%04d-02", year)
		in.Range = MonthRange{month, month}
		in.Today = month + "-01"
		out := projectTest(t, in)
		if len(out.Windows) != 1 {
			t.Fatal(year, out)
		}
		w := out.Windows[0]
		switch w.Resolution {
		case "RESOLVED":
			resolved++
			if w.Period.Dates.From != month+"-29" || w.Period.Dates.To != month+"-29" {
				t.Fatal(year, w)
			}
		case "INVALID_DATE":
			review++
			if w.Period != nil || w.DisplayMonths != nil {
				t.Fatal("invalid date gained bounds", w)
			}
		default:
			t.Fatal(year, w)
		}
	}
	if resolved != 97 || review != 303 {
		t.Fatal("Gregorian leap-day cycle changed", resolved, review)
	}
}
