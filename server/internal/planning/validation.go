package planning

import (
	"strings"
	"time"
	"unicode/utf8"

	dbgen "canopy/server/db/generated"
	"canopy/server/internal/money"
	"github.com/jackc/pgx/v5/pgtype"
)

func contains(value string, allowed ...string) bool {
	for _, a := range allowed {
		if value == a {
			return true
		}
	}
	return false
}
func active(state dbgen.PlantingPlanState) bool {
	return contains(string(state), "IDEA", "PLANNED", "IN_PROGRESS")
}
func normalizedOptional(in *string) *string {
	if in == nil {
		return nil
	}
	v := strings.TrimSpace(*in)
	return &v
}

// Storage conversion validates again below transport so CLI/agent callers share
// exactly the same rules. Unknown and explicitly free remain different values.
func validateIntent(in Intent) (dbgen.InsertPlantingPlanParams, []Problem) {
	p := dbgen.InsertPlantingPlanParams{Name: strings.TrimSpace(in.Name), Category: dbgen.PlantCategory(in.Category)}
	var problems []Problem
	add := func(field, msg string) { problems = append(problems, invalid(field, msg)...) }
	if n := utf8.RuneCountInString(p.Name); n < 1 || n > 160 {
		add("input.name", "Enter a name between 1 and 160 characters.")
	}
	for _, f := range []struct {
		field string
		value *string
		dest  *pgtype.Text
	}{
		{"scientificName", in.ScientificName, &p.ScientificName}, {"cultivar", in.Cultivar, &p.Cultivar},
	} {
		v := normalizedOptional(f.value)
		*f.dest = text(v)
		if v != nil {
			if n := utf8.RuneCountInString(*v); n < 1 || n > 160 {
				add("input."+f.field, "Enter between 1 and 160 characters, or leave unknown.")
			}
		}
	}
	if p.Category == "" {
		p.Category = dbgen.PlantCategoryOTHER
	}
	if !contains(string(p.Category), "CITRUS", "FIG", "STRAWBERRY", "BLACKBERRY", "OTHER") {
		add("input.category", "Choose a valid category.")
	}
	if in.DesiredQuantity != nil {
		p.DesiredQuantity = pgtype.Int4{Int32: *in.DesiredQuantity, Valid: true}
		if *in.DesiredQuantity <= 0 {
			add("input.desiredQuantity", "Enter a positive quantity, or leave unknown.")
		}
	}
	if in.Method != nil {
		p.Method = dbgen.NullPlantingMethod{PlantingMethod: dbgen.PlantingMethod(*in.Method), Valid: true}
		if !contains(*in.Method, "START_INDOORS", "DIRECT_SOW", "NURSERY_PLANT") {
			add("input.method", "Choose a starting method.")
		}
	}
	if in.IntendedPlacement != nil {
		p.IntendedPlacement = dbgen.NullContextPlacement{ContextPlacement: dbgen.ContextPlacement(*in.IntendedPlacement), Valid: true}
		if !contains(*in.IntendedPlacement, "CONTAINER", "GROUND") {
			add("input.intendedPlacement", "Choose container or ground.")
		}
	}
	if in.IntendedLocationID != nil {
		var ok bool
		p.IntendedLocationID, ok = parseID(*in.IntendedLocationID)
		if !ok {
			add("input.intendedLocationId", "Choose a valid location.")
		}
	}
	periodProblems := ValidatePeriod(in.TargetPeriod)
	for _, e := range periodProblems {
		add(e.Field, e.Message)
	}
	if len(periodProblems) == 0 {
		putPeriod(&p, in.TargetPeriod)
	}
	if in.Budget != nil {
		value, err := money.Parse(in.Budget.MinorUnits)
		if err != nil {
			add("input.budget.minorUnits", "Enter canonical nonnegative minor units, at most 1000000000000000.")
		} else {
			p.BudgetMinor = pgtype.Int8{Int64: value, Valid: true}
		}
		if !money.ValidCurrency(in.Budget.Currency) {
			add("input.budget.currency", "Use a three-letter uppercase currency code.")
		}
		p.BudgetCurrency = pgtype.Text{String: in.Budget.Currency, Valid: true}
	}
	p.Notes = text(in.Notes)
	if in.Notes != nil && utf8.RuneCountInString(*in.Notes) > 20000 {
		add("input.notes", "Keep notes within 20,000 characters.")
	}
	return p, problems
}
func putPeriod(p *dbgen.InsertPlantingPlanParams, v *Period) {
	if v == nil {
		return
	}
	p.TargetPrecision = dbgen.NullPlantingPeriodPrecision{PlantingPeriodPrecision: dbgen.PlantingPeriodPrecision(v.Precision), Valid: true}
	if v.Dates != nil {
		from, _ := time.Parse(time.DateOnly, v.Dates.From)
		to, _ := time.Parse(time.DateOnly, v.Dates.To)
		p.TargetFrom = pgtype.Date{Time: from, Valid: true}
		p.TargetTo = pgtype.Date{Time: to, Valid: true}
	}
	if v.Months != nil {
		p.TargetFromMonth = text(&v.Months.From)
		p.TargetToMonth = text(&v.Months.To)
	}
	if v.Season != nil {
		p.TargetYear = pgtype.Int4{Int32: int32(v.Season.Year), Valid: true}
		p.TargetSeason = dbgen.NullPlantingSeason{PlantingSeason: dbgen.PlantingSeason(v.Season.Season), Valid: true}
	}
	if v.Year != nil {
		p.TargetYear = pgtype.Int4{Int32: int32(*v.Year), Valid: true}
	}
}
func PeriodFromRow(row dbgen.PlantingPlan) *Period {
	if !row.TargetPrecision.Valid {
		return nil
	}
	p := &Period{Precision: PeriodPrecision(row.TargetPrecision.PlantingPeriodPrecision)}
	switch p.Precision {
	case DateRangePrecision:
		p.Dates = &DateRange{From: row.TargetFrom.Time.Format(time.DateOnly), To: row.TargetTo.Time.Format(time.DateOnly)}
	case MonthRangePrecision:
		p.Months = &MonthRange{From: row.TargetFromMonth.String, To: row.TargetToMonth.String}
	case SeasonPrecision:
		p.Season = &SeasonPeriod{Season: Season(row.TargetSeason.PlantingSeason), Year: int(row.TargetYear.Int32)}
	case YearPrecision:
		y := int(row.TargetYear.Int32)
		p.Year = &y
	}
	return p
}
func validateAction(in ActionInput) (dbgen.InsertPlantingActionParams, []Problem) {
	p := dbgen.InsertPlantingActionParams{Kind: dbgen.PlantingActionKind(in.Kind), Title: strings.TrimSpace(in.Title), Notes: text(in.Notes)}
	var problems []Problem
	add := func(field, msg string) { problems = append(problems, invalid(field, msg)...) }
	if !contains(in.Kind, "PREPARE", "BUY", "START_INDOORS", "DIRECT_SOW", "TRANSPLANT", "PLANT_OUT") {
		add("input.kind", "Choose an action kind.")
	}
	if n := utf8.RuneCountInString(p.Title); n < 1 || n > 160 {
		add("input.title", "Enter a title between 1 and 160 characters.")
	}
	if !validDate(in.ScheduledOn) {
		add("input.scheduledOn", "Select a real day as YYYY-MM-DD.")
	} else {
		t, _ := time.Parse(time.DateOnly, in.ScheduledOn)
		p.ScheduledOn = pgtype.Date{Time: t, Valid: true}
	}
	if in.ReminderOn != nil {
		if !validDate(*in.ReminderOn) {
			add("input.reminderOn", "Select a real reminder day as YYYY-MM-DD.")
		} else if validDate(in.ScheduledOn) && *in.ReminderOn > in.ScheduledOn {
			add("input.reminderOn", "The reminder cannot follow the action day.")
		} else {
			t, _ := time.Parse(time.DateOnly, *in.ReminderOn)
			p.ReminderOn = pgtype.Date{Time: t, Valid: true}
		}
	}
	if in.Notes != nil && utf8.RuneCountInString(*in.Notes) > 20000 {
		add("input.notes", "Keep notes within 20,000 characters.")
	}
	return p, problems
}

// LegalTransition is pure. Completion additionally requires zero open actions.
func LegalTransition(from, to string) bool {
	if from == to {
		return contains(from, "IDEA", "PLANNED", "IN_PROGRESS", "COMPLETED", "CANCELLED")
	}
	switch from {
	case "IDEA":
		return contains(to, "PLANNED", "IN_PROGRESS")
	case "PLANNED":
		return contains(to, "IDEA", "IN_PROGRESS")
	case "IN_PROGRESS":
		return to == "COMPLETED"
	}
	return false
}
