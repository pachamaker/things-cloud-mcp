package civil

import (
	"testing"
	"time"
)

func mustZone(t *testing.T, name string) *time.Location {
	t.Helper()
	loc, err := LoadLocation(name)
	if err != nil {
		t.Fatalf("LoadLocation(%q): %v", name, err)
	}
	return loc
}

func fixedClock(at time.Time) Clock {
	return ClockFunc(func() time.Time { return at })
}

func utc(s string) time.Time {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		panic(err)
	}
	return t
}

// TestToday_KI03 pins the owner's calendar day at instants where the UTC day
// differs from it, and around DST changes where a fixed offset would be wrong.
func TestToday_KI03(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		zone string
		at   string
		want Date
	}{
		{"Moscow 00:30 is still the previous UTC day", "Europe/Moscow", "2026-10-10T21:30:00Z", Date{2026, 10, 11}},
		{"Moscow 02:59", "Europe/Moscow", "2026-10-10T23:59:59Z", Date{2026, 10, 11}},
		{"Moscow 23:59", "Europe/Moscow", "2026-10-11T20:59:59Z", Date{2026, 10, 11}},
		{"Moscow midnight", "Europe/Moscow", "2026-10-11T21:00:00Z", Date{2026, 10, 12}},
		{"UTC midnight", "UTC", "2026-10-11T00:00:00Z", Date{2026, 10, 11}},
		{"UTC last nanosecond of the day", "UTC", "2026-10-10T23:59:59.999999999Z", Date{2026, 10, 10}},
		{"Los Angeles evening is already the next UTC day", "America/Los_Angeles", "2026-10-11T03:00:00Z", Date{2026, 10, 10}},
		{"Pago Pago 23:30 (UTC-11)", "Pacific/Pago_Pago", "2026-10-11T10:30:00Z", Date{2026, 10, 10}},
		{"Kiritimati 00:30 (UTC+14)", "Pacific/Kiritimati", "2026-10-10T10:30:00Z", Date{2026, 10, 11}},
		{"New York 00:30 EDT in summer", "America/New_York", "2026-07-01T04:30:00Z", Date{2026, 7, 1}},
		{"New York 23:30 EST in winter", "America/New_York", "2026-01-16T04:30:00Z", Date{2026, 1, 15}},
		{"New York first midnight after spring forward", "America/New_York", "2026-03-09T04:00:00Z", Date{2026, 3, 9}},
		{"New York repeated hour on fall back", "America/New_York", "2026-11-01T06:30:00Z", Date{2026, 11, 1}},
		{"New York first evening after fall back", "America/New_York", "2026-11-02T04:30:00Z", Date{2026, 11, 1}},
		{"Berlin 00:30 CEST after spring forward", "Europe/Berlin", "2026-03-29T22:30:00Z", Date{2026, 3, 30}},
		{"Santiago 01:00 right after the skipped midnight", "America/Santiago", "2026-09-06T04:00:00Z", Date{2026, 9, 6}},
		{"Santiago 23:59 before the skipped midnight", "America/Santiago", "2026-09-06T03:59:59Z", Date{2026, 9, 5}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := Today(fixedClock(utc(tt.at)), mustZone(t, tt.zone))
			if got != tt.want {
				t.Errorf("Today at %s in %s = %s, want %s", tt.at, tt.zone, got, tt.want)
			}
		})
	}
}

func TestWire_ProtocolCapture(t *testing.T) {
	t.Parallel()

	// docs/protocol/things-cloud.md §4: a task made "Today" on 2026-02-10.
	const captured = int64(1770681600)
	d := Date{2026, 2, 10}
	if got := d.Wire(); got != captured {
		t.Errorf("Wire() = %d, want %d", got, captured)
	}
	if got := FromWire(captured); got != d {
		t.Errorf("FromWire(%d) = %s, want %s", captured, got, d)
	}
	// The SDK may hand the value over in any location; the day is the UTC one.
	moscow := mustZone(t, "Europe/Moscow")
	la := mustZone(t, "America/Los_Angeles")
	for _, at := range []time.Time{time.Unix(captured, 0).UTC(), time.Unix(captured, 0).In(moscow), time.Unix(captured, 0).In(la)} {
		if got := FromWireTime(at); got != d {
			t.Errorf("FromWireTime(%s) = %s, want %s", at, got, d)
		}
	}
	// The "never" sentinel (KI-04) decodes as the date it encodes.
	if got := FromWire(64092211200); got != (Date{4001, 1, 1}) {
		t.Errorf("FromWire(sentinel) = %s, want 4001-01-01", got)
	}
}

