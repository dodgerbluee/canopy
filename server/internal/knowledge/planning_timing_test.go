package knowledge

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"canopy/server/internal/planning"
)

func plantingFixtureSource() Source {
	return Source{ID: "fixture-fig", Crop: "fig", Country: "US", Regions: []string{"Texas"}, Title: "Synthetic planting guide", Publisher: "Fixture publisher", Scope: "Synthetic test region; not planting advice.", URL: "https://fixture.example.invalid/guide", Hemisphere: "NORTHERN"}
}
func TestPlantingTimingPrecisionAndDistinctWindows(t *testing.T) {
	for _, tc := range []struct {
		text              string
		count             int
		precision, annual string
		from, to          int
	}{
		{"December through February", 1, "ANNUAL", "MONTH_RANGE", 12, 2},
		{"March and September", 2, "ANNUAL", "MONTH_RANGE", 3, 3},
		{"March through May, September through October", 2, "ANNUAL", "MONTH_RANGE", 3, 5},
		{"late winter and early spring", 2, "ANNUAL", "SEASON", 0, 0},
		{"winter through spring", 2, "ANNUAL", "SEASON", 0, 0},
		{"February 29 to March 2", 1, "ANNUAL", "DATE_RANGE", 0, 0},
		{"Jan. 15 through Feb. 20", 1, "ANNUAL", "DATE_RANGE", 0, 0},
		{"March 1-15", 1, "ANNUAL", "DATE_RANGE", 0, 0},
		{"after harvest", 1, "RELATIVE", "", 0, 0},
		{"two months after planting", 1, "RELATIVE", "", 0, 0},
		{"after mid-April", 1, "UNKNOWN", "", 0, 0},
		{"Do not plant before May", 1, "UNKNOWN", "", 0, 0},
		{"February 30", 1, "UNKNOWN", "", 0, 0},
		{"may need protection", 1, "UNKNOWN", "", 0, 0},
		{"March 1, 2027", 1, "UNKNOWN", "", 0, 0},
	} {
		t.Run(tc.text, func(t *testing.T) {
			w := parsePlantingTiming(tc.text, "NORTHERN")
			if len(w) != tc.count || w[0].Precision != tc.precision {
				t.Fatalf("%s: %+v", tc.text, w)
			}
			if tc.annual != "" {
				if w[0].Annual == nil || w[0].Annual.Precision != tc.annual {
					t.Fatal(w)
				}
				if tc.annual == "MONTH_RANGE" && (w[0].Annual.Months.From != tc.from || w[0].Annual.Months.To != tc.to) {
					t.Fatal(w[0])
				}
			}
		})
	}
	w := parsePlantingTiming("March 1-15", "NORTHERN")
	if w[0].Annual.Dates.To.Day != 15 {
		t.Fatal("lost same-month range end", w)
	}
	dates := parsePlantingTiming("2027-03-01 and 2027-09-01", "NORTHERN")
	if len(dates) != 2 || dates[0].Absolute.Dates.From != dates[0].Absolute.Dates.To {
		t.Fatal("invented interval between discrete dates", dates)
	}
}
func TestPlantingRelativeAnchorsAndUnknownOffsets(t *testing.T) {
	for _, tc := range []struct {
		text, anchor string
		from, to     int
	}{
		{"6 to 8 weeks before the last spring frost", "LAST_FROST", -56, -42},
		{"100 days before the first expected frost", "FIRST_FROST", -100, -100},
		{"Fall plantings: about 100 days before the first frost", "FIRST_FROST", -100, -100},
		{"50 to 85 days from transplanting", "TRANSPLANTING", 50, 85},
		{"14 days after sowing", "SOWING", 14, 14},
	} {
		w := parsePlantingTiming(tc.text, "NORTHERN")
		if len(w) != 1 || w[0].Relative == nil {
			t.Fatal(tc.text, w)
		}
		r := w[0].Relative
		if r.Anchor != tc.anchor || r.FromDays == nil || r.ToDays == nil || *r.FromDays != tc.from || *r.ToDays != tc.to {
			t.Fatal(tc.text, r)
		}
	}
	for _, text := range []string{"after harvest", "two months after planting"} {
		w := parsePlantingTiming(text, "NORTHERN")
		if w[0].Relative.FromDays != nil || w[0].Relative.ToDays != nil {
			t.Fatal("unknown became zero", w)
		}
	}
	for _, hemi := range []string{"NORTHERN", "SOUTHERN", ""} {
		w := parsePlantingTiming("winter", hemi)
		if w[0].Annual.Season == nil || *w[0].Annual.Season != planning.Winter || w[0].Absolute != nil {
			t.Fatal("season projected prematurely", w)
		}
	}
}
func TestPlantingSourceEvidenceAndCompleteness(t *testing.T) {
	source := plantingFixtureSource()
	text := "Plant nursery fig trees in December through February. Harvest ripe fruit in September continuing through October."
	good := PlantingDraft{Phase: "PLANT_OUT", Label: "Planting", Summary: "Nursery trees have a winter planting window.", Evidence: "Plant nursery fig trees in December through February.", Timing: "December through February", Methods: []string{"NURSERY_PLANT"}}
	facts, err := validatePlantingSource(source, text, []PlantingDraft{good}, time.Now(), strings.Repeat("a", 64))
	if err != nil || len(facts) != 1 {
		t.Fatal(facts, err)
	}
	if facts[0].Fact.Windows[0].Annual.Months.From != 12 || facts[0].Fact.Windows[0].Annual.Months.To != 2 {
		t.Fatal(facts)
	}
	for name, edit := range map[string]func(*PlantingDraft){
		"fabricated evidence": func(d *PlantingDraft) { d.Evidence = "Plant only on the date the assistant chooses." },
		"fabricated date":     func(d *PlantingDraft) { d.Timing = "March" },
		"truncated window":    func(d *PlantingDraft) { d.Timing = "December" },
		"wrong phase":         func(d *PlantingDraft) { d.Phase = "BLOOM" },
		"made up subject":     func(d *PlantingDraft) { d.Subjects = []string{"made up cultivar"} },
		"made up locality":    func(d *PlantingDraft) { d.Localities = []string{"North Pole"} },
	} {
		t.Run(name, func(t *testing.T) {
			d := good
			edit(&d)
			if _, err := validatePlantingSource(source, text, []PlantingDraft{d}, time.Now(), "test"); err == nil {
				t.Fatal("unsupported draft accepted")
			}
		})
	}
	harvest := PlantingDraft{Phase: "HARVEST", Label: "Fruit", Summary: "Ripening continues into autumn.", Evidence: "Harvest ripe fruit in September continuing through October.", Timing: "September"}
	if _, err := validatePlantingSource(source, text, []PlantingDraft{harvest}, time.Now(), "test"); err == nil {
		t.Fatal("truncated harvest accepted")
	}
	multi := "Harvest fruit in May. Harvest the second crop in August through October."
	draft := PlantingDraft{Phase: "HARVEST", Label: "Crop", Summary: "Fruit has a source-supported crop window.", Evidence: "Harvest fruit in May.", Timing: "May"}
	facts, err = validatePlantingSource(source, multi, []PlantingDraft{draft}, time.Now(), "test")
	if err != nil || facts[0].Fact.Windows[0].Precision != "UNKNOWN" {
		t.Fatal("partial source phase calendar remained precise", facts, err)
	}
}
func TestPlantingTimingMustRetainRelativeAndDayBounds(t *testing.T) {
	source := plantingFixtureSource()
	for _, tc := range []struct{ evidence, timing string }{
		{"Start seeds indoors 6 to 8 weeks before the last frost.", "8 weeks before the last frost"},
		{"Plant nursery trees from March 1 to April 15.", "March 1 to April"},
	} {
		d := PlantingDraft{Phase: "PLANT_OUT", Label: "Window", Summary: "Use the complete source timing.", Evidence: tc.evidence, Timing: tc.timing}
		if strings.HasPrefix(tc.evidence, "Start") {
			d.Phase = "START_INDOORS"
		}
		if _, err := validatePlantingSource(source, tc.evidence, []PlantingDraft{d}, time.Now(), "test"); err == nil {
			t.Fatal("accepted incomplete precise bounds", tc)
		}
	}
}
func TestPlantingSubtypeMethodAndRegionFiltering(t *testing.T) {
	source := plantingFixtureSource()
	source.Crop = "citrus"
	evidence := "Lemons bloom in March."
	d := PlantingDraft{Phase: "BLOOM", Label: "Flowers", Summary: "Lemon flowers appear in the source's spring window.", Evidence: evidence, Timing: "March"}
	facts, err := validatePlantingSource(source, evidence, []PlantingDraft{d}, time.Now(), "test")
	if err != nil {
		t.Fatal(err)
	}
	if matchesRestriction("Owari satsuma mandarin", facts[0].Subjects) || !matchesRestriction("Meyer lemon", facts[0].Subjects) {
		t.Fatal("plural lemon advice became generic", facts)
	}
	if cropFor("Tomatillo", "Physalis philadelphica", "OTHER") != "" || cropFor("Tomato", "Solanum lycopersicum", "OTHER") != "tomato" {
		t.Fatal("tomato taxonomy")
	}
	if len(Applicable(Registry(), "GB", "England", "strawberry")) != 1 || len(Applicable(Registry(), "US", "England", "strawberry")) != 0 {
		t.Fatal("country scope")
	}
	// Snapshot JSON retains exact evidence but shape validation stays independent
	// of provider-specific code and accepts the original PLAN01 contract fixtures.
	data, err := json.Marshal([]planning.GuidanceFact{facts[0].Fact})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = planning.DecodeGuidanceFacts(data); err != nil {
		t.Fatal(err)
	}
}

