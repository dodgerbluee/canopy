package engine

import (
	"fmt"
	"time"
)

// NotificationInstant converts a local due date + wall-clock time-of-day
// ("HH:MM", default 09:00) in the plan timezone into a real instant,
// following the 03-contracts DST rules:
//   - nonexistent local time (spring-forward gap) moves to the next valid
//     instant (the transition itself);
//   - ambiguous local time (fall-back overlap) uses the earlier offset.
//
// Notes describe any adjustment so the UI can say so honestly.
func NotificationInstant(d Date, timeOfDay, tz string) (time.Time, []string, error) {
	if timeOfDay == "" {
		timeOfDay = "09:00"
	}
	var hh, mm int
	if _, err := fmt.Sscanf(timeOfDay, "%02d:%02d", &hh, &mm); err != nil ||
		hh < 0 || hh > 23 || mm < 0 || mm > 59 || len(timeOfDay) != 5 {
		return time.Time{}, nil, fmt.Errorf("engine: expected HH:MM, found %q", timeOfDay)
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		return time.Time{}, nil, fmt.Errorf("engine: unknown timezone %q", tz)
	}
	// wall is the requested wall-clock reading as a naive template.
	wall := time.Date(d.Year, time.Month(d.Month), d.Day, hh, mm, 0, 0, time.UTC)
	wallStr := wall.Format("2006-01-02 15:04")

	// Go normalizes a nonexistent local time forward past the gap, so the
	// round-tripped wall reading no longer matches what we asked for.
	nominal := time.Date(d.Year, time.Month(d.Month), d.Day, hh, mm, 0, 0, loc)
	if nominal.In(loc).Format("2006-01-02 15:04") != wallStr {
		trans := findTransition(nominal, wall, loc)
		return trans, []string{fmt.Sprintf(
			"%s does not exist in %s (DST); moved to next valid instant %s",
			wallStr, tz, trans.In(loc).Format("2006-01-02 15:04 MST"))}, nil
	}

	// Ambiguous local time: the same wall reading maps to two instants.
	// Scan plausible offsets-shift distances (30min/1h/2h transitions all
	// exist in IANA zones) and keep the earliest instant that shares the
	// wall clock.
	earlier := nominal
	ambiguous := false
	for _, delta := range []time.Duration{
		-2 * time.Hour, -time.Hour, -30 * time.Minute,
		30 * time.Minute, time.Hour, 2 * time.Hour,
	} {
		alt := nominal.Add(delta)
		if alt.In(loc).Format("2006-01-02 15:04") == wallStr {
			ambiguous = true
			if alt.Before(earlier) {
				earlier = alt
			}
		}
	}
	if ambiguous {
		return earlier, []string{fmt.Sprintf(
			"%s is ambiguous in %s (DST end); using the earlier offset",
			wallStr, tz)}, nil
	}
	return nominal, nil, nil
}

// findTransition binary-searches for the first instant whose local clock
// reading is at or past the requested (nonexistent) wall reading — i.e.
// the DST jump target. The local clock is monotonic non-decreasing in the
// instant, so the predicate is binary-searchable.
func findTransition(nominal, wall time.Time, loc *time.Location) time.Time {
	lo, hi := nominal.Add(-12*time.Hour), nominal.Add(12*time.Hour)
	atOrPast := func(t time.Time) bool {
		return t.In(loc).Format("2006-01-02 15:04") >= wall.Format("2006-01-02 15:04")
	}
	for hi.Sub(lo) > time.Second {
		mid := lo.Add(hi.Sub(lo) / 2)
		if atOrPast(mid) {
			hi = mid
		} else {
			lo = mid
		}
	}
	// IANA transitions land on whole seconds (usually whole minutes): floor
	// to the second, then walk forward to the exact first valid instant.
	hi = hi.Truncate(time.Second)
	for !atOrPast(hi) {
		hi = hi.Add(time.Second)
	}
	return hi
}
