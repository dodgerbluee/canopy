package insights

// Evidence preparation for the image receipt path (T33 follow-up). The
// problem this file exists for was proven against an actual receipt: the
// overview image alone loses small date digits and garbles product
// names, and full-page OCR misreads the date as well. Enlarged date
// regions read with tesseract PSM 6 on 3x and 4x RGB crops agree on the
// correct date where the full-image model drops a digit, and an enlarged
// grayscale whole-page read helps with cultivar spelling.
//
// So the receipt run sends ONE native vision call carrying: the full-
// resolution overview, enlarged date closeups, macro detail strips, and
// untrusted OCR hint text; and it corroborates the printed date from two
// focused OCR reads. Nothing here hardcodes a vendor, an item, a date or
// any receipt fact: the plan is geometry, the regions come from word
// coordinates, and every date must be read from a focused OCR pass and
// agree with a second one before it can be used.
//
// All pixel work happens inside the existing pinned media sandbox
// (direct argv, no shell, no Go decoding). Every read is bounded and the
// stored object is never modified.

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	dbgen "canopy/server/db/generated"
	"canopy/server/internal/media/processor"
)

const (
	// receiptMaxImages bounds the whole evidence set: overview + date
	// closeups + macro strips. More images cost tokens without adding
	// information the overview does not already carry.
	receiptMaxImages = 5
	// receiptMaxDatePixels bounds one scaled date closeup: beyond this a
	// crop is skipped rather than allocating unbounded pixels.
	receiptMaxDatePixels int64 = 4_000_000
	// receiptMaxWorkPixels bounds any image the pipeline asks the sandbox
	// to produce (crop, resize, whole-page read). It is the same figure
	// the media pipeline uses before touching a pixel.
	receiptMaxWorkPixels int64 = processor.DefaultMaxPixels
	// receiptMaxPagePixels bounds the whole-page OCR read only. The
	// source probe still uses the media pipeline's bound; this is the
	// working image the enlarged page read is allowed to produce, and it
	// is deliberately smaller so a huge page is downsampled into it.
	receiptMaxPagePixels int64 = 16_000_000
	// receiptMaxOCRParse caps how much tesseract TSV is parsed.
	receiptMaxOCRParse = 256 << 10
	// receiptMaxHints caps the untrusted hint text carried to the model.
	receiptMaxHints = 16 << 10
	// receiptMaxDateOCR caps one focused date read.
	receiptMaxDateOCR = 2048
	// receiptDateOCRChars is the whitelist for focused date reads: the
	// digits and the separators a printed date can contain.
	receiptDateOCRChars = "0123456789/.-"
)

// receiptRect is one pixel region of the source image.
type receiptRect struct {
	Left, Top, Width, Height int
}

// receiptDetailPlan tiles the long axis of a tall (or wide) receipt into
// at most four overlapping strips that together cover the whole image.
// Small or near-square images need no strips: the overview already
// resolves them. Input invalid -> nil. There is no vendor-specific
// layout here: the only assumption is that a receipt is long.
func receiptDetailPlan(w, h int) []receiptRect {
	if w <= 0 || h <= 0 {
		return nil
	}
	long, short := h, w
	if w > h {
		long, short = w, h
	}
	if long < 1000 || float64(long)/float64(short) < 1.4 {
		return nil
	}
	overlap := int(math.Ceil(float64(short) * 0.12))
	if overlap < 1 {
		overlap = 1
	}
	if overlap >= short {
		overlap = short - 1
	}
	// n is the fewest strips of one short-side span that cover the long
	// axis, capped at 4. With the cap in force the strips are spread
	// evenly over the whole axis, so coverage stays complete.
	n := int(math.Ceil(float64(long) / float64(short)))
	if n < 1 {
		n = 1
	}
	if n > 4 {
		n = 4
	}
	step := int(math.Ceil(float64(long) / float64(n)))
	if step < 1 {
		step = 1
	}
	out := make([]receiptRect, 0, n)
	for i := 0; i < n; i++ {
		start := i * step
		if start >= long {
			break
		}
		start -= overlap
		if start < 0 {
			start = 0
		}
		stop := (i+1)*step + overlap
		if stop > long {
			stop = long
		}
		if stop-start < 1 {
			continue
		}
		if w > h {
			out = append(out, receiptRect{Left: start, Top: 0, Width: stop - start, Height: h})
		} else {
			out = append(out, receiptRect{Left: 0, Top: start, Width: w, Height: stop - start})
		}
	}
	return out
}

