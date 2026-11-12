// Package scalars implements the frozen contract scalar mappings
// (contracts/graphql/shared.graphql, semantics per 03-contracts.md
// "Shared value rules"). Validation is strict and never silent: an invalid
// literal is a GraphQL error, never a coerced zero value.
package scalars

import (
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// DateTime is an RFC3339 UTC instant. Marshaling always emits UTC with
// second precision and a Z suffix; parsing accepts any RFC3339 form.
type DateTime time.Time

var rfc3339 = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(\.\d+)?(Z|[+-]\d{2}:\d{2})$`)

func (t DateTime) MarshalGQL(w io.Writer) {
	b, _ := json.Marshal(time.Time(t).UTC().Format(time.RFC3339))
	_, _ = w.Write(b)
}

func (t *DateTime) UnmarshalGQL(v any) error {
	s, ok := v.(string)
	if !ok || !rfc3339.MatchString(s) {
		return fmt.Errorf("DateTime: want RFC3339 UTC instant, got %v", v)
	}
	parsed, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return fmt.Errorf("DateTime: %w", err)
	}
	*t = DateTime(parsed)
	return nil
}

// Date is a strict Gregorian calendar date YYYY-MM-DD. It never silently
// becomes an instant and never accepts month/day overflow.
type Date string

var dateRe = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)

func (d Date) MarshalGQL(w io.Writer) {
	b, _ := json.Marshal(string(d))
	_, _ = w.Write(b)
}

func (d *Date) UnmarshalGQL(v any) error {
	s, ok := v.(string)
	if !ok || !dateRe.MatchString(s) {
		return fmt.Errorf("LocalDate: want YYYY-MM-DD, got %v", v)
	}
	y, _ := strconv.Atoi(s[0:4])
	m, _ := strconv.Atoi(s[5:7])
	day, _ := strconv.Atoi(s[8:10])
	// Explicit bounds guard month/day overflow (e.g. 2024-02-30).
	if y < 1 || y > 9999 || m < 1 || m > 12 || day < 1 ||
		day > daysInMonth(y, time.Month(m)) {
		return fmt.Errorf("LocalDate: not a real calendar date: %q", s)
	}
	*d = Date(s)
	return nil
}

func daysInMonth(year int, m time.Month) int {
	return 32 - time.Date(year, m, 32, 0, 0, 0, 0, time.UTC).Day()
}

// Decimal is a canonical decimal string: optional minus sign, digits, at
// most 6 fractional digits, never an exponent, no redundant leading zeros.
// Exact; used for measurements and coordinates — never for money.
type Decimal string

var decimalRe = regexp.MustCompile(`^-?\d+(\.\d{1,6})?$`)

func (d Decimal) canonical() bool {
	s := string(d)
	if !decimalRe.MatchString(s) {
		return false
	}
	digits := strings.TrimPrefix(s, "-")
	if strings.HasPrefix(digits, "0") && len(digits) > 1 && digits[1] != '.' {
		return false // 007, 01.5
	}
	return true
}

func (d Decimal) MarshalGQL(w io.Writer) {
	b, _ := json.Marshal(string(d))
	_, _ = w.Write(b)
}

func (d *Decimal) UnmarshalGQL(v any) error {
	s, ok := v.(string)
	if !ok || !Decimal(s).canonical() {
		return fmt.Errorf("Decimal: want canonical decimal string (no exponent, <=6 fraction digits), got %v", v)
	}
	*d = Decimal(s)
	return nil
}

// Big converts for range checks; callers must not use it for equality.
func (d Decimal) Big() (*big.Rat, error) {
	r := new(big.Rat)
	if _, ok := r.SetString(string(d)); !ok {
		return nil, fmt.Errorf("Decimal: not numeric: %q", string(d))
	}
	return r, nil
}

// MinorUnits is a nonnegative digit string in minor units: no sign, no
// separators, no leading zeros (except the single "0").
type MinorUnits string

var minorRe = regexp.MustCompile(`^(0|[1-9]\d*)$`)

func (m MinorUnits) MarshalGQL(w io.Writer) {
	b, _ := json.Marshal(string(m))
	_, _ = w.Write(b)
}

func (m *MinorUnits) UnmarshalGQL(v any) error {
	s, ok := v.(string)
	if !ok || !minorRe.MatchString(s) {
		return fmt.Errorf("MinorUnits: want nonnegative digit string, got %v", v)
	}
	*m = MinorUnits(s)
	return nil
}
