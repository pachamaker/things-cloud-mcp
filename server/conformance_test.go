package main

// Conformance harness of ADR-0005 (docs/testing/conformance.md). A snapshot in
// testdata/conformance/<name>/ pairs what the Things app showed with what the
// server's mirror held for the same account at the same time:
//
//   - expected.json: scripts/conformance/export_things.py, from the Things
//     app's own database through things.py (read-only);
//   - input.json: TestSandboxConformanceDump (build tag e2e), from the SDK
//     mirror and the raw history of the sandbox account.
//
// TestConformance_Snapshots replays every input.json through the read model on
// the snapshot's day and compares the views with expected.json. Everything
// else here is pure and unit-tested in conformance_unit_test.go.

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	things "github.com/arthursoares/things-cloud-sdk"

	"github.com/pachamaker/things-cloud-mcp/domain/civil"
)

const (
	conformanceRoot         = "../testdata/conformance"
	conformanceFormat       = 1
	conformanceExpectedFile = "expected.json"
	conformanceInputFile    = "input.json"
	conformanceTitleKeyLen  = 32
)

// Views as expected.json names them: things.py's view functions.
const (
	cvInbox    = "inbox"
	cvToday    = "today"
	cvAnytime  = "anytime"
	cvSomeday  = "someday"
	cvUpcoming = "upcoming"
	cvLogbook  = "logbook"
	cvTrash    = "trash"
)

var (
	conformanceViews     = []string{cvInbox, cvToday, cvAnytime, cvSomeday, cvUpcoming, cvLogbook, cvTrash}
	conformanceTitleHash = regexp.MustCompile(`^[0-9a-f]{16}$`)
)

// conformanceExpected is expected.json: the Things app's side of a snapshot.
type conformanceExpected struct {
	Format          int            `json:"format"`
	Source          string         `json:"source"`
	ThingsVersion   string         `json:"things_version"`
	DatabaseVersion int            `json:"database_version"`
	AppRunning      bool           `json:"app_running"`
	ExportedAt      string         `json:"exported_at"`
	Today           string         `json:"today"`
	Timezone        string         `json:"timezone"`
	TitleKeyID      string         `json:"title_key_id"`
	MarkerArea      string         `json:"marker_area"`
	Tasks           []expectedTask `json:"tasks"`
}

// expectedTask is one to-do or project of the Things app: the views things.py
// lists it in, whether Today shows it under This Evening, and the fields the
// app's database holds for it.
type expectedTask struct {
	UUID      string     `json:"uuid"`
	Type      string     `json:"type"`
	TitleHash string     `json:"title_hash"`
	Views     []string   `json:"views"`
	Evening   bool       `json:"evening"`
	App       taskFields `json:"app"`
}

// taskFields are the classification inputs of a task under their wire names
// (docs/protocol/things-cloud.md §5), as the Things app's database or Things
// Cloud's history holds them. Day values are civil dates (YYYY-MM-DD); a value
// that does not decode as a day is kept as "raw:<value>". null is a value: no
// date, or a key history never set. rr is whether a repeat rule is set (a
// repeating template), rt the template of an instance, pr and agr the project
// and heading ("" for none).
type taskFields struct {
	Type               *int    `json:"tp"`
	Status             *int    `json:"ss"`
	Trashed            *bool   `json:"tr"`
	Start              *int    `json:"st"`
	StartDate          *string `json:"sr"`
	TodayRef           *string `json:"tir"`
	Deadline           *string `json:"dd"`
	DeadlineSuppressed *string `json:"dds"`
	Bucket             *int    `json:"sb"`
	Repeating          bool    `json:"rr"`
	Template           string  `json:"rt"`
	Project            string  `json:"pr"`
	Heading            string  `json:"agr"`
}

// conformanceInput is input.json: the server's side of a snapshot.
type conformanceInput struct {
	Format      int         `json:"format"`
	Source      string      `json:"source"`
	SDK         string      `json:"sdk"`
	DumpedAt    string      `json:"dumped_at"`
	Today       string      `json:"today"`
	Timezone    string      `json:"timezone"`
	HistoryHead int         `json:"history_head"`
	TitleKeyID  string      `json:"title_key_id"`
	MarkerArea  string      `json:"marker_area"`
	Tasks       []inputTask `json:"tasks"`
}

