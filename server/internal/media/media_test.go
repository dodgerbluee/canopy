package media

import (
	"encoding/base64"
	"strings"
	"testing"
	"time"
)

func base64Enc(s string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(s))
}

func TestSanitizeFilename(t *testing.T) {
	cases := []struct {
		in   string
		want *string
	}{
		{"../../etc/passwd", strp("passwd")},
		{`C:\pics\a b  c.jpg`, strp("a b c.jpg")},
		{"field\nnotes\t.jpg", strp("fieldnotes.jpg")}, // control chars are dropped
		{"  ", nil},
		{".", nil},
		{"..", nil},
		{"", nil},
		{strings.Repeat("x", 300), strp(strings.Repeat("x", 200))},
	}
	for _, c := range cases {
		got := SanitizeFilename(c.in)
		switch {
		case c.want == nil && got != nil:
			t.Errorf("SanitizeFilename(%q) = %q, want nil", c.in, *got)
		case c.want != nil && got == nil:
			t.Errorf("SanitizeFilename(%q) = nil, want %q", c.in, *c.want)
		case c.want != nil && *got != *c.want:
			t.Errorf("SanitizeFilename(%q) = %q, want %q", c.in, *got, *c.want)
		}
	}
}

func strp(s string) *string { return &s }

func TestSniffMIME(t *testing.T) {
	ftyp := func(brand string) []byte {
		b := []byte{0, 0, 0, 0x18}
		b = append(b, []byte("ftyp")...)
		b = append(b, []byte(brand)...)
		return append(b, make([]byte, 16)...)
	}
	cases := []struct {
		head []byte
		want string
	}{
		{[]byte{0xFF, 0xD8, 0xFF, 0xE0}, "image/jpeg"},
		{[]byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A}, "image/png"},
		{append([]byte("RIFF\x00\x00\x00\x00WEBP"), make([]byte, 4)...), "image/webp"},
		{ftyp("heic"), "image/heic"},
		{ftyp("mif1"), "image/heif"},
		{ftyp("avif"), ""}, // avif is not an R1 input format
		{[]byte("%PDF-1.7\n"), "application/pdf"},
		{[]byte("GIF89a"), ""},
		{[]byte("<html>"), ""},
	}
	for _, c := range cases {
		if got := SniffMIME(c.head); got != c.want {
			t.Errorf("SniffMIME(%q...) = %q, want %q", c.head[:min(len(c.head), 8)], got, c.want)
		}
	}
}

func TestMIMEAllowed(t *testing.T) {
	yes := [][2]string{
		{"PHOTO", "image/jpeg"}, {"PHOTO", "image/png"}, {"PHOTO", "image/webp"},
		{"PHOTO", "image/heic"}, {"PHOTO", "image/heif"},
		{"RECEIPT", "application/pdf"}, {"RECEIPT", "image/png"},
	}
	no := [][2]string{
		{"PHOTO", "application/pdf"}, {"PHOTO", ""}, {"RECEIPT", ""},
		{"RECEIPT", "text/html"}, {"BOGUS", "image/png"},
	}
	for _, c := range yes {
		if !MIMEAllowed(c[0], c[1]) {
			t.Errorf("%s/%s should be allowed", c[0], c[1])
		}
	}
	for _, c := range no {
		if MIMEAllowed(c[0], c[1]) {
			t.Errorf("%s/%s should be rejected", c[0], c[1])
		}
	}
}

func TestCursorRoundTrip(t *testing.T) {
	id := "6f1d2c3b-0a01-4b02-8c03-0d0e0f010203"
	ts := time.Date(2026, 11, 1, 12, 34, 56, 789012000, time.UTC)
	cur := encodeCursor(ts, id)
	gotTS, gotID, err := decodeCursor(cur)
	if err != nil {
		t.Fatal(err)
	}
	if !gotTS.Time.Equal(ts) {
		t.Errorf("time = %v", gotTS.Time)
	}
	if uuidString(gotID) != id {
		t.Errorf("id = %s", uuidString(gotID))
	}
	for _, bad := range []string{"", "!!!", "aGVsbG8",
		// wrong order tag
		base64Enc("createdAsc|2026-11-01T00:00:00.000000Z|" + id),
		// bad uuid
		base64Enc("createdDesc|2026-11-01T00:00:00.000000Z|nope"),
		// bad time
		base64Enc("createdDesc|yesterday|" + id),
	} {
		if _, _, err := decodeCursor(bad); err != ErrInvalidCursor {
			t.Errorf("decodeCursor(%q) = %v, want ErrInvalidCursor", bad, err)
		}
	}
}
