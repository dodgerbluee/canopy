// Package photodate reads capture timestamps only; it never decodes pixels,
// uses modification timestamps, or retains location/device metadata.
package photodate

import (
	"bytes"
	"encoding/binary"
	"strings"
	"time"
)

type Date struct {
	Instant     *time.Time
	OffsetKnown bool
}

// Read accepts embedded TIFF metadata (JPEG, HEIF and WebP). A nil result
// means missing/invalid capture metadata; a nil Instant means a valid camera
// wall time without a known offset/timezone. Work is bounded for hostile files.
func Read(data []byte, timezone string) *Date {
	for _, magic := range [][]byte{{'I', 'I', 42, 0}, {'M', 'M', 0, 42}} {
		start := 0
		for tries := 0; tries < 64; tries++ {
			i := bytes.Index(data[start:], magic)
			if i < 0 {
				break
			}
			start += i
			if date := readTIFF(data[start:], timezone); date != nil {
				return date
			}
			start += 4
		}
	}
	return nil
}
func readTIFF(b []byte, zone string) *Date {
	if len(b) < 8 {
		return nil
	}
	var order binary.ByteOrder = binary.LittleEndian
	if b[0] == 'M' {
		order = binary.BigEndian
	}
	fields := map[uint16]string{}
	visited := map[uint32]bool{}
	var walk func(uint32, int)
	walk = func(offset uint32, depth int) {
		if depth > 1 || visited[offset] || offset < 8 || uint64(offset)+2 > uint64(len(b)) {
			return
		}
		visited[offset] = true
		n := int(order.Uint16(b[int(offset):]))
		if n > 512 || uint64(offset)+2+uint64(n)*12 > uint64(len(b)) {
			return
		}
		for i := 0; i < n; i++ {
			at := int(offset) + 2 + i*12
			tag := order.Uint16(b[at:])
			typ := order.Uint16(b[at+2:])
			count := order.Uint32(b[at+4:])
			value := order.Uint32(b[at+8:])
			if tag == 0x8769 && typ == 4 && count == 1 {
				walk(value, depth+1)
			}
			if (tag != 0x9003 && tag != 0x9011 && tag != 0x9291) || typ != 2 || count == 0 || count > 64 {
				continue
			}
			pos := uint64(value)
			if count <= 4 {
				pos = uint64(at + 8)
			}
			if pos+uint64(count) > uint64(len(b)) {
				continue
			}
			text := b[int(pos) : int(pos)+int(count)]
			if text[len(text)-1] != 0 {
				continue
			}
			fields[tag] = string(text[:len(text)-1])
		}
	}
	walk(order.Uint32(b[4:]), 0)
	raw := fields[0x9003]
	if len(raw) != 19 {
		return nil
	}
	parsed, err := time.Parse("2006:01:02 15:04:05", raw)
	if err != nil || parsed.Year() < 1900 || parsed.Year() > 2200 {
		return nil
	}
	subseconds := fields[0x9291]
	if len(subseconds) > 9 {
		subseconds = subseconds[:9]
	}
	if subseconds != "" {
		for _, r := range subseconds {
			if r < '0' || r > '9' {
				return nil
			}
		}
	}
	fraction := ""
	if subseconds != "" {
		fraction = "." + subseconds
	}
	offset := fields[0x9011]
	if offset != "" {
		if len(offset) != 6 || (offset[0] != '+' && offset[0] != '-') || offset[3] != ':' || offset[1:] > "14:00" || offset[4:] > "59" || offset[4:] < "00" {
			return nil
		}
		value, err := time.Parse("2006:01:02 15:04:05Z07:00", raw+fraction+offset)
		if err != nil {
			return nil
		}
		value = value.UTC()
		return &Date{Instant: &value, OffsetKnown: true}
	}
	if strings.TrimSpace(zone) == "" {
		return &Date{}
	}
	loc, err := time.LoadLocation(zone)
	if err != nil {
		return &Date{}
	}
	value, err := time.ParseInLocation("2006:01:02 15:04:05", raw+fraction, loc)
	if err != nil || value.Format("2006:01:02 15:04:05") != raw {
		return &Date{}
	}
	// Refuse repeated local times during a clock rollback rather than silently
	// selecting one of two distinct capture instants.
	for _, delta := range []time.Duration{-2 * time.Hour, -time.Hour, -30 * time.Minute, 30 * time.Minute, time.Hour, 2 * time.Hour} {
		if value.Add(delta).In(loc).Format("2006:01:02 15:04:05") == raw {
			return &Date{}
		}
	}
	value = value.UTC()
	return &Date{Instant: &value}
}