func TestPlantingRelativeQualifierIsNotAnotherWindow(t *testing.T) {
	for _, text := range []string{"Plant in spring or 100 days before the first frost if tomatoes need protection", "Plant in March or 6 weeks before last frost"} {
		if w := parsePlantingTiming(text, "NORTHERN"); w[0].Precision != "UNKNOWN" {
			t.Fatal("discarded an independent window", w)
		}
	}
	w := parsePlantingTiming("Fall plantings may be placed 100 days before the first frost", "NORTHERN")
	if w[0].Relative == nil || w[0].Relative.Anchor != "FIRST_FROST" {
		t.Fatal("crop qualifier/modal may blocked relative timing", w)
	}
}
func TestPlantingMissingSubtypeCropStillLosesPrecisePartialCalendar(t *testing.T) {
	source := plantingFixtureSource()
	source.Crop = "citrus"
	text := "Lemons bloom in March. Lemons bloom again in October. Mandarins bloom in April."
	draft := PlantingDraft{Phase: "BLOOM", Label: "Flowering", Summary: "Lemon flowers have a source window.", Evidence: "Lemons bloom in March.", Timing: "March"}
	facts, err := validatePlantingSource(source, text, []PlantingDraft{draft}, time.Now(), "test")
	if err != nil || facts[0].Fact.Windows[0].Precision != "UNKNOWN" {
		t.Fatal("omitted same-subtype window stayed precise", facts, err)
	}
	text = "Lemons bloom in March. Mandarins bloom in April."
	facts, err = validatePlantingSource(source, text, []PlantingDraft{draft}, time.Now(), "test")
	if err != nil || facts[0].Fact.Windows[0].Precision != "ANNUAL" {
		t.Fatal("different subtype poisoned valid window", facts, err)
	}
}

