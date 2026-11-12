package processor

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
)

// Derivative is one produced variant, sitting in workDir ready to stage.
type Derivative struct {
	Name    string
	Width   int
	Height  int
	SrcPath string // host path inside workDir
	Bytes   int64
}

// BudgetError means the source exceeds the pixel budget. It is detected
// from header dimensions only — no pixel was decoded when it fires.
type BudgetError struct {
	W, H int
	Max  int64
}

func (e *BudgetError) Error() string {
	return fmt.Sprintf("image dimensions %dx%d (%d pixels) exceed budget %d", e.W, e.H, int64(e.W)*int64(e.H), e.Max)
}

// headerRe matches vipsheader -a's first line:
// "/work/in.jpg: 4032x3024 8-bit srgb, 3 bands, heifload"
var headerRe = regexp.MustCompile(`: (\d+)x(\d+) `)

// ParseHeader extracts width/height from vipsheader -a output.
func ParseHeader(out []byte) (int, int, error) {
	m := headerRe.FindSubmatch(out)
	if m == nil {
		return 0, 0, fmt.Errorf("vipsheader output not understood: %q", tail(out, 200))
	}
	w, err1 := strconv.Atoi(string(m[1]))
	h, err2 := strconv.Atoi(string(m[2]))
	if err1 != nil || err2 != nil || w <= 0 || h <= 0 {
		return 0, 0, fmt.Errorf("vipsheader dimensions invalid: %q", tail(out, 200))
	}
	return w, h, nil
}

// Run executes the normalization pipeline in the sandbox and returns the
// derivatives written into workDir. The pixel budget is checked from the
// header before any decode; the decode itself uses the strict loader
// (--fail) so truncated or corrupt scans fail loudly instead of
// producing half-an-image derivatives (vips defaults to lenient).
// Orientation is applied explicitly (vips 8.15's heifload does not
// expose EXIF orientation, so auto-rotate cannot be trusted for HEIC),
// and every output is saved with --keep none so EXIF/GPS never survives
// normalization.
func Run(ctx context.Context, sb Sandbox, workDir, srcName string, orientation int, maxPixels int64) ([]Derivative, error) {
	head, err := sb.Run(ctx, workDir, "vipsheader", "-a", "/work/"+srcName)
	if err != nil {
		return nil, err // SandboxError => corrupt/undecodable
	}
	w, h, err := ParseHeader(head.Stdout)
	if err != nil {
		return nil, err
	}
	if int64(w)*int64(h) > maxPixels {
		return nil, &BudgetError{W: w, H: h, Max: maxPixels}
	}

	// strict full decode once; every derivative works off the raw
	// intermediate, so loader leniency can never leak through
	loader, loaderFlags := loaderFor(srcName)
	argv := append([]string{"vips", loader}, loaderFlags...)
	argv = append(argv, "/work/"+srcName, "/work/full.v")
	if _, err := sb.Run(ctx, workDir, argv...); err != nil {
		return nil, err
	}

	var ds []Derivative
	for _, v := range Variants {
		cur := "mid-" + v.Name + ".v"
		if v.LongEdge > 0 {
			if _, err := sb.Run(ctx, workDir, "vips", "thumbnail",
				"--size", "down", "--no-rotate",
				"/work/full.v", "/work/"+cur, strconv.Itoa(v.LongEdge)); err != nil {
				return nil, err
			}
		} else {
			if _, err := sb.Run(ctx, workDir, "vips", "copy",
				"/work/full.v", "/work/"+cur); err != nil {
				return nil, err
			}
		}
		for i, op := range rotateChain(orientation) {
			next := fmt.Sprintf("rot-%s-%d.v", v.Name, i)
			// vips rot/flip take the enum as a trailing parameter
			argv := []string{"vips", op[0], "/work/" + cur, "/work/" + next, op[1]}
			if _, err := sb.Run(ctx, workDir, argv...); err != nil {
				return nil, err
			}
			cur = next
		}
		out := "out-" + v.Name + ".jpg"
		if _, err := sb.Run(ctx, workDir, "vips", "jpegsave",
			"--keep", "none", "--Q", strconv.Itoa(v.Quality),
			"/work/"+cur, "/work/"+out); err != nil {
			return nil, err
		}
		hd, err := sb.Run(ctx, workDir, "vipsheader", "-a", "/work/"+out)
		if err != nil {
			return nil, err
		}
		ow, oh, err := ParseHeader(hd.Stdout)
		if err != nil {
			return nil, err
		}
		host := filepath.Join(workDir, out)
		st, err := os.Stat(host)
		if err != nil {
			return nil, err
		}
		ds = append(ds, Derivative{Name: v.Name, Width: ow, Height: oh, SrcPath: host, Bytes: st.Size()})
	}
	return ds, nil
}

// loaderFor picks the strict loader for a server-chosen extension.
// heifload needs no --fail: the HEIF container parse is strict already.
func loaderFor(srcName string) (op string, flags []string) {
	switch ext := filepath.Ext(srcName); ext {
	case ".png":
		return "pngload", []string{"--fail"}
	case ".webp":
		return "webpload", []string{"--fail"}
	case ".heic", ".heif":
		return "heifload", nil
	default:
		return "jpegload", []string{"--fail"}
	}
}

// rotateChain maps EXIF orientation to vips ops. vips rot d90 rotates
// clockwise — verified against vips's own EXIF auto-rotate on a rotated
// JPEG fixture (mean abs diff ~1/255, i.e. identical orientation).
// flip horizontal mirrors left-right. Orientations 5/7 combine a
// rotation with a mirror.
func rotateChain(o int) [][]string {
	switch o {
	case 2:
		return [][]string{{"flip", "horizontal"}}
	case 3:
		return [][]string{{"rot", "d180"}}
	case 4:
		return [][]string{{"flip", "vertical"}}
	case 5:
		return [][]string{{"rot", "d90"}, {"flip", "horizontal"}}
	case 6:
		return [][]string{{"rot", "d90"}}
	case 7:
		return [][]string{{"rot", "d270"}, {"flip", "horizontal"}}
	case 8:
		return [][]string{{"rot", "d270"}}
	}
	return nil
}
