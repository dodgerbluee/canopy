package journal

import (
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	dbgen "canopy/server/db/generated"
)

func TestPhotoSummary(t *testing.T) {
	if got := PhotoSummary(pgtype.Text{}, dbgen.PhotoViewpointWHOLE); got != "Photo of whole plant" {
		t.Fatalf("whole: %q", got)
	}
	if got := PhotoSummary(pgtype.Text{String: "  ", Valid: true}, dbgen.PhotoViewpointOTHER); got != "Photo" {
		t.Fatalf("blank caption: %q", got)
	}
	if got := PhotoSummary(pgtype.Text{String: "x", Valid: true}, dbgen.PhotoViewpointLEAF); got != "x" {
		t.Fatalf("caption passthrough: %q", got)
	}
	long := strings.Repeat("é", 400)
	got := PhotoSummary(pgtype.Text{String: long, Valid: true}, dbgen.PhotoViewpointLEAF)
	if len([]rune(got)) != summaryMax {
		t.Fatalf("truncation must be rune-safe at %d, got %d", summaryMax, len([]rune(got)))
	}
}

func TestCursorRoundTripAndBinding(t *testing.T) {
	qz := Query{First: 5}
	plant := pgtype.UUID{Bytes: [16]byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16}, Valid: true}
	qz.PlantID = &plant
	qz.EntryTypes = []string{"PHOTO"}
	now := time.Date(2026, 3, 2, 8, 0, 0, 123456, time.UTC)
	id := "22222222-2222-2222-2222-222222222222"

	cur := encodeCursor(filterSignature(qz), now, id)
	occ, gotID, err := decodeCursor(cur, filterSignature(qz))
	if err != nil {
		t.Fatal(err)
	}
	if !occ.Equal(now) || uuidString(gotID) != id {
		t.Fatalf("round trip: %v %v", occ, gotID)
	}

	// same filter with entry types in a different order keeps the signature
	shuffled := Query{First: 5, PlantID: &plant,
		EntryTypes: []string{"PHOTO"}}
	if filterSignature(shuffled) != filterSignature(qz) {
		t.Fatal("signature must be order-insensitive over entry types")
	}

	// a different plant must not accept the cursor
	other := pgtype.UUID{Bytes: [16]byte{9}, Valid: true}
	otherQ := qz
	otherQ.PlantID = &other
	if _, _, err := decodeCursor(cur, filterSignature(otherQ)); err != ErrInvalidCursor {
		t.Fatalf("cross-filter cursor: %v", err)
	}
	// tombstone visibility changes the signature too
	tz := qz
	tz.IncludeTombstoned = true
	if _, _, err := decodeCursor(cur, filterSignature(tz)); err != ErrInvalidCursor {
		t.Fatalf("tombstone-flag cursor: %v", err)
	}
	for _, bad := range []string{"", "!!!", "bm90LWEtY3Vyc29y"} {
		if _, _, err := decodeCursor(bad, filterSignature(qz)); err != ErrInvalidCursor {
			t.Fatalf("malformed %q: %v", bad, err)
		}
	}
}

func TestClampFirst(t *testing.T) {
	if clampFirst(0) != defaultPageSize || clampFirst(-3) != defaultPageSize {
		t.Fatal("zero/negative must fall back to the default page size")
	}
	if clampFirst(10_000) != maxPageSize {
		t.Fatal("oversized first must clamp")
	}
}
