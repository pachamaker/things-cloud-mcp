package main

// Pins what the official Things apps wrote for the repeating sandbox seeds
// (testdata/captures/README.md) to the claims of docs/protocol/things-cloud.md
// §5.3, and diffs the server's payload builders against it (§8 step 3).

import (
	"bytes"
	"encoding/json"
	"os"
	"reflect"
	"slices"
	"testing"
	"time"
)

const (
	recurrenceCapturePath = "../testdata/captures/recurrence-seeds-2026-10-11.json"
	// The same seeds read again after the owner completed both instance 1s and,
	// ahead of time, the weekly occurrence of 2026-10-18.
	recurrenceCompletionCapturePath = "../testdata/captures/recurrence-completion-2026-10-11.json"
)

// captureDay is a calendar day as decoded from a capture's JSON (§4).
func captureDay(t *testing.T, s string) float64 {
	t.Helper()
	return float64(wireDay(t, s))
}

// anInstant stands for any positive Unix time in a wanted payload (sp, md, cd).
type anInstant struct{}

// checkPayload reports keys p has or lacks against want, and values that differ.
func checkPayload(t *testing.T, label string, p map[string]json.RawMessage, want map[string]any) {
	t.Helper()
	if got, wantKeys := sortedKeys(p), sortedKeys(want); !slices.Equal(got, wantKeys) {
		t.Errorf("%s: keys %v, want %v", label, got, wantKeys)
	}
	for key, w := range want {
		raw, ok := p[key]
		if !ok {
			continue
		}
		got := decodeAny(t, raw)
		if _, ok := w.(anInstant); ok {
			if f, isNum := got.(float64); !isNum || f <= 0 {
				t.Errorf("%s %s = %v, want an instant", label, key, got)
			}
			continue
		}
		if !reflect.DeepEqual(got, w) {
			t.Errorf("%s %s = %v, want %v", label, key, got, w)
		}
	}
}

func loadCapture(t *testing.T, path string) ([]wireItem, captureFixture) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var fx captureFixture
	if err := json.Unmarshal(raw, &fx); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte("@")) || len(fx.Redacted) != 0 {
		t.Fatalf("%s carries an '@' or redacted values; re-check its sanitization", path)
	}
	items := make([]wireItem, len(fx.Items))
	for i, it := range fx.Items {
		var p map[string]json.RawMessage
		if err := json.Unmarshal(it.Payload, &p); err != nil {
			t.Fatal(err)
		}
		items[i] = wireItem{UUID: it.UUID, Action: it.Action, Kind: it.Kind, Payload: p, ServerIndex: it.Index}
	}
	return items, fx
}

func captureRole(fx captureFixture, role string) string {
	for _, o := range fx.Objects {
		if o.Role == role {
			return o.UUID
		}
	}
	return ""
}

func decodeAny(t *testing.T, raw json.RawMessage) any {
	t.Helper()
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatal(err)
	}
	return v
}

