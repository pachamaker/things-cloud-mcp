// Package civil models calendar dates in the owner's timezone and their Things
// wire encoding (ADR-0003). Things stores day-precision fields (sr, tir, dd) as
// the Unix time of 00:00 UTC on the owner's local calendar date
// (docs/protocol/things-cloud.md §4), so "today" needs the owner's zone, not
// the UTC day (KI-03).
package civil

import (
	"fmt"
	"time"
	// The container image has no zoneinfo; embed it so LoadLocation works there.
	_ "time/tzdata"
)

const layout = "2006-01-02"

// Date is a calendar day with no time and no zone.
type Date struct {
	Year  int
	Month time.Month
	Day   int
}

// DateOf returns the calendar day of t in t's own location.
func DateOf(t time.Time) Date {
	y, m, d := t.Date()
	return Date{Year: y, Month: m, Day: d}
}

// Parse reads a YYYY-MM-DD date.
func Parse(s string) (Date, error) {
	t, err := time.Parse(layout, s)
	if err != nil {
		return Date{}, err
	}
	return DateOf(t), nil
}

// FromWire decodes a Things day value: Unix seconds of 00:00 UTC.
func FromWire(sec int64) Date {
	return DateOf(time.Unix(sec, 0).UTC())
}

// FromWireTime decodes a day value the SDK already turned into a time.Time.
func FromWireTime(t time.Time) Date {
	return DateOf(t.UTC())
}

// Wire encodes d the way Things stores day-precision fields: 00:00 UTC of d.
func (d Date) Wire() int64 {
	return d.midnightUTC().Unix()
}

func (d Date) midnightUTC() time.Time {
	return time.Date(d.Year, d.Month, d.Day, 0, 0, 0, 0, time.UTC)
}

// AddDays returns the date n days after d (before it for negative n).
func (d Date) AddDays(n int) Date {
	return DateOf(d.midnightUTC().AddDate(0, 0, n))
}

// Compare returns -1, 0 or +1 as d is before, equal to or after o.
func (d Date) Compare(o Date) int {
	return d.midnightUTC().Compare(o.midnightUTC())
}

// Before reports whether d is an earlier day than o.
func (d Date) Before(o Date) bool { return d.Compare(o) < 0 }

// After reports whether d is a later day than o.
func (d Date) After(o Date) bool { return d.Compare(o) > 0 }

// String formats d as YYYY-MM-DD.
func (d Date) String() string {
	return d.midnightUTC().Format(layout)
}

// Start returns the first instant of d in loc. time.Date alone gets DST changes
// at midnight wrong: where midnight is skipped (America/Santiago in September)
// it returns 23:00 of the previous day, and where midnight repeats (Asia/Amman
// in 2021) it may return the later of the two.
func (d Date) Start(loc *time.Location) time.Time {
	t := time.Date(d.Year, d.Month, d.Day, 0, 0, 0, 0, loc)
	if DateOf(t).Before(d) {
		_, end := t.ZoneBounds()
		return end
	}
	if prev := t.Add(-time.Nanosecond); DateOf(prev) == d {
		_, offset := prev.Zone()
		return time.Date(d.Year, d.Month, d.Day, 0, 0, 0, 0, time.FixedZone("", offset)).In(loc)
	}
	return t
}

// Clock reports the current instant; tests inject a fixed one.
type Clock interface {
	Now() time.Time
}

// ClockFunc adapts a function to Clock.
type ClockFunc func() time.Time

// Now calls f.
func (f ClockFunc) Now() time.Time { return f() }

// SystemClock is the wall clock.
type SystemClock struct{}

// Now returns time.Now().
func (SystemClock) Now() time.Time { return time.Now() }

// Today is the calendar day in loc at the clock's current instant.
func Today(c Clock, loc *time.Location) Date {
	return DateOf(c.Now().In(loc))
}

// LoadLocation resolves an IANA zone name such as "Europe/Moscow". It refuses
// "" and "Local": the host's zone is an accident of deployment (ADR-0003,
// option B).
func LoadLocation(name string) (*time.Location, error) {
	if name == "" || name == "Local" {
		return nil, fmt.Errorf("want an IANA time zone name such as Europe/Moscow, got %q", name)
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		return nil, fmt.Errorf("want an IANA time zone name such as Europe/Moscow: %w", err)
	}
	return loc, nil
}
