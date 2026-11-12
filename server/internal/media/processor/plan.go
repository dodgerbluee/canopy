// Package processor normalizes uploaded photos (T14): orientation,
// responsive derivatives, metadata stripping, and honest failure states.
// Decoding never happens inside the API process and never inside this
// process either: codec work runs as resource-limited direct-argv
// subprocesses in the application container.
package processor

// Variant describes one derivative the pipeline must produce. Names are
// frozen by the asset_variants CHECK constraint (thumb|medium|display|
// original).
type Variant struct {
	Name     string
	LongEdge int // 0 = keep full (normalized) size
	Quality  int // JPEG quality
}

// Variants is the deterministic derivative plan. A retried job produces
// the same set with the same storage keys (see VariantKey), so at-least-
// once processing converges instead of duplicating.
var Variants = []Variant{
	{Name: "thumb", LongEdge: 320, Quality: 80},
	{Name: "medium", LongEdge: 1280, Quality: 80},
	{Name: "display", LongEdge: 2048, Quality: 85},
	{Name: "original", LongEdge: 0, Quality: 85},
}

// DefaultMaxPixels bounds decode work before any pixel is touched: the
// codec header probe reports dimensions, and anything over budget is
// refused without decoding (decompression-bomb guard). 50MP covers a
// 12MP iPhone with wide headroom while refusing 576MP bombs.
const DefaultMaxPixels int64 = 50_000_000

// Failure codes land in assets.error_code.
const (
	CodeDecodeFailed   = "DECODE_FAILED"
	CodePixelBudget    = "PIXEL_BUDGET_EXCEEDED"
	CodeProcessTimeout = "PROCESSING_TIMEOUT"
	CodeObjectMissing  = "OBJECT_MISSING"
)