// receiptTSVRows caps how many TSV word rows are scanned.
const receiptTSVRows = 2048

// receiptDateRegions finds the pixel regions where OCR saw a date-shaped
// token, using ONLY the word coordinates the TSV reports. It never
// infers a date value from this pass: the token text only has to look
// date-like (separators plus a four-digit year), because the day digits
// may be damaged exactly where the model drops them (e.g. "07/.3/2028").
// Card and auth digit strings have no separators, so they never match.
//
// Padding is per WORD height: max(12, height/2). A word-scaled floor
// keeps a small word's closeup readable while a wide date word still
// gets margin proportional to itself; a cap would leave the large word
// almost unpadded and the enlargement with nothing to resolve. The
// result is clamped to the original image edges.
func receiptDateRegions(tsv string, w, h int) []receiptRect {
	if w <= 0 || h <= 0 {
		return nil
	}
	var found []receiptRect
	seen := map[receiptRect]bool{}
	rows := 0
	for _, line := range strings.Split(tsv, "\n") {
		if rows >= receiptTSVRows {
			break
		}
		rows++
		cols := strings.Split(line, "\t")
		if len(cols) < 12 {
			continue
		}
		if !receiptDateShaped(cols[11]) {
			continue
		}
		left, err1 := strconv.Atoi(strings.TrimSpace(cols[6]))
		top, err2 := strconv.Atoi(strings.TrimSpace(cols[7]))
		width, err3 := strconv.Atoi(strings.TrimSpace(cols[8]))
		height, err4 := strconv.Atoi(strings.TrimSpace(cols[9]))
		if err1 != nil || err2 != nil || err3 != nil || err4 != nil {
			continue
		}
		// Coordinates are validated BEFORE any padding is applied: a
		// malformed or off-image word box must be rejected, not rescued
		// into a rectangle that happens to overlap the page.
		if width <= 0 || height <= 0 || left < 0 || top < 0 ||
			left >= w || top >= h || left+width > w || top+height > h {
			continue
		}
		// Padding is a floor, not a cap: max(12, height/2). A cap would
		// make the closeup of a large date word barely larger than the
		// word itself, and the enlargement would have nothing to resolve.
		pad := height / 2
		if pad < 12 {
			pad = 12
		}
		r := clampReceiptRect(left-pad, top-pad, width+2*pad, height+2*pad, w, h)
		if r.Width <= 0 || r.Height <= 0 || seen[r] {
			continue
		}
		seen[r] = true
		found = append(found, r)
	}
	sort.Slice(found, func(i, j int) bool {
		if found[i].Top != found[j].Top {
			return found[i].Top < found[j].Top
		}
		return found[i].Left < found[j].Left
	})
	if len(found) > 2 {
		found = found[:2]
	}
	return found
}

// clampReceiptRect fits a padded region into the image. Clamping works
// from the ORIGINAL edges: a negative origin reduces the extent, it does
// not slide the rectangle along the page, which would move the crop off
// the word it was taken for.
func clampReceiptRect(left, top, width, height, w, h int) receiptRect {
	if width <= 0 || height <= 0 || w <= 0 || h <= 0 {
		return receiptRect{}
	}
	if left < 0 {
		width += left
		left = 0
	}
	if top < 0 {
		height += top
		top = 0
	}
	if left >= w || top >= h {
		return receiptRect{}
	}
	if width > w-left {
		width = w - left
	}
	if height > h-top {
		height = h - top
	}
	if width <= 0 || height <= 0 {
		return receiptRect{}
	}
	return receiptRect{Left: left, Top: top, Width: width, Height: height}
}

