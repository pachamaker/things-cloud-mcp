package main

import (
	"slices"
	"strings"
	"testing"
	"time"

	things "github.com/arthursoares/things-cloud-sdk"

	"github.com/pachamaker/things-cloud-mcp/domain/civil"
)

func mustLoadZone(t *testing.T, name string) *time.Location {
	t.Helper()
	loc, err := civil.LoadLocation(name)
	if err != nil {
		t.Fatalf("load zone %q: %v", name, err)
	}
	return loc
}

// ownerDayAt is the owner's calendar day at a UTC instant, through an injected clock.
func ownerDayAt(t *testing.T, instant, zone string) civil.Date {
	t.Helper()
	at, err := time.Parse(time.RFC3339, instant)
	if err != nil {
		t.Fatal(err)
	}
	return civil.Today(civil.ClockFunc(func() time.Time { return at }), mustLoadZone(t, zone))
}

func wireDay(t *testing.T, s string) int64 {
	t.Helper()
	d, err := civil.Parse(s)
	if err != nil {
		t.Fatal(err)
	}
	return d.Wire()
}

// Instants where the owner's day and the UTC day differ.
const (
	moscowAfterMidnight = "2026-10-10T21:30:00Z" // 00:30 on 2026-10-11 in Moscow, still 2026-10-10 in UTC
	laEvening           = "2026-10-11T03:00:00Z" // 20:00 on 2026-10-10 in Los Angeles, already 2026-10-11 in UTC
)

func TestOwnerTimezone_KI03(t *testing.T) {
	t.Parallel()

	for raw, want := range map[string]string{"": "UTC", "UTC": "UTC", "Europe/Moscow": "Europe/Moscow"} {
		loc, err := ownerTimezone(raw)
		if err != nil || loc.String() != want {
			t.Errorf("ownerTimezone(%q) = %v, %v; want %s", raw, loc, err, want)
		}
	}
	for _, raw := range []string{"Local", "Europe/Moskva", " Europe/Moscow", "+03:00"} {
		loc, err := ownerTimezone(raw)
		if err == nil {
			t.Errorf("ownerTimezone(%q) = %v, want an error", raw, loc)
			continue
		}
		if !strings.Contains(err.Error(), "THINGS_TIMEZONE") {
			t.Errorf("ownerTimezone(%q) error %q does not name the variable", raw, err)
		}
	}
}

func TestParseWhen_KI03_OwnerDay(t *testing.T) {
	t.Parallel()

	type placement struct {
		st      int
		sr, tir string // "" = null
	}
	tests := []struct {
		name    string
		at      string
		zone    string
		when    string
		want    placement
		handled bool
	}{
		{"today after Moscow midnight", moscowAfterMidnight, "Europe/Moscow", "today", placement{1, "2026-10-11", "2026-10-11"}, true},
		{"owner's date is Today", moscowAfterMidnight, "Europe/Moscow", "2026-10-11", placement{1, "2026-10-11", "2026-10-11"}, true},
		{"UTC date is already past in Moscow", moscowAfterMidnight, "Europe/Moscow", "2026-10-10", placement{1, "2026-10-11", "2026-10-11"}, true},
		{"next owner's day is Upcoming", moscowAfterMidnight, "Europe/Moscow", "2026-10-12", placement{2, "2026-10-12", ""}, true},
		{"today in a Los Angeles evening", laEvening, "America/Los_Angeles", "today", placement{1, "2026-10-10", "2026-10-10"}, true},
		{"UTC date is still future in Los Angeles", laEvening, "America/Los_Angeles", "2026-10-11", placement{2, "2026-10-11", ""}, true},
		{"today at UTC midnight", "2026-10-11T00:00:00Z", "UTC", "today", placement{1, "2026-10-11", "2026-10-11"}, true},
		{"yesterday at UTC midnight", "2026-10-11T00:00:00Z", "UTC", "2026-10-10", placement{1, "2026-10-11", "2026-10-11"}, true},
		{"anytime", moscowAfterMidnight, "Europe/Moscow", "anytime", placement{1, "", ""}, true},
		{"someday", moscowAfterMidnight, "Europe/Moscow", "someday", placement{2, "", ""}, true},
		{"inbox", moscowAfterMidnight, "Europe/Moscow", "inbox", placement{0, "", ""}, true},
		{"none", moscowAfterMidnight, "Europe/Moscow", "none", placement{-1, "", ""}, false},
		{"bad date", moscowAfterMidnight, "Europe/Moscow", "2026-13-01", placement{-1, "", ""}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			today := ownerDayAt(t, tt.at, tt.zone)
			st, sr, tir, handled := parseWhen(tt.when, today)
			if handled != tt.handled || st != tt.want.st {
				t.Fatalf("parseWhen(%q) st=%d handled=%v, want st=%d handled=%v", tt.when, st, handled, tt.want.st, tt.handled)
			}
			for _, f := range []struct {
				name string
				got  *int64
				want string
			}{{"sr", sr, tt.want.sr}, {"tir", tir, tt.want.tir}} {
				switch {
				case f.want == "" && f.got != nil:
					t.Errorf("%s = %s, want null", f.name, civil.FromWire(*f.got))
				case f.want != "" && (f.got == nil || *f.got != wireDay(t, f.want)):
					t.Errorf("%s = %v, want %s", f.name, f.got, f.want)
				}
			}
			if st == 2 && sr != nil && *sr == today.Wire() {
				t.Errorf("st=2 with today's date crashed Things (AGENTS.md §7)")
			}
		})
	}
}

