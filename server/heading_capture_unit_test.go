package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func headingHistoryForTest() []wireItem {
	ids := []string{generateUUID(), generateUUID(), generateUUID()}
	makeItem := func(index int, id string, action int, p map[string]any) wireItem {
		fields := map[string]json.RawMessage{}
		for k, v := range p {
			fields[k], _ = json.Marshal(v)
		}
		return wireItem{ServerIndex: index, UUID: id, Action: action, Kind: "Task7", Payload: fields}
	}
	return []wireItem{
		makeItem(0, ids[0], 0, map[string]any{"tp": 1, "tt": "SEED project", "rr": nil}),
		makeItem(1, ids[1], 0, map[string]any{"tp": 2, "tt": "SEED heading", "pr": []string{ids[0]}, "rr": nil}),
		makeItem(2, ids[2], 0, map[string]any{"tp": 0, "tt": "SEED under heading", "agr": []string{ids[1]}, "pr": []string{ids[0]}, "ar": []string{}, "rr": nil, "nt": map[string]any{"v": "private note secret@example.invalid"}}),
		makeItem(3, ids[2], 1, map[string]any{"agr": []string{ids[1]}, "pr": []string{ids[0]}, "ar": []string{}}),
		makeItem(3, "not-selected", 0, map[string]any{"tp": 0, "tt": "unrelated private task"}),
	}
}

func TestHeadingCaptureSanitizesAndPreservesHistory(t *testing.T) {
	t.Parallel()
	fx, err := buildHeadingCapture(headingHistoryForTest())
	if err != nil {
		t.Fatal(err)
	}
	if len(fx.Objects) != 3 || len(fx.Items) != 4 {
		t.Fatalf("objects=%d items=%d", len(fx.Objects), len(fx.Items))
	}
	b, _ := json.Marshal(fx)
	for _, secret := range []string{"private note", "secret@example.invalid", "unrelated private task", "not-selected"} {
		if strings.Contains(string(b), secret) {
			t.Error("unselected or private data exported")
		}
	}
	if fx.Items[3].SlotItems != 2 {
		t.Error("lost commit grouping")
	}
	if len(fx.Redacted) == 0 {
		t.Error("missing redaction paths")
	}
	if err := checkHeadingEvidence(fx); err != nil {
		t.Fatal(err)
	}
}

func TestHeadingEvidenceRejectsIncompleteCapture(t *testing.T) {
	t.Parallel()
	fx, err := buildHeadingCapture(headingHistoryForTest())
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name  string
		items []captureItem
	}{
		{"create only", fx.Items[:3]}, {"modify only", fx.Items[3:]}, {"empty", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			copy := fx
			copy.Items = tc.items
			if checkHeadingEvidence(copy) == nil {
				t.Error("accepted incomplete native evidence")
			}
		})
	}
	bad := fx
	bad.Items = append([]captureItem(nil), fx.Items...)
	bad.Items[3].Payload = json.RawMessage(`{"agr":[],"pr":[],"ar":[]}`)
	if checkHeadingEvidence(bad) == nil {
		t.Error("accepted wrong membership")
	}
}

func TestHeadingCaptureRejectsMissingOrAmbiguousSeeds(t *testing.T) {
	t.Parallel()
	items := headingHistoryForTest()
	if _, err := buildHeadingCapture(items[1:]); err == nil {
		t.Error("accepted missing project")
	}
	duplicate := items[0]
	duplicate.UUID = "different-project"
	if _, err := buildHeadingCapture(append(items, duplicate)); err == nil {
		t.Error("accepted ambiguous project")
	}
}
