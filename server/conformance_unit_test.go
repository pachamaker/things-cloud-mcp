package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	things "github.com/arthursoares/things-cloud-sdk"

	"github.com/pachamaker/things-cloud-mcp/domain/civil"
)

var conformanceTestKey = func() []byte {
	key := make([]byte, conformanceTitleKeyLen)
	for i := range key {
		key[i] = byte(i)
	}
	return key
}()

// The same vectors are in scripts/conformance/test_export_things.py: both
// halves of a snapshot must hash a title alike.
func TestConformanceTitleHash_SameAsExporter(t *testing.T) {
	t.Parallel()
	for title, want := range map[string]string{
		"SEED anytime":     "76b5488b7e83e880",
		"Ünïcødé — задача": "5ea6b9d96912e761",
		"":                 "d38b42096d80f45f",
	} {
		if got := conformanceTitleHashOf(conformanceTestKey, title); got != want {
			t.Errorf("title hash of %q = %s, want %s", title, got, want)
		}
	}
	if got := conformanceKeyID(conformanceTestKey); got != "630dcd29" {
		t.Errorf("key id = %s, want 630dcd29", got)
	}
}

func TestParseConformanceTitleKey(t *testing.T) {
	t.Parallel()
	good := "000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f\n"
	if key, err := parseConformanceTitleKey([]byte(good)); err != nil || !bytes.Equal(key, conformanceTestKey) {
		t.Fatalf("parse %q = %x, %v", good, key, err)
	}
	for _, bad := range []string{"", "00", "zz" + good[2:], good + "00"} {
		if _, err := parseConformanceTitleKey([]byte(bad)); err == nil {
			t.Errorf("parse %q: want an error", bad)
		}
	}
}

