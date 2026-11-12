// Package engine is the pure care engine (T22): a deterministic function of
// profile-version rules + plant context + user decisions + weather snapshot
// + clock. It performs no DB, network or HTTP I/O — the scheduler (T23) and
// notification adapters call into it.
//
// Contract source: 03-contracts.md "Scheduling contract" and the frozen
// recurrence jsonb shape in ADR-0002. Only the four supported recurrence
// forms exist (never arbitrary RRULE): every-N-days (anchored to plan start
// or last qualifying completion), selected weekdays, annual month/day
// windows with year cadence, and one-time dates. Horizon is 60 days; dates
// beyond it are generated on future ticks.
package engine

import (
	"fmt"
	"time"
)

// Date is a civil calendar date with no zone or instant component. All
// care scheduling happens on local dates in the plan timezone; conversion
// to instants happens only at the notification boundary.
type Date struct {
	Year, Month, Day int
}

// DateFrom reads the calendar date off t in t's own location.
func DateFrom(t time.Time) Date { return Date{t.Year(), int(t.Month()), t.Day()} }

// ParseDate parses YYYY-MM-DD and rejects impossible calendar days.
func ParseDate(s string) (Date, error) {
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		return Date{}, fmt.Errorf("engine: expected YYYY-MM-DD, found %q", s)
	}
	return DateFrom(t), nil
}

func (d Date) String() string { return fmt.Sprintf("%04d-%02d-%02d", d.Year, d.Month, d.Day) }

// dayNum is the number of days since 1970-01-01 (negative for earlier).
// Howard Hinnant's civil-from-days algorithm: pure integer math, so spans
// of any length are exact — time.Duration would silently overflow past
// ~292 years and hang generation loops (a fuzz seed proved it).
func (d Date) dayNum() int64 {
	y := d.Year
	m := d.Month
	if m <= 2 {
		y--
	}
	era := int64(y) / 400
	if y < 0 {
		era = (int64(y) - 399) / 400
	}
	yoe := int64(y) - era*400 // [0, 399]
	mp := m + 9
	if m > 2 {
		mp = m - 3
	}
	doy := int64(153*mp+2)/5 + int64(d.Day) - 1
	doe := yoe*365 + yoe/4 - yoe/100 + doy
	return era*146097 + doe - 719468
}

// civilFromDays is the exact inverse of dayNum.
func civilFromDays(z int64) (y, m, d int) {
	z += 719468
	era := z / 146097
	if z < 0 {
		era = (z - 146096) / 146097
	}
	doe := z - era*146097 // [0, 146096]
	yoe := (doe - doe/1460 + doe/36524 - doe/146096) / 365
	y = int(yoe) + int(era*400)
	doy := doe - (yoe*365 + yoe/4 - yoe/100)
	mp := (5*doy + 2) / 153
	d = int(doy-(153*mp+2)/5) + 1
	if mp >= 10 {
		m = int(mp) - 9
		y++
	} else {
		m = int(mp) + 3
	}
	return
}

func (d Date) AddDays(n int) Date {
	y, m, day := civilFromDays(d.dayNum() + int64(n))
	return Date{y, m, day}
}

// DaysUntil counts whole days from d to o (negative if o precedes d).
func (d Date) DaysUntil(o Date) int { return int(o.dayNum() - d.dayNum()) }

// Weekday: 1970-01-01 (dayNum 0) was a Thursday (time.Sunday == 0).
func (d Date) Weekday() time.Weekday {
	n := (d.dayNum() + 4) % 7
	if n < 0 {
		n += 7
	}
	return time.Weekday(n)
}

// Before reports whether d precedes o. DaysUntil(o) is positive when o is
// in the future of d, so "d before o" means DaysUntil(o) > 0.
func (d Date) Before(o Date) bool { return d.DaysUntil(o) > 0 }
func (d Date) After(o Date) bool  { return d.DaysUntil(o) < 0 }

func isLeap(y int) bool {
	return y%4 == 0 && (y%100 != 0 || y%400 == 0)
}

// HorizonDays is the fixed schedule horizon from 03-contracts.
const HorizonDays = 60
