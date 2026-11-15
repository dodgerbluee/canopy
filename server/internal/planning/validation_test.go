package planning

import (
	"strings"
	"testing"
)

func ptr[T any](v T) *T { return &v }
func TestIntentValidationPreservesUnknownAndExactValues(t *testing.T) {
	for _, budget := range []*Budget{nil, {MinorUnits: "0", Currency: "USD"}, {MinorUnits: "1000000000000000", Currency: "JPY"}} {
		in := Intent{Name: "  " + strings.Repeat("🌱", 160) + "  ", Budget: budget, TargetPeriod: &Period{Precision: YearPrecision, Year: ptr(2027)}}
		out, ps := validateIntent(in)
		if len(ps) > 0 {
			t.Fatal(ps)
		}
		if out.Name != strings.Repeat("🌱", 160) || out.DesiredQuantity.Valid || out.Method.Valid || out.IntendedPlacement.Valid || out.IntendedLocationID.Valid {
			t.Fatal("unknowns or Unicode label changed")
		}
		if out.TargetFrom.Valid || out.TargetTo.Valid || !out.TargetYear.Valid {
			t.Fatal("fabricated concrete day")
		}
		if out.BudgetMinor.Valid != (budget != nil) {
			t.Fatal("unknown budget became zero")
		}
	}
}
func TestInvalidIntentAndActionValues(t *testing.T) {
	for name, edit := range map[string]func(*Intent){
		"blank name": func(v *Intent) { v.Name = "  " }, "long Unicode name": func(v *Intent) { v.Name = strings.Repeat("🌱", 161) },
		"blank cultivar": func(v *Intent) { v.Cultivar = ptr(" ") }, "bad category": func(v *Intent) { v.Category = "ANY" },
		"zero count": func(v *Intent) { v.DesiredQuantity = ptr(int32(0)) }, "bad method": func(v *Intent) { v.Method = ptr("UNKNOWN") },
		"bad location": func(v *Intent) { v.IntendedLocationID = ptr("bad") }, "bad placement": func(v *Intent) { v.IntendedPlacement = ptr("ROOF") },
		"long note":          func(v *Intent) { v.Notes = ptr(strings.Repeat("🌱", 20001)) },
		"leading zero money": func(v *Intent) { v.Budget = &Budget{MinorUnits: "01", Currency: "USD"} },
		"large money":        func(v *Intent) { v.Budget = &Budget{MinorUnits: "1000000000000001", Currency: "USD"} },
		"negative money":     func(v *Intent) { v.Budget = &Budget{MinorUnits: "-1", Currency: "USD"} },
		"lowercase currency": func(v *Intent) { v.Budget = &Budget{MinorUnits: "0", Currency: "usd"} },
		"invalid period": func(v *Intent) {
			v.TargetPeriod = &Period{Precision: MonthRangePrecision, Months: &MonthRange{From: "2027-13", To: "2028-01"}}
		},
	} {
		t.Run(name, func(t *testing.T) {
			in := Intent{Name: "Fig"}
			edit(&in)
			_, ps := validateIntent(in)
			if len(ps) == 0 {
				t.Fatal("accepted invalid intent")
			}
		})
	}
	for _, in := range []ActionInput{
		{Kind: "WATER", Title: "X", ScheduledOn: "2027-01-01"},
		{Kind: "BUY", Title: " ", ScheduledOn: "2027-01-01"},
		{Kind: "BUY", Title: "X", ScheduledOn: "2027-02-29"},
		{Kind: "BUY", Title: "X", ScheduledOn: "0000-01-01"},
		{Kind: "BUY", Title: "X", ScheduledOn: "2027-01-01T00:00:00Z"},
		{Kind: "BUY", Title: "X", ScheduledOn: "2027-01-01", ReminderOn: ptr("2027-01-02")},
	} {
		if _, ps := validateAction(in); len(ps) == 0 {
			t.Fatalf("accepted %+v", in)
		}
	}
}
func TestLegalTransitions(t *testing.T) {
	allowed := map[string]bool{"IDEA/PLANNED": true, "IDEA/IN_PROGRESS": true, "PLANNED/IDEA": true, "PLANNED/IN_PROGRESS": true, "IN_PROGRESS/COMPLETED": true}
	states := []string{"IDEA", "PLANNED", "IN_PROGRESS", "COMPLETED", "CANCELLED"}
	for _, from := range states {
		for _, to := range states {
			want := from == to || allowed[from+"/"+to]
			if got := LegalTransition(from, to); got != want {
				t.Fatalf("%s -> %s = %v", from, to, got)
			}
		}
	}
	if LegalTransition("ANY", "ANY") {
		t.Fatal("invalid state no-op")
	}
}