// inputTask is one to-do, project or heading on Things Cloud. Mirror is the
// SDK's things.Task exactly as the server's mirror held it, title and note
// cleared: replaying it needs no mapping, and fields a later SDK adds decode
// as zero values. Wire is the history folded per key, which also carries the
// fields the SDK did not keep at dump time (tir, sb, dds, rr, rt).
type inputTask struct {
	UUID      string       `json:"uuid"`
	TitleHash string       `json:"title_hash"`
	Mirror    *things.Task `json:"mirror,omitempty"`
	Wire      *taskFields  `json:"wire,omitempty"`
}

type conformanceSnapshot struct {
	Name     string
	Today    civil.Date
	Expected conformanceExpected
	Input    conformanceInput
}

// ---------------------------------------------------------------------------
// Titles: keyed hashes, so the two halves can be matched without the text
// ---------------------------------------------------------------------------

// parseConformanceTitleKey reads the one-off key of a snapshot: 32 bytes as
// hex. scripts/conformance/snapshot.sh makes it outside the repository and
// deletes it afterwards, so committed hashes cannot be checked against
// guessed titles.
func parseConformanceTitleKey(raw []byte) ([]byte, error) {
	key, err := hex.DecodeString(strings.TrimSpace(string(raw)))
	if err != nil || len(key) != conformanceTitleKeyLen {
		return nil, fmt.Errorf("title key: want %d bytes as hex", conformanceTitleKeyLen)
	}
	return key, nil
}

// conformanceTitleHashOf is HMAC-SHA256 of the UTF-8 title, first 8 bytes as
// hex; export_things.py computes the same.
func conformanceTitleHashOf(key []byte, title string) string {
	m := hmac.New(sha256.New, key)
	m.Write([]byte(title))
	return hex.EncodeToString(m.Sum(nil)[:8])
}

// conformanceKeyID names a title key without revealing it, so the test can
// tell that both halves of a snapshot used the same one.
func conformanceKeyID(key []byte) string {
	sum := sha256.Sum256(key)
	return hex.EncodeToString(sum[:4])
}

// ---------------------------------------------------------------------------
// Building input.json (TestSandboxConformanceDump)
// ---------------------------------------------------------------------------

func intPtr(v int) *int          { return &v }
func boolPtr(v bool) *bool       { return &v }
func stringPtr(v string) *string { return &v }

// historyDay decodes a day value of history (Unix seconds of 00:00 UTC).
func historyDay(raw json.RawMessage) *string {
	if raw == nil || isJSONNull(raw) {
		return nil
	}
	if sec, ok := jsonInt(raw); ok {
		return stringPtr(civil.FromWire(sec).String())
	}
	return stringPtr("raw:" + string(raw))
}

func wireIntField(raw json.RawMessage) *int {
	if v, ok := jsonInt(raw); ok {
		return intPtr(int(v))
	}
	return nil
}

func firstOf(ids []string) string {
	if len(ids) == 0 {
		return ""
	}
	return ids[0]
}

// wireFieldsOf reads the classification inputs of one task from history.
func wireFieldsOf(e *foldedEntity) taskFields {
	f := e.Fields
	w := taskFields{
		Type:               wireIntField(f["tp"]),
		Status:             wireIntField(f["ss"]),
		Start:              wireIntField(f["st"]),
		StartDate:          historyDay(f["sr"]),
		TodayRef:           historyDay(f["tir"]),
		Deadline:           historyDay(f["dd"]),
		DeadlineSuppressed: historyDay(f["dds"]),
		Bucket:             wireIntField(f["sb"]),
		Repeating:          f["rr"] != nil && !isJSONNull(f["rr"]),
		Template:           firstOf(jsonIDs(f["rt"])),
		Project:            firstOf(jsonIDs(f["pr"])),
		Heading:            firstOf(jsonIDs(f["agr"])),
	}
	var tr bool
	if raw := f["tr"]; raw != nil && json.Unmarshal(raw, &tr) == nil {
		w.Trashed = boolPtr(tr)
	}
	return w
}

// mirrorDay is a day value of the mirror as a civil date.
func mirrorDay(t *time.Time) *string {
	if t == nil {
		return nil
	}
	return stringPtr(civil.FromWireTime(*t).String())
}

