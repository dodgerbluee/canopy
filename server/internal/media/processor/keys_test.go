package processor

import (
	"regexp"
	"testing"
)

// Deterministic variant keys are the convergence mechanism: a retried
// job restages the same object and the (asset_id, name) upsert reuses
// the same row. These tests pin that contract.

var keyRe = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-5[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

func TestVariantKeyDeterministicAndOpaque(t *testing.T) {
	const asset = "2f6a1c9d-0b7e-4a51-9d2f-6c1e8b3a4d55"
	for _, v := range Variants {
		k1 := VariantKey(asset, v.Name)
		k2 := VariantKey(asset, v.Name)
		if k1 != k2 {
			t.Fatalf("key for %s not stable: %s vs %s", v.Name, k1, k2)
		}
		if !keyRe.MatchString(k1) {
			t.Fatalf("key %q is not an opaque v5 UUID (store rejects non-UUID keys)", k1)
		}
	}
}

func TestVariantKeysDifferAcrossAssetsAndNames(t *testing.T) {
	seen := map[string]bool{}
	for _, a := range []string{"aaaaaaaa-0000-4000-8000-000000000000", "bbbbbbbb-0000-4000-8000-000000000000"} {
		for _, v := range Variants {
			k := VariantKey(a, v.Name)
			if seen[k] {
				t.Fatalf("collision: %s for asset %s variant %s", k, a, v.Name)
			}
			seen[k] = true
		}
	}
}