func rawFields(t *testing.T, payload string) map[string]json.RawMessage {
	t.Helper()
	var m map[string]json.RawMessage
	if err := json.Unmarshal([]byte(payload), &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestWireFieldsOf(t *testing.T) {
	t.Parallel()
	// 1791763200 is 2026-10-12, 64092211200 the 4001-01-01 sentinel.
	e := &foldedEntity{UUID: "A", Kinds: map[string]bool{"Task7": true}, Fields: rawFields(t, `{
		"tp": 0, "ss": 0, "tr": false, "st": 2, "sr": 1791763200, "tir": 1791763200.0,
		"dd": 64092211200, "dds": null, "sb": 1, "rr": {"tp": 0}, "rt": ["T"], "pr": [], "agr": ["H"], "tt": "x"}`)}
	want := taskFields{
		Type: intPtr(0), Status: intPtr(0), Trashed: boolPtr(false), Start: intPtr(2),
		StartDate: stringPtr("2026-10-12"), TodayRef: stringPtr("2026-10-12"), Deadline: stringPtr("4001-01-01"),
		Bucket: intPtr(1), Repeating: true, Template: "T", Heading: "H",
	}
	if got := wireFieldsOf(e); !reflect.DeepEqual(got, want) {
		t.Errorf("wireFieldsOf = %s\nwant %s", describeAll(got), describeAll(want))
	}

	odd := &foldedEntity{Fields: rawFields(t, `{"sr": 1791763200.5, "rr": null, "tr": "no"}`)}
	got := wireFieldsOf(odd)
	if got.StartDate == nil || *got.StartDate != "raw:1791763200.5" || got.Repeating || got.Trashed != nil {
		t.Errorf("wireFieldsOf(odd) = %s", describeAll(got))
	}
}

func describeAll(f taskFields) string {
	raw, _ := json.Marshal(f)
	return string(raw)
}

func day(s string) *time.Time {
	d, err := civil.Parse(s)
	if err != nil {
		panic(err)
	}
	t := time.Unix(d.Wire(), 0).UTC()
	return &t
}

func TestBuildConformanceInput(t *testing.T) {
	t.Parallel()
	mirror := []*things.Task{
		{UUID: "both", Title: "SEED anytime", Note: "secret note", Type: things.TaskTypeTask, Schedule: things.TaskScheduleAnytime},
		{UUID: "mirror-only", Title: "m", Type: things.TaskTypeProject},
	}
	live := map[string]*foldedEntity{
		"both":      {UUID: "both", Kinds: map[string]bool{"Task6": true}, Fields: rawFields(t, `{"tp": 0, "st": 1, "tt": "SEED anytime"}`)},
		"wire-only": {UUID: "wire-only", Kinds: map[string]bool{"Task7": true}, Fields: rawFields(t, `{"tp": 2, "tt": "SEED heading"}`)},
		"area":      {UUID: "area", Kinds: map[string]bool{"Area3": true}, Fields: rawFields(t, `{"tt": "MCP E2E SANDBOX"}`)},
		"odd-kind":  {UUID: "odd-kind", Kinds: map[string]bool{"Task6": true}, Fields: rawFields(t, `{"tp": 9}`)},
	}
	got := buildConformanceInput(mirror, live, conformanceTestKey)

	var ids []string
	for _, in := range got {
		ids = append(ids, in.UUID)
	}
	if want := []string{"both", "mirror-only", "wire-only"}; !slices.Equal(ids, want) {
		t.Fatalf("UUIDs %v, want %v", ids, want)
	}
	both := got[0]
	if both.Mirror == nil || both.Wire == nil || both.Mirror.Title != "" || both.Mirror.Note != "" {
		t.Errorf("both: mirror %+v, wire %v", both.Mirror, both.Wire)
	}
	if both.TitleHash != "76b5488b7e83e880" {
		t.Errorf("both: title hash %s", both.TitleHash)
	}
	if mirror[0].Title != "SEED anytime" {
		t.Error("buildConformanceInput changed the mirror's task")
	}
	if got[1].Wire != nil || got[2].Mirror != nil || got[2].TitleHash != conformanceTitleHashOf(conformanceTestKey, "SEED heading") {
		t.Errorf("mirror-only %+v, wire-only %+v", got[1], got[2])
	}
	if err := validateConformanceInput(conformanceInput{Format: conformanceFormat, Tasks: got}); err != nil {
		t.Error(err)
	}
}

func TestEncodeConformanceJSON(t *testing.T) {
	t.Parallel()
	in := conformanceInput{Format: 1, Today: "2026-10-12", Tasks: []inputTask{
		{UUID: "a", TitleHash: "0000000000000000", Mirror: &things.Task{UUID: "a"}},
		{UUID: "b", TitleHash: "0000000000000001", Wire: &taskFields{Start: intPtr(1)}},
	}}
	raw, err := encodeConformanceJSON(in)
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(raw), "\n    {"); n != 2 {
		t.Errorf("want one line per task, got %d task lines:\n%s", n, raw)
	}
	var back conformanceInput
	if err := decodeStrict(raw, &back); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(back, in) {
		t.Errorf("round trip changed the input:\n%s", raw)
	}
}

func TestAppViewsAsServer(t *testing.T) {
	t.Parallel()
	tests := []struct{ app, want []string }{
		{[]string{"anytime", "today"}, []string{"today"}},
		{[]string{"anytime"}, []string{"anytime"}},
		{[]string{"inbox", "today"}, []string{"inbox", "today"}},
		{[]string{"trash"}, nil},
		{[]string{"upcoming"}, []string{"upcoming"}},
		{nil, nil},
	}
	for _, tt := range tests {
		if got := appViewsAsServer(tt.app); !slices.Equal(got, tt.want) {
			t.Errorf("appViewsAsServer(%v) = %v, want %v", tt.app, got, tt.want)
		}
	}
}

func TestServerViews(t *testing.T) {
	t.Parallel()
	today := civil.Date{Year: 2026, Month: 10, Day: 12}
	mirror := []things.Task{
		{UUID: "inbox", Type: things.TaskTypeTask, Schedule: things.TaskScheduleInbox},
		{UUID: "inbox-due", Type: things.TaskTypeTask, Schedule: things.TaskScheduleInbox, DeadlineDate: day("2026-10-12")},
		{UUID: "today", Type: things.TaskTypeTask, Schedule: things.TaskScheduleAnytime, ScheduledDate: day("2026-10-11")},
		{UUID: "anytime", Type: things.TaskTypeTask, Schedule: things.TaskScheduleAnytime},
		{UUID: "arrived", Type: things.TaskTypeTask, Schedule: things.TaskScheduleSomeday, ScheduledDate: day("2026-10-12")},
		{UUID: "upcoming", Type: things.TaskTypeTask, Schedule: things.TaskScheduleSomeday, ScheduledDate: day("2026-10-13")},
		{UUID: "someday", Type: things.TaskTypeTask, Schedule: things.TaskScheduleSomeday},
		{UUID: "never", Type: things.TaskTypeTask, Schedule: things.TaskScheduleSomeday, ScheduledDate: day("4001-01-01")},
		{UUID: "done", Type: things.TaskTypeTask, Status: things.TaskStatusCompleted, CompletionDate: day("2026-10-01")},
		{UUID: "canceled", Type: things.TaskTypeTask, Status: things.TaskStatusCanceled},
		{UUID: "trashed", Type: things.TaskTypeTask, Schedule: things.TaskScheduleAnytime, InTrash: true},
		{UUID: "project", Type: things.TaskTypeProject, Schedule: things.TaskScheduleAnytime},
		{UUID: "evening", Type: things.TaskTypeTask, Schedule: things.TaskScheduleAnytime, ScheduledDate: day("2026-10-12"), StartBucket: 1},
		{UUID: "evening-anytime", Type: things.TaskTypeTask, Schedule: things.TaskScheduleAnytime, StartBucket: 1},
		{UUID: "dismissed", Type: things.TaskTypeTask, Schedule: things.TaskScheduleAnytime, DeadlineDate: day("2026-10-10"), DeadlineSuppressionDate: day("2026-10-11")},
	}
	got, evening, err := serverViews(mirror, today)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string][]string{
		"inbox": {"inbox"}, "inbox-due": {"inbox", "today"}, "today": {"today"}, "anytime": {"anytime"},
		"arrived": {"today"}, "upcoming": {"upcoming"}, "someday": {"someday"}, "never": {"someday"}, "done": {"logbook"},
		"evening": {"today"}, "evening-anytime": {"anytime"}, "dismissed": {"anytime"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("serverViews = %v\nwant %v", got, want)
	}
	if want := map[string]bool{"evening": true}; !reflect.DeepEqual(evening, want) {
		t.Errorf("This Evening = %v, want %v", evening, want)
	}
}

// conformanceFixture builds a case the way conformanceCases does.
func conformanceFixture(t *testing.T, app *expectedTask, mirror *things.Task, wire *taskFields) conformanceCase {
	t.Helper()
	today := civil.Date{Year: 2026, Month: 10, Day: 12}
	c := conformanceCase{UUID: "X", App: app, Today: today}
	if mirror != nil || wire != nil {
		c.Input = &inputTask{UUID: "X", TitleHash: "0000000000000000", Mirror: mirror, Wire: wire}
	}
	if app != nil {
		c.Want, c.WantEvening = appViewsAsServer(app.Views), app.Evening
	}
	if mirror != nil {
		got, evening, err := serverViews([]things.Task{*mirror}, today)
		if err != nil {
			t.Fatal(err)
		}
		c.Got, c.GotEvening = got[mirror.UUID], evening[mirror.UUID]
	}
	return c
}

// fields are the classification inputs of an open, untrashed to-do; the
// app's and history's copies start equal.
func fields(start int, sr, dd *string) taskFields {
	return taskFields{Type: intPtr(0), Status: intPtr(0), Trashed: boolPtr(false), Start: intPtr(start), StartDate: sr, Deadline: dd, Bucket: intPtr(0)}
}

func TestJudgeConformanceCase(t *testing.T) {
	t.Parallel()
	d := stringPtr
	anytimeToDo := &things.Task{UUID: "X", Type: things.TaskTypeTask, Schedule: things.TaskScheduleAnytime}
	todayToDo := &things.Task{UUID: "X", Type: things.TaskTypeTask, Schedule: things.TaskScheduleAnytime, ScheduledDate: day("2026-10-12")}
	eveningToDo := &things.Task{UUID: "X", Type: things.TaskTypeTask, Schedule: things.TaskScheduleAnytime, ScheduledDate: day("2026-10-12"), StartBucket: 1}
	dueToDo := &things.Task{UUID: "X", Type: things.TaskTypeTask, Schedule: things.TaskScheduleAnytime, DeadlineDate: day("2026-10-10")}
	dismissedToDo := &things.Task{UUID: "X", Type: things.TaskTypeTask, Schedule: things.TaskScheduleAnytime, DeadlineDate: day("2026-10-10"), DeadlineSuppressionDate: day("2026-10-11")}

	evening := fields(1, d("2026-10-12"), nil)
	evening.Bucket = intPtr(1)
	dismissed := fields(1, nil, d("2026-10-10"))
	dismissed.DeadlineSuppressed = d("2026-10-11")
	template := fields(2, nil, nil)
	template.Repeating = true
	canceled := fields(1, nil, nil)
	canceled.Status = intPtr(2)
	project := fields(1, nil, nil)
	project.Type = intPtr(1)
	moved := fields(1, nil, nil)
	moved.Start = intPtr(2)
	placed := fields(1, d("2026-10-12"), nil)
	placed.TodayRef = d("2026-10-12")
	noBucket := fields(1, nil, nil)
	noBucket.Bucket = nil

	tests := []struct {
		name      string
		c         conformanceCase
		gaps      []string
		problem   string // substring of the only problem; "" for none
		wantNotes bool
	}{
		{
			name: "agree",
			c:    conformanceFixture(t, &expectedTask{Type: "to-do", Views: []string{"anytime"}, App: fields(1, nil, nil)}, anytimeToDo, ptr(fields(1, nil, nil))),
		},
		{
			name: "today is also in the app's anytime",
			c:    conformanceFixture(t, &expectedTask{Type: "to-do", Views: []string{"anytime", "today"}, App: fields(1, d("2026-10-12"), nil)}, todayToDo, ptr(fields(1, d("2026-10-12"), nil))),
		},
		{
			name:    "mismatch",
			c:       conformanceFixture(t, &expectedTask{Type: "to-do", Views: []string{"someday"}, App: fields(1, nil, nil)}, anytimeToDo, ptr(fields(1, nil, nil))),
			problem: "the app shows [someday], the server lists [anytime]",
		},
		{
			name: "this evening",
			c: conformanceFixture(t, &expectedTask{Type: "to-do", Views: []string{"anytime", "today"}, Evening: true, App: evening},
				eveningToDo, ptr(evening)),
		},
		{
			name: "This Evening in the app only",
			c: conformanceFixture(t, &expectedTask{Type: "to-do", Views: []string{"anytime", "today"}, Evening: true, App: fields(1, d("2026-10-12"), nil)},
				todayToDo, ptr(fields(1, d("2026-10-12"), nil))),
			problem: "the app shows [today] (This Evening), the server lists [today]",
		},
		{
			name: "This Evening on the server only",
			c: conformanceFixture(t, &expectedTask{Type: "to-do", Views: []string{"anytime", "today"}, App: evening},
				eveningToDo, ptr(evening)),
			problem: "the app shows [today], the server lists [today] (This Evening)",
		},
		{
			name: "dismissed deadline",
			c: conformanceFixture(t, &expectedTask{Type: "to-do", Views: []string{"anytime"}, App: dismissed},
				dismissedToDo, ptr(dismissed)),
		},
		{
			name: "placed in Today with tir",
			c: conformanceFixture(t, &expectedTask{Type: "to-do", Views: []string{"anytime", "today"}, App: placed},
				&things.Task{UUID: "X", Type: things.TaskTypeTask, Schedule: things.TaskScheduleAnytime, ScheduledDate: day("2026-10-12"), TodayIndexReferenceDate: day("2026-10-12")}, ptr(placed)),
		},
		{
			name: "history without sb is the main section",
			c:    conformanceFixture(t, &expectedTask{Type: "to-do", Views: []string{"anytime"}, App: noBucket}, anytimeToDo, ptr(noBucket)),
		},
		{
			name: "project",
			c: conformanceFixture(t, &expectedTask{Type: "project", Views: []string{"anytime"}, App: project},
				&things.Task{UUID: "X", Type: things.TaskTypeProject, Schedule: things.TaskScheduleAnytime}, ptr(project)),
			gaps: []string{"KI-02 projects-not-listed"},
		},
		{
			name: "repeating template",
			c: conformanceFixture(t, &expectedTask{Type: "to-do", App: template},
				&things.Task{UUID: "X", Type: things.TaskTypeTask, Schedule: things.TaskScheduleSomeday}, ptr(template)),
			gaps: []string{"KI-05 repeating-template"},
		},
		{
			name: "canceled",
			c: conformanceFixture(t, &expectedTask{Type: "to-do", Views: []string{"logbook"}, App: canceled},
				&things.Task{UUID: "X", Type: things.TaskTypeTask, Schedule: things.TaskScheduleAnytime, Status: things.TaskStatusCanceled}, ptr(canceled)),
			gaps: []string{"KI-05 canceled-not-in-logbook"},
		},
		{
			name: "a known gap that does not explain everything",
			c: conformanceFixture(t, &expectedTask{Type: "to-do", Views: []string{"logbook", "today"}, App: canceled},
				&things.Task{UUID: "X", Type: things.TaskTypeTask, Schedule: things.TaskScheduleAnytime, Status: things.TaskStatusCanceled}, ptr(canceled)),
			problem: "not explained by the known gaps KI-05 canceled-not-in-logbook",
		},
		{
			name: "drift between the app and the cloud",
			c: conformanceFixture(t, &expectedTask{Type: "to-do", Views: []string{"someday"}, App: moved},
				anytimeToDo, ptr(fields(1, nil, nil))),
			problem: "st: app 2, cloud 1",
		},
		{
			name: "drift that changes no view",
			c: conformanceFixture(t, &expectedTask{Type: "to-do", Views: []string{"anytime"}, TitleHash: "1111111111111111", App: fields(1, nil, nil)},
				anytimeToDo, ptr(fields(1, nil, nil))),
			wantNotes: true,
		},
		{
			name:    "the SDK mirror disagrees with history",
			c:       conformanceFixture(t, &expectedTask{Type: "to-do", Views: []string{"anytime"}, App: fields(1, nil, nil)}, anytimeToDo, ptr(fields(2, nil, nil))),
			problem: "the SDK mirror disagrees with history on st: mirror 1, history 2",
		},
		{
			name:    "the SDK mirror lost dds",
			c:       conformanceFixture(t, &expectedTask{Type: "to-do", Views: []string{"anytime"}, App: dismissed}, dueToDo, ptr(dismissed)),
			problem: `the SDK mirror disagrees with history on dds: mirror null, history "2026-10-11"`,
		},
		{
			name:    "the SDK mirror lost sb",
			c:       conformanceFixture(t, &expectedTask{Type: "to-do", Views: []string{"anytime", "today"}, Evening: true, App: evening}, todayToDo, ptr(evening)),
			problem: "the SDK mirror disagrees with history on sb: mirror 0, history 1",
		},
		{
			name:    "the SDK mirror lost tir",
			c:       conformanceFixture(t, &expectedTask{Type: "to-do", Views: []string{"anytime", "today"}, App: placed}, todayToDo, ptr(placed)),
			problem: `the SDK mirror disagrees with history on tir: mirror null, history "2026-10-12"`,
		},
		{
			name:    "missing from the mirror",
			c:       conformanceFixture(t, &expectedTask{Type: "to-do", Views: []string{"anytime"}, App: fields(1, nil, nil)}, nil, ptr(fields(1, nil, nil))),
			problem: "the SDK dropped it",
		},
		{
			name:    "missing from Things Cloud",
			c:       conformanceFixture(t, &expectedTask{Type: "to-do", Views: []string{"anytime"}, App: fields(1, nil, nil)}, nil, nil),
			problem: "Things Cloud has no such object",
		},
		{
			name:    "unknown to the app",
			c:       conformanceFixture(t, nil, anytimeToDo, ptr(fields(1, nil, nil))),
			problem: "the Things app does not have it",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if tt.c.App != nil && tt.c.App.TitleHash == "" {
				tt.c.App.TitleHash = "0000000000000000"
			}
			v := judgeConformanceCase(tt.c)
			switch {
			case tt.problem == "" && len(v.Problems) > 0:
				t.Errorf("problems %q", v.Problems)
			case tt.problem != "" && (len(v.Problems) != 1 || !strings.Contains(v.Problems[0], tt.problem)):
				t.Errorf("problems %q, want one containing %q", v.Problems, tt.problem)
			}
			if !slices.Equal(v.Gaps, tt.gaps) {
				t.Errorf("gaps %q, want %q", v.Gaps, tt.gaps)
			}
			if tt.wantNotes != (len(v.Notes) > 0) {
				t.Errorf("notes %q", v.Notes)
			}
		})
	}
}