func TestRecurrenceCapture_KI01_TemplateAndInstance(t *testing.T) {
	t.Parallel()

	items, fx := loadCapture(t, recurrenceCapturePath)
	folded := foldHistory(items)
	oct11, oct12, oct18 := captureDay(t, "2026-10-11"), captureDay(t, "2026-10-12"), captureDay(t, "2026-10-18")
	never := captureDay(t, "4001-01-01")

	for _, it := range items {
		if it.Kind != "Task7" {
			t.Errorf("index %d %s: kind %s; the apps wrote every item of the capture as Task7", it.ServerIndex, it.UUID, it.Kind)
		}
	}

	tests := []struct {
		title             string
		rr                map[string]any
		templateFields    map[string]any
		instanceCreatedAt int
	}{
		{
			title: "SEED repeat weekly",
			// 2026-10-11 is a Sunday: wd 0 is Sunday, as Go's time.Weekday.
			rr: map[string]any{"fu": 256.0, "fa": 1.0, "tp": 0.0, "of": []any{map[string]any{"wd": 0.0}},
				"ia": oct11, "sr": oct11, "ed": never, "rc": 0.0, "ts": 0.0, "rrv": 4.0},
			templateFields: map[string]any{"st": 2.0, "sr": nil, "tir": oct18, "icsd": oct18, "icc": 1.0, "rt": []any{}, "rp": nil, "ato": nil},
		},
		{
			title: "SEED repeat monthly after completion",
			rr: map[string]any{"fu": 8.0, "fa": 1.0, "tp": 1.0, "of": []any{},
				"ia": 0.0, "sr": oct11, "ed": never, "rc": 0.0, "ts": 0.0, "rrv": 4.0},
			templateFields: map[string]any{"st": 2.0, "sr": nil, "tir": nil, "icsd": oct12, "icc": 1.0, "rt": []any{}, "rp": nil, "ato": nil},
		},
	}
	for _, tc := range tests {
		tmplID := captureRole(fx, `template "`+tc.title+`"`)
		instID := captureRole(fx, `instance 1 of "`+tc.title+`"`)
		tmpl, inst := folded[tmplID], folded[instID]
		if tmpl == nil || inst == nil {
			t.Fatalf("%q: template %q or instance %q missing from the capture", tc.title, tmplID, instID)
		}
		if got := decodeAny(t, tmpl.Fields["rr"]); !reflect.DeepEqual(got, any(tc.rr)) {
			t.Errorf("%q template rr = %v, want %v", tc.title, got, tc.rr)
		}
		for key, want := range tc.templateFields {
			if got := decodeAny(t, tmpl.Fields[key]); !reflect.DeepEqual(got, want) {
				t.Errorf("%q template %s = %v, want %v", tc.title, key, got, want)
			}
		}
		instWant := map[string]any{"rt": []any{tmplID}, "rr": nil, "st": 1.0, "sr": oct11, "tir": oct11, "icsd": nil, "icc": 0.0, "rp": nil}
		for key, want := range instWant {
			if got := decodeAny(t, inst.Fields[key]); !reflect.DeepEqual(got, want) {
				t.Errorf("%q instance %s = %v, want %v", tc.title, key, got, want)
			}
		}

		// Making a to-do repeat is one commit: the template's create and the
		// modify that links the existing to-do to it as its first instance.
		var created, linked int
		for _, it := range items {
			if it.UUID == tmplID && it.Action == 0 {
				created = it.ServerIndex
			}
			if it.UUID == instID && it.Action == 1 && slices.Equal(jsonIDs(it.Payload["rt"]), []string{tmplID}) {
				linked = it.ServerIndex
			}
		}
		if created == 0 || created != linked {
			t.Errorf("%q: template created at index %d, instance linked at %d; want one commit", tc.title, created, linked)
		}
	}
}

// History is append-only: the second read repeats every item of the first,
// unchanged, and adds only slots at or above the first read's head.
func TestRecurrenceCapture_KI01_SecondReadExtendsFirst(t *testing.T) {
	t.Parallel()

	_, first := loadCapture(t, recurrenceCapturePath)
	_, second := loadCapture(t, recurrenceCompletionCapturePath)
	compact := func(items []captureItem) []captureItem {
		out := make([]captureItem, len(items))
		for i, it := range items {
			var buf bytes.Buffer
			if err := json.Compact(&buf, it.Payload); err != nil {
				t.Fatal(err)
			}
			it.Payload = buf.Bytes()
			out[i] = it
		}
		return out
	}
	var older []captureItem
	for _, it := range second.Items {
		if it.Index < first.HistoryHead {
			older = append(older, it)
		}
	}
	if !reflect.DeepEqual(compact(older), compact(first.Items)) {
		t.Errorf("the second read's items below index %d differ from the first read's", first.HistoryHead)
	}
	if len(second.Objects) < len(first.Objects) || !reflect.DeepEqual(second.Objects[:len(first.Objects)], first.Objects) {
		t.Errorf("the second read's objects %v do not start with the first read's %v", second.Objects, first.Objects)
	}
}