// fieldsOfMirror puts the mirror's view of a task beside history's.
func fieldsOfMirror(t *things.Task) taskFields {
	return taskFields{
		Type:               intPtr(int(t.Type)),
		Status:             intPtr(int(t.Status)),
		Trashed:            boolPtr(t.InTrash),
		Start:              intPtr(int(t.Schedule)),
		StartDate:          mirrorDay(t.ScheduledDate),
		TodayRef:           mirrorDay(t.TodayIndexReferenceDate),
		Deadline:           mirrorDay(t.DeadlineDate),
		DeadlineSuppressed: mirrorDay(t.DeadlineSuppressionDate),
		Bucket:             intPtr(t.StartBucket),
		Project:            firstOf(t.ParentTaskIDs),
		Heading:            firstOf(t.ActionGroupIDs),
	}
}

// historyAsMirror is history as the SDK mirror can hold it: a task whose
// history never set sb, or set it to null, is in Today's main section (0).
func historyAsMirror(w taskFields) taskFields {
	if w.Bucket == nil {
		w.Bucket = intPtr(0)
	}
	return w
}

// buildConformanceInput merges the SDK mirror (to-dos, projects, headings)
// with history folded per object. Titles become keyed hashes; notes are
// dropped.
func buildConformanceInput(mirror []*things.Task, live map[string]*foldedEntity, key []byte) []inputTask {
	byID := map[string]*inputTask{}
	get := func(uuid string) *inputTask {
		if byID[uuid] == nil {
			byID[uuid] = &inputTask{UUID: uuid}
		}
		return byID[uuid]
	}
	for _, t := range mirror {
		c := *t
		in := get(c.UUID)
		in.TitleHash = conformanceTitleHashOf(key, c.Title)
		c.Title, c.Note = "", ""
		in.Mirror = &c
	}
	for uuid, e := range live {
		if !hasKindPrefix(e, "Task") {
			continue
		}
		w := wireFieldsOf(e)
		if w.Type != nil && (*w.Type < 0 || *w.Type > 2) {
			continue
		}
		in := get(uuid)
		in.Wire = &w
		if in.TitleHash == "" {
			in.TitleHash = conformanceTitleHashOf(key, e.title())
		}
	}
	out := make([]inputTask, 0, len(byID))
	for _, uuid := range sortedKeys(byID) {
		out = append(out, *byID[uuid])
	}
	return out
}

// encodeConformanceJSON writes one task per line: snapshots stay diffable
// without growing to one line per field.
func encodeConformanceJSON(v any) ([]byte, error) {
	var head map[string]json.RawMessage
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(raw, &head); err != nil {
		return nil, err
	}
	var tasks []json.RawMessage
	if err := json.Unmarshal(head["tasks"], &tasks); err != nil {
		return nil, fmt.Errorf("no tasks list: %w", err)
	}
	delete(head, "tasks")
	var b bytes.Buffer
	b.WriteString("{\n")
	for _, k := range sortedKeys(head) {
		fmt.Fprintf(&b, "  %q: %s,\n", k, head[k])
	}
	b.WriteString("  \"tasks\": [")
	for i, task := range tasks {
		if i > 0 {
			b.WriteString(",")
		}
		b.WriteString("\n    ")
		b.Write(task)
	}
	b.WriteString("\n  ]\n}\n")
	return b.Bytes(), nil
}

// ---------------------------------------------------------------------------
// Loading snapshots
// ---------------------------------------------------------------------------

func decodeStrict(raw []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	return dec.Decode(v)
}

func readConformanceExpected(path string) (conformanceExpected, error) {
	var exp conformanceExpected
	raw, err := os.ReadFile(path)
	if err != nil {
		return exp, err
	}
	if err := decodeStrict(raw, &exp); err != nil {
		return exp, fmt.Errorf("%s: %w", path, err)
	}
	return exp, validateConformanceExpected(exp)
}

