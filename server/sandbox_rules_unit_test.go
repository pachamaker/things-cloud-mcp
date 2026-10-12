package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	thingscloud "github.com/arthursoares/things-cloud-sdk"
	"github.com/google/uuid"

	"github.com/pachamaker/things-cloud-mcp/domain/civil"
)

const testPassword = "pa$$ word#1"

func TestParseEnvFile(t *testing.T) {
	t.Parallel()

	in := strings.Join([]string{
		"# sandbox account",
		"",
		"export THINGS_SANDBOX_USERNAME=sandbox@example.com",
		`THINGS_SANDBOX_PASSWORD="` + testPassword + `"`,
		"OTHER='single quoted'",
		"EMPTY=",
	}, "\n")
	got, err := parseEnvFile(strings.NewReader(in))
	if err != nil {
		t.Fatalf("parseEnvFile: %v", err)
	}
	want := map[string]string{
		"THINGS_SANDBOX_USERNAME": "sandbox@example.com",
		"THINGS_SANDBOX_PASSWORD": testPassword,
		"OTHER":                   "single quoted",
		"EMPTY":                   "",
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %q, want %q", k, got[k], v)
		}
	}

	_, err = parseEnvFile(strings.NewReader("OK=1\n" + testPassword + "\n"))
	if err == nil {
		t.Fatal("want an error for a line without '='")
	}
	if strings.Contains(err.Error(), testPassword) || !strings.Contains(err.Error(), "line 2") {
		t.Errorf("error %q must name the line and not echo its content", err)
	}
}

func TestLoadSandboxCredentials(t *testing.T) {
	t.Parallel()

	file := []byte("THINGS_SANDBOX_USERNAME=file@example.com\nTHINGS_SANDBOX_PASSWORD=" + testPassword + "\n")
	readFile := func(path string) ([]byte, error) {
		if path == "/secrets/sandbox.env" {
			return file, nil
		}
		return nil, os.ErrNotExist
	}
	env := func(m map[string]string) func(string) string {
		return func(k string) string { return m[k] }
	}

	cases := []struct {
		name    string
		env     map[string]string
		want    sandboxCredentials
		wantErr string
		notConf bool
	}{
		{name: "nothing set", env: nil, notConf: true},
		{
			name: "environment only",
			env:  map[string]string{envSandboxUsername: "env@example.com", envSandboxPassword: testPassword},
			want: sandboxCredentials{Username: "env@example.com", Password: testPassword},
		},
		{
			name: "file only",
			env:  map[string]string{envSandboxEnvFile: "/secrets/sandbox.env"},
			want: sandboxCredentials{Username: "file@example.com", Password: testPassword},
		},
		{
			name: "environment wins over file",
			env:  map[string]string{envSandboxEnvFile: "/secrets/sandbox.env", envSandboxUsername: "env@example.com"},
			want: sandboxCredentials{Username: "env@example.com", Password: testPassword},
		},
		{name: "missing password", env: map[string]string{envSandboxUsername: "env@example.com"}, wantErr: envSandboxPassword},
		{name: "missing username", env: map[string]string{envSandboxPassword: testPassword}, wantErr: envSandboxUsername},
		{name: "unreadable file", env: map[string]string{envSandboxEnvFile: "/nope"}, wantErr: "cannot read"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := loadSandboxCredentials(env(tc.env), readFile)
			switch {
			case tc.notConf:
				if !errors.Is(err, errSandboxNotConfigured) {
					t.Fatalf("err = %v, want errSandboxNotConfigured", err)
				}
			case tc.wantErr != "":
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v, want it to mention %q", err, tc.wantErr)
				}
				if strings.Contains(err.Error(), testPassword) {
					t.Fatal("error leaks the password")
				}
			default:
				if err != nil {
					t.Fatalf("loadSandboxCredentials: %v", err)
				}
				if got != tc.want {
					t.Fatalf("got %+v, want %+v", got, tc.want)
				}
			}
		})
	}
}

func TestCheckSandboxAccount(t *testing.T) {
	t.Parallel()

	creds := sandboxCredentials{Username: "Sandbox@Example.com", Password: testPassword}
	withMarker := []string{"Work", sandboxMarkerArea}

	cases := []struct {
		name     string
		owner    string
		verified string
		areas    []string
		wantErr  string
	}{
		{name: "sandbox with marker", owner: "owner@example.com", verified: "sandbox@example.com", areas: withMarker},
		{name: "no THINGS_USERNAME", areas: withMarker},
		{name: "marker missing", owner: "owner@example.com", areas: []string{"Work", "sandbox"}, wantErr: "no area titled"},
		{name: "same as owner", owner: " sandbox@example.COM ", areas: withMarker, wantErr: "THINGS_USERNAME"},
		{name: "verified email is owner", owner: "owner@example.com", verified: "Owner@example.com", areas: withMarker, wantErr: "THINGS_USERNAME"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := checkSandboxAccount(creds, tc.owner, tc.verified, tc.areas)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("guard refused: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("err = %v, want it to mention %q", err, tc.wantErr)
			}
		})
	}
}