// receiptDateShaped reports whether OCR text has a date-like shape:
// digits around at least one separator, and a four-digit year. It makes
// no claim about the value, so damaged day digits still match.
func receiptDateShaped(s string) bool {
	s = strings.TrimSpace(s)
	if len(s) < 6 || len(s) > 32 {
		return false
	}
	seps := 0
	digits := 0
	year := false
	for i := 0; i < len(s); i++ {
		switch c := s[i]; {
		case c >= '0' && c <= '9':
			digits++
			if i+3 < len(s) && s[i+1] >= '0' && s[i+1] <= '9' &&
				s[i+2] >= '0' && s[i+2] <= '9' && s[i+3] >= '0' && s[i+3] <= '9' {
				year = true
			}
		case c == '/' || c == '.' || c == '-':
			seps++
		default:
			return false
		}
	}
	return year && seps >= 1 && digits >= 5
}

// receiptTokenRe matches one maximal run of digits and date separators.
// It is deliberately greedy: a run is taken whole, so a date-shaped
// pattern inside a longer numeric string ("110/15/2026", "10/15/20260")
// can never be matched as a partial substring.
var receiptTokenRe = regexp.MustCompile(`[0-9][0-9./-]*`)

// receiptDatePartsRe is the complete shape of one date value: an ISO
// YYYY-MM-DD form, or three numeric groups whose year is exactly four
// digits. Fully anchored, so it describes the whole token rather than a
// fragment of it. Month and day groups may carry a leading zero, but only
// ever exactly two digits: a three-digit group ("10/005/2026") is not a
// date-shaped token at all.
var receiptDatePartsRe = regexp.MustCompile(`^(?:[0-9]{4}-[0-9]{2}-[0-9]{2}|[0-9]{1,2}[./-][0-9]{1,2}[./-][0-9]{4})$`)

// receiptDateTokens extracts the set of unambiguous calendar dates a
// focused OCR read evidences. Extraction is bounded and whole-token:
// damaged digits ("10/.5/2026"), digits glued to a longer numeric run
// ("010/15/2026", "10/15/2026/7"), impossible dates ("31/02/2026") and
// two-digit years are rejected rather than guessed.
//
// A token that has the shape of a date but cannot be resolved to exactly
// one calendar date (the ambiguous-but-valid pair "10/05/2026", or a
// broken group) voids the WHOLE read rather than being skipped beside
// another date. Keeping the neighbour would let a read whose own date
// token is unresolved still corroborate a different number on the same
// line.
func receiptDateTokens(text string) []string {
	seen := map[string]bool{}
	var out []string
	for _, tok := range receiptTokenRe.FindAllString(text, 64) {
		if !receiptDatePartsRe.MatchString(tok) {
			continue
		}
		can, ok := canonicalReceiptDate(tok)
		if !ok {
			// date-shaped form, no single valid reading: nothing from
			// this read may be used
			return nil
		}
		if seen[can] {
			continue
		}
		seen[can] = true
		out = append(out, can)
	}
	sort.Strings(out)
	return out
}

// canonicalReceiptDate resolves one complete token to ISO YYYY-MM-DD. A
// numeric M/D/YYYY or D/M/YYYY reading is accepted only when exactly one
// interpretation is a real calendar date (one side > 12); an
// ambiguous-but-valid pair such as 10/5/2026 stays unknown. A pair like
// 11/11/2026 is ambiguous in form but single in value, so it is fine.
// Month and day groups may carry a leading zero ("10/05/2026" has the
// right shape) but never a third digit, and the year must be exactly
// four digits.
func canonicalReceiptDate(tok string) (string, bool) {
	tok = strings.TrimSpace(tok)
	if m := ocrISODate.FindString(tok); m == tok {
		if _, err := time.Parse("2006-01-02", tok); err == nil {
			return tok, true
		}
		return "", false
	}
	parts := strings.FieldsFunc(tok, func(r rune) bool { return r == '/' || r == '.' || r == '-' })
	if len(parts) != 3 {
		return "", false
	}
	a, errA := strconv.Atoi(parts[0])
	b, errB := strconv.Atoi(parts[1])
	y, errY := strconv.Atoi(parts[2])
	if errA != nil || errB != nil || errY != nil {
		return "", false
	}
	if len(parts[2]) != 4 || len(parts[0]) > 2 || len(parts[1]) > 2 {
		return "", false
	}
	us, usOK := "", false
	if a >= 1 && a <= 12 {
		us, usOK = receiptISO(y, a, b)
	}
	eu, euOK := "", false
	if b >= 1 && b <= 12 {
		eu, euOK = receiptISO(y, b, a)
	}
	switch {
	case usOK && euOK:
		if us == eu {
			// day > 12 is the only way both readings can agree, and then
			// the reading is unambiguous either way
			return us, true
		}
		return "", false
	case usOK:
		return us, true
	case euOK:
		return eu, true
	default:
		return "", false
	}
}