func validateConformanceExpected(exp conformanceExpected) error {
	var problems []string
	if exp.Format != conformanceFormat {
		problems = append(problems, fmt.Sprintf("format %d, want %d", exp.Format, conformanceFormat))
	}
	if _, err := civil.Parse(exp.Today); err != nil {
		problems = append(problems, fmt.Sprintf("today %q: %v", exp.Today, err))
	}
	if _, err := civil.LoadLocation(exp.Timezone); err != nil {
		problems = append(problems, fmt.Sprintf("timezone: %v", err))
	}
	if exp.TitleKeyID == "" || exp.MarkerArea == "" {
		problems = append(problems, "title_key_id and marker_area are required")
	}
	seen := map[string]bool{}
	for _, task := range exp.Tasks {
		switch {
		case task.UUID == "" || seen[task.UUID]:
			problems = append(problems, fmt.Sprintf("task %q: empty or repeated UUID", task.UUID))
		case task.Type != "to-do" && task.Type != "project":
			problems = append(problems, fmt.Sprintf("task %s: type %q", task.UUID, task.Type))
		case !conformanceTitleHash.MatchString(task.TitleHash):
			problems = append(problems, fmt.Sprintf("task %s: title_hash is not 16 hex digits", task.UUID))
		}
		seen[task.UUID] = true
		for _, v := range task.Views {
			if !slices.Contains(conformanceViews, v) {
				problems = append(problems, fmt.Sprintf("task %s: unknown view %q", task.UUID, v))
			}
		}
	}
	if len(problems) > 0 {
		return fmt.Errorf("expected.json: %s", strings.Join(problems, "; "))
	}
	return nil
}

func validateConformanceInput(in conformanceInput) error {
	var problems []string
	if in.Format != conformanceFormat {
		problems = append(problems, fmt.Sprintf("format %d, want %d", in.Format, conformanceFormat))
	}
	seen := map[string]bool{}
	for _, task := range in.Tasks {
		switch {
		case task.UUID == "" || seen[task.UUID]:
			problems = append(problems, fmt.Sprintf("task %q: empty or repeated UUID", task.UUID))
		case task.Mirror == nil && task.Wire == nil:
			problems = append(problems, fmt.Sprintf("task %s: neither mirror nor wire", task.UUID))
		case !conformanceTitleHash.MatchString(task.TitleHash):
			problems = append(problems, fmt.Sprintf("task %s: title_hash is not 16 hex digits", task.UUID))
		case task.Mirror != nil && (task.Mirror.Title != "" || task.Mirror.Note != ""):
			problems = append(problems, fmt.Sprintf("task %s: the mirror carries its title or note", task.UUID))
		case task.Mirror != nil && task.Mirror.UUID != task.UUID:
			problems = append(problems, fmt.Sprintf("task %s: the mirror's UUID is %s", task.UUID, task.Mirror.UUID))
		}
		seen[task.UUID] = true
	}
	if len(problems) > 0 {
		return fmt.Errorf("input.json: %s", strings.Join(problems, "; "))
	}
	return nil
}

func loadConformanceSnapshot(dir string) (conformanceSnapshot, error) {
	snap := conformanceSnapshot{Name: filepath.Base(dir)}
	exp, err := readConformanceExpected(filepath.Join(dir, conformanceExpectedFile))
	if err != nil {
		return snap, err
	}
	raw, err := os.ReadFile(filepath.Join(dir, conformanceInputFile))
	if err != nil {
		return snap, fmt.Errorf("an incomplete snapshot: %w", err)
	}
	var in conformanceInput
	if err := decodeStrict(raw, &in); err != nil {
		return snap, fmt.Errorf("input.json: %w", err)
	}
	if err := validateConformanceInput(in); err != nil {
		return snap, err
	}
	for _, pair := range [][3]string{
		{"today", exp.Today, in.Today},
		{"timezone", exp.Timezone, in.Timezone},
		{"title_key_id", exp.TitleKeyID, in.TitleKeyID},
		{"marker_area", exp.MarkerArea, in.MarkerArea},
	} {
		if pair[1] != pair[2] {
			return snap, fmt.Errorf("expected.json and input.json disagree on %s: %q, %q", pair[0], pair[1], pair[2])
		}
	}
	snap.Today, _ = civil.Parse(exp.Today)
	snap.Expected, snap.Input = exp, in
	return snap, nil
}

// conformanceSnapshotDirs lists the snapshot directories under root; a
// directory without expected.json is not one.
func conformanceSnapshotDirs(root string) ([]string, error) {
	entries, err := os.ReadDir(root)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var dirs []string
	for _, e := range entries {
		dir := filepath.Join(root, e.Name())
		if _, err := os.Stat(filepath.Join(dir, conformanceExpectedFile)); e.IsDir() && err == nil {
			dirs = append(dirs, dir)
		}
	}
	return dirs, nil
}

// ---------------------------------------------------------------------------
// Replaying the server and comparing
// ---------------------------------------------------------------------------