// The owner completed both instance 1s and, ahead of time, the weekly
// occurrence of 2026-10-18 within two seconds. The app sent that, the instance
// it generated for 2026-10-18 and the templates' bookkeeping as one commit:
// completion = ss 3 + sp + md; the weekly template counts the new instance and
// moves on; the after-completion template records the completion day and
// schedules its next occurrence a month later, without creating an instance.
func TestRecurrenceCapture_KI01_CompletionCommit(t *testing.T) {
	t.Parallel()

	items, fx := loadCapture(t, recurrenceCompletionCapturePath)
	_, first := loadCapture(t, recurrenceCapturePath)
	const weekly, monthly = "SEED repeat weekly", "SEED repeat monthly after completion"
	completed := map[string]any{"ss": 3.0, "sp": anInstant{}, "md": anInstant{}}
	want := map[string]map[string]any{
		`instance 1 of "` + weekly + `"`:  completed,
		`instance 1 of "` + monthly + `"`: completed,
		// icsd: the day after the generated occurrence; tir: the next one.
		`template "` + weekly + `"`: {"icc": 2.0, "icsd": captureDay(t, "2026-10-19"), "tir": captureDay(t, "2026-10-25")},
		// acrd: the completion day; tir: a month later (fu 8, fa 1).
		`template "` + monthly + `"`: {"acrd": captureDay(t, "2026-10-11"), "tir": captureDay(t, "2026-11-11")},
	}

	slot, size := first.HistoryHead, len(want)+1
	for _, it := range fx.Items {
		if it.Index >= slot && (it.Index != slot || it.SlotItems != size) {
			t.Errorf("%s: index %d with %d items in its slot; want all new items in slot %d of %d", it.UUID, it.Index, it.SlotItems, slot, size)
		}
	}
	var commit []wireItem
	for _, it := range items {
		if it.ServerIndex == slot {
			commit = append(commit, it)
		}
	}
	if len(commit) != size {
		t.Fatalf("%d items in slot %d, want %d", len(commit), slot, size)
	}

	var creates []string
	for _, it := range commit {
		if it.Kind != "Task7" {
			t.Errorf("%s: kind %s, want Task7", it.UUID, it.Kind)
		}
		if it.Action == 0 {
			creates = append(creates, it.UUID)
			continue
		}
		var role string
		for _, o := range fx.Objects {
			if o.UUID == it.UUID {
				role = o.Role
			}
		}
		w, ok := want[role]
		if !ok || it.Action != 1 {
			t.Errorf("%s (%s): unexpected action %d", it.UUID, role, it.Action)
			continue
		}
		checkPayload(t, role, it.Payload, w)
	}
	if !slices.Equal(creates, []string{captureRole(fx, `instance 2 of "`+weekly+`"`)}) {
		t.Errorf("creates in the commit: %v; want only instance 2 of %q (none for the after-completion template)", creates, weekly)
	}
}

// The instance the app generated for the weekly occurrence of 2026-10-18 (here
// completed ahead of time) is a create of every to-do key: a copy of the
// template as it stood, linked to it by rt, dated the template's tir, with
// rr null, icsd null, icc 0 and lt true.
func TestRecurrenceCapture_KI01_GeneratedInstance(t *testing.T) {
	t.Parallel()

	items, fx := loadCapture(t, recurrenceCompletionCapturePath)
	_, first := loadCapture(t, recurrenceCapturePath)
	tmplID := captureRole(fx, `template "SEED repeat weekly"`)
	instID := captureRole(fx, `instance 2 of "SEED repeat weekly"`)
	var before []wireItem
	var create *wireItem
	for i, it := range items {
		if it.ServerIndex < first.HistoryHead {
			before = append(before, it)
		}
		if it.UUID == instID && it.Action == 0 {
			create = &items[i]
		}
	}
	tmpl := foldHistory(before)[tmplID]
	if tmpl == nil || create == nil {
		t.Fatalf("template %q or the create of instance 2 %q missing from the capture", tmplID, instID)
	}
	occurrence := decodeAny(t, tmpl.Fields["tir"])
	if occurrence != captureDay(t, "2026-10-18") {
		t.Fatalf("template tir before the commit = %v, want 2026-10-18", occurrence)
	}

	own := map[string]any{
		"rt": []any{tmplID}, "rr": nil, "sr": occurrence, "icsd": nil, "icc": 0.0, "lt": true,
		"ss": 3.0, "sp": anInstant{}, "cd": anInstant{}, "md": anInstant{},
	}
	want := map[string]any{}
	for key, raw := range tmpl.Fields {
		want[key] = decodeAny(t, raw)
	}
	for key, v := range own {
		want[key] = v
	}
	checkPayload(t, "instance 2", create.Payload, want)
}