func TestNewSandboxRunID(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 10, 11, 15, 30, 59, 0, time.FixedZone("MSK", 3*3600))
	id, err := newSandboxRunID(now, bytes.NewReader([]byte{0xab, 0xcd}))
	if err != nil {
		t.Fatal(err)
	}
	if id != "e2e-20261011-1230-abcd" {
		t.Fatalf("run id = %q", id)
	}
	if prefix := sandboxRunPrefix(id); !strings.HasPrefix(prefix, sandboxRunTitleRoot) {
		t.Fatalf("prefix %q does not start with %q", prefix, sandboxRunTitleRoot)
	}
}

func TestRedactor(t *testing.T) {
	t.Parallel()

	var r redactor
	r.add("sandbox+e2e@example.com")
	r.add(testPassword)
	r.add("")
	in := `Get "https://cloud.culturedcode.com/version/1/account/sandbox+e2e@example.com": x; q=sandbox%2Be2e%40example.com; ` + testPassword +
		`; Post "https://cloud.culturedcode.com/version/1/history/KeyNotYetKnown-42/commit?ancestor-index=7": refused`
	got := r.redact(in)
	if !strings.Contains(got, "/history/<redacted>/commit") {
		t.Errorf("history key not masked: %s", got)
	}
	for _, leak := range []string{"sandbox+e2e@example.com", "sandbox%2Be2e%40example.com", testPassword, "KeyNotYetKnown-42"} {
		if strings.Contains(got, leak) {
			t.Errorf("redacted text still contains %q: %s", leak, got)
		}
	}

	var buf bytes.Buffer
	w := redactingWriter{r: &r, w: &buf}
	if _, err := w.Write([]byte("login " + testPassword + "\n")); err != nil {
		t.Fatal(err)
	}
	if buf.String() != "login <redacted>\n" {
		t.Fatalf("writer output = %q", buf.String())
	}
}

