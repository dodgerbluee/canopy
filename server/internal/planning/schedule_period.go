package planning

import (
	"errors"
	"fmt"
	"time"
)

var ErrInvalidSchedule = errors.New("invalid planting schedule")

// Comparisons may expand coarse periods to their inclusive civil bounds. These
// internal bounds never change the stored precision or become scheduled actions.
func periodBounds(p *Period, hemisphere string) (string, string, string) {
	if p == nil {
		return "", "", "UNKNOWN"
	}
	if len(ValidatePeriod(p)) > 0 {
		return "", "", "INVALID_DATE"
	}
	switch p.Precision {
	case DateRangePrecision:
		return p.Dates.From, p.Dates.To, "RESOLVED"
	case MonthRangePrecision:
		return p.Months.From + "-01", monthEnd(p.Months.To), "RESOLVED"
	case YearPrecision:
		return fmt.Sprintf("%04d-01-01", *p.Year), fmt.Sprintf("%04d-12-31", *p.Year), "RESOLVED"
	case SeasonPrecision:
		months, resolution := seasonMonths(p.Season.Season, p.Season.Year, hemisphere)
		if resolution != "RESOLVED" {
			return "", "", resolution
		}
		return months.From + "-01", monthEnd(months.To), "RESOLVED"
	}
	return "", "", "UNKNOWN"
}
func monthEnd(month string) string {
	y, m, _ := CanonicalYearMonth(month)
	return time.Date(y, m+1, 0, 0, 0, 0, 0, time.UTC).Format(time.DateOnly)
}
func seasonMonths(season Season, year int, hemisphere string) (*MonthRange, string) {
	if !contains(hemisphere, "NORTHERN", "SOUTHERN") {
		return nil, "NEEDS_HEMISPHERE"
	}
	start := map[Season]int{Spring: 3, Summer: 6, Autumn: 9, Winter: 12}[season]
	if hemisphere == "SOUTHERN" {
		start = (start+5)%12 + 1
	}
	end, endYear := start+2, year
	if end > 12 {
		end -= 12
		endYear++
	}
	if !validYear(year) || !validYear(endYear) {
		return nil, "OUT_OF_RANGE"
	}
	return &MonthRange{From: fmt.Sprintf("%04d-%02d", year, start), To: fmt.Sprintf("%04d-%02d", endYear, end)}, "RESOLVED"
}
func validateScheduleAssumptions(a ScheduleAssumptions) error {
	if a.Hemisphere != nil && !contains(*a.Hemisphere, "NORTHERN", "SOUTHERN") {
		return ErrInvalidSchedule
	}
	if len(a.Anchors) > MaxScheduleAnchors {
		return ErrInvalidSchedule
	}
	seen := map[string]bool{}
	for _, anchor := range a.Anchors {
		if !contains(anchor.Kind, "LAST_FROST", "FIRST_FROST", "SOWING", "TRANSPLANTING", "PLANTING", "HARVEST") || !validDate(anchor.On) || seen[anchor.Kind] {
			return ErrInvalidSchedule
		}
		seen[anchor.Kind] = true
	}
	return nil
}
func validateScheduleRange(r MonthRange) error {
	fromYear, fromMonth, err := CanonicalYearMonth(r.From)
	if err != nil {
		return ErrInvalidSchedule
	}
	toYear, toMonth, err := CanonicalYearMonth(r.To)
	count := (toYear-fromYear)*12 + int(toMonth-fromMonth) + 1
	if err != nil || count < 1 || count > 12 {
		return ErrInvalidSchedule
	}
	return nil
}
func periodStatus(p *Period, hemisphere, today string) string {
	if p == nil {
		return "UNSCHEDULED"
	}
	from, to, resolution := periodBounds(p, hemisphere)
	if resolution != "RESOLVED" {
		return "NEEDS_REVIEW"
	}
	if today < from {
		return "UPCOMING"
	}
	if today > to {
		return "PASSED"
	}
	return "IN_WINDOW"
}
func periodBand(p *Period, hemisphere string, view MonthRange) (*MonthRange, bool, bool) {
	from, to, resolution := periodBounds(p, hemisphere)
	if resolution != "RESOLVED" || to[:7] < view.From || from[:7] > view.To {
		return nil, false, false
	}
	band := &MonthRange{From: from[:7], To: to[:7]}
	before, after := band.From < view.From, band.To > view.To
	if before {
		band.From = view.From
	}
	if after {
		band.To = view.To
	}
	return band, before, after
}
func periodContained(chosen, supported *Period, hemisphere string) bool {
	from, to, state := periodBounds(chosen, hemisphere)
	sourceFrom, sourceTo, sourceState := periodBounds(supported, hemisphere)
	return state == "RESOLVED" && sourceState == "RESOLVED" && from >= sourceFrom && to <= sourceTo
}
func hemisphereValue(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}
func clonePeriod(p *Period) *Period {
	if p == nil {
		return nil
	}
	out := *p
	if p.Dates != nil {
		v := *p.Dates
		out.Dates = &v
	}
	if p.Months != nil {
		v := *p.Months
		out.Months = &v
	}
	if p.Season != nil {
		v := *p.Season
		out.Season = &v
	}
	if p.Year != nil {
		v := *p.Year
		out.Year = &v
	}
	return &out
}
