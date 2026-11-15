package planning

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func year(n int) *int { return &n }
func TestPeriodPreservesCivilPrecision(t *testing.T) {
	cases := []struct {
		name string
		p    *Period
	}{
		{"unscheduled", nil},
		{"year without a fabricated day", &Period{Precision: YearPrecision, Year: year(2027)}},
		{"abstract season", &Period{Precision: SeasonPrecision, Season: &SeasonPeriod{Season: Winter, Year: 2027}}},
		{"months cross year", &Period{Precision: MonthRangePrecision, Months: &MonthRange{From: "2026-12", To: "2027-02"}}},
		{"leap day", &Period{Precision: DateRangePrecision, Dates: &DateRange{From: "2028-02-29", To: "2028-03-01"}}},
		{"civil dates span DST", &Period{Precision: DateRangePrecision, Dates: &DateRange{From: "2027-03-13", To: "2027-03-15"}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			before, _ := json.Marshal(tc.p)
			if problems := ValidatePeriod(tc.p); len(problems) > 0 {
				t.Fatal(problems)
			}
			after, _ := json.Marshal(tc.p)
			if string(before) != string(after) {
				t.Fatal("validation changed user's period")
			}
			if tc.p != nil && tc.p.Precision != DateRangePrecision && strings.Contains(string(after), "\"dates\"") {
				t.Fatal("coarse intent became exact dates")
			}
		})
	}
}
func TestPeriodRejectsInvalidAndAmbiguousInput(t *testing.T) {
	cases := []struct {
		name  string
		p     Period
		field string
	}{
		{"missing body", Period{Precision: YearPrecision}, "precision"},
		{"mixed shapes", Period{Precision: YearPrecision, Year: year(2027), Dates: &DateRange{From: "2027-01-01", To: "2027-01-02"}}, "precision"},
		{"wrong body", Period{Precision: YearPrecision, Months: &MonthRange{From: "2027-01", To: "2027-02"}}, "year"},
		{"invalid precision", Period{Precision: "APPROXIMATE", Year: year(2027)}, "precision"},
		{"year zero", Period{Precision: YearPrecision, Year: year(0)}, "year"},
		{"year too large", Period{Precision: YearPrecision, Year: year(10000)}, "year"},
		{"bad season", Period{Precision: SeasonPrecision, Season: &SeasonPeriod{Season: "MONSOON", Year: 2027}}, "season.season"},
		{"bad season year", Period{Precision: SeasonPrecision, Season: &SeasonPeriod{Season: Spring, Year: -1}}, "season.year"},
		{"day overflow", Period{Precision: DateRangePrecision, Dates: &DateRange{From: "2027-02-29", To: "2027-03-01"}}, "dates.from"},
		{"reversed dates", Period{Precision: DateRangePrecision, Dates: &DateRange{From: "2027-03-02", To: "2027-03-01"}}, "dates.to"},
		{"date is instant", Period{Precision: DateRangePrecision, Dates: &DateRange{From: "2027-03-01T00:00:00Z", To: "2027-03-02"}}, "dates.from"},
		{"blank date", Period{Precision: DateRangePrecision, Dates: &DateRange{From: "2027-03-01"}}, "dates.to"},
		{"date year zero", Period{Precision: DateRangePrecision, Dates: &DateRange{From: "0000-01-01", To: "2027-03-02"}}, "dates.from"},
		{"month overflow", Period{Precision: MonthRangePrecision, Months: &MonthRange{From: "2027-13", To: "2028-01"}}, "months.from"},
		{"month not canonical", Period{Precision: MonthRangePrecision, Months: &MonthRange{From: "2027-1", To: "2027-02"}}, "months.from"},
		{"reversed months", Period{Precision: MonthRangePrecision, Months: &MonthRange{From: "2027-02", To: "2027-01"}}, "months.to"},
		{"month year zero", Period{Precision: MonthRangePrecision, Months: &MonthRange{From: "0000-01", To: "2027-01"}}, "months.from"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			errors := ValidatePeriod(&tc.p)
			if len(errors) != 1 || errors[0].Field != "input.targetPeriod."+tc.field {
				t.Fatalf("unexpected errors: %+v", errors)
			}
		})
	}
}
func TestCanonicalYearMonth(t *testing.T) {
	y, m, err := CanonicalYearMonth("2027-02")
	if err != nil || y != 2027 || m != time.February {
		t.Fatalf("%v %v %v", y, m, err)
	}
	for _, input := range []string{"2027-2", " 2027-02", "2027-00", "2027-13", "0000-02", "2027-02-01"} {
		if _, _, err := CanonicalYearMonth(input); err == nil {
			t.Fatalf("accepted %q", input)
		}
	}
}
