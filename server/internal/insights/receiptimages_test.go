package insights

import (
	"fmt"
	"strings"
	"testing"
)

func TestReceiptDetailPlanCoversLongReceiptsWithinBounds(t *testing.T) {
	for _, dim := range [][2]int{{701, 1561}, {1200, 6000}, {6000, 1200}} {
		w, h := dim[0], dim[1]
		rects := receiptDetailPlan(w, h)
		if len(rects) < 2 || len(rects) > 4 {
			t.Fatalf("plan %dx%d count %d", w, h, len(rects))
		}
		end := 0
		for _, r := range rects {
			if r.Left < 0 || r.Top < 0 || r.Width <= 0 || r.Height <= 0 || r.Left+r.Width > w || r.Top+r.Height > h {
				t.Fatalf("out of bounds: %+v", r)
			}
			start, stop := r.Top, r.Top+r.Height
			if w > h {
				start, stop = r.Left, r.Left+r.Width
			}
			if start > end {
				t.Fatal("receipt detail plan leaves a gap")
			}
			if stop > end {
				end = stop
			}
		}
		long := h
		if w > h {
			long = w
		}
		if end != long {
			t.Fatal("receipt details do not cover full long axis")
		}
	}
	for _, dim := range [][2]int{{0, 1000}, {1000, 0}, {-1, 1000}, {700, 700}} {
		if len(receiptDetailPlan(dim[0], dim[1])) != 0 {
			t.Fatal("invalid/small image unexpectedly tiled")
		}
	}
}
func tsvWord(left, top, width, height int, text string) string {
	return fmt.Sprintf("5\t1\t1\t1\t1\t1\t%d\t%d\t%d\t%d\t50\t%s\n", left, top, width, height, text)
}
func TestReceiptDateRegionsUseCoordinatesNotDamagedDateText(t *testing.T) {
	raw := "level\tpage_num\tblock_num\tpar_num\tline_num\tword_num\tleft\ttop\twidth\theight\tconf\ttext\n" + tsvWord(50, 480, 140, 30, "10/.5/2026") + tsvWord(40, 900, 150, 30, "07/23/2028") + tsvWord(20, 1200, 150, 30, "1111222233334444") + tsvWord(0, 0, 0, 0, "10/15/2026")
	regions := receiptDateRegions(raw, 701, 1561)
	if len(regions) != 2 {
		t.Fatalf("date candidates %v", regions)
	}
	for _, r := range regions {
		if r.Left < 0 || r.Top < 0 || r.Width <= 0 || r.Height <= 0 || r.Left+r.Width > 701 || r.Top+r.Height > 1561 {
			t.Fatalf("bad date crop %+v", r)
		}
	}
	if regions[0].Left > 50 || regions[0].Top > 480 || regions[0].Left+regions[0].Width < 190 || regions[0].Top+regions[0].Height < 510 {
		t.Fatal("date crop lost source word pixels")
	}
}
func TestAgreeingReceiptDateRequiresTwoUnambiguousReadings(t *testing.T) {
	for _, tc := range []struct{ a, b, want string }{
		{"10/15/2026", "10/15/2026", "2026-10-15"},
		{"07/23/2028", "23/07/2028", "2028-07-23"},
		{"2026-10-15", "10/15/2026", "2026-10-15"},
		{"10/5/2026", "10/5/2026", ""},
		{"10/.5/2026", "10/15/2026", ""},
		{"31/02/2026", "31/02/2026", ""},
		{"10/15/2026", "10/16/2026", ""},
		{"10/15/2026 10/16/2026", "10/15/2026", ""},
	} {
		if got := agreeingReceiptDate(tc.a, tc.b); got != tc.want {
			t.Errorf("date agreement %q / %q -> %q expected %q", tc.a, tc.b, got, tc.want)
		}
	}
}
func TestResolveReceiptDateDoesNotKeepUnsupportedModelGuess(t *testing.T) {
	for _, tc := range []struct {
		model string
		dates []string
		want  string
	}{
		{"2026-10-05", []string{"2026-10-15"}, "2026-10-15"},
		{"", []string{"2026-10-15"}, ""},
		{"2026-10-05", nil, ""},
		{"2026-10-17", []string{"2026-10-15", "2026-10-17"}, "2026-10-17"},
		{"2026-10-05", []string{"2026-10-15", "2026-10-17"}, ""},
	} {
		if got := resolveReceiptDate(tc.model, tc.dates); got != tc.want {
			t.Errorf("resolved date %q from %v ->%q expected %q", tc.model, tc.dates, got, tc.want)
		}
	}
}
func TestReceiptOCRHintsKeepNamesWithoutWeakDateOrPaymentText(t *testing.T) {
	got := receiptOCRHints("Example Nursery\n10/.5/2026 4:56:22 PM\n491234 SHR Viburnum 3 GAL\nTaxus shrub\nTax 3.50\nVISA xxxx1234\nAuth:123456\n")
	if !strings.Contains(got, "Viburnum") || !strings.Contains(got, "Taxus shrub") {
		t.Fatal("product hint text lost")
	}
	for _, s := range []string{"2026", "4:56", "VISA", "Auth", "xxxx1234", "123456"} {
		if strings.Contains(got, s) {
			t.Fatalf("weak date/payment detail forwarded in hint: %q", s)
		}
	}
}

func TestReceiptDateRegionIsTightAndClamped(t *testing.T) {
	regions := receiptDateRegions(tsvWord(56, 484, 136, 30, "10/.5/2026"), 701, 1561)
	if len(regions) != 1 || regions[0] != (receiptRect{41, 469, 166, 60}) {
		t.Fatalf("date region must use word height padding: %+v", regions)
	}
	edge := receiptDateRegions(tsvWord(2, 3, 100, 20, "07/23/2028"), 701, 1561)
	if len(edge) != 1 || edge[0] != (receiptRect{0, 0, 114, 35}) {
		t.Fatalf("clamped crop %+v", edge)
	}
}

func TestReceiptDateAgreementRejectsPartialOrExtraAmbiguousTokens(t *testing.T) {
	for _, text := range []string{"110/15/2026", "10/15/20260", "10/15/2026 10/05/2026", "010/15/2026", "10..15/2026", "10/15/2026/7"} {
		if got := agreeingReceiptDate(text, "10/15/2026"); got != "" {
			t.Errorf("partial or ambiguous read %q accepted %q", text, got)
		}
	}
}