func TestParse(t *testing.T) {
	t.Parallel()

	tests := []struct {
		in      string
		want    Date
		wantErr bool
	}{
		{"2026-10-11", Date{2026, 10, 11}, false},
		{"2028-02-29", Date{2028, 2, 29}, false},
		{"2026-02-29", Date{}, true},
		{"2026-1-5", Date{}, true},
		{"11.10.2026", Date{}, true},
		{"2026-10-11T00:00:00Z", Date{}, true},
		{"today", Date{}, true},
		{"", Date{}, true},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			t.Parallel()
			got, err := Parse(tt.in)
			if (err != nil) != tt.wantErr {
				t.Fatalf("Parse(%q) error = %v, wantErr %v", tt.in, err, tt.wantErr)
			}
			if got != tt.want {
				t.Errorf("Parse(%q) = %s, want %s", tt.in, got, tt.want)
			}
		})
	}
}

func TestDate_ArithmeticAndOrder(t *testing.T) {
	t.Parallel()

	tests := []struct {
		from Date
		days int
		want Date
	}{
		{Date{2026, 12, 31}, 1, Date{2027, 1, 1}},
		{Date{2028, 2, 28}, 1, Date{2028, 2, 29}},
		{Date{2026, 3, 1}, -1, Date{2026, 2, 28}},
		{Date{2026, 3, 28}, 2, Date{2026, 3, 30}},
		{Date{2026, 10, 11}, 0, Date{2026, 10, 11}},
	}
	for _, tt := range tests {
		if got := tt.from.AddDays(tt.days); got != tt.want {
			t.Errorf("%s.AddDays(%d) = %s, want %s", tt.from, tt.days, got, tt.want)
		}
	}

	a, b := Date{2026, 10, 10}, Date{2026, 10, 11}
	if !a.Before(b) || a.After(b) || b.Before(a) || !b.After(a) {
		t.Errorf("order of %s and %s is wrong", a, b)
	}
	if a.Before(a) || a.After(a) || a.Compare(a) != 0 {
		t.Errorf("%s is not equal to itself", a)
	}
	if got := (Date{2026, 1, 5}).String(); got != "2026-01-05" {
		t.Errorf("String() = %q, want 2026-01-05", got)
	}
}

func TestDate_Start(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		zone string
		date Date
		want string
	}{
		{"UTC", "UTC", Date{2026, 10, 11}, "2026-10-11T00:00:00Z"},
		{"Moscow", "Europe/Moscow", Date{2026, 10, 11}, "2026-10-10T21:00:00Z"},
		{"Los Angeles", "America/Los_Angeles", Date{2026, 10, 11}, "2026-10-11T07:00:00Z"},
		{"Berlin spring forward day", "Europe/Berlin", Date{2026, 3, 29}, "2026-03-28T23:00:00Z"},
		{"Santiago skipped midnight", "America/Santiago", Date{2026, 9, 6}, "2026-09-06T04:00:00Z"},
		{"Havana skipped midnight", "America/Havana", Date{2026, 3, 8}, "2026-03-08T05:00:00Z"},
		{"Beirut skipped midnight", "Asia/Beirut", Date{2026, 3, 29}, "2026-03-28T22:00:00Z"},
		{"Havana repeated midnight", "America/Havana", Date{2026, 11, 1}, "2026-11-01T04:00:00Z"},
		{"Amman repeated midnight", "Asia/Amman", Date{2021, 10, 29}, "2021-10-28T21:00:00Z"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			loc := mustZone(t, tt.zone)
			got := tt.date.Start(loc)
			if want := utc(tt.want); !got.Equal(want) {
				t.Fatalf("%s.Start(%s) = %s, want %s", tt.date, tt.zone, got.UTC(), want)
			}
			if DateOf(got.In(loc)) != tt.date {
				t.Errorf("start %s is not on %s in %s", got, tt.date, tt.zone)
			}
			if before := got.Add(-time.Nanosecond).In(loc); DateOf(before) == tt.date {
				t.Errorf("%s is on %s too, so %s is not the first instant", before, tt.date, got)
			}
		})
	}
}

func TestLoadLocation(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"Europe/Moscow", "UTC", "America/Los_Angeles"} {
		loc, err := LoadLocation(name)
		if err != nil || loc.String() != name {
			t.Errorf("LoadLocation(%q) = %v, %v", name, loc, err)
		}
	}
	for _, name := range []string{"", "Local", "Europe/Moskva", "MSK", "+03:00", "../etc/passwd"} {
		if loc, err := LoadLocation(name); err == nil {
			t.Errorf("LoadLocation(%q) = %v, want an error", name, loc)
		}
	}
}