func ptr[T any](v T) *T { return &v }

func writeSnapshot(t *testing.T, exp conformanceExpected, in conformanceInput) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "2026-10-12T1300-test")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	for name, v := range map[string]any{conformanceExpectedFile: exp, conformanceInputFile: in} {
		raw, err := encodeConformanceJSON(v)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), raw, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func validSnapshotFiles() (conformanceExpected, conformanceInput) {
	exp := conformanceExpected{
		Format: 1, Today: "2026-10-12", Timezone: "Europe/Moscow", TitleKeyID: "630dcd29", MarkerArea: "M",
		Tasks: []expectedTask{
			{UUID: "a", Type: "to-do", TitleHash: "76b5488b7e83e880", Views: []string{"anytime"}, App: fields(1, nil, nil)},
			{UUID: "p", Type: "project", TitleHash: "76b5488b7e83e881", Views: []string{"anytime"}, App: fields(1, nil, nil)},
		},
	}
	in := conformanceInput{
		Format: 1, Today: "2026-10-12", Timezone: "Europe/Moscow", TitleKeyID: "630dcd29", MarkerArea: "M",
		Tasks: []inputTask{
			{UUID: "a", TitleHash: "76b5488b7e83e880", Mirror: &things.Task{UUID: "a", Type: things.TaskTypeTask, Schedule: things.TaskScheduleAnytime}, Wire: ptr(fields(1, nil, nil))},
			{UUID: "p", TitleHash: "76b5488b7e83e881", Mirror: &things.Task{UUID: "p", Type: things.TaskTypeProject, Schedule: things.TaskScheduleAnytime}, Wire: ptr(fields(1, nil, nil))},
		},
	}
	in.Tasks[1].Wire.Type = intPtr(1)
	exp.Tasks[1].App.Type = intPtr(1)
	return exp, in
}