// commitBody encodes envelopes the way History.Write does: a map keyed by ID.
func commitBody(t *testing.T, envs ...writeEnvelope) []byte {
	t.Helper()
	m := map[string]any{}
	for _, e := range envs {
		m[e.id] = e
	}
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func parseOne(t *testing.T, env writeEnvelope) wireItem {
	t.Helper()
	items, err := parseCommitBody(commitBody(t, env))
	if err != nil || len(items) != 1 {
		t.Fatalf("parseCommitBody: %v (%d items)", err, len(items))
	}
	return items[0]
}

const testToday = int64(1791676800) // 2026-10-11 00:00 UTC

func testCreatePayload(tp, st int, sr, tir *int64) taskCreatePayload {
	return taskCreatePayload{
		Tp: tp, Sr: sr, Rt: []string{}, St: st, Tir: tir, Dl: []string{}, Ar: []string{},
		Tt: "[e2e-x] task", Tg: []string{}, Agr: []string{}, Cd: float64(testToday) + 3600.5,
		Nt: emptyNote(), Pr: []string{}, Xx: defaultExtension(),
	}
}

func i64(v int64) *int64 { return &v }

// The shapes below follow server/write.go; the e2e run checks the real ones.
func TestWireViolations_VerifiedWritesPass(t *testing.T) {
	t.Parallel()

	future := testToday + 7*86400
	task := generateUUID()
	update := func() *taskUpdate {
		u := newTaskUpdate()
		u.fields["md"] = float64(testToday) + 7200.25
		return u
	}

	cases := []struct {
		name string
		env  writeEnvelope
	}{
		{"create inbox", writeEnvelope{generateUUID(), 0, "Task6", testCreatePayload(0, 0, nil, nil)}},
		{"create today", writeEnvelope{generateUUID(), 0, "Task6", testCreatePayload(0, 1, i64(testToday), i64(testToday))}},
		{"create anytime", writeEnvelope{generateUUID(), 0, "Task6", testCreatePayload(0, 1, nil, nil)}},
		{"create someday", writeEnvelope{generateUUID(), 0, "Task6", testCreatePayload(0, 2, nil, nil)}},
		{"create upcoming", writeEnvelope{generateUUID(), 0, "Task6", testCreatePayload(0, 2, i64(future), nil)}},
		{"create project", writeEnvelope{generateUUID(), 0, "Task6", testCreatePayload(1, 1, nil, nil)}},
		{"create heading", writeEnvelope{generateUUID(), 0, "Task6", testCreatePayload(2, 1, nil, nil)}},
		{"move today", writeEnvelope{task, 1, "Task6", update().schedule(1, testToday, testToday).build()}},
		{"move anytime", writeEnvelope{task, 1, "Task6", update().schedule(1, nil, nil).build()}},
		{"move someday", writeEnvelope{task, 1, "Task6", update().schedule(2, nil, nil).build()}},
		{"move inbox", writeEnvelope{task, 1, "Task6", update().schedule(0, nil, nil).build()}},
		{"edit upcoming", writeEnvelope{task, 1, "Task6", update().schedule(2, &future, (*int64)(nil)).build()}},
		{"complete", writeEnvelope{task, 1, "Task6", update().status(3).stopDate(float64(testToday) + 10).build()}},
		{"edit title note", writeEnvelope{task, 1, "Task6", update().title("[e2e-x] t").note("n").build()}},
		{"trash", writeEnvelope{task, 1, "Task6", update().trash(true).build()}},
		{"checklist create", writeEnvelope{generateUUID(), 0, "ChecklistItem3", map[string]any{"tt": "[e2e-x] c", "ts": []string{task}, "ix": 0, "cd": 1.5, "md": nil, "ss": 0, "sp": nil, "lt": false, "xx": defaultExtension()}}},
		{"checklist reopen", writeEnvelope{generateUUID(), 1, "ChecklistItem3", map[string]any{"md": 1.5, "ss": 0, "sp": nil}}},
		{"tombstone", writeEnvelope{generateUUID(), 0, "Tombstone2", map[string]any{"dloid": task, "dld": 1.5}}},
		{"area", writeEnvelope{generateUUID(), 0, "Area3", map[string]any{"ix": 0, "tt": "[e2e-x] a", "tg": []string{}, "xx": defaultExtension()}}},
		{"tag", writeEnvelope{generateUUID(), 0, "Tag4", map[string]any{"ix": 0, "tt": "[e2e-x] t", "sh": nil, "pn": []string{}, "xx": defaultExtension()}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			item := parseOne(t, tc.env)
			if v := wireViolations(item, itemCivilToday(item.Payload, time.UTC, civil.FromWire(testToday))); len(v) != 0 {
				t.Fatalf("verified write flagged: %v", v)
			}
		})
	}
}

func TestWireViolations_RejectsUnverifiedWrites(t *testing.T) {
	t.Parallel()

	task := generateUUID()
	zeroFirst := thingscloud.EncodeUUID(uuid.UUID{0, 0xff, 0xee, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15})
	short := thingscloud.EncodeUUID(uuid.UUID{1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1})

	withRR := testCreatePayload(0, 1, nil, nil)
	rr := json.RawMessage(`{"fu":16}`)
	withRR.Rr = &rr
	withRT := testCreatePayload(0, 1, nil, nil)
	withRT.Rt = []string{task}

	cases := []struct {
		name string
		item wireItem
		want string
	}{
		{"Task7 create", wireItem{UUID: generateUUID(), Action: 0, Kind: "Task7", Payload: map[string]json.RawMessage{}}, "Task7"},
		{"delete action", wireItem{UUID: task, Action: 2, Kind: "Task6", Payload: map[string]json.RawMessage{}}, "t=2"},
		{"Area3 modify", wireItem{UUID: task, Action: 1, Kind: "Area3", Payload: map[string]json.RawMessage{}}, "Area3"},
		{"rr null on modify", parseOne(t, writeEnvelope{task, 1, "Task6", map[string]any{"rr": nil, "md": 1.5}}), "rr"},
		{"rr on create", parseOne(t, writeEnvelope{generateUUID(), 0, "Task6", withRR}), "rr"},
		{"rt on create", parseOne(t, writeEnvelope{generateUUID(), 0, "Task6", withRT}), "rt"},
		{"st=2 with today", parseOne(t, writeEnvelope{task, 1, "Task6", map[string]any{"st": 2, "sr": testToday, "tir": nil, "md": float64(testToday)}}), "placement"},
		{"today without tir", parseOne(t, writeEnvelope{task, 1, "Task6", map[string]any{"st": 1, "sr": testToday, "tir": nil, "md": float64(testToday)}}), "placement"},
		{"sr without st", parseOne(t, writeEnvelope{task, 1, "Task6", map[string]any{"sr": nil, "tir": nil, "md": 1.5}}), "without st"},
		{"st without sr", parseOne(t, writeEnvelope{task, 1, "Task6", map[string]any{"st": 1, "md": 1.5}}), "without both"},
		{"negative ix", parseOne(t, writeEnvelope{generateUUID(), 0, "Area3", map[string]any{"ix": -1, "tt": "a"}}), "KI-31"},
		{"sp zero", parseOne(t, writeEnvelope{task, 1, "Task6", map[string]any{"ss": 3, "sp": 0, "md": 1.5}}), "sp="},
		{"21-char id", wireItem{UUID: short, Action: 0, Kind: "Area3", Payload: map[string]json.RawMessage{}}, "KI-26"},
		{"zero first byte", wireItem{UUID: zeroFirst, Action: 0, Kind: "Area3", Payload: map[string]json.RawMessage{}}, "KI-26"},
		{"empty tag shortcut", parseOne(t, writeEnvelope{generateUUID(), 0, "Tag4", map[string]any{"tt": "t", "sh": ""}}), "sh="},
		{"bad tombstone target", parseOne(t, writeEnvelope{generateUUID(), 0, "Tombstone2", map[string]any{"dloid": "not-an-id"}}), "dloid"},
	}
	if len(short) != 21 || len(zeroFirst) != 22 {
		t.Fatalf("fixture IDs: short=%d zeroFirst=%d characters", len(short), len(zeroFirst))
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			v := wireViolations(tc.item, testToday)
			if len(v) == 0 || !strings.Contains(strings.Join(v, "\n"), tc.want) {
				t.Fatalf("violations %v, want one mentioning %q", v, tc.want)
			}
		})
	}
}

