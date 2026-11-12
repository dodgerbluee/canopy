// Package money holds the exact minor-unit arithmetic shared by the
// purchase and spending surfaces (03 "Money and purchases"). All values
// are integer minor units; floats never appear.
package money

import (
	"errors"
	"fmt"
	"math/big"
	"regexp"
	"sort"
	"strconv"
)

// MaxMinor bounds stored amounts: 10^15 minor units. Anything larger is
// rejected as VALIDATION rather than silently widened; the DB column is
// numeric, but int64 arithmetic must stay provably overflow-free.
const MaxMinor int64 = 1_000_000_000_000_000

var minorRe = regexp.MustCompile(`^(0|[1-9][0-9]*)$`)
var currencyRe = regexp.MustCompile(`^[A-Z]{3}$`)

// ErrMalformed reports a non-canonical or out-of-range amount string.
var ErrMalformed = errors.New("malformed minor units")

// Parse converts a canonical MinorUnits string ("0", "10000") to an
// int64 minor amount. Leading zeros, signs, separators and oversized
// values are rejected.
func Parse(s string) (int64, error) {
	if !minorRe.MatchString(s) {
		return 0, ErrMalformed
	}
	n, ok := new(big.Int).SetString(s, 10)
	if !ok || !n.IsInt64() {
		return 0, ErrMalformed
	}
	v := n.Int64()
	if v > MaxMinor {
		return 0, ErrMalformed
	}
	return v, nil
}

// Format renders a minor amount canonically (no leading zeros). Negative
// values only ever appear inside violation messages quoting a computed
// deficit (e.g. total minus tax minus shipping), never as stored amounts.
func Format(v int64) string {
	return strconv.FormatInt(v, 10)
}

// ValidCurrency accepts ISO-4217 letter triples only; conversion between
// currencies never happens implicitly.
func ValidCurrency(code string) bool { return currencyRe.MatchString(code) }

// Violation is one exact arithmetic discrepancy, reported with the
// offending amounts so the UI can show the difference verbatim.
type Violation struct {
	Field   string
	Message string
}

// AllocationSpec is one allocation's component amounts. PlantSet marks
// whether the allocation names a plant (nil plant = unallocated
// supplies bucket).
type AllocationSpec struct {
	Index    int // position in the request, for stable error fields
	PlantSet bool
	Line     int64
	Tax      int64
	Shipping int64
}

// LineSpec is one purchase line and its allocations.
type LineSpec struct {
	Index       int
	Description string
	Total       int64
	Allocations []AllocationSpec
}

// PurchaseSpec is the full arithmetic shape of a create/update purchase
// command, already parsed to minor ints.
type PurchaseSpec struct {
	Total    int64
	Tax      int64
	Shipping int64
	Lines    []LineSpec
}

// Validate enforces the frozen invariants from 03/purchases.graphql:
//   - tax + shipping <= total
//   - sum(line totals) == total - tax - shipping
//   - per line: sum(allocation lineMinor) == line total
//   - sum(allocation tax) == purchase tax (unallocated bucket included)
//   - sum(allocation shipping) == purchase shipping
//
// Every violation message quotes the exact expected and found amounts.
func Validate(p PurchaseSpec) []Violation {
	var out []Violation
	if p.Tax+p.Shipping > p.Total {
		out = append(out, Violation{"input.taxMinor", fmt.Sprintf(
			"tax (%s) + shipping (%s) exceeds total (%s)",
			Format(p.Tax), Format(p.Shipping), Format(p.Total))})
	}
	var lineSum int64
	for _, l := range p.Lines {
		lineSum += l.Total
	}
	if net := p.Total - p.Tax - p.Shipping; lineSum != net {
		out = append(out, Violation{"input.lines", fmt.Sprintf(
			"line totals sum to %s but total minus tax minus shipping is %s",
			Format(lineSum), Format(net))})
	}
	var taxSum, shipSum int64
	for _, l := range p.Lines {
		var lsum int64
		for _, al := range l.Allocations {
			lsum += al.Line
			taxSum += al.Tax
			shipSum += al.Shipping
		}
		if lsum != l.Total {
			out = append(out, Violation{fmt.Sprintf("input.lines[%d].allocations", l.Index), fmt.Sprintf(
				"allocation line amounts sum to %s but line total is %s",
				Format(lsum), Format(l.Total))})
		}
	}
	if taxSum != p.Tax {
		out = append(out, Violation{"input.allocations.tax", fmt.Sprintf(
			"allocated tax sums to %s but purchase tax is %s",
			Format(taxSum), Format(p.Tax))})
	}
	if shipSum != p.Shipping {
		out = append(out, Violation{"input.allocations.shipping", fmt.Sprintf(
			"allocated shipping sums to %s but purchase shipping is %s",
			Format(shipSum), Format(p.Shipping))})
	}
	return out
}

// Distribute splits total across weights with largest-remainder
// rounding: every part is within 1 minor unit of its exact proportional
// share, the parts sum exactly to total, and ties break by input order
// (callers pass allocations sorted by stable id to get the 03 tie-break).
// All-zero weights with a positive total are rejected — 03 requires an
// explicit overhead allocation in that case.
func Distribute(total int64, weights []int64) ([]int64, error) {
	if total < 0 {
		return nil, errors.New("negative total")
	}
	var sum int64
	for _, w := range weights {
		if w < 0 {
			return nil, errors.New("negative weight")
		}
		sum += w
	}
	if sum == 0 {
		if total == 0 {
			return make([]int64, len(weights)), nil
		}
		return nil, errors.New("all-zero subtotal requires explicit overhead allocation")
	}
	parts := make([]int64, len(weights))
	exact := make([]int64, len(weights)) // fractional remainder scaled by sum
	var assigned int64
	for i, w := range weights {
		q := new(big.Int).Mul(big.NewInt(w), big.NewInt(total))
		base := new(big.Int).Div(q, big.NewInt(sum))
		parts[i] = base.Int64()
		exact[i] = new(big.Int).Sub(q, new(big.Int).Mul(base, big.NewInt(sum))).Int64()
		assigned += parts[i]
	}
	order := make([]int, len(weights))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(i, j int) bool { return exact[order[i]] > exact[order[j]] })
	for k := int64(0); k < total-assigned; k++ {
		parts[order[k]]++
	}
	return parts, nil
}
