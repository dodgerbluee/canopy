package planning

import (
	"fmt"
	"sort"
	"strconv"
	"time"
)

// ProjectSchedule performs no I/O and never mutates its input. Today is supplied
// as a workspace-local civil date; neither time.Local nor the system clock is used.
func ProjectSchedule(in ScheduleInput) (ScheduleProjection, error) {
	if validateScheduleRange(in.Range) != nil || validateScheduleAssumptions(in.Assumptions) != nil || !validDate(in.Today) || len(ValidatePeriod(in.Target)) > 0 || ValidateGuidanceFacts(in.Facts) != nil || !contains(in.Method, "", "START_INDOORS", "DIRECT_SOW", "NURSERY_PLANT") {
		return ScheduleProjection{}, ErrInvalidSchedule
	}
	hemi := hemisphereValue(in.Assumptions.Hemisphere)
	targetHemi := hemi
	if in.TargetHemisphere != nil {
		if !contains(*in.TargetHemisphere, "NORTHERN", "SOUTHERN") {
			return ScheduleProjection{}, ErrInvalidSchedule
		}
		targetHemi = *in.TargetHemisphere
	}
	band, _, _ := periodBand(in.Target, targetHemi, in.Range)
	out := ScheduleProjection{TargetStatus: periodStatus(in.Target, targetHemi, in.Today), TargetDisplayMonths: band, TargetVisible: band != nil, Windows: []ProjectedWindow{}}
	fromYear, _, _ := CanonicalYearMonth(in.Range.From)
	toYear, _, _ := CanonicalYearMonth(in.Range.To)
	seen := map[string]bool{}
	for _, fact := range in.Facts {
		if seen[fact.ID] {
			return ScheduleProjection{}, ErrInvalidSchedule
		}
		seen[fact.ID] = true
		for index, window := range fact.Windows {
			years := []int{fromYear}
			switch window.Precision {
			case "ANNUAL":
				years = []int{}
				for y := max(1, fromYear-1); y <= toYear; y++ {
					if annualMayOverlap(window, y, in.Range, hemi) {
						years = append(years, y)
					}
				}
			case "ABSOLUTE":
				years = []int{periodStartingYear(window.Absolute)}
			case "RELATIVE":
				for _, a := range in.Assumptions.Anchors {
					if a.Kind == window.Relative.Anchor {
						y, _ := strconv.Atoi(a.On[:4])
						years = []int{y}
					}
				}
			}
			for _, y := range years {
				projected := projectGuidanceWindow(fact, index, y, in.Assumptions, in.Today, in.Method)
				if projected.Resolution == "RESOLVED" {
					projected.DisplayMonths, projected.ContinuesBefore, projected.ContinuesAfter = periodBand(projected.Period, effectiveWindowHemisphere(window, hemi), in.Range)
					if projected.DisplayMonths == nil {
						continue
					}
				}
				out.Windows = append(out.Windows, projected)
			}
		}
	}
	sort.SliceStable(out.Windows, func(i, j int) bool {
		a, b := out.Windows[i], out.Windows[j]
		if (a.DisplayMonths == nil) != (b.DisplayMonths == nil) {
			return a.DisplayMonths != nil
		}
		if a.DisplayMonths != nil && a.DisplayMonths.From != b.DisplayMonths.From {
			return a.DisplayMonths.From < b.DisplayMonths.From
		}
		if a.FactID != b.FactID {
			return a.FactID < b.FactID
		}
		if a.WindowIndex != b.WindowIndex {
			return a.WindowIndex < b.WindowIndex
		}
		return a.Year < b.Year
	})
	return out, nil
}
func effectiveWindowHemisphere(w GuidanceWindow, garden string) string {
	if garden != "" {
		return garden
	}
	return hemisphereValue(w.Hemisphere)
}
func annualMayOverlap(w GuidanceWindow, year int, view MonthRange, hemi string) bool {
	if hemi != "" && w.Hemisphere != nil && hemi != *w.Hemisphere {
		return year == mustMonthYear(view.From)
	}
	var from, to, endYear int
	endYear = year
	switch a := w.Annual; a.Precision {
	case "DATE_RANGE":
		from, to = a.Dates.From.Month, a.Dates.To.Month
		if to < from || (to == from && a.Dates.To.Day < a.Dates.From.Day) {
			endYear++
		}
	case "MONTH_RANGE":
		from, to = a.Months.From, a.Months.To
		if to < from {
			endYear++
		}
	case "SEASON":
		months, state := seasonMonths(*a.Season, year, effectiveWindowHemisphere(w, hemi))
		if state != "RESOLVED" {
			return year == mustMonthYear(view.From)
		}
		return months.To >= view.From && months.From <= view.To
	}
	viewFromYear, viewFromMonth, _ := CanonicalYearMonth(view.From)
	viewToYear, viewToMonth, _ := CanonicalYearMonth(view.To)
	return endYear*12+to >= viewFromYear*12+int(viewFromMonth) && year*12+from <= viewToYear*12+int(viewToMonth)
}
func mustMonthYear(month string) int { y, _, _ := CanonicalYearMonth(month); return y }
func periodStartingYear(p *Period) int {
	switch p.Precision {
	case DateRangePrecision:
		y, _ := strconv.Atoi(p.Dates.From[:4])
		return y
	case MonthRangePrecision:
		return mustMonthYear(p.Months.From)
	case SeasonPrecision:
		return p.Season.Year
	case YearPrecision:
		return *p.Year
	}
	return 0
}
func phaseMatchesMethod(phase, method string) bool {
	switch method {
	case "START_INDOORS":
		return contains(phase, "START_INDOORS", "TRANSPLANT", "PLANT_OUT")
	case "DIRECT_SOW":
		return phase == "DIRECT_SOW"
	case "NURSERY_PLANT":
		return contains(phase, "TRANSPLANT", "PLANT_OUT")
	}
	return false
}
func projectGuidanceWindow(fact GuidanceFact, index, year int, a ScheduleAssumptions, today, method string) ProjectedWindow {
	w := fact.Windows[index]
	id, _ := requestHash([]any{fact.ID, index, year})
	out := ProjectedWindow{ID: id, FactID: fact.ID, WindowIndex: index, Year: year, Phase: fact.Phase, Label: w.Label, Resolution: "RESOLVED", Status: "NEEDS_REVIEW", FirstHarvestUnknown: fact.Phase == "HARVEST"}
	hemi := effectiveWindowHemisphere(w, hemisphereValue(a.Hemisphere))
	if a.Hemisphere != nil && w.Hemisphere != nil && *a.Hemisphere != *w.Hemisphere {
		out.Resolution = "HEMISPHERE_MISMATCH"
	} else {
		switch w.Precision {
		case "ABSOLUTE":
			out.Period = clonePeriod(w.Absolute)
			if year != periodStartingYear(w.Absolute) {
				out.Resolution = "OUT_OF_RANGE"
			}
		case "ANNUAL":
			out.Period, out.Resolution = projectAnnual(w.Annual, year)
		case "RELATIVE":
			out.Estimated = true
			out.Period, out.Resolution = projectRelative(w.Relative, a.Anchors, year)
			if out.Resolution == "RESOLVED" && contains(w.Relative.Anchor, "SOWING", "TRANSPLANTING", "PLANTING") {
				out.FirstHarvestUnknown = false
			}
		default:
			out.Resolution = "UNKNOWN"
		}
	}
	if out.Resolution == "RESOLVED" {
		_, _, out.Resolution = periodBounds(out.Period, hemi)
	}
	if out.Resolution != "RESOLVED" {
		out.Period = nil
		out.Message = scheduleResolutionMessage(out.Resolution)
		return out
	}
	out.Status = periodStatus(out.Period, hemi, today)
	out.Selectable = phaseMatchesMethod(fact.Phase, method)
	if out.FirstHarvestUnknown {
		out.Message = "Typical seasonal harvest guidance; a new planting's first harvest is unknown."
	}
	return out
}
func projectAnnual(a *AnnualPeriod, year int) (*Period, string) {
	if !validYear(year) {
		return nil, "OUT_OF_RANGE"
	}
	switch a.Precision {
	case "DATE_RANGE":
		endYear := year
		if a.Dates.To.Month < a.Dates.From.Month || (a.Dates.To.Month == a.Dates.From.Month && a.Dates.To.Day < a.Dates.From.Day) {
			endYear++
		}
		if !validYear(endYear) {
			return nil, "OUT_OF_RANGE"
		}
		p := &Period{Precision: DateRangePrecision, Dates: &DateRange{From: fmt.Sprintf("%04d-%02d-%02d", year, a.Dates.From.Month, a.Dates.From.Day), To: fmt.Sprintf("%04d-%02d-%02d", endYear, a.Dates.To.Month, a.Dates.To.Day)}}
		if len(ValidatePeriod(p)) > 0 {
			return nil, "INVALID_DATE"
		}
		return p, "RESOLVED"
	case "MONTH_RANGE":
		endYear := year
		if a.Months.To < a.Months.From {
			endYear++
		}
		if !validYear(endYear) {
			return nil, "OUT_OF_RANGE"
		}
		return &Period{Precision: MonthRangePrecision, Months: &MonthRange{From: fmt.Sprintf("%04d-%02d", year, a.Months.From), To: fmt.Sprintf("%04d-%02d", endYear, a.Months.To)}}, "RESOLVED"
	case "SEASON":
		return &Period{Precision: SeasonPrecision, Season: &SeasonPeriod{Season: *a.Season, Year: year}}, "RESOLVED"
	}
	return nil, "UNKNOWN"
}
func projectRelative(r *RelativeWindow, anchors []ScheduleAnchor, year int) (*Period, string) {
	if r.Anchor == "UNKNOWN" {
		return nil, "NEEDS_ANCHOR"
	}
	if r.FromDays == nil || r.ToDays == nil {
		return nil, "NEEDS_OFFSET"
	}
	for _, anchor := range anchors {
		if anchor.Kind != r.Anchor {
			continue
		}
		date, _ := time.Parse(time.DateOnly, anchor.On)
		if date.Year() != year {
			return nil, "OUT_OF_RANGE"
		}
		from, to := date.AddDate(0, 0, *r.FromDays), date.AddDate(0, 0, *r.ToDays)
		if !validYear(from.Year()) || !validYear(to.Year()) {
			return nil, "OUT_OF_RANGE"
		}
		return &Period{Precision: DateRangePrecision, Dates: &DateRange{From: from.Format(time.DateOnly), To: to.Format(time.DateOnly)}}, "RESOLVED"
	}
	return nil, "NEEDS_ANCHOR"
}
func scheduleResolutionMessage(state string) string {
	switch state {
	case "NEEDS_ANCHOR":
		return "Supply the named anchor date as a planning assumption; no date was guessed."
	case "NEEDS_OFFSET":
		return "The source does not supply a complete day interval."
	case "NEEDS_HEMISPHERE":
		return "Confirm a hemisphere before placing this season on the calendar."
	case "HEMISPHERE_MISMATCH":
		return "The source hemisphere conflicts with the intended site."
	case "INVALID_DATE":
		return "This source date does not exist in that year; review it without substituting another day."
	case "OUT_OF_RANGE":
		return "The supported period is outside years 1–9999 or the selected occurrence year."
	}
	return "The source does not provide a supported calendar window."
}