// The server's to-do completion sends the keys of the app's completion of a
// repeating instance. That is not enough to complete one: for an
// after-completion series the app advances the template in the same commit
// (TestRecurrenceCapture_KI01_CompletionCommit), which completeTask does not.
func TestCompleteTaskUpdate_KI01_KeysMatchOfficialInstanceCompletion(t *testing.T) {
	t.Parallel()

	items, fx := loadCapture(t, recurrenceCompletionCapturePath)
	raw, err := json.Marshal(newTaskUpdate().status(3).stopDate(1791726599.5).build())
	if err != nil {
		t.Fatal(err)
	}
	var server map[string]json.RawMessage
	if err := json.Unmarshal(raw, &server); err != nil {
		t.Fatal(err)
	}
	for _, role := range []string{`instance 1 of "SEED repeat weekly"`, `instance 1 of "SEED repeat monthly after completion"`} {
		id := captureRole(fx, role)
		found := false
		for _, it := range items {
			if it.UUID != id || it.Action != 1 || it.Payload["ss"] == nil {
				continue
			}
			found = true
			if got, want := sortedKeys(it.Payload), sortedKeys(server); !slices.Equal(got, want) {
				t.Errorf("%s completion has keys %v; completeTask sends %v", role, got, want)
			}
			if got, want := decodeAny(t, it.Payload["ss"]), decodeAny(t, server["ss"]); got != want {
				t.Errorf("%s completion ss = %v; completeTask sends %v", role, got, want)
			}
		}
		if !found {
			t.Errorf("no completion of %s in the capture", role)
		}
	}
}

// The server's to-do create sends the same key set as the official client's
// to-do creates, templates and the instance the app generated (Task7 there,
// Task6 here).
func TestTaskCreatePayload_KeysMatchOfficialCreate(t *testing.T) {
	t.Parallel()

	raw, err := json.Marshal(taskCreatePayload{Nt: emptyNote(), Xx: defaultExtension()})
	if err != nil {
		t.Fatal(err)
	}
	var server map[string]json.RawMessage
	if err := json.Unmarshal(raw, &server); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		path    string
		creates int
	}{
		{recurrenceCapturePath, 4},           // two to-dos, two templates
		{recurrenceCompletionCapturePath, 5}, // and the generated instance 2
	}
	for _, tc := range tests {
		items, _ := loadCapture(t, tc.path)
		creates := 0
		for _, it := range items {
			if it.Action != 0 {
				continue
			}
			creates++
			if got, want := sortedKeys(it.Payload), sortedKeys(server); !slices.Equal(got, want) {
				t.Errorf("%s: official create of %s has keys %v; taskCreatePayload sends %v", tc.path, it.UUID, got, want)
			}
		}
		if creates != tc.creates {
			t.Errorf("%s: %d creates, want %d", tc.path, creates, tc.creates)
		}
	}
}

// buildRepeatRule (unused since KI-01) matches the official weekly rule and
// differs from the after-completion one in ia and of. The rule is only part of
// the shape: the official client puts it on a hidden template, never on the
// visible to-do (docs/protocol/things-cloud.md §5.3).
func TestBuildRepeatRule_KI01_AgainstCapture(t *testing.T) {
	t.Parallel()

	items, fx := loadCapture(t, recurrenceCapturePath)
	folded := foldHistory(items)
	created := time.Date(2026, time.October, 11, 0, 0, 0, 0, time.UTC)
	tests := []struct {
		repeat, title string
		want          []string
	}{
		{"weekly", "SEED repeat weekly", nil},
		{"monthly after completion", "SEED repeat monthly after completion", []string{
			"ia: sent 1791676800, stored 0",
			`of: sent [{"dy":10}], stored []`,
		}},
	}
	for _, tc := range tests {
		built, err := buildRepeatRule(tc.repeat, created)
		if err != nil {
			t.Fatal(err)
		}
		var sent, stored map[string]json.RawMessage
		if err := json.Unmarshal(*built, &sent); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(folded[captureRole(fx, `template "`+tc.title+`"`)].Fields["rr"], &stored); err != nil {
			t.Fatal(err)
		}
		if got := payloadDifferences(sent, stored); !slices.Equal(got, tc.want) {
			t.Errorf("buildRepeatRule(%q) vs the official rule: %q, want %q", tc.repeat, got, tc.want)
		}
	}
}
