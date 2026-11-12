package processor

import (
	"os"
	"path/filepath"
	"testing"
)

// Corpus fixtures are self-authored (see SHA256SUMS) and carry known
// orientation tags; the HEIC one reproduces the iPhone case where vips
// 8.15's heifload does NOT expose orientation, which is exactly why the
// pipeline reads it here.

func corpus(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("../../../testdata/media-corpus", name))
	if err != nil {
		t.Fatalf("read corpus %s: %v", name, err)
	}
	return b
}

func TestOrientationFromCorpus(t *testing.T) {
	cases := []struct {
		file string
		want int
	}{
		{"iphone-12mp.heic", 6}, // iPhone-shaped: EXIF orientation 6, TIFF near EOF with no prefix
		{"rotated-12mp.jpg", 6},
		{"tiny.png", 1},    // no EXIF at all
		{"corrupt.jpg", 6}, // truncated scan data, but the EXIF block survived intact
		{"bomb.png", 1},
		{"photo-640.webp", 1}, // EXIF present with orientation 1
		{"receipt.pdf", 1},
	}
	for _, c := range cases {
		if got := Orientation(corpus(t, c.file)); got != c.want {
			t.Errorf("Orientation(%s) = %d, want %d", c.file, got, c.want)
		}
	}
}

func TestOrientationGarbageSafe(t *testing.T) {
	for _, b := range [][]byte{
		nil, {}, []byte("Exif\x00\x00"), []byte("Exif\x00\x00II*\x00"),
		[]byte("II*\x00\xff\xff\xff\xff\xff\xff\xff\xff"),
	} {
		if got := Orientation(b); got != 1 {
			t.Errorf("Orientation(%q) = %d, want 1", b, got)
		}
	}
}

func TestOrientationBigEndianTIFF(t *testing.T) {
	// minimal big-endian TIFF: MM, magic 42, IFD at 8, one entry:
	// tag 0x0112, type 3 (SHORT), count 1, value 8
	tiff := []byte("MM\x00*\x00\x00\x00\x08" +
		"\x00\x01" + // entry count
		"\x01\x12" + // tag
		"\x00\x03" + // type SHORT
		"\x00\x00\x00\x01" + // count
		"\x00\x08\x00\x00" + // value 8
		"\x00\x00\x00\x00") // next IFD
	if got, ok := tiffOrientation(tiff); !ok || got != 8 {
		t.Fatalf("got (%d,%v), want (8,true)", got, ok)
	}
}
