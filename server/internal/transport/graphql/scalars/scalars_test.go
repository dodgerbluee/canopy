package scalars

import (
	"bytes"
	"io"
	"testing"
)

func marshal(t *testing.T, v interface{ MarshalGQL(w io.Writer) }) string {
	t.Helper()
	var buf bytes.Buffer
	v.MarshalGQL(&buf)
	return buf.String()
}

func TestDateTimeMarshalUTC(t *testing.T) {
	// 2026-11-01T19:40:03Z expressed with an offset must marshal as UTC Z.
	var d DateTime
	if err := d.UnmarshalGQL("2026-11-01T15:40:03-04:00"); err != nil {
		t.Fatal(err)
	}
	if got, want := marshal(t, d), `"2026-11-01T19:40:03Z"`; got != want {
		t.Errorf("marshal = %s, want %s", got, want)
	}
}

func TestDateTimeRejectsGarbage(t *testing.T) {
	for _, bad := range []string{"", "2026-11-01", "2026-13-01T00:00:00Z", "2026-11-01T00:00:00", "not-a-time"} {
		var d DateTime
		if err := d.UnmarshalGQL(bad); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
	var d DateTime
	if err := d.UnmarshalGQL(12345); err == nil {
		t.Error("accepted non-string")
	}
}

func TestLocalDateStrict(t *testing.T) {
	for _, ok := range []string{"2024-02-29", "2026-11-01", "0001-01-01"} {
		var d Date
		if err := d.UnmarshalGQL(ok); err != nil {
			t.Errorf("rejected %q: %v", ok, err)
		}
	}
	// Month/day overflow, wrong shape, instant coercion — all rejected.
	for _, bad := range []string{"2024-02-30", "2023-02-29", "2026-13-01", "2026-1-05", "2026-11-01T00:00:00Z", "11/01/2026", ""} {
		var d Date
		if err := d.UnmarshalGQL(bad); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
}

func TestDecimalCanonical(t *testing.T) {
	for _, ok := range []string{"0", "-0", "40.7128", "-74.006", "123456.789012", "-0.5"} {
		var d Decimal
		if err := d.UnmarshalGQL(ok); err != nil {
			t.Errorf("rejected %q: %v", ok, err)
		}
	}
	// exponent, plus sign, leading zeros, dangling point, >6 fraction digits.
	for _, bad := range []string{"1e5", "1E5", "+1", "007", "01.5", "1.", ".5", "1.1234567", "", "12,345"} {
		var d Decimal
		if err := d.UnmarshalGQL(bad); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
}

func TestMinorUnitsBoundaries(t *testing.T) {
	for _, ok := range []string{"0", "1", "100", "999999999999"} {
		var m MinorUnits
		if err := m.UnmarshalGQL(ok); err != nil {
			t.Errorf("rejected %q: %v", ok, err)
		}
	}
	// no sign, no separators, no decimals, no leading zeros, never negative.
	for _, bad := range []string{"-1", "+1", "1,234", "1 234", "12.5", "007", "", "12USD", "١٢٣"} {
		var m MinorUnits
		if err := m.UnmarshalGQL(bad); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
}

func TestDecimalBig(t *testing.T) {
	var d Decimal
	if err := d.UnmarshalGQL("-74.006"); err != nil {
		t.Fatal(err)
	}
	r, err := d.Big()
	if err != nil {
		t.Fatal(err)
	}
	if r.Sign() >= 0 {
		t.Error("want negative")
	}
}
