package money

import (
	"math"
	"testing"
)

func TestParseCanonical(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want int64
		ok   bool
	}{
		{"0", 0, true},
		{"5", 5, true},
		{"10000", 10000, true},
		{"999999999999999", MaxMinor - 1, true},
		{"1000000000000000", MaxMinor, true}, // boundary is inclusive
		{"1000000000000001", 0, false},       // > MaxMinor
		{"007", 0, false},                    // leading zeros
		{"-5", 0, false},                     // never negative
		{"1_000", 0, false},                  // separators
		{"1.5", 0, false},                    // not minor units
		{"", 0, false},
		{"99999999999999999999999999", 0, false}, // overflow guard
	} {
		got, err := Parse(tc.in)
		if tc.ok && err != nil {
			t.Errorf("Parse(%q): unexpected err %v", tc.in, err)
		}
		if !tc.ok && err == nil {
			t.Errorf("Parse(%q): expected rejection, got %d", tc.in, got)
		}
		if tc.ok && got != tc.want {
			t.Errorf("Parse(%q)=%d want %d", tc.in, got, tc.want)
		}
	}
}

func TestValidateExact100Example(t *testing.T) {
	// 03 example: $100.00 incl $5 tax + $5 shipping, two $45.00 lines,
	// each fig allocated $4500 + $250 tax + $250 shipping.
	p := PurchaseSpec{Total: 10000, Tax: 500, Shipping: 500, Lines: []LineSpec{
		{Index: 0, Total: 4500, Allocations: []AllocationSpec{{Index: 0, PlantSet: true, Line: 4500, Tax: 250, Shipping: 250}}},
		{Index: 1, Total: 4500, Allocations: []AllocationSpec{{Index: 1, PlantSet: true, Line: 4500, Tax: 250, Shipping: 250}}},
	}}
	if v := Validate(p); len(v) != 0 {
		t.Fatalf("exact example must validate clean, got %+v", v)
	}
}

func TestValidateExactDiscrepancies(t *testing.T) {
	p := PurchaseSpec{Total: 10000, Tax: 500, Shipping: 500, Lines: []LineSpec{
		{Index: 0, Total: 4500, Allocations: []AllocationSpec{{Index: 0, PlantSet: true, Line: 4499, Tax: 250, Shipping: 250}}},
		{Index: 1, Total: 4500, Allocations: []AllocationSpec{{Index: 1, PlantSet: true, Line: 4500, Tax: 250, Shipping: 249}}},
	}}
	v := Validate(p)
	if len(v) != 2 {
		t.Fatalf("want 2 violations, got %+v", v)
	}
	// line under-allocation and shipping mismatch; line totals still sum
	// to total-minus-overhead here, so no input.lines violation.
	want := map[string]bool{
		"input.lines[0].allocations": false,
		"input.allocations.shipping": false,
	}
	for _, x := range v {
		if _, ok := want[x.Field]; !ok {
			t.Errorf("unexpected field %q", x.Field)
			continue
		}
		want[x.Field] = true
	}
	for f, seen := range want {
		if !seen {
			t.Errorf("missing violation for %q in %+v", f, v)
		}
	}
}

func TestValidateOverheadAndUnallocated(t *testing.T) {
	// Unallocated supplies bucket carries the leftover overhead: plant
	// totals may under-sum overhead only if the nil-plant bucket covers it.
	p := PurchaseSpec{Total: 10000, Tax: 500, Shipping: 500, Lines: []LineSpec{
		{Index: 0, Total: 9000, Allocations: []AllocationSpec{
			{Index: 0, PlantSet: true, Line: 4500, Tax: 250, Shipping: 250},
			{Index: 1, PlantSet: true, Line: 4500, Tax: 250, Shipping: 250},
		}},
	}}
	if v := Validate(p); len(v) != 0 {
		t.Fatalf("clean split rejected: %+v", v)
	}
	// Overhead exceeding total.
	bad := PurchaseSpec{Total: 1000, Tax: 600, Shipping: 500, Lines: []LineSpec{
		{Index: 0, Total: 0, Allocations: []AllocationSpec{{Index: 0, PlantSet: false, Line: 0, Tax: 600, Shipping: 400}}},
	}}
	v := Validate(bad)
	if len(v) == 0 {
		t.Fatal("tax+shipping > total must be rejected")
	}
	// All-zero lines with overhead but no explicit bucket.
	noBucket := PurchaseSpec{Total: 500, Tax: 500, Shipping: 0, Lines: []LineSpec{
		{Index: 0, Total: 0},
	}}
	v = Validate(noBucket)
	found := false
	for _, x := range v {
		if x.Field == "input.allocations.tax" {
			found = true
		}
	}
	if !found {
		t.Fatalf("all-zero subtotal must require explicit overhead allocation: %+v", v)
	}
}

func TestDistributeLargestRemainder(t *testing.T) {
	// 100 across 3 equal weights: 34/33/33, remainder to first (stable).
	got, err := Distribute(100, []int64{1, 1, 1})
	if err != nil {
		t.Fatal(err)
	}
	if got[0] != 34 || got[1] != 33 || got[2] != 33 {
		t.Fatalf("got %v", got)
	}
	// Exact division stays exact.
	got, _ = Distribute(100, []int64{45, 45, 10})
	if got[0] != 45 || got[1] != 45 || got[2] != 10 {
		t.Fatalf("got %v", got)
	}
	// All-zero weights with positive total: explicit allocation required.
	if _, err := Distribute(500, []int64{0, 0}); err == nil {
		t.Fatal("all-zero subtotal must error")
	}
	// All-zero weights and total: all zeros.
	got, err = Distribute(0, []int64{0, 0})
	if err != nil || len(got) != 2 || got[0] != 0 {
		t.Fatalf("got %v err %v", got, err)
	}
}

func FuzzDistributeConservation(f *testing.F) {
	f.Add(int64(10000), []byte{100, 100, 10})
	f.Add(int64(1), []byte{1, 1, 1})
	f.Add(int64(999999999), []byte{7, 11, 13, 17})
	f.Add(int64(math.MaxInt32), []byte{1, 2, 3})
	f.Fuzz(func(t *testing.T, total int64, raw []byte) {
		if total < 0 || total > MaxMinor {
			t.Skip()
		}
		var weights []int64
		for _, b := range raw {
			weights = append(weights, int64(b))
		}
		var sum int64
		for _, w := range weights {
			sum += w
			if sum < 0 {
				t.Skip()
			}
		}
		parts, err := Distribute(total, weights)
		if sum == 0 {
			if total != 0 && err == nil {
				t.Fatal("all-zero weights with positive total must error")
			}
			return
		}
		if err != nil {
			t.Fatal(err)
		}
		var got int64
		for _, p := range parts {
			got += p
		}
		if got != total {
			t.Fatalf("conservation: parts %v sum %d != total %d", parts, got, total)
		}
		// Determinism.
		again, _ := Distribute(total, weights)
		for i := range parts {
			if parts[i] != again[i] {
				t.Fatal("non-deterministic distribution")
			}
		}
	})
}

func FuzzParseRoundTrip(f *testing.F) {
	f.Add("0")
	f.Add("10000")
	f.Fuzz(func(t *testing.T, s string) {
		v, err := Parse(s)
		if err != nil {
			return
		}
		if Format(v) != s {
			t.Fatalf("round trip: %q -> %d -> %q", s, v, Format(v))
		}
	})
}