func TestPlanningSourceAdapterExcludesNeighboringCrop(t *testing.T) {
	source := Source{ID: "osu-tomato"}
	text := "Tomatoes\nPlant tomatoes in May.\nTomatillos\nPlant these in June."
	got := planningSourceText(source, text)
	if strings.Contains(got, "June") || !strings.Contains(got, "May") {
		t.Fatal("neighboring crop leaked", got)
	}
	if got := planningSourceText(source, "Tomatoes and tomatillos: an unrecognized publication layout."); got != "" {
		t.Fatal("missing reviewed section boundary did not fail closed", got)
	}
}

func TestPlantingUsesDirectSourceFallbackWithoutRelaxingEvidence(t *testing.T) {
	source := plantingFixtureSource()
	text := "Transplant fall tomatoes about 100 days before the first frost."
	bad := PlantingDraft{Phase: "PLANT_OUT", Label: "Invented", Summary: "Plant in June.", Evidence: "A fabricated quotation that is not present.", Timing: "June"}
	facts, err := usablePlantingSource(source, text, []PlantingDraft{bad}, time.Now(), strings.Repeat("a", 64))
	if err != nil || len(facts) != 1 || facts[0].Fact.Phase != "TRANSPLANT" || facts[0].Fact.Windows[0].Relative == nil || *facts[0].Fact.Windows[0].Relative.FromDays != -100 {
		t.Fatal("complete direct source fallback failed", facts, err)
	}
	if facts[0].Fact.ExtractionModel == strings.Repeat("a", 64) {
		t.Fatal("deterministic extraction was attributed to the model")
	}
}