func TestItemCivilToday(t *testing.T) {
	t.Parallel()

	moscow := mustLoadZone(t, "Europe/Moscow")
	newYork := mustLoadZone(t, "America/New_York")
	day := func(y int, m time.Month, d int) civil.Date { return civil.Date{Year: y, Month: m, Day: d} }
	at := func(utc time.Time) json.RawMessage { return json.RawMessage(strconv.FormatInt(utc.Unix(), 10) + ".25") }
	fallback := day(2030, time.January, 1)
	cases := []struct {
		name string
		loc  *time.Location
		p    map[string]json.RawMessage
		want civil.Date
	}{
		{"md wins", time.UTC, map[string]json.RawMessage{"md": json.RawMessage(`1791763199.9`), "cd": json.RawMessage(`1`)}, day(2026, time.October, 11)},
		{"cd when md null", time.UTC, map[string]json.RawMessage{"md": json.RawMessage(`null`), "cd": json.RawMessage(`1791763199`)}, day(2026, time.October, 11)},
		{"fallback", moscow, map[string]json.RawMessage{}, fallback},
		// 00:30 in Moscow is 21:30 of the previous day in UTC.
		{"Moscow 00:30", moscow, map[string]json.RawMessage{"md": at(time.Date(2026, 10, 10, 21, 30, 0, 0, time.UTC))}, day(2026, time.October, 11)},
		// 22:30 EDT is 02:30 of the next day in UTC.
		{"New York 22:30", newYork, map[string]json.RawMessage{"md": at(time.Date(2026, 10, 12, 2, 30, 0, 0, time.UTC))}, day(2026, time.October, 11)},
	}
	for _, tc := range cases {
		if got, want := itemCivilToday(tc.p, tc.loc, fallback), tc.want.Wire(); got != want {
			t.Errorf("%s: got %s, want %s", tc.name, civil.FromWire(got), tc.want)
		}
	}
}