func receiptISO(y, mo, d int) (string, bool) {
	s := fmt.Sprintf("%04d-%02d-%02d", y, mo, d)
	if _, err := time.Parse("2006-01-02", s); err != nil {
		return "", false
	}
	return s, true
}

// agreeingReceiptDate is the date corroboration check: two focused OCR
// reads of the same printed date must each evidence exactly one
// unambiguous calendar date and those dates must be the same. Both reads
// come from the same OCR engine at different scales, so agreement is
// corroboration of a plausible value, not a claim of independent
// accuracy. Anything else (no date, ambiguous read, two different dates)
// yields "".
func agreeingReceiptDate(a, b string) string {
	da, db := receiptDateTokens(a), receiptDateTokens(b)
	if len(da) != 1 || len(db) != 1 || da[0] != db[0] {
		return ""
	}
	return da[0]
}

// resolveReceiptDate decides what the stored draft may claim as the
// purchase date. An empty model date stays empty (never invent one).
// One agreeing pair of focused reads is the stronger evidence and wins,
// because the model is exactly the path observed to drop a faint digit.
// With several candidate dates the model value is kept only when it
// matches one of them; otherwise nothing is verified and the field stays
// empty. Every outcome still carries needsReconciliation for the owner.
func resolveReceiptDate(modelDate string, dates []string) string {
	if strings.TrimSpace(modelDate) == "" {
		return ""
	}
	uniq := map[string]bool{}
	var agree []string
	for _, d := range dates {
		if d == "" || uniq[d] {
			continue
		}
		uniq[d] = true
		agree = append(agree, d)
	}
	if len(agree) == 0 {
		return ""
	}
	if len(agree) == 1 {
		return agree[0]
	}
	model := ""
	if m := ocrISODate.FindString(modelDate); m == strings.TrimSpace(modelDate) {
		model = m
	}
	for _, d := range agree {
		if d == model {
			return d
		}
	}
	return ""
}

// receiptOCRHints trims whole-page OCR text down to what is safe to
// carry as an untrusted spelling aid: name-bearing lines only. The
// payment cut-off is checked FIRST, so a subtotal or tender row can
// never leak into the hints through the date filter below it. Lines
// carrying a date, a bare year or a time are then dropped, because the
// full-page read is the weaker date source and must not be able to
// contradict the focused date checks. A lone "Total" table heading does
// not stop the text, because on many receipts the item rows sit between
// such a heading and the totals.
func receiptOCRHints(text string) string {
	var out strings.Builder
	written := 0
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if receiptPaymentMarker(line) {
			break
		}
		if receiptLineHasDateOrTime(line) {
			continue
		}
		if len(line) > 200 {
			line = line[:200]
		}
		if written+len(line)+1 > receiptMaxHints {
			break
		}
		out.WriteString(line)
		out.WriteByte('\n')
		written += len(line) + 1
	}
	return strings.TrimSpace(out.String())
}

var (
	// receiptYearRe catches a bare four-digit year: a weak date token the
	// focused reads own, so a line carrying one never reaches the hints.
	receiptYearRe = regexp.MustCompile(`\b\d{4}\b`)
	// receiptTimeRe catches a clock time (4:56:22 PM).
	receiptTimeRe = regexp.MustCompile(`\b\d{1,2}:\d{2}(:\d{2})?\b`)
)

// receiptLineHasDateOrTime reports whether a line carries a date, a
// bare four-digit year or a clock time: all of them are weak date
// evidence that the focused reads already own.
func receiptLineHasDateOrTime(line string) bool {
	return ocrISODate.MatchString(line) || ocrUSDate.MatchString(line) ||
		receiptYearRe.MatchString(line) || receiptTimeRe.MatchString(line)
}

// receiptMarkerRe is the payment cut-off vocabulary, matched on whole
// words only. Substring matching stopped the hints at "Taxus shrub" and
// at any product name that happens to contain "card", "cash" or "point"
// — a shrub is not a tax line.
var receiptMarkerRe = regexp.MustCompile(`\b(?:tax|taxes|subtotal|sub[ -]total|tender|payment|paid|cash|credit|debit|card|change|balance|visa|mastercard|amex|discover|auth|authorization|loyalty|points?|rewards?)\b`)