// serverViews lists, per to-do, the views the server's list tools put it in on
// day today: the read-model methods the MCP tools call, over the mirror as it
// was dumped. Logbook is things_list_completed. evening holds the to-dos
// things_list_today flags as This Evening.
func serverViews(mirror []things.Task, today civil.Date) (map[string][]string, map[string]bool, error) {
	r := readModel{mirror: listMirror(mirror)}
	out, evening := map[string][]string{}, map[string]bool{}
	for _, v := range []struct {
		view listView
		name string
	}{{viewInbox, cvInbox}, {viewToday, cvToday}, {viewAnytime, cvAnytime}, {viewSomeday, cvSomeday}, {viewUpcoming, cvUpcoming}} {
		tasks, err := r.tasksInViewOn(v.view, listOpts{}, today)
		if err != nil {
			return nil, nil, err
		}
		for _, t := range tasks {
			out[t.UUID] = append(out[t.UUID], v.name)
			if v.view == viewToday && inEvening(t, today) {
				evening[t.UUID] = true
			}
		}
	}
	done, err := r.CompletedTasksInRange(len(mirror)+1, nil, nil)
	if err != nil {
		return nil, nil, err
	}
	for _, t := range done {
		out[t.UUID] = append(out[t.UUID], cvLogbook)
	}
	for uuid := range out {
		sort.Strings(out[uuid])
	}
	return out, evening, nil
}

