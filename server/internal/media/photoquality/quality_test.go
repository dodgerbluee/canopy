package photoquality

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"testing"
	"time"
)

func TestScorePrefersVisibleDetailedFoliageOverDarkOrFeaturelessFrames(t *testing.T) {
	score := func(kind string) float64 {
		img := image.NewRGBA(image.Rect(0, 0, 96, 96))
		for y := 0; y < 96; y++ {
			for x := 0; x < 96; x++ {
				c := color.RGBA{110, 110, 110, 255}
				if kind == "dark" {
					c = color.RGBA{3, 3, 3, 255}
				}
				if kind == "foliage" {
					if (x/3+y/3)%2 == 0 {
						c = color.RGBA{40, 150, 45, 255}
					} else {
						c = color.RGBA{80, 115, 45, 255}
					}
				}
				img.SetRGBA(x, y, c)
			}
		}
		var data bytes.Buffer
		if err := jpeg.Encode(&data, img, &jpeg.Options{Quality: 90}); err != nil {
			t.Fatal(err)
		}
		value, err := Score(data.Bytes())
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	foliage, plain, dark := score("foliage"), score("plain"), score("dark")
	if foliage < plain+10 || plain < dark+10 {
		t.Fatal("quality ranking", foliage, plain, dark)
	}
	if _, err := Score([]byte("not a processed thumbnail")); err == nil {
		t.Fatal("accepted invalid thumbnail")
	}
}
func TestCoverPolicyPreservesHistoryViewpointAndQuality(t *testing.T) {
	now := time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)
	older := now.Add(-24 * time.Hour)
	fresh := now.Add(35 * 24 * time.Hour)
	base := Candidate{Score: 70, Scored: true, Viewpoint: "WHOLE", CapturedAt: &now}
	for _, tc := range []struct {
		name string
		next Candidate
		want bool
	}{
		{"clearly better", Candidate{90, true, "WHOLE", &now}, true},
		{"blurry new", Candidate{35, true, "WHOLE", &fresh}, false},
		{"archived import", Candidate{100, true, "WHOLE", &older}, false},
		{"fruit closeup", Candidate{100, true, "FRUIT", &fresh}, false},
		{"unknown age", Candidate{100, true, "WHOLE", nil}, false},
		{"similar currentness", Candidate{70, true, "WHOLE", &fresh}, true},
		{"no churn", base, false},
		{"unscored", Candidate{90, false, "WHOLE", &fresh}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := Prefer(tc.next, base); got != tc.want {
				t.Fatal(got, tc.want)
			}
		})
	}
}
