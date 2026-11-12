// T17 unit verification: decimal round-trip fidelity, kind/unit validation
// tables and timeline summary construction — no DB required.
package journal

import (
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
)

func TestNumericStringRoundTrip(t *testing.T) {
	for _, s := range []string{
		"0", "2", "100", "1.5", "0.050", "-12.25", "-0.5",
		"12345678901234567890.5", "0.000001", "-273.15",
	} {
		n, ok := parseDecimal(s)
		if !ok {
			t.Fatalf("parseDecimal(%q) rejected", s)
		}
		if got := numericString(n); got != s {
			t.Fatalf("round-trip %q rendered %q", s, got)
		}
	}
}

func TestParseDecimalRejects(t *testing.T) {
	for _, s := range []string{"", "abc", "1e5", "1.2.3", "NaN", "Infinity", " 1", "1 ", "0x10", "--1"} {
		if _, ok := parseDecimal(s); ok {
			t.Fatalf("parseDecimal(%q) accepted", s)
		}
	}
}

func TestDecimalSign(t *testing.T) {
	cases := map[string]int{"-0.5": -1, "0": 0, "0.00": 0, "-0": 0, "7": 1, "-7.25": -1}
	for s, want := range cases {
		if got := decimalSign(s); got != want {
			t.Fatalf("decimalSign(%q)=%d want %d", s, got, want)
		}
	}
}

func ptr[T any](v T) *T { return &v }

func TestValidateActivityQuantity(t *testing.T) {
	// kind, quantity, unit, wantProblem (nil = OK)
	cases := []struct {
		kind, quantity, unit string
		wantProblem          bool
		wantField            string
	}{
		{"WATER", "2", "L", false, ""},
		{"WATER", "250", "ML", false, ""},
		{"WATER", "", "", false, ""},                // unmeasured watering remains unknown
		{"WATER", "2", "", true, "input.unit"},      // unit missing
		{"WATER", "", "L", true, "input.quantity"},  // quantity missing
		{"WATER", "2", "KG", true, "input.unit"},    // wrong dimension
		{"WATER", "0", "L", true, "input.quantity"}, // zero meaningless
		{"WATER", "-2", "L", true, "input.quantity"},
		{"FERTILIZE", "1.5", "KG", false, ""},
		{"FERTILIZE", "5", "G", false, ""},
		{"FERTILIZE", "20", "ML", false, ""},
		{"FERTILIZE", "1", "C", true, "input.unit"},
		{"PRUNE", "", "", false, ""},
		{"PRUNE", "2", "L", true, "input.unit"}, // carries nothing
		{"OBSERVE", "", "", false, ""},
		{"NOTE", "3", "G", true, "input.unit"},
		{"HARVEST", "", "", false, ""},
		{"REPOT", "", "", false, ""},
	}
	for _, c := range cases {
		var q, u *string
		if c.quantity != "" {
			q = ptr(c.quantity)
		}
		if c.unit != "" {
			u = ptr(c.unit)
		}
		_, _, p := validateActivityQuantity(c.kind, q, u)
		if c.wantProblem && p == nil {
			t.Fatalf("%s q=%q u=%q: want problem, got none", c.kind, c.quantity, c.unit)
		}
		if !c.wantProblem && p != nil {
			t.Fatalf("%s q=%q u=%q: unexpected problem %v", c.kind, c.quantity, c.unit, *p)
		}
		if p != nil && p.Field != c.wantField {
			t.Fatalf("%s q=%q u=%q: field %q want %q", c.kind, c.quantity, c.unit, p.Field, c.wantField)
		}
	}
}

func TestValidateActivityQuantityQuotesExactValues(t *testing.T) {
	_, _, p := validateActivityQuantity("WATER", ptr("2"), ptr("KG"))
	if p == nil {
		t.Fatal("want problem")
	}
	// must name the accepted set and the offending value, never a bare "invalid"
	if !strings.Contains(p.Message, "L or ML") || !strings.Contains(p.Message, "KG") {
		t.Fatalf("message %q must quote expected and found units", p.Message)
	}
}

func TestValidateMeasurementUnit(t *testing.T) {
	ok := [][2]string{{"VOLUME", "L"}, {"MASS", "G"}, {"LENGTH", "MM"}, {"COUNT", "COUNT"}, {"TEMPERATURE", "F"}}
	for _, c := range ok {
		if p := validateMeasurementUnit(c[0], c[1]); p != nil {
			t.Fatalf("%s/%s rejected: %v", c[0], c[1], *p)
		}
	}
	bad := [][2]string{{"LENGTH", "ML"}, {"MASS", "CM"}, {"COUNT", "G"}, {"TEMPERATURE", "KG"}, {"VOLUME", "COUNT"}}
	for _, c := range bad {
		if p := validateMeasurementUnit(c[0], c[1]); p == nil {
			t.Fatalf("%s/%s accepted", c[0], c[1])
		} else if p.Field != "input.unit" {
			t.Fatalf("%s/%s field %q", c[0], c[1], p.Field)
		}
	}
	if p := validateMeasurementUnit("DENSITY", "G"); p == nil || p.Field != "input.quantityKind" {
		t.Fatalf("unknown quantity kind must be refused on quantityKind, got %v", p)
	}
}

func TestActivitySummary(t *testing.T) {
	qty := func(s string) *pgtype.Numeric {
		n, ok := parseDecimal(s)
		if !ok {
			t.Fatalf("bad qty %q", s)
		}
		return &n
	}
	cases := []struct {
		kind                string
		quantity            *pgtype.Numeric
		unit, product, note *string
		want                string
	}{
		{"WATER", qty("2"), ptr("L"), nil, nil, "Watered 2 L"},
		{"WATER", nil, nil, nil, nil, "Watered"},
		{"FERTILIZE", qty("1.5"), ptr("KG"), ptr("10-10-10"), nil, "Fertilized 1.5 KG (10-10-10)"},
		{"PRUNE", nil, nil, nil, ptr("cut the water sprout"), "Pruned"},
		{"NOTE", nil, nil, nil, ptr("  new growth on the lower branch  "), "new growth on the lower branch"},
		{"NOTE", nil, nil, nil, ptr("   "), "Note"},
		{"OBSERVE", nil, nil, nil, ptr("leaf color deepening"), "leaf color deepening"},
		{"HARVEST", nil, nil, nil, nil, "Harvested"},
	}
	for _, c := range cases {
		if got := ActivitySummary(c.kind, c.quantity, c.unit, c.product, c.note); got != c.want {
			t.Fatalf("ActivitySummary(%s)=%q want %q", c.kind, got, c.want)
		}
	}
}

func TestActivitySummaryTruncatesRuneSafe(t *testing.T) {
	note := strings.Repeat("é", summaryMax+50)
	got := ActivitySummary("NOTE", nil, nil, nil, &note)
	if len([]rune(got)) != summaryMax {
		t.Fatalf("rune length %d, want %d", len([]rune(got)), summaryMax)
	}
	if !strings.HasPrefix(got, strings.Repeat("é", summaryMax)) {
		t.Fatal("truncation cut a multibyte rune")
	}
}

func TestMeasurementSummary(t *testing.T) {
	v, ok := parseDecimal("12")
	if !ok {
		t.Fatal("bad value")
	}
	if got := MeasurementSummary("LENGTH", v, "CM"); got != "Length 12 CM" {
		t.Fatalf("got %q", got)
	}
	t2, _ := parseDecimal("-4")
	if got := MeasurementSummary("TEMPERATURE", t2, "C"); got != "Temperature -4 C" {
		t.Fatalf("got %q", got)
	}
}
