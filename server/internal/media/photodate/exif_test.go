package photodate

import (
	"encoding/binary"
	"os"
	"testing"
	"time"
)

func fixture(order binary.ByteOrder, raw, offset string) []byte {
	fields := []struct {
		tag   uint16
		value string
	}{{0x9003, raw}, {0x9011, offset}}
	b := make([]byte, 64)
	b[0] = 'I'
	b[1] = 'I'
	if order == binary.BigEndian {
		b[0] = 'M'
		b[1] = 'M'
	}
	order.PutUint16(b[2:], 42)
	order.PutUint32(b[4:], 8)
	order.PutUint16(b[8:], 2)
	for i, f := range fields {
		at := 10 + i*12
		order.PutUint16(b[at:], f.tag)
		order.PutUint16(b[at+2:], 2)
		order.PutUint32(b[at+4:], uint32(len(f.value)+1))
		order.PutUint32(b[at+8:], uint32(len(b)))
		b = append(b, []byte(f.value)...)
		b = append(b, 0)
	}
	return b
}
func TestCaptureMetadata(t *testing.T) {
	for _, order := range []binary.ByteOrder{binary.LittleEndian, binary.BigEndian} {
		date := Read(fixture(order, "2026:10:31 00:30:39", "-05:00"), "Asia/Tokyo")
		if date == nil || date.Instant == nil || date.Instant.Format(time.RFC3339) != "2026-10-31T05:30:39Z" || !date.OffsetKnown {
			t.Fatal("camera offset must win", date)
		}
	}
	b, err := os.ReadFile("testdata/android.jpg")
	if err != nil {
		t.Fatal(err)
	}
	date := Read(b, "")
	if date == nil || date.Instant == nil || date.Instant.Format(time.RFC3339Nano) != "2026-10-31T05:30:39.125Z" {
		t.Fatal("real JPEG ExifIFD/subseconds", date)
	}
	naive := fixture(binary.LittleEndian, "2026:10:31 00:30:39", "")
	d := Read(naive, "")
	if d == nil || d.Instant != nil {
		t.Fatal("naive time invented an offset")
	}
	d = Read(naive, "America/Chicago")
	if d == nil || d.Instant == nil || d.Instant.Format(time.RFC3339) != "2026-10-31T05:30:39Z" {
		t.Fatal("explicit timezone not used")
	}
	for _, raw := range []string{"2026:03:08 02:30:00", "2026:11:01 01:30:00"} {
		d := Read(fixture(binary.LittleEndian, raw, ""), "America/Chicago")
		if d == nil || d.Instant != nil {
			t.Fatal("ambiguous or nonexistent DST instant accepted", raw)
		}
	}
}
func TestMalformedOrMissingCaptureDate(t *testing.T) {
	for _, raw := range []string{"0000:00:00 00:00:00", "2026:02:31 12:00:00", "not a capture date!"} {
		if Read(fixture(binary.LittleEndian, raw, "-05:00"), "") != nil {
			t.Fatal(raw)
		}
	}
	for _, offset := range []string{"+05:60", "+14:01", "-99:99", "UTC"} {
		if Read(fixture(binary.LittleEndian, "2026:10:31 00:30:39", offset), "") != nil {
			t.Fatal(offset)
		}
	}
	b := fixture(binary.LittleEndian, "2026:10:31 00:30:39", "-05:00")
	for n := 0; n < len(b); n++ {
		_ = Read(b[:n], "")
	}
	for i := range b {
		copy := append([]byte{}, b...)
		copy[i] = 255
		_ = Read(copy, "")
	}
	// DateTime is a modification time, not a capture-date fallback.
	binary.LittleEndian.PutUint16(b[10:], 0x132)
	if Read(b, "") != nil {
		t.Fatal("modification date used as capture")
	}
}
func TestReportedOriginalCaptureMetadata(t *testing.T) {
	path := os.Getenv("CANOPY_CAPTURE_DIAGNOSTIC_FILE")
	if path == "" {
		t.Skip("local original opt-in")
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	date := Read(b, "")
	if date == nil || date.Instant == nil {
		t.Fatal("original has no resolved capture date")
	}
	t.Log("Capture timestamp from original:", date.Instant.Format(time.RFC3339Nano))
}
