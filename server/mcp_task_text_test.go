package main

import (
	"context"
	things "github.com/arthursoares/things-cloud-sdk"
	"github.com/pachamaker/things-cloud-mcp/domain/civil"
	"strings"
	"testing"
)

func TestTaskDetailNormalAndOversized(t *testing.T) {
	d, _ := civil.Parse("2026-11-14")
	s := readRevisionState{Today: d, Timezone: "UTC"}
	task := &things.Task{UUID: "one", Title: "Title", Note: "full normal note"}
	var normal map[string]any
	readJSON(t, boundedTaskDetail(task, "rev", s), &normal)
	if normal["note"] != task.Note {
		t.Fatal(normal)
	}
	task.Note = strings.Repeat("secret", 10000)
	var fallback map[string]any
	text := readJSON(t, boundedTaskDetail(task, "rev", s), &fallback)
	if strings.Contains(text, "secret") || fallback["truncated_fields"] == nil {
		t.Fatal(text)
	}
	if task.Note != strings.Repeat("secret", 10000) {
		t.Fatal("mutated task")
	}
}
func TestTaskTextChunkReconstruction(t *testing.T) {
	useReadFixture(t, 0)
	source := strings.Repeat("界😀\n\"é", 6000)
	tasks := listMirror{{UUID: "one", Title: "title", Note: source}}
	mirror.readSource = func() (readModel, readRevisionState, error) {
		d, _ := civil.Parse("2026-11-14")
		return readModel{mirror: tasks}, readRevisionState{Identity: mirrorIdentity{key: "test", cursor: 1}, Today: d, Timezone: "UTC"}, nil
	}
	var rebuilt strings.Builder
	offset := 0
	rev := ""
	for {
		r, e := mcpGetTaskText(context.Background(), readRequest(map[string]any{"uuid": "one", "field": "note", "offset": offset, "limit": 8000, "revision": rev}))
		if e != nil || r.IsError {
			t.Fatalf("%v %+v", e, r)
		}
		var p taskTextOutput
		readJSON(t, r, &p)
		rebuilt.WriteString(p.Text)
		if !p.HasMore {
			break
		}
		if p.ReturnedCharacters <= 0 {
			t.Fatal("no progress")
		}
		offset = *p.NextOffset
		rev = p.Revision
	}
	if rebuilt.String() != source {
		t.Fatal("text corrupted")
	}
}
func TestTaskTextRevisionChanges(t *testing.T) {
	s := useReadFixture(t, 1)
	r, _ := mcpGetTaskText(context.Background(), readRequest(map[string]any{"uuid": "task-00000", "field": "note", "limit": 1}))
	var p taskTextOutput
	readJSON(t, r, &p)
	s.Identity.cursor++
	r, _ = mcpGetTaskText(context.Background(), readRequest(map[string]any{"uuid": "task-00000", "field": "note", "offset": 1, "revision": p.Revision}))
	if !r.IsError {
		t.Fatal("accepted stale text")
	}
}

func TestOversizedDetailTitleStillHasTextRetrieval(t *testing.T) {
	day, _ := civil.Parse("2026-11-14")
	for _, note := range []string{"", strings.Repeat("note", 20000)} {
		task := &things.Task{UUID: "task", Title: strings.Repeat("界", 30000), Note: note}
		result := boundedTaskDetail(task, strings.Repeat("r", 64), readRevisionState{Today: day, Timezone: "Europe/Moscow"})
		var v map[string]any
		readJSON(t, result, &v)
		if result.IsError || v["detail_instructions"] == nil {
			t.Fatal("cannot retrieve oversized task title")
		}
	}
}
