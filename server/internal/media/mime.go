package media

import "bytes"

// SniffMIME identifies the content type from magic bytes only — the
// client's extension and Content-Type header are never trusted, and no
// image decoding happens inside the API request (T13 rule; decode
// validity is the processor's verdict in T14). Returns "" when nothing
// recognized is present.
func SniffMIME(head []byte) string {
	switch {
	case bytes.HasPrefix(head, []byte{0xFF, 0xD8, 0xFF}):
		return "image/jpeg"
	case bytes.HasPrefix(head, []byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A}):
		return "image/png"
	case len(head) >= 12 && bytes.HasPrefix(head, []byte("RIFF")) && bytes.Equal(head[8:12], []byte("WEBP")):
		return "image/webp"
	case len(head) >= 12 && bytes.Equal(head[4:8], []byte("ftyp")):
		// ISO base media file container: only HEIF/HEIC brands count.
		switch string(head[8:12]) {
		case "heic", "heix", "hevc", "hevx", "heim", "heis", "hevm", "hevs":
			return "image/heic"
		case "mif1", "msdh", "msf3":
			return "image/heif"
		}
		return ""
	case bytes.HasPrefix(head, []byte("%PDF-")):
		return "application/pdf"
	}
	return ""
}

// MIMEAllowed gates what each purpose may hold. Photos are images;
// receipts additionally accept PDF.
func MIMEAllowed(purpose, mime string) bool {
	switch mime {
	case "image/jpeg", "image/png", "image/webp", "image/heic", "image/heif":
		return purpose == "PHOTO" || purpose == "RECEIPT"
	case "application/pdf":
		return purpose == "RECEIPT"
	}
	return false
}
