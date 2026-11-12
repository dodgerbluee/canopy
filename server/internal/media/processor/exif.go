package processor

import (
	"bytes"
	"encoding/binary"
)

// Orientation extracts the EXIF orientation (1..8) from image bytes
// without decoding pixels: it walks EXIF/TIFF structures only. JPEG
// embeds EXIF up front behind an "Exif\0\0" marker; HEIC carries the
// Exif item inside mdat — often near the end of the file and, with
// modern libheif, starting at the bare TIFF magic with no prefix. So we
// scan for TIFF structures anywhere in the provided bytes and take the
// first well-formed parse, preferring one that carries a real
// orientation. Missing or malformed EXIF yields 1 (normal): the safe
// no-rotation default. vips 8.15's heifload does not map HEIC
// orientation to its metadata, which is exactly why this exists.
func Orientation(data []byte) int {
	best := 0
	for _, tiff := range tiffCandidates(data) {
		v, ok := tiffOrientation(tiff)
		if !ok {
			continue
		}
		if v >= 2 {
			return v
		}
		if best == 0 {
			best = 1
		}
	}
	if best == 0 {
		return 1
	}
	return best
}

var exifPrefix = []byte("Exif\x00\x00")

var tiffMagics = [][]byte{{'I', 'I', '*', 0}, {'M', 'M', 0, '*'}}

// tiffCandidates yields every plausible TIFF start: after each
// "Exif\0\0" prefix and at each bare II*/MM* magic. (A fourcc 'Exif'
// inside a HEIC iinf box looks like the prefix but is not followed by a
// TIFF header; the parse rejects it.)
func tiffCandidates(data []byte) [][]byte {
	var out [][]byte
	for i := 0; ; {
		j := bytes.Index(data[i:], exifPrefix)
		if j < 0 {
			break
		}
		i += j + len(exifPrefix)
		out = append(out, data[i:])
	}
	for _, m := range tiffMagics {
		for i := 0; ; {
			j := bytes.Index(data[i:], m)
			if j < 0 {
				break
			}
			i += j + len(m)
			out = append(out, data[i-4:])
		}
	}
	return out
}

// tiffOrientation walks IFD0. ok=false means the bytes are not a
// well-formed TIFF at all; ok=true with v=1 means a valid TIFF that
// carries no usable orientation.
func tiffOrientation(tiff []byte) (v int, ok bool) {
	if len(tiff) < 8 {
		return 1, false
	}
	var bo binary.ByteOrder
	switch {
	case tiff[0] == 'I' && tiff[1] == 'I':
		bo = binary.LittleEndian
	case tiff[0] == 'M' && tiff[1] == 'M':
		bo = binary.BigEndian
	default:
		return 1, false
	}
	ifd := int(bo.Uint32(tiff[4:8]))
	if ifd < 8 || ifd+2 > len(tiff) {
		return 1, false
	}
	n := int(bo.Uint16(tiff[ifd : ifd+2]))
	for i := 0; i < n; i++ {
		e := ifd + 2 + i*12
		if e+12 > len(tiff) {
			return 1, false
		}
		if bo.Uint16(tiff[e:e+2]) != 0x0112 { // orientation tag
			continue
		}
		typ := bo.Uint16(tiff[e+2 : e+4])
		cnt := bo.Uint32(tiff[e+4 : e+8])
		if typ == 3 && cnt == 1 { // SHORT, single value inline
			if o := int(bo.Uint16(tiff[e+8 : e+10])); o >= 1 && o <= 8 {
				return o, true
			}
		}
		return 1, true
	}
	return 1, true
}