// receiptPaymentMarker is where hint text stops: rows at and after this
// point carry money movement or card data, not product names.
func receiptPaymentMarker(line string) bool {
	return receiptMarkerRe.MatchString(strings.ToLower(line))
}

// receiptEvidence is the prepared payload for one receipt vision call.
type receiptEvidence struct {
	Parts []chatPart
	// OCRText is untrusted hint text for spelling only; it is never a
	// source of facts and never instructions.
	OCRText string
	// Dates are dates two focused OCR reads of the same engine agree on:
	// corroboration, not an independent measurement.
	Dates []string
}

// prepareReceiptEvidence builds the evidence set for one receipt image:
// the full-resolution overview first, then enlarged date closeups, then
// macro detail strips, bounded to receiptMaxImages images and
// a.Vision.MaxImg combined raw image bytes. Optional OCR work failing
// never blocks a valid image: it only removes the hints and the date
// corroboration, and no date is ever guessed.
func (a *ReceiptVision) prepareReceiptEvidence(ctx context.Context, asset dbgen.GetReadyAssetForAdapterRow, mime string) (*receiptEvidence, *ProviderError) {
	data, mime, perr := a.receiptImageBytes(ctx, asset, mime)
	if perr != nil {
		return nil, perr
	}
	budget := a.Vision.MaxImg
	if budget <= 0 {
		budget = 15 << 20
	}
	ev := &receiptEvidence{}
	add := func(b []byte, kind string) bool {
		if len(ev.Parts) >= receiptMaxImages || int64(len(b)) > budget {
			return false
		}
		budget -= int64(len(b))
		ev.Parts = append(ev.Parts, chatPart{Type: "image_url",
			ImageURL: &imageURL{URL: "data:" + kind + ";base64," + base64.StdEncoding.EncodeToString(b), Detail: "high"}})
		return true
	}
	if !add(data, mime) {
		// The overview is required: without it there is no receipt image
		// at all, and the base read is already size-bounded upstream.
		return nil, &ProviderError{Class: ClassPermanent, Err: errors.New("receipt overview image exceeds size bound")}
	}

	workDir, err := os.MkdirTemp("", "canopy-receipt-evidence-")
	if err != nil {
		return nil, &ProviderError{Class: ClassRetryable, Err: err}
	}
	defer func() { _ = os.RemoveAll(workDir) }()
	srcExt := ".jpg"
	if mime == "image/png" {
		srcExt = ".png"
	}
	if err := os.WriteFile(filepath.Join(workDir, "source"+srcExt), data, 0o600); err != nil {
		return nil, &ProviderError{Class: ClassRetryable, Err: err}
	}
	if a.Vision.SB == nil {
		// No sandbox: overview only, no hints, no date corroboration. The
		// model's own date stays as-is and the owner reviews it.
		return ev, nil
	}

	w, h, perr := a.probeImageDims(ctx, workDir, "source"+srcExt)
	if perr != nil {
		return nil, perr
	}

	langs := a.OCR.Langs
	if langs == "" {
		langs = "eng"
	}
	// Layout pass: word coordinates drive the date regions. Its failure
	// is optional: hints and date closeups are simply skipped.
	var regions []receiptRect
	if out, err := a.Vision.SB.Run(ctx, workDir, "tesseract", "source"+srcExt, "stdout", "-l", langs, "tsv"); err == nil {
		tsv := out.Stdout
		if len(tsv) > receiptMaxOCRParse {
			tsv = tsv[:receiptMaxOCRParse]
		}
		regions = receiptDateRegions(string(tsv), w, h)
	}

	// Focused date reads: each region is cropped at native colour depth
	// and enlarged twice (3x and 4x) from the original ROI, each from its
	// own source file so a failed resize can never read the previous
	// region's or the previous scale's output. Both reads use the same
	// OCR engine, so agreement between them is CORROBORATION of a
	// plausible value, not a calibrated independent measurement: the
	// result still carries needsReconciliation and the owner reviews it.
	// Grayscale is deliberately not used here: experiments showed it
	// drops a faint digit.
	for i, r := range regions {
		if !receiptFitsPixels(r.Width, r.Height, receiptMaxWorkPixels) {
			continue
		}
		roi := fmt.Sprintf("roi%d.png", i)
		// vips crop takes: source out LEFT TOP WIDTH HEIGHT.
		if _, err := a.Vision.SB.Run(ctx, workDir, "vips", "crop", "source"+srcExt, roi,
			strconv.Itoa(r.Left), strconv.Itoa(r.Top), strconv.Itoa(r.Width), strconv.Itoa(r.Height)); err != nil {
			continue
		}
		var reads []string
		// closeup is the one render of this region that reaches the
		// provider: the last scale that generated successfully, so a
		// failed generation can never leave a stale file behind.
		closeup := ""
		for _, scale := range []int{3, 4} {
			name := fmt.Sprintf("date%d-%d.png", i, scale)
			if !receiptFitsPixels(r.Width*scale, r.Height*scale, receiptMaxDatePixels) {
				continue // skip an oversized closeup rather than allocate unbounded pixels
			}
			// vips resize takes: source out SCALE --kernel lanczos3. One
			// scale factor, not a width/height pair, so the aspect ratio
			// cannot drift and the call matches the tool's real signature.
			if _, err := a.Vision.SB.Run(ctx, workDir, "vips", "resize", roi, name,
				strconv.Itoa(scale), "--kernel", "lanczos3"); err != nil {
				continue
			}
			closeup = name
			if txt, ok := a.ocrDate(ctx, workDir, name); ok {
				reads = append(reads, txt)
			}
		}
		if len(reads) < 2 {
			continue
		}
		if d := agreeingReceiptDate(reads[0], reads[1]); d != "" && len(ev.Dates) < 2 {
			ev.Dates = append(ev.Dates, d)
		}
		// Only ONE closeup per region joins the provider images: the 3x
		// and 4x renders show the same digits, and sending both would
		// crowd out the macro strips that carry the product names.
		if closeup != "" {
			if b, err := readBounded(filepath.Join(workDir, closeup), budget); err == nil {
				add(b, "image/png")
			}
		}
	}

	// Whole-page enlarged read: spelling aid for product names and
	// cultivars only. The scale is a FLOAT factor, not a floored integer:
	// flooring would turn a needed downsample into 0 and skip the read
	// entirely, when a huge page is exactly the case that has to come
	// down into the bound. Any scale > 0 is run, so an oversized page is
	// downsampled and a small one is enlarged; the output dimensions are
	// verified against the bound before the read.
	if receiptFitsPixels(w, h, receiptMaxWorkPixels) {
		// Division, not w*h: the product of two header-reported dimensions
		// must never be formed as arithmetic.
		scale := math.Min(3, math.Sqrt(float64(receiptMaxPagePixels)/float64(w)/float64(h)))
		if scale > 0 {
			if _, err := a.Vision.SB.Run(ctx, workDir, "vips", "resize", "source"+srcExt, "full.png",
				strconv.FormatFloat(scale, 'f', 8, 64), "--kernel", "lanczos3"); err == nil {
				if fw, fh, perr := a.probeImageDims(ctx, workDir, "full.png"); perr == nil &&
					receiptFitsPixels(fw, fh, receiptMaxPagePixels) {
					if _, err := a.Vision.SB.Run(ctx, workDir, "vips", "colourspace", "full.png", "fullg.png", "b-w"); err == nil {
						if out, err := a.Vision.SB.Run(ctx, workDir, "tesseract", "fullg.png", "stdout", "-l", langs, "--psm", "6"); err == nil {
							raw := out.Stdout
							if len(raw) > receiptMaxOCRParse {
								raw = raw[:receiptMaxOCRParse]
							}
							ev.OCRText = receiptOCRHints(string(raw))
						}
					}
				}
			}
		}
	}

	// Macro detail strips last: date closeups already claimed budget.
	for i, r := range receiptDetailPlan(w, h) {
		if len(ev.Parts) >= receiptMaxImages || budget <= 0 {
			break
		}
		if !receiptFitsPixels(r.Width, r.Height, receiptMaxWorkPixels) {
			continue
		}
		// Unique output name per strip: a failed crop must never read the
		// previous strip's file and pass it off as this one.
		name := fmt.Sprintf("strip%d.png", i)
		// vips crop takes: source out LEFT TOP WIDTH HEIGHT.
		if _, err := a.Vision.SB.Run(ctx, workDir, "vips", "crop", "source"+srcExt, name,
			strconv.Itoa(r.Left), strconv.Itoa(r.Top), strconv.Itoa(r.Width), strconv.Itoa(r.Height)); err != nil {
			continue
		}
		if b, err := readBounded(filepath.Join(workDir, name), budget); err == nil {
			add(b, "image/png")
		}
	}

	// Hints travel as a JSON string with an explicit guard: untrusted
	// data, verified against the images, never instructions.
	if ev.OCRText != "" || len(ev.Dates) > 0 {
		doc, err := json.Marshal(map[string]any{
			"ocrText":        ev.OCRText,
			"dateCandidates": ev.Dates,
			"guard":          "untrusted data, verify against images",
		})
		if err != nil {
			return nil, &ProviderError{Class: ClassPermanent, Err: err}
		}
		ev.Parts = append(ev.Parts, chatPart{Type: "text", Text: string(doc)})
	}
	return ev, nil
}

