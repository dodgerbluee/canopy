// Package photoquality ranks normalized thumbnails locally. It does not
// identify plants or infer health. Never pass original/untrusted image bytes.
package photoquality

import (
	"bytes"
	"errors"
	"image"
	_ "image/jpeg"
	"math"
	"time"
)

const Version = 1

// Score accepts only bounded JPEG derivatives from the existing sandbox.
func Score(data []byte) (float64, error) {
	if len(data) > 2<<20 {
		return 0, errors.New("thumbnail exceeds scoring limit")
	}
	config, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || format != "jpeg" || config.Width < 1 || config.Height < 1 || config.Width > 1024 || config.Height > 1024 {
		return 0, errors.New("unsupported normalized thumbnail")
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return 0, err
	}
	edge := 96
	w, h := min(edge, config.Width), min(edge, config.Height)
	sample := make([]float64, w*h)
	var good, green, total, square, gradient float64
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			r16, g16, b16, _ := img.At(img.Bounds().Min.X+x*config.Width/w, img.Bounds().Min.Y+y*config.Height/h).RGBA()
			r, g, b := float64(r16)/65535, float64(g16)/65535, float64(b16)/65535
			l := .2126*r + .7152*g + .0722*b
			sample[y*w+x] = l
			total += l
			square += l * l
			if l > .06 && l < .94 {
				good++
			}
			// Prefer visible foliage in the central plant region over distant grass.
			if g > r*1.08 && g > b*1.08 && g-r > .025 && g > .12 && x >= w/8 && x < w*7/8 && y < h*7/8 {
				green++
			}
			if x > 0 {
				gradient += math.Abs(l - sample[y*w+x-1])
			}
			if y > 0 {
				gradient += math.Abs(l - sample[(y-1)*w+x])
			}
		}
	}
	n := float64(w * h)
	mean := total / n
	lighting := good / n
	detail := math.Min(1, gradient/(2*n)*10)
	foliage := math.Min(1, green/n/.25)
	contrast := math.Min(1, math.Sqrt(math.Max(0, square/n-mean*mean))/.20)
	return math.Max(0, math.Min(100, 35*lighting+25*detail+25*foliage+15*contrast)), nil
}

type Candidate struct {
	Score      float64
	Scored     bool
	Viewpoint  string
	CapturedAt *time.Time
}

// Prefer avoids cover churn and historical-import regressions. Freshness is
// a small bonus only when quality remains comparable; upload time is never
// substituted for an unknown capture date.
func Prefer(next, current Candidate) bool {
	if !next.Scored || next.Viewpoint != "WHOLE" || next.Score < 45 {
		return false
	}
	if current.CapturedAt != nil && (next.CapturedAt == nil || next.CapturedAt.Before(*current.CapturedAt)) {
		return false
	}
	if current.Viewpoint != "WHOLE" {
		return true
	}
	if !current.Scored {
		return false
	}
	freshness := 0.0
	if next.CapturedAt != nil && current.CapturedAt != nil {
		freshness = math.Min(8, math.Max(0, next.CapturedAt.Sub(*current.CapturedAt).Hours()/24/7*2))
	}
	return next.Score >= current.Score-2 && next.Score+freshness >= current.Score+7
}