func TestSandboxNearMidnight(t *testing.T) {
	t.Parallel()

	moscow := mustLoadZone(t, "Europe/Moscow")
	newYork := mustLoadZone(t, "America/New_York")
	santiago := mustLoadZone(t, "America/Santiago")
	cases := []struct {
		name string
		loc  *time.Location
		at   time.Time
		want bool
	}{
		{"UTC 00:10", time.UTC, time.Date(2026, 10, 11, 0, 10, 0, 0, time.UTC), true},
		{"UTC 23:50", time.UTC, time.Date(2026, 10, 11, 23, 50, 0, 0, time.UTC), true},
		{"UTC noon", time.UTC, time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC), false},
		{"UTC 00:15 is outside", time.UTC, time.Date(2026, 10, 11, 0, 15, 0, 0, time.UTC), false},
		{"Moscow 00:05", moscow, time.Date(2026, 10, 10, 21, 5, 0, 0, time.UTC), true},
		{"Moscow 03:05 at 00:05 UTC", moscow, time.Date(2026, 10, 11, 0, 5, 0, 0, time.UTC), false},
		{"New York 23:55", newYork, time.Date(2026, 10, 12, 3, 55, 0, 0, time.UTC), true},
		{"New York 20:05 at 00:05 UTC", newYork, time.Date(2026, 10, 12, 0, 5, 0, 0, time.UTC), false},
		// Chile skips 2026-09-06 00:00: the day starts at 01:00 -03 (04:00 UTC).
		{"Santiago 01:10 after skipped midnight", santiago, time.Date(2026, 9, 6, 4, 10, 0, 0, time.UTC), true},
	}
	for _, tc := range cases {
		clock := civil.ClockFunc(func() time.Time { return tc.at })
		if got := sandboxNearMidnight(clock, tc.loc, 15*time.Minute); got != tc.want {
			t.Errorf("%s: sandboxNearMidnight = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// KI-34: the firewall judges a Today placement by the owner's day, so a write
// at 00:30 in Moscow (21:30 UTC of the day before) is the verified Today shape.
func TestWriteFirewall_KI34_OwnerZone(t *testing.T) {
	t.Parallel()

	written := time.Date(2026, 10, 10, 21, 30, 0, 0, time.UTC)
	today := civil.Date{Year: 2026, Month: time.October, Day: 11}.Wire()
	task := generateUUID()
	u := newTaskUpdate()
	u.fields["md"] = float64(written.Unix())
	item := parseOne(t, writeEnvelope{task, 1, "Task6", u.schedule(1, today, today).build()})

	cases := []struct {
		name    string
		loc     *time.Location
		wantErr string
	}{
		{"owner's zone", mustLoadZone(t, "Europe/Moscow"), ""},
		{"UTC day", time.UTC, "placement"},
	}
	for _, tc := range cases {
		f := newWriteFirewall(func() time.Time { return written }, tc.loc)
		f.setMode(firewallWrites)
		f.own(task, "Task6")
		err := f.check([]wireItem{item})
		switch {
		case tc.wantErr == "" && err != nil:
			t.Errorf("%s: Today write refused: %v", tc.name, err)
		case tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)):
			t.Errorf("%s: got %v, want an error mentioning %q", tc.name, err, tc.wantErr)
		}
	}
}

func TestWriteFirewall(t *testing.T) {
	t.Parallel()

	now := func() time.Time { return time.Unix(testToday+3600, 0) }
	prefix := "[e2e-20261011-1230-abcd] "
	area := writeEnvelope{generateUUID(), 0, "Area3", map[string]any{"ix": 0, "tt": prefix + "area", "tg": []string{}, "xx": defaultExtension()}}
	stranger := writeEnvelope{generateUUID(), 0, "Area3", map[string]any{"ix": 0, "tt": "SEED area", "tg": []string{}, "xx": defaultExtension()}}
	task := generateUUID()
	trash := writeEnvelope{task, 1, "Task6", map[string]any{"tr": true, "md": float64(testToday) + 60}}
	tomb := writeEnvelope{generateUUID(), 0, "Tombstone2", map[string]any{"dloid": task, "dld": 1.5}}
	items := func(env writeEnvelope) []wireItem { return []wireItem{parseOne(t, env)} }

	f := newWriteFirewall(now, time.UTC, prefix)
	if err := f.check(items(area)); err == nil || !strings.Contains(err.Error(), "locked") {
		t.Fatalf("locked firewall let a commit through: %v", err)
	}
	f.setMode(firewallReadOnly)
	if err := f.check(items(area)); err == nil || !strings.Contains(err.Error(), "read-only") {
		t.Fatalf("read-only firewall let a commit through: %v", err)
	}

	f.setMode(firewallWrites)
	if err := f.check(items(area)); err != nil {
		t.Fatalf("create with the run prefix refused: %v", err)
	}
	if err := f.check(items(stranger)); err == nil || !strings.Contains(err.Error(), "run prefix") {
		t.Fatalf("create without the run prefix allowed: %v", err)
	}
	if err := f.check(items(trash)); err == nil || !strings.Contains(err.Error(), "does not own") {
		t.Fatalf("modify of a foreign object allowed: %v", err)
	}
	if err := f.check(items(tomb)); err == nil || !strings.Contains(err.Error(), "does not own") {
		t.Fatalf("tombstone of a foreign object allowed: %v", err)
	}

	f.committed([]wireItem{{UUID: task, Action: 0, Kind: "Task6"}})
	if err := f.check(items(trash)); err != nil {
		t.Fatalf("modify of an owned object refused: %v", err)
	}
	if err := f.check(items(tomb)); err != nil {
		t.Fatalf("tombstone of an owned object refused: %v", err)
	}
	retitle := writeEnvelope{task, 1, "Task6", map[string]any{"tt": "SEED anytime", "md": 1.5}}
	if err := f.check(items(retitle)); err == nil || strings.Contains(err.Error(), "SEED anytime") {
		t.Fatalf("retitling away from the run prefix allowed, or the title was echoed: %v", err)
	}

	other := generateUUID()
	f.own(other, "Task6")
	if !f.owns(other) {
		t.Fatal("own() did not register the object")
	}
	if got := f.violationCount(); got != 6 {
		t.Fatalf("violationCount = %d, want 6", got)
	}
	if v := f.violationsSince(5); len(v) != 1 {
		t.Fatalf("violationsSince(5) = %v", v)
	}
}

func TestPayloadDifferences(t *testing.T) {
	t.Parallel()

	raw := func(s string) map[string]json.RawMessage {
		var m map[string]json.RawMessage
		if err := json.Unmarshal([]byte(s), &m); err != nil {
			t.Fatal(err)
		}
		return m
	}
	sent := raw(`{"tt":"a","md":1791676800.4716659,"pr":["x"],"nt":{"_t":"tx","v":"n"}}`)
	if d := payloadDifferences(sent, raw(`{"tt":"a","md":1791676800.471666,"pr":["x"],"nt":{"_t":"tx","v":"n"}}`)); len(d) != 0 {
		t.Fatalf("equivalent payloads differ: %v", d)
	}
	d := payloadDifferences(sent, raw(`{"tt":"b","md":1791676800.4716659,"nt":{"_t":"tx","v":"n"},"rr":null}`))
	joined := strings.Join(d, "\n")
	if strings.Contains(joined, `"b"`) {
		t.Errorf("a title value leaked into the differences: %v", d)
	}
	for _, want := range []string{"tt: stored text differs", "pr: sent but not stored", "rr: stored but not sent"} {
		if !strings.Contains(joined, want) {
			t.Errorf("differences %v lack %q", d, want)
		}
	}
}

func TestFoldHistory(t *testing.T) {
	t.Parallel()

	raw := func(s string) map[string]json.RawMessage {
		var m map[string]json.RawMessage
		if err := json.Unmarshal([]byte(s), &m); err != nil {
			t.Fatal(err)
		}
		return m
	}
	items := []wireItem{
		{UUID: "T", Action: 0, Kind: "Task7", Payload: raw(`{"tt":"SEED repeat daily","rr":{"fu":16}}`)},
		{UUID: "T", Action: 1, Kind: "Task6", Payload: raw(`{"tt":"SEED repeat daily!"}`)},
		{UUID: "C", Action: 0, Kind: "ChecklistItem3", Payload: raw(`{"tt":"one"}`)},
		{UUID: "Z", Action: 0, Kind: "Tombstone2", Payload: raw(`{"dloid":"C"}`)},
	}
	got := foldHistory(items)
	if _, ok := got["C"]; ok {
		t.Fatal("tombstoned checklist item still folded")
	}
	e := got["T"]
	if e == nil || e.title() != "SEED repeat daily!" || !e.Kinds["Task7"] || !e.Kinds["Task6"] {
		t.Fatalf("folded entity = %+v", e)
	}
	if isJSONNull(e.Fields["rr"]) {
		t.Fatal("rr lost while folding")
	}
}

func rawObject(t *testing.T, s string) map[string]json.RawMessage {
	t.Helper()
	var m map[string]json.RawMessage
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func foldedOf(t *testing.T, uuid, kind, payload string) *foldedEntity {
	t.Helper()
	return &foldedEntity{UUID: uuid, Kinds: map[string]bool{kind: true}, Fields: rawObject(t, payload)}
}

func TestFoldHistoryWithDeleted_KI34(t *testing.T) {
	t.Parallel()

	items := []wireItem{
		{UUID: "A", Action: 0, Kind: "Tag4", Payload: rawObject(t, `{"tt":"SEED tag"}`)},
		{UUID: "B", Action: 0, Kind: "Tag4", Payload: rawObject(t, `{"tt":"SEED tag"}`)},
		{UUID: "Z", Action: 0, Kind: "Tombstone2", Payload: rawObject(t, `{"dloid":"A"}`)},
		{UUID: "B", Action: 2, Kind: "Tag4", Payload: rawObject(t, `{}`)},
		{UUID: "C", Action: 0, Kind: "Tag4", Payload: rawObject(t, `{"tt":"first"}`)},
		{UUID: "C", Action: 2, Kind: "Tag4", Payload: rawObject(t, `{}`)},
		{UUID: "C", Action: 0, Kind: "Tag4", Payload: rawObject(t, `{"tt":"again"}`)},
	}
	live, deleted := foldHistoryWithDeleted(items)
	for _, id := range []string{"A", "B"} {
		if live[id] != nil {
			t.Errorf("deleted %s is still live", id)
		}
		if e := deleted[id]; e == nil || e.title() != "SEED tag" || !e.Kinds["Tag4"] {
			t.Errorf("deleted[%s] = %+v, want its state before the deletion", id, e)
		}
	}
	if e := live["C"]; e == nil || e.title() != "again" {
		t.Errorf("recreated C = %+v", e)
	}
	if deleted["C"] != nil {
		t.Error("recreated C is still listed as deleted")
	}
	if live["Z"] != nil || deleted["Z"] != nil {
		t.Error("the tombstone itself was folded")
	}
	if got := foldHistory(items); len(got) != 1 || got["C"] == nil {
		t.Errorf("foldHistory = %v, want only C", sortedKeys(got))
	}
}

func TestSeedCandidates_KI34(t *testing.T) {
	t.Parallel()

	folded := map[string]*foldedEntity{}
	for _, e := range []*foldedEntity{
		foldedOf(t, "tag2", "Tag4", `{"tt":"SEED tag"}`),
		foldedOf(t, "tag1", "Tag3", `{"tt":"SEED tag"}`),
		foldedOf(t, "area", "Area3", `{"tt":"SEED tag"}`),
		foldedOf(t, "check", "ChecklistItem3", `{"tt":"SEED tag"}`),
		foldedOf(t, "task", "Task7", `{"tt":"SEED tag"}`),
		foldedOf(t, "todo", "Task7", `{"tt":"SEED tagged","tr":false}`),
		foldedOf(t, "trashed", "Task7", `{"tt":"SEED tagged","tr":true}`),
		foldedOf(t, "other", "Tag4", `{"tt":"SEED tag "}`),
	} {
		folded[e.UUID] = e
	}
	all := func(*foldedEntity) bool { return true }
	uuids := func(found []*foldedEntity) []string {
		var out []string
		for _, e := range found {
			out = append(out, e.UUID)
		}
		return out
	}

	tests := []struct {
		title, kindPrefix string
		keep              func(*foldedEntity) bool
		want              []string
	}{
		{"SEED tag", "Tag", all, []string{"tag1", "tag2"}},
		{"SEED tag", "Area", all, []string{"area"}},
		{"SEED tag", "Task", all, []string{"task"}},
		{"SEED tagged", "Task", all, []string{"todo"}},
		{"SEED tag", "Tag", func(e *foldedEntity) bool { return e.UUID != "tag1" }, []string{"tag2"}},
		{"SEED missing", "Tag", all, nil},
	}
	for _, tt := range tests {
		got := uuids(seedCandidates(folded, tt.title, tt.kindPrefix, tt.keep))
		if !slices.Equal(got, tt.want) {
			t.Errorf("seedCandidates(%q, %q) = %v, want %v", tt.title, tt.kindPrefix, got, tt.want)
		}
	}
}

func TestSeedTagProblem_KI34(t *testing.T) {
	t.Parallel()

	seedTag := foldedOf(t, "SeedTag", "Tag4", `{"tt":"SEED tag"}`)
	secondSeedTag := foldedOf(t, "SeedTag2", "Tag4", `{"tt":"SEED tag"}`)
	live := map[string]*foldedEntity{}
	for _, e := range []*foldedEntity{
		seedTag,
		secondSeedTag,
		foldedOf(t, "Other", "Tag4", `{"tt":"Errand"}`),
		foldedOf(t, "Variant", "Tag4", `{"tt":"seed tag "}`),
		foldedOf(t, "Area", "Area3", `{"tt":"Home"}`),
	} {
		live[e.UUID] = e
	}
	deleted := map[string]*foldedEntity{"Gone": foldedOf(t, "Gone", "Tag4", `{"tt":"SEED tag"}`)}
	tagged := func(tg ...string) *foldedEntity {
		raw, err := json.Marshal(tg)
		if err != nil {
			t.Fatal(err)
		}
		return &foldedEntity{UUID: "Tagged", Kinds: map[string]bool{"Task7": true}, Fields: map[string]json.RawMessage{"tt": json.RawMessage(`"SEED tagged"`), "tg": raw}}
	}
	const fixHint = `docs/testing/sandbox-e2e.md, "Fixing seeds"`

	tests := []struct {
		name     string
		server   []string
		tagged   *foldedEntity
		seeds    []*foldedEntity
		want     []string
		mustNot  []string
		wantNone bool
	}{
		{name: "carries the seed tag", server: []string{"Other", "SeedTag"}, tagged: tagged("Other", "SeedTag"), seeds: []*foldedEntity{seedTag}, wantNone: true},
		{name: "carries one of two seed tags", server: []string{"SeedTag2"}, tagged: tagged("SeedTag2"), seeds: []*foldedEntity{seedTag, secondSeedTag}, wantNone: true},
		{
			name: "deleted tag", server: []string{"Gone"}, tagged: tagged("Gone"), seeds: []*foldedEntity{seedTag},
			want: []string{"SEED tagged does not carry SEED tag [SeedTag]", "tags as [Gone]", `Gone is a deleted Tag4 titled "SEED tag"`, fixHint},
		},
		{
			name: "tag with another title", server: []string{"Other"}, tagged: tagged("Other"), seeds: []*foldedEntity{seedTag},
			want: []string{"Other is a live Tag4 with another title (6 chars)", fixHint}, mustNot: []string{"Errand"},
		},
		{
			name: "title differs in case and spaces", server: []string{"Variant"}, tagged: tagged("Variant"), seeds: []*foldedEntity{seedTag},
			want: []string{`Variant is a live Tag4 whose title differs from "SEED tag" only in case or surrounding spaces`, fixHint},
		},
		{
			name: "not a tag", server: []string{"Area"}, tagged: tagged("Area"), seeds: []*foldedEntity{seedTag},
			want: []string{"Area is a live Area3, not a tag", fixHint}, mustNot: []string{"Home"},
		},
		{
			name: "not in history", server: []string{"Nowhere"}, tagged: tagged("Nowhere"), seeds: []*foldedEntity{seedTag},
			want: []string{"Nowhere is not in history", fixHint},
		},
		{
			name: "no tags", server: nil, tagged: tagged(), seeds: []*foldedEntity{seedTag},
			want: []string{"it has no tags", fixHint},
		},
		{
			name: "order differs between server and history", server: []string{"Gone", "Other"}, tagged: tagged("Other", "Gone"), seeds: []*foldedEntity{seedTag},
			want: []string{"Gone is a deleted Tag4", "Other is a live Tag4"}, mustNot: []string{"read bug"},
		},
		{
			name: "server reads other tags than history holds", server: nil, tagged: tagged("SeedTag"), seeds: []*foldedEntity{seedTag},
			want: []string{"history has tg [SeedTag]", "a read bug, not a seed problem"}, mustNot: []string{fixHint},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := seedTagProblem(tt.server, tt.tagged, tt.seeds, live, deleted)
			if tt.wantNone {
				if got != "" {
					t.Fatalf("seedTagProblem = %q, want none", got)
				}
				return
			}
			for _, w := range tt.want {
				if !strings.Contains(got, w) {
					t.Errorf("seedTagProblem = %q, want it to contain %q", got, w)
				}
			}
			for _, w := range tt.mustNot {
				if strings.Contains(got, w) {
					t.Errorf("seedTagProblem = %q, must not contain %q", got, w)
				}
			}
		})
	}
}

func TestSandboxRunIDHelpers(t *testing.T) {
	t.Parallel()

	for _, id := range []string{"e2e-gh12345678901-1", "e2e-20261011-1230-abcd"} {
		if err := validateSandboxRunID(id); err != nil {
			t.Errorf("validateSandboxRunID(%q): %v", id, err)
		}
		if got := runOfTitle(sandboxRunPrefix(id) + "area"); got != id {
			t.Errorf("runOfTitle = %q, want %q", got, id)
		}
	}
	for _, id := range []string{"", "e2e-", "gh-1", "e2e-a b", "e2e-x]", "e2e-" + strings.Repeat("a", 41)} {
		if validateSandboxRunID(id) == nil {
			t.Errorf("validateSandboxRunID(%q) accepted", id)
		}
	}
	if got := runOfTitle("SEED anytime"); got != "?" {
		t.Errorf("runOfTitle of a foreign title = %q", got)
	}
}

func TestPublicTask_HidesText(t *testing.T) {
	t.Parallel()

	secret := "Call the notary about the flat"
	o := taskOutput{UUID: "u", Title: secret, Note: secret, Status: "open", Tags: []string{"t"}}
	sdk := &thingscloud.Task{UUID: "u", Title: secret, Note: secret}
	for _, got := range []string{publicTask(o), publicSDKTask(sdk), publicSDKTask(nil)} {
		if strings.Contains(got, "notary") {
			t.Errorf("text leaked: %s", got)
		}
	}
	if !strings.Contains(publicTask(o), "title:30 chars") {
		t.Errorf("publicTask = %s", publicTask(o))
	}
}

func TestSandboxRunRequired_KI34(t *testing.T) {
	t.Parallel()

	tests := []struct {
		raw  string
		want bool
	}{
		{"", false},
		{"  ", false},
		{"0", false},
		{"false", false},
		{"1", true},
		{"true", true},
		{"TRUE", true},
		{"yes", true},
	}
	for _, tt := range tests {
		getenv := func(key string) string {
			if key == envSandboxRequireRun {
				return tt.raw
			}
			return ""
		}
		if got := sandboxRunRequired(getenv); got != tt.want {
			t.Errorf("sandboxRunRequired(%q) = %v, want %v", tt.raw, got, tt.want)
		}
	}
}

func TestBuildRecurrenceCapture_KI01(t *testing.T) {
	t.Parallel()

	area, weekly, monthly, inst, other := generateUUID(), generateUUID(), generateUUID(), generateUUID(), generateUUID()
	check, tomb, otherTag := generateUUID(), generateUUID(), generateUUID()
	at := func(index int, uuid string, action int, kind, payload string) wireItem {
		return wireItem{UUID: uuid, Action: action, Kind: kind, Payload: rawObject(t, payload), ServerIndex: index}
	}
	items := []wireItem{
		at(1, area, 0, "Area3", `{"tt":"MCP E2E SANDBOX"}`),
		at(2, weekly, 0, "Task7", `{"tt":"SEED repeat weekly","rr":{"fu":256,"tp":0},"rt":[],"ar":["`+area+`"],"tg":["`+otherTag+`"],`+
			`"md":1770713623.4716659,"nt":{"_t":"tx","t":1,"ch":0,"v":""},"xx":{"_t":"oo","sn":{"Pavel's iPhone":"device 42"}}}`),
		at(3, monthly, 0, "Task7", `{"tt":"SEED repeat monthly after completion","rr":{"fu":8,"tp":1},"rt":[]}`),
		at(4, inst, 0, "Task7", `{"tt":"SEED repeat weekly","rr":null,"rt":["`+weekly+`"],"nt":{"_t":"tx","t":1,"v":"call the notary"}}`),
		at(4, other, 0, "Task7", `{"tt":"Buy a flat","rt":[]}`),
		at(5, weekly, 1, "Task7", `{"icsd":1791676800,"icc":1}`),
		at(6, check, 0, "ChecklistItem3", `{"tt":"one","ts":["`+inst+`"]}`),
		at(7, tomb, 0, "Tombstone2", `{"dloid":"`+check+`","dld":1791677000.5}`),
		at(8, other, 1, "Task7", `{"tt":"Sell the flat"}`),
	}

	fx, err := buildRecurrenceCapture(items, recurrenceSeedTitles)
	if err != nil {
		t.Fatal(err)
	}
	wantObjects := []captureObject{
		{weekly, `template "SEED repeat weekly"`},
		{monthly, `template "SEED repeat monthly after completion"`},
		{inst, `instance 1 of "SEED repeat weekly"`},
		{check, `ChecklistItem3 (ts) of instance 1 of "SEED repeat weekly"`},
		{tomb, `Tombstone2 (dloid) of ChecklistItem3 (ts) of instance 1 of "SEED repeat weekly"`},
	}
	if !slices.Equal(fx.Objects, wantObjects) {
		t.Errorf("objects = %+v\nwant %+v", fx.Objects, wantObjects)
	}
	var got []string
	for _, it := range fx.Items {
		got = append(got, fmt.Sprintf("%d/%d %s t=%d %s", it.Index, it.SlotItems, it.UUID, it.Action, it.Kind))
	}
	want := []string{
		"2/1 " + weekly + " t=0 Task7", "3/1 " + monthly + " t=0 Task7", "4/2 " + inst + " t=0 Task7",
		"5/1 " + weekly + " t=1 Task7", "6/1 " + check + " t=0 ChecklistItem3", "7/1 " + tomb + " t=0 Tombstone2",
	}
	if !slices.Equal(got, want) {
		t.Errorf("items = %q\nwant %q", got, want)
	}

	var buf bytes.Buffer
	if err := encodeCaptureJSON(&buf, fx, ""); err != nil {
		t.Fatal(err)
	}
	all := buf.String()
	for _, leak := range []string{"flat", "notary", "Pavel", "device 42", otherTag, other, `"one"`} {
		if strings.Contains(all, leak) {
			t.Errorf("capture leaks %q: %s", leak, all)
		}
	}
	for _, kept := range []string{`"tt":"SEED repeat weekly"`, `"ar":["` + area + `"]`, `"tg":["<other id>"]`, `"md":1770713623.4716659`, `"_t":"oo"`, `"v":""`, `"rt":["` + weekly + `"]`} {
		if !strings.Contains(all, kept) {
			t.Errorf("capture lost %s: %s", kept, all)
		}
	}
	wantRedacted := []string{
		"items[0].p.xx.sn: key of 14 chars",
		"items[0].p.xx.sn.<redacted key 0>: 9 chars",
		"items[2].p.nt.v: 15 chars",
		"items[4].p.tt: 3 chars",
	}
	if !slices.Equal(fx.Redacted, wantRedacted) {
		t.Errorf("redacted = %q\nwant %q", fx.Redacted, wantRedacted)
	}

	if _, err := buildRecurrenceCapture(items[:2], recurrenceSeedTitles); err == nil {
		t.Error("a missing template was accepted")
	}
	dup := append(slices.Clone(items), at(9, generateUUID(), 0, "Task7", `{"tt":"SEED repeat weekly","rr":{"fu":256}}`))
	if _, err := buildRecurrenceCapture(dup, recurrenceSeedTitles); err == nil {
		t.Error("two weekly templates were accepted")
	}
}
