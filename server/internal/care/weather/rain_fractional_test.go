package weather

import (
	"testing"
	"time"
)

func TestRecentRainFractionalOffsetMidnight(t *testing.T) {
	loc, _ := time.LoadLocation("Asia/Kolkata")
	end := time.Date(2026, 11, 15, 19, 0, 0, 0, time.UTC) // local 00:30 on November 16
	body := rainSeries(end, 1, func(i int) (*float64, *float64) { return f64p(2), f64p(0) })
	days, err := parseRecentRain(body, loc, end)
	if err != nil {
		t.Fatal(err)
	}
	if days[0].Day != "2026-11-16" || days[0].AmountMm == nil || *days[0].AmountMm != 1 || days[1].AmountMm == nil || *days[1].AmountMm != 1 {
		t.Fatalf("midnight interval not apportioned: %+v", days)
	}
}