func TestLoadConformanceSnapshot(t *testing.T) {
	t.Parallel()
	exp, in := validSnapshotFiles()
	snap, err := loadConformanceSnapshot(writeSnapshot(t, exp, in))
	if err != nil {
		t.Fatal(err)
	}
	cases, err := conformanceCases(snap)
	if err != nil {
		t.Fatal(err)
	}
	var gaps []string
	for _, c := range cases {
		v := judgeConformanceCase(c)
		if len(v.Problems) > 0 {
			t.Errorf("%s: %q", c.UUID, v.Problems)
		}
		gaps = append(gaps, v.Gaps...)
	}
	if !slices.Equal(gaps, []string{"KI-02 projects-not-listed"}) {
		t.Errorf("gaps %q", gaps)
	}

	broken := map[string]func(*conformanceExpected, *conformanceInput){
		"another day":           func(e *conformanceExpected, _ *conformanceInput) { e.Today = "2026-10-13" },
		"another key":           func(_ *conformanceExpected, i *conformanceInput) { i.TitleKeyID = "00000000" },
		"another account":       func(_ *conformanceExpected, i *conformanceInput) { i.MarkerArea = "N" },
		"a title in the mirror": func(_ *conformanceExpected, i *conformanceInput) { i.Tasks[0].Mirror.Title = "SEED anytime" },
		"a note in the mirror":  func(_ *conformanceExpected, i *conformanceInput) { i.Tasks[0].Mirror.Note = "n" },
		"a clear-text title":    func(e *conformanceExpected, _ *conformanceInput) { e.Tasks[0].TitleHash = "SEED anytime" },
		"an unknown view":       func(e *conformanceExpected, _ *conformanceInput) { e.Tasks[0].Views = []string{"evening"} },
		"a heading in expected": func(e *conformanceExpected, _ *conformanceInput) { e.Tasks[0].Type = "heading" },
		"a local zone":          func(e *conformanceExpected, i *conformanceInput) { e.Timezone, i.Timezone = "Local", "Local" },
		"another format":        func(e *conformanceExpected, _ *conformanceInput) { e.Format = 2 },
		"a repeated UUID":       func(_ *conformanceExpected, i *conformanceInput) { i.Tasks[1].UUID = "a" },
	}
	for name, breakIt := range broken {
		e, i := validSnapshotFiles()
		breakIt(&e, &i)
		if _, err := loadConformanceSnapshot(writeSnapshot(t, e, i)); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}

	dir := writeSnapshot(t, exp, in)
	raw, err := os.ReadFile(filepath.Join(dir, conformanceInputFile))
	if err != nil {
		t.Fatal(err)
	}
	unknown := bytes.Replace(raw, []byte(`"format": 1`), []byte(`"format": 1, "extra": true`), 1)
	if err := os.WriteFile(filepath.Join(dir, conformanceInputFile), unknown, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadConformanceSnapshot(dir); err == nil || !strings.Contains(err.Error(), "extra") {
		t.Errorf("unknown field: err = %v", err)
	}
	if err := os.Remove(filepath.Join(dir, conformanceInputFile)); err != nil {
		t.Fatal(err)
	}
	if _, err := loadConformanceSnapshot(dir); err == nil || !strings.Contains(err.Error(), "incomplete") {
		t.Errorf("no input.json: err = %v", err)
	}
}

func TestConformanceSnapshotDirs(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	if dirs, err := conformanceSnapshotDirs(filepath.Join(root, "missing")); err != nil || dirs != nil {
		t.Fatalf("missing root: %v, %v", dirs, err)
	}
	for _, dir := range []string{"b-snap", "a-snap", "not-a-snap"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	for _, f := range []string{"a-snap/expected.json", "b-snap/expected.json", "README.md"} {
		if err := os.WriteFile(filepath.Join(root, f), []byte("{}"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	dirs, err := conformanceSnapshotDirs(root)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{filepath.Join(root, "a-snap"), filepath.Join(root, "b-snap")}; !slices.Equal(dirs, want) {
		t.Errorf("dirs %v, want %v", dirs, want)
	}
}