// appViewsAsServer maps the app's views onto the server's list tools: there is
// no Trash tool, and things_list_anytime leaves out what Today shows, while
// the app's Anytime (and things.py's) includes it.
func appViewsAsServer(views []string) []string {
	today := slices.Contains(views, cvToday)
	var out []string
	for _, v := range views {
		if v == cvTrash || (v == cvAnytime && today) {
			continue
		}
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}

// conformanceCase is one to-do or project of a snapshot.
type conformanceCase struct {
	UUID        string
	App         *expectedTask // nil: not in the Things app's database
	Input       *inputTask    // nil: not on Things Cloud
	Today       civil.Date
	Want        []string // the app's views, as the server's tools would list them
	WantEvening bool
	Got         []string // the server's views
	GotEvening  bool     // things_list_today flags it as This Evening
}

func (c conformanceCase) mirror() *things.Task {
	if c.Input == nil {
		return nil
	}
	return c.Input.Mirror
}

func (c conformanceCase) wire() *taskFields {
	if c.Input == nil {
		return nil
	}
	return c.Input.Wire
}

// knownGap is a documented difference between the server and the Things app.
// adjust applies the gap to the app's views (as the server would list them)
// and reports whether it concerns the case at all; a case counts as a known
// gap only when the adjusted views equal the server's exactly.
type knownGap struct {
	KI     string
	Name   string
	Why    string
	adjust func(c conformanceCase, want []string, evening bool) ([]string, bool, bool)
}

func (g knownGap) label() string { return g.KI + " " + g.Name }

var conformanceKnownGaps = []knownGap{
	{
		KI: "KI-02", Name: "projects-not-listed",
		Why: "the list tools classify to-dos only (SDK AllTasks); the app lists projects in Today, Anytime, Someday, Upcoming and Logbook",
		adjust: func(c conformanceCase, want []string, evening bool) ([]string, bool, bool) {
			if c.App == nil || c.App.Type != "project" {
				return want, evening, false
			}
			return nil, false, true
		},
	},
	{
		KI: "KI-05", Name: "repeating-template",
		Why: "the SDK mirror does not keep rr, so a repeating template is listed like a to-do of its own; the app lists it in no view",
		adjust: func(c conformanceCase, want []string, evening bool) ([]string, bool, bool) {
			w := c.wire()
			if w == nil || !w.Repeating || len(want) > 0 || slices.Contains(c.Got, cvLogbook) {
				return want, evening, false
			}
			return c.Got, evening, true
		},
	},
	{
		KI: "KI-05", Name: "canceled-not-in-logbook",
		Why: "things_list_completed lists completed to-dos only; canceled ones are in no list",
		adjust: func(c conformanceCase, want []string, evening bool) ([]string, bool, bool) {
			m := c.mirror()
			if m == nil || m.Status != things.TaskStatusCanceled || !slices.Contains(want, cvLogbook) {
				return want, evening, false
			}
			return slices.DeleteFunc(slices.Clone(want), func(v string) bool { return v == cvLogbook }), evening, true
		},
	},
}

// conformanceVerdict is the outcome of one case.
type conformanceVerdict struct {
	Problems []string // each one fails the case
	Gaps     []string // labels of the known gaps that explain the difference
	Notes    []string // differences that did not change any view
}

// fieldDifferences lists the keys on which two field sets disagree.
func fieldDifferences(a, b taskFields, aName, bName string, keys []string) []string {
	am, bm := fieldMap(a), fieldMap(b)
	var out []string
	for _, k := range keys {
		if string(am[k]) != string(bm[k]) {
			out = append(out, fmt.Sprintf("%s: %s %s, %s %s", k, aName, am[k], bName, bm[k]))
		}
	}
	return out
}

func fieldMap(f taskFields) map[string]json.RawMessage {
	raw, _ := json.Marshal(f)
	m := map[string]json.RawMessage{}
	_ = json.Unmarshal(raw, &m)
	return m
}

// mirrorKeys are the fields both the SDK mirror and history carry.
var mirrorKeys = []string{"tp", "ss", "tr", "st", "sr", "tir", "dd", "dds", "sb", "pr", "agr"}

// driftKeys are the fields that decide a view, compared between the app's
// database and Things Cloud.
var driftKeys = []string{"tp", "ss", "tr", "st", "sr", "dd", "dds", "sb", "rr"}

func judgeConformanceCase(c conformanceCase) conformanceVerdict {
	var v conformanceVerdict
	same := slices.Equal(c.Want, c.Got) && c.WantEvening == c.GotEvening
	m, w := c.mirror(), c.wire()

	switch {
	case c.App == nil:
		if len(c.Got) > 0 {
			v.Problems = append(v.Problems, fmt.Sprintf("the Things app does not have it, the server lists it in %v: the app has not synced it, or it was deleted there; retake the snapshot", c.Got))
		}
		return v
	case w == nil && m == nil:
		if len(c.Want) > 0 {
			v.Problems = append(v.Problems, fmt.Sprintf("the app shows it in %v, Things Cloud has no such object: the app has not synced it; retake the snapshot", c.Want))
		} else {
			v.Notes = append(v.Notes, "not on Things Cloud")
		}
		return v
	}

	if w != nil && m == nil {
		if len(c.Want) > 0 {
			v.Problems = append(v.Problems, fmt.Sprintf("history has it, the server's mirror does not: the SDK dropped it, so the server cannot list it in %v", c.Want))
			return v
		}
		v.Notes = append(v.Notes, "in history but not in the server's mirror")
	}
	if w != nil && m != nil {
		for _, d := range fieldDifferences(fieldsOfMirror(m), historyAsMirror(*w), "mirror", "history", mirrorKeys) {
			v.Problems = append(v.Problems, "the SDK mirror disagrees with history on "+d)
		}
		if len(v.Problems) > 0 {
			return v
		}
	}

	var drift []string
	if w != nil {
		drift = fieldDifferences(c.App.App, *w, "app", "cloud", driftKeys)
	}
	if c.Input != nil && c.App.TitleHash != c.Input.TitleHash {
		drift = append(drift, "title")
	}

	if same {
		if len(drift) > 0 {
			v.Notes = append(v.Notes, "app and cloud differ without changing a view: "+strings.Join(drift, "; "))
		}
		return v
	}
	if len(drift) > 0 {
		v.Problems = append(v.Problems, fmt.Sprintf("the app shows %s, the server lists %s, and the app's database and Things Cloud disagree on %s: the snapshot caught an unsynced change; retake it",
			describeViews(c.Want, c.WantEvening), describeViews(c.Got, c.GotEvening), strings.Join(drift, "; ")))
		return v
	}

	want, evening := c.Want, c.WantEvening
	var applied []string
	for _, g := range conformanceKnownGaps {
		var ok bool
		if want, evening, ok = g.adjust(c, want, evening); ok {
			applied = append(applied, g.label())
		}
	}
	if slices.Equal(want, c.Got) && evening == c.GotEvening {
		v.Gaps = applied
		return v
	}
	msg := fmt.Sprintf("the app shows %s, the server lists %s", describeViews(c.Want, c.WantEvening), describeViews(c.Got, c.GotEvening))
	if len(applied) > 0 {
		msg += fmt.Sprintf(" (not explained by the known gaps %s)", strings.Join(applied, ", "))
	}
	if w != nil {
		msg += "; history: " + describeFields(*w)
	}
	v.Problems = append(v.Problems, msg)
	return v
}

func describeViews(views []string, evening bool) string {
	s := "[" + strings.Join(views, " ") + "]"
	if evening {
		s += " (This Evening)"
	}
	return s
}

func describeFields(f taskFields) string {
	m := fieldMap(f)
	var parts []string
	for _, k := range driftKeys {
		parts = append(parts, k+"="+string(m[k]))
	}
	return strings.Join(parts, " ")
}

// conformanceCases pairs the app's to-dos and projects with the server's
// replay, plus whatever the server lists that the app does not have.
func conformanceCases(snap conformanceSnapshot) ([]conformanceCase, error) {
	inputs := map[string]*inputTask{}
	var mirror []things.Task
	for i := range snap.Input.Tasks {
		in := &snap.Input.Tasks[i]
		inputs[in.UUID] = in
		if in.Mirror != nil {
			mirror = append(mirror, *in.Mirror)
		}
	}
	got, evening, err := serverViews(mirror, snap.Today)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var cases []conformanceCase
	for i := range snap.Expected.Tasks {
		app := &snap.Expected.Tasks[i]
		seen[app.UUID] = true
		cases = append(cases, conformanceCase{
			UUID: app.UUID, App: app, Input: inputs[app.UUID], Today: snap.Today,
			Want: appViewsAsServer(app.Views), WantEvening: app.Evening, Got: got[app.UUID], GotEvening: evening[app.UUID],
		})
	}
	for _, uuid := range sortedKeys(inputs) {
		if seen[uuid] {
			continue
		}
		if len(got[uuid]) == 0 {
			continue
		}
		cases = append(cases, conformanceCase{UUID: uuid, Input: inputs[uuid], Today: snap.Today, Got: got[uuid], GotEvening: evening[uuid]})
	}
	sort.Slice(cases, func(i, j int) bool { return cases[i].UUID < cases[j].UUID })
	return cases, nil
}

// TestConformance_Snapshots is the ADR-0005 conformance suite: every
// mismatch between the app and the server is a failing subtest named after
// the snapshot and the UUID; differences explained by a known gap are logged
// with their KI.
func TestConformance_Snapshots(t *testing.T) {
	dirs, err := conformanceSnapshotDirs(conformanceRoot)
	if err != nil {
		t.Fatal(err)
	}
	if len(dirs) == 0 {
		t.Skip("no snapshots in testdata/conformance yet; docs/testing/conformance.md says how to take one")
	}
	used := map[string]int{}
	for _, dir := range dirs {
		t.Run(filepath.Base(dir), func(t *testing.T) {
			snap, err := loadConformanceSnapshot(dir)
			if err != nil {
				t.Fatal(err)
			}
			cases, err := conformanceCases(snap)
			if err != nil {
				t.Fatal(err)
			}
			gaps := map[string][]string{}
			agree, failed, notes := 0, 0, 0
			for _, c := range cases {
				v := judgeConformanceCase(c)
				for _, n := range v.Notes {
					notes++
					t.Logf("note %s: %s", c.UUID, n)
				}
				switch {
				case len(v.Problems) > 0:
					failed++
					t.Run(c.UUID, func(t *testing.T) {
						for _, p := range v.Problems {
							t.Error(p)
						}
					})
				case len(v.Gaps) > 0:
					for _, g := range v.Gaps {
						gaps[g] = append(gaps[g], c.UUID)
						used[g]++
					}
				default:
					agree++
				}
			}
			for _, g := range sortedKeys(gaps) {
				t.Logf("KNOWN GAP %s: %d item(s) %v", g, len(gaps[g]), gaps[g])
			}
			t.Logf("today %s in %s: %d items, %d agree, %d known gaps, %d mismatches, %d notes",
				snap.Expected.Today, snap.Expected.Timezone, len(cases), agree, len(cases)-agree-failed, failed, notes)
		})
	}
	for _, g := range conformanceKnownGaps {
		if used[g.label()] == 0 {
			t.Logf("known gap %s explains nothing in any snapshot: %s. If its KI is fixed, delete the gap", g.label(), g.Why)
		}
	}
}