func TestParseDeadline_KI03_OwnerDay(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		at, zone string
		deadline string
		wantErr  string
	}{
		{"owner's today after Moscow midnight", moscowAfterMidnight, "Europe/Moscow", "2026-10-11", ""},
		{"UTC today is past in Moscow", moscowAfterMidnight, "Europe/Moscow", "2026-10-10", "in the past"},
		{"owner's today in a Los Angeles evening", laEvening, "America/Los_Angeles", "2026-10-10", ""},
		{"day before the owner's today", laEvening, "America/Los_Angeles", "2026-10-09", "in the past"},
		{"future", moscowAfterMidnight, "Europe/Moscow", "2026-12-31", ""},
		{"bad format", moscowAfterMidnight, "Europe/Moscow", "11.10.2026", "YYYY-MM-DD"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := parseDeadline(tt.deadline, ownerDayAt(t, tt.at, tt.zone))
			if tt.wantErr != "" {
				if err == nil || !isInvalidInput(err) || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("parseDeadline(%q) = %d, %v; want an invalid-input error with %q", tt.deadline, got, err, tt.wantErr)
				}
				return
			}
			if err != nil || got != wireDay(t, tt.deadline) {
				t.Fatalf("parseDeadline(%q) = %d, %v; want %d", tt.deadline, got, err, wireDay(t, tt.deadline))
			}
		})
	}
}

// TestViews_KI03_OwnerDay classifies to-dos dated by a Things app in the
// owner's zone right after local midnight, when the UTC day is still the
// previous one, and in a Los Angeles evening, when it is already the next.
func TestViews_KI03_OwnerDay(t *testing.T) {
	t.Parallel()

	at := func(s string) *time.Time {
		v := time.Unix(wireDay(t, s), 0).UTC()
		return &v
	}
	tasks := []*things.Task{
		{UUID: "started-oct11", Schedule: things.TaskScheduleAnytime, ScheduledDate: at("2026-10-11")},
		{UUID: "deferred-oct11", Schedule: things.TaskScheduleSomeday, ScheduledDate: at("2026-10-11")},
		{UUID: "deadline-oct11", Schedule: things.TaskScheduleAnytime, DeadlineDate: at("2026-10-11")},
		{UUID: "started-oct10", Schedule: things.TaskScheduleAnytime, ScheduledDate: at("2026-10-10")},
	}
	tests := []struct {
		name, at, zone string
		today          []string
		upcoming       []string
		anytime        []string
	}{
		{"Moscow 00:30", moscowAfterMidnight, "Europe/Moscow",
			[]string{"started-oct11", "deferred-oct11", "deadline-oct11", "started-oct10"}, nil, nil},
		{"UTC day at the same instant", moscowAfterMidnight, "UTC",
			[]string{"started-oct10"}, []string{"deferred-oct11"}, []string{"started-oct11", "deadline-oct11"}},
		{"Los Angeles 20:00", laEvening, "America/Los_Angeles",
			[]string{"started-oct10"}, []string{"deferred-oct11"}, []string{"started-oct11", "deadline-oct11"}},
	}
	uuids := func(ts []*things.Task) []string {
		var out []string
		for _, t := range ts {
			out = append(out, t.UUID)
		}
		slices.Sort(out)
		return out
	}
	sorted := func(s []string) []string {
		s = slices.Clone(s)
		slices.Sort(s)
		return s
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			today := ownerDayAt(t, tt.at, tt.zone)
			for _, v := range []struct {
				view listView
				want []string
			}{{viewToday, tt.today}, {viewUpcoming, tt.upcoming}, {viewAnytime, tt.anytime}} {
				if got := uuids(tasksInView(tasks, v.view, today)); !slices.Equal(got, sorted(v.want)) {
					t.Errorf("%s on %s: view %d = %v, want %v", tt.zone, today, v.view, got, sorted(v.want))
				}
			}
		})
	}
}