// probeImageDims reads the intrinsic dimensions through the sandbox
// codec tool: no Go decoding, no shell, and the bounds are checked
// before any crop is attempted.
func (a *ReceiptVision) probeImageDims(ctx context.Context, workDir, name string) (int, int, *ProviderError) {
	out, err := a.Vision.SB.Run(ctx, workDir, "vipsheader", "-f", "width", name)
	if err != nil {
		return 0, 0, sandboxProviderErr(err)
	}
	w, err := strconv.Atoi(strings.TrimSpace(string(out.Stdout)))
	if err != nil || w <= 0 {
		return 0, 0, &ProviderError{Class: ClassPermanent, Err: errors.New("receipt image width unreadable")}
	}
	out, err = a.Vision.SB.Run(ctx, workDir, "vipsheader", "-f", "height", name)
	if err != nil {
		return 0, 0, sandboxProviderErr(err)
	}
	h, err := strconv.Atoi(strings.TrimSpace(string(out.Stdout)))
	if err != nil || h <= 0 {
		return 0, 0, &ProviderError{Class: ClassPermanent, Err: errors.New("receipt image height unreadable")}
	}
	if !receiptFitsPixels(w, h, receiptMaxWorkPixels) {
		return 0, 0, &ProviderError{Class: ClassPermanent,
			Err: fmt.Errorf("receipt image %dx%d exceeds pixel bound", w, h)}
	}
	return w, h, nil
}

