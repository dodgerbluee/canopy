// Package planning contains the future planting domain. No transport or model
// provider is needed to validate a user's intended period.
package planning

import (
	"regexp"
	"strconv"
	"time"
)

type PeriodPrecision string

const (
	DateRangePrecision  PeriodPrecision = "DATE_RANGE"
	MonthRangePrecision PeriodPrecision = "MONTH_RANGE"
	SeasonPrecision     PeriodPrecision = "SEASON"
	YearPrecision       PeriodPrecision = "YEAR"
)

type Season string

const (
	Spring Season = "SPRING"
	Summer Season = "SUMMER"
	Autumn Season = "AUTUMN"
	Winter Season = "WINTER"
)

type DateRange struct {
	From string `json:"from"`
	To   string `json:"to"`
}
type MonthRange struct {
	From string `json:"from"`
	To   string `json:"to"`
}
type SeasonPeriod struct {
	Season Season `json:"season"`
	Year   int    `json:"year"`
}

// Exactly one member is present, selected by Precision. A nil Period means
// unscheduled. Coarse intent never becomes a fabricated concrete date.
type Period struct {
	Precision PeriodPrecision `json:"precision"`
	Dates     *DateRange      `json:"dates,omitempty"`
	Months    *MonthRange     `json:"months,omitempty"`
	Season    *SeasonPeriod   `json:"season,omitempty"`
	Year      *int            `json:"year,omitempty"`
}
type FieldError struct {
	Field   string
	Message string
}

func (e FieldError) Error() string { return e.Field + ": " + e.Message }

var datePattern = regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}$`)
var monthPattern = regexp.MustCompile(`^[0-9]{4}-(0[1-9]|1[0-2])$`)

func validDate(raw string) bool {
	if !datePattern.MatchString(raw) || raw[:4] == "0000" {
		return false
	}
	_, err := time.Parse(time.DateOnly, raw)
	return err == nil
}
func validMonth(raw string) bool { return monthPattern.MatchString(raw) && raw[:4] != "0000" }
func validYear(year int) bool    { return year >= 1 && year <= 9999 }

func ValidatePeriod(period *Period) []FieldError {
	if period == nil {
		return nil
	}
	fail := func(field, message string) []FieldError {
		return []FieldError{{Field: "input.targetPeriod." + field, Message: message}}
	}
	members := 0
	if period.Dates != nil {
		members++
	}
	if period.Months != nil {
		members++
	}
	if period.Season != nil {
		members++
	}
	if period.Year != nil {
		members++
	}
	if members != 1 {
		return fail("precision", "Supply exactly one period matching its precision.")
	}
	switch period.Precision {
	case DateRangePrecision:
		if period.Dates == nil {
			return fail("dates", "Supply a date range only.")
		}
		if !validDate(period.Dates.From) {
			return fail("dates.from", "Enter a real date as YYYY-MM-DD.")
		}
		if !validDate(period.Dates.To) {
			return fail("dates.to", "Enter a real date as YYYY-MM-DD.")
		}
		if period.Dates.From > period.Dates.To {
			return fail("dates.to", "The end must be on or after the start.")
		}
	case MonthRangePrecision:
		if period.Months == nil {
			return fail("months", "Supply a month range only.")
		}
		if !validMonth(period.Months.From) {
			return fail("months.from", "Enter a month as YYYY-MM.")
		}
		if !validMonth(period.Months.To) {
			return fail("months.to", "Enter a month as YYYY-MM.")
		}
		if period.Months.From > period.Months.To {
			return fail("months.to", "The end must be on or after the start.")
		}
	case SeasonPrecision:
		if period.Season == nil {
			return fail("season", "Supply a season and its starting year only.")
		}
		if !validYear(period.Season.Year) {
			return fail("season.year", "Choose a year between 1 and 9999.")
		}
		switch period.Season.Season {
		case Spring, Summer, Autumn, Winter:
		default:
			return fail("season.season", "Choose spring, summer, autumn or winter.")
		}
	case YearPrecision:
		if period.Year == nil {
			return fail("year", "Supply a year only.")
		}
		if !validYear(*period.Year) {
			return fail("year", "Choose a year between 1 and 9999.")
		}
	default:
		return fail("precision", "Choose a supported period precision.")
	}
	return nil
}

// CanonicalYearMonth returns a civil month without consulting time.Local or an
// instant. Exposed for DB adapters; malformed values remain errors.
func CanonicalYearMonth(raw string) (year int, month time.Month, err error) {
	if !validMonth(raw) {
		return 0, 0, FieldError{Field: "month", Message: "Enter a month as YYYY-MM."}
	}
	year, _ = strconv.Atoi(raw[:4])
	value, _ := strconv.Atoi(raw[5:])
	return year, time.Month(value), nil
}
