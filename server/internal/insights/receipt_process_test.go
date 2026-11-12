package insights

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"canopy/server/internal/media/processor"
)

// No database or provider is required: exercise the production OCR method and
// parser against the actual installed tools in the released application image.
func TestReceiptProcessPDFAndImage(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("run inside combined image")
	}
	if _, err := exec.LookPath("tesseract"); err != nil {
		t.Skip("OCR tools not installed")
	}
	sb, err := processor.NewProcessSandbox("512m", 120*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "input.pdf"), makeReceiptPDF(), 0600); err != nil {
		t.Fatal(err)
	}
	ocr := &ReceiptOCR{SB: sb, Langs: "eng", MaxPages: 3, MaxTextBytes: 100000}
	text, perr := ocr.ocr(context.Background(), dir, ".pdf", "eng")
	if perr != nil {
		t.Fatal(perr)
	}
	result := localParseReceipt(text)
	if result.TotalMinor != "1234" || result.Currency != "USD" || !strings.Contains(text, "GARDEN") {
		t.Fatalf("PDF OCR failed: %q", text)
	}
	pages, err := filepath.Glob(filepath.Join(dir, "page-*.png"))
	if err != nil || len(pages) == 0 {
		t.Fatal("no rendered receipt")
	}
	page, err := os.ReadFile(pages[0])
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "input.png"), page, 0600); err != nil {
		t.Fatal(err)
	}
	text, perr = ocr.ocr(context.Background(), dir, ".png", "eng")
	if perr != nil {
		t.Fatal(perr)
	}
	if localParseReceipt(text).TotalMinor != "1234" {
		t.Fatalf("PNG OCR failed: %q", text)
	}
	// Also exercise the image-normalization branch used for JPEG receipts.
	if _, err := sb.Run(context.Background(), dir, "vips", "jpegsave", "input.png", "input.jpg"); err != nil {
		t.Fatal(err)
	}
	text, perr = ocr.ocr(context.Background(), dir, ".jpg", "eng")
	if perr != nil {
		t.Fatal(perr)
	}
	if localParseReceipt(text).TotalMinor != "1234" {
		t.Fatalf("JPEG OCR failed: %q", text)
	}
}