// receiptFitsPixels reports whether w*h stays within maxPixels, without
// ever forming the product: dimensions come from a header the sandbox
// reported, so a huge width times a huge height must not be evaluated as
// arithmetic at all. Non-positive dimensions never fit.
func receiptFitsPixels(w, h int, maxPixels int64) bool {
	if w <= 0 || h <= 0 || maxPixels <= 0 {
		return false
	}
	if int64(w) > maxPixels/int64(h) {
		return false
	}
	return int64(w)*int64(h) <= maxPixels
}

// ocrDate performs one focused, whitelisted date read. Its output is
// bounded; a failure is not an error, it just removes one read from the
// agreement check.
func (a *ReceiptVision) ocrDate(ctx context.Context, workDir, name string) (string, bool) {
	out, err := a.Vision.SB.Run(ctx, workDir, "tesseract", name, "stdout", "-l", "eng",
		"--psm", "6", "-c", "tessedit_char_whitelist="+receiptDateOCRChars)
	if err != nil {
		return "", false
	}
	txt := strings.TrimSpace(string(out.Stdout))
	if len(txt) > receiptMaxDateOCR {
		txt = txt[:receiptMaxDateOCR]
	}
	return txt, true
}

// readBounded reads one generated sandbox output file under the
// remaining image budget. Never unbounded, never ReadAll without a cap.
func readBounded(path string, budget int64) ([]byte, error) {
	if budget <= 0 {
		return nil, errors.New("image budget exhausted")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	b, err := io.ReadAll(io.LimitReader(f, budget+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > budget {
		return nil, errors.New("generated image exceeds remaining budget")
	}
	return b, nil
}
