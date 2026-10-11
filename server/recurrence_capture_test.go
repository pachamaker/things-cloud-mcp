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

const recurrenceCapturePath = "../testdata/captures/recurrence-seeds-2026-10-11.json"

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
	day := func(y int, m time.Month, d int) float64 {
		return float64(time.Date(y, m, d, 0, 0, 0, 0, time.UTC).Unix())
	}
	oct11, oct12, oct18 := day(2026, time.October, 11), day(2026, time.October, 12), day(2026, time.October, 18)
	never := day(4001, time.January, 1)

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

// The server's to-do create sends the same key set as the official client's
// to-do create (Task7 there, Task6 here).
func TestTaskCreatePayload_KeysMatchOfficialCreate(t *testing.T) {
	t.Parallel()

	items, _ := loadCapture(t, recurrenceCapturePath)
	raw, err := json.Marshal(taskCreatePayload{Nt: emptyNote(), Xx: defaultExtension()})
	if err != nil {
		t.Fatal(err)
	}
	var server map[string]json.RawMessage
	if err := json.Unmarshal(raw, &server); err != nil {
		t.Fatal(err)
	}
	creates := 0
	for _, it := range items {
		if it.Action != 0 {
			continue
		}
		creates++
		if got, want := sortedKeys(it.Payload), sortedKeys(server); !slices.Equal(got, want) {
			t.Errorf("official create of %s has keys %v; taskCreatePayload sends %v", it.UUID, got, want)
		}
	}
	if creates != 4 {
		t.Errorf("%d creates in the capture, want 4 (two to-dos, two templates)", creates)
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
