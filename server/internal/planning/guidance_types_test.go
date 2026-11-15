package planning

import "testing"

func TestGuidanceAnnualShapePreservesLeapDayAndRejectsImpossibleMonth(t *testing.T) {
	p := &AnnualPeriod{Precision: "DATE_RANGE", Dates: &AnnualDateRange{From: &MonthDay{Month: 2, Day: 29}, To: &MonthDay{Month: 3, Day: 1}}}
	if !ValidAnnualPeriod(p) {
		t.Fatal("annual leap day must remain representable")
	}
	p.Dates.From.Day = 30
	if ValidAnnualPeriod(p) {
		t.Fatal("impossible annual day accepted")
	}
	if ValidAnnualPeriod(&AnnualPeriod{Precision: "MONTH_RANGE", Months: &AnnualMonthRange{From: 0, To: 12}}) {
		t.Fatal("invalid annual month")
	}
	p = &AnnualPeriod{Precision: "SEASON", Season: ptr(Winter), Months: &AnnualMonthRange{From: 12, To: 2}}
	if ValidAnnualPeriod(p) {
		t.Fatal("mixed annual bodies")
	}
}
