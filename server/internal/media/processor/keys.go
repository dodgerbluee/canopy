package processor

import "github.com/google/uuid"

// variantNS is a fixed namespace for deriving variant storage keys.
// Keys are opaque UUIDs (the store only accepts UUID-shaped keys) but
// deterministic per (asset, variant), so a retried job restages the same
// object and the upsert converges — at-least-once never duplicates.
var variantNS = uuid.MustParse("6f2f6f4a-2b9a-5a4e-9c31-8d7e0a5b9c22")

// VariantKey derives the storage key for one variant of one asset.
// assetID must be the canonical lowercase UUID string.
func VariantKey(assetID, name string) string {
	return uuid.NewSHA1(variantNS, []byte(assetID+"|"+name)).String()
}
