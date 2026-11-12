package agentapi

import (
	"bytes"
	"image"
	"image/jpeg"
	"testing"
)

func TestReceiptPreviewStripsMetadata(t *testing.T) {
	var original bytes.Buffer
	if err := jpeg.Encode(&original, image.NewRGBA(image.Rect(0, 0, 20, 30)), nil); err != nil {
		t.Fatal(err)
	}
	// A JPEG APP1 segment can contain EXIF/GPS. It must never reach an agent.
	marker := []byte("Exif\x00\x00fixture-private-gps")
	segment := append([]byte{0xff, 0xe1, 0, byte(len(marker) + 2)}, marker...)
	source := append([]byte{}, original.Bytes()[:2]...)
	source = append(source, segment...)
	source = append(source, original.Bytes()[2:]...)
	preview, err := receiptPreview(source)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(preview, marker) {
		t.Fatal("preview leaked embedded metadata")
	}
	config, _, err := image.DecodeConfig(bytes.NewReader(preview))
	if err != nil || config.Width != 20 || config.Height != 30 {
		t.Fatal("preview lost image dimensions", err)
	}
	if _, err = receiptPreview([]byte("invalid image")); err == nil {
		t.Fatal("malformed preview accepted")
	}
}
