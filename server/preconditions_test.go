package main

import (
	"errors"
	"net/http"
	"strings"
	"testing"

	things "github.com/arthursoares/things-cloud-sdk"
	"github.com/arthursoares/things-cloud-sdk/sync"
)

// fakeMirror is an in-memory mirrorLookup. With anyOpenToDo set, every unknown
// task UUID resolves to an open to-do, for tests that exercise later stages.
type fakeMirror struct {
	tasks       map[string]*things.Task
	areas       map[string]*things.Area
	tags        map[string]*things.Tag
	checklists  map[string][]*things.CheckListItem
	anyOpenToDo bool
}

func (f fakeMirror) Task(uuid string) (*things.Task, error) {
	if t, ok := f.tasks[uuid]; ok {
		return t, nil
	}
	if f.anyOpenToDo {
		return &things.Task{UUID: uuid, Type: things.TaskTypeTask, Status: things.TaskStatusPending}, nil
	}
	return nil, nil
}

func (f fakeMirror) Area(uuid string) (*things.Area, error) { return f.areas[uuid], nil }
func (f fakeMirror) Tag(uuid string) (*things.Tag, error)   { return f.tags[uuid], nil }

func (f fakeMirror) AllTasks(sync.QueryOpts) ([]*things.Task, error) {
	var out []*things.Task
	for _, t := range f.tasks {
		if t.Type == things.TaskTypeTask {
			out = append(out, t)
		}
	}
	return out, nil
}

func (f fakeMirror) ChecklistItems(taskUUID string) ([]*things.CheckListItem, error) {
	return f.checklists[taskUUID], nil
}

func newKI10Mirror() fakeMirror {
	task := func(uuid string, tp things.TaskType, status things.TaskStatus, trashed bool) *things.Task {
		return &things.Task{UUID: uuid, Type: tp, Status: status, InTrash: trashed}
	}
	return fakeMirror{
		tasks: map[string]*things.Task{
			"open":      task("open", things.TaskTypeTask, things.TaskStatusPending, false),
			"completed": task("completed", things.TaskTypeTask, things.TaskStatusCompleted, false),
			"canceled":  task("canceled", things.TaskTypeTask, things.TaskStatusCanceled, false),
			"trashed":   task("trashed", things.TaskTypeTask, things.TaskStatusPending, true),
			"project":   task("project", things.TaskTypeProject, things.TaskStatusPending, false),
			"trashed-p": task("trashed-p", things.TaskTypeProject, things.TaskStatusPending, true),
			"heading":   task("heading", things.TaskTypeHeading, things.TaskStatusPending, false),
		},
		areas: map[string]*things.Area{"area": {UUID: "area"}},
		tags:  map[string]*things.Tag{"tag": {UUID: "tag"}},
		checklists: map[string][]*things.CheckListItem{
			"open": {
				{UUID: "item-open", Status: things.TaskStatusPending},
				{UUID: "item-done", Status: things.TaskStatusCompleted},
			},
		},
	}
}

func TestPreconditions_KI10_TaskTransitions(t *testing.T) {
	t.Parallel()

	m := newKI10Mirror()
	tests := []struct {
		name    string
		check   func(mirrorLookup, string) error
		uuid    string
		wantErr string
	}{
		{"complete open", checkComplete, "open", ""},
		{"complete unknown", checkComplete, "missing", "does not exist"},
		{"complete project", checkComplete, "project", "is a project, expected a to-do"},
		{"complete heading", checkComplete, "heading", "is a heading, expected a to-do"},
		{"complete completed", checkComplete, "completed", "already completed"},
		{"complete canceled", checkComplete, "canceled", "is canceled"},
		{"complete trashed", checkComplete, "trashed", "in the trash"},
		{"uncomplete completed", checkUncomplete, "completed", ""},
		{"uncomplete canceled", checkUncomplete, "canceled", ""},
		{"uncomplete open", checkUncomplete, "open", "not completed or canceled"},
		{"trash open", checkTrash, "open", ""},
		{"trash trashed", checkTrash, "trashed", "to-do trashed is already in the trash"},
		{"trash project", checkTrash, "project", ""},
		{"trash heading", checkTrash, "heading", ""},
		{"trash unknown", checkTrash, "missing", "item missing does not exist"},
		{"trash trashed project", checkTrash, "trashed-p", "project trashed-p is already in the trash"},
		{"untrash trashed", checkUntrash, "trashed", ""},
		{"untrash trashed project", checkUntrash, "trashed-p", ""},
		{"untrash open", checkUntrash, "open", "not in the trash"},
		{"move open", func(m mirrorLookup, id string) error { _, err := requireActiveToDo(m, id); return err }, "open", ""},
		{"move completed", func(m mirrorLookup, id string) error { _, err := requireActiveToDo(m, id); return err }, "completed", ""},
		{"move trashed", func(m mirrorLookup, id string) error { _, err := requireActiveToDo(m, id); return err }, "trashed", "in the trash"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assertPrecondition(t, tt.check(m, tt.uuid), tt.wantErr)
		})
	}
}

func TestPreconditions_KI10_References(t *testing.T) {
	t.Parallel()

	m := newKI10Mirror()
	tests := []struct {
		name    string
		refs    taskRefs
		wantErr string
	}{
		{"none", taskRefs{}, ""},
		{"all valid", taskRefs{Project: "project", ParentTask: "open", Area: "area", Tags: []string{"tag"}}, ""},
		{"project is a to-do", taskRefs{Project: "open"}, "is a to-do, expected a project"},
		{"project missing", taskRefs{Project: "missing"}, "project missing does not exist"},
		{"project literal none", taskRefs{Project: "none"}, "project none does not exist"},
		{"project trashed", taskRefs{Project: "trashed-p"}, "project trashed-p is in the trash"},
		{"parent is a project", taskRefs{ParentTask: "project"}, "is a project, expected a to-do"},
		{"parent trashed", taskRefs{ParentTask: "trashed"}, "parent_task trashed is in the trash"},
		{"area missing", taskRefs{Area: "missing"}, "area missing does not exist"},
		{"tag missing", taskRefs{Tags: []string{"tag", "missing"}}, "tag missing does not exist"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assertPrecondition(t, checkRefs(m, tt.refs), tt.wantErr)
		})
	}
}

func TestPreconditions_KI10_ChecklistItems(t *testing.T) {
	t.Parallel()

	m := newKI10Mirror()
	tests := []struct {
		name    string
		uuid    string
		want    things.TaskStatus
		wantErr string
	}{
		{"complete open item", "item-open", things.TaskStatusCompleted, ""},
		{"complete done item", "item-done", things.TaskStatusCompleted, "already completed"},
		{"reopen done item", "item-done", things.TaskStatusPending, ""},
		{"reopen open item", "item-open", things.TaskStatusPending, "not completed"},
		{"unknown item", "missing", things.TaskStatusCompleted, "checklist item missing does not exist"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assertPrecondition(t, checkChecklistStatus(m, tt.uuid, tt.want), tt.wantErr)
		})
	}
}

// TestCompleteTask_KI10_UnknownUUIDSendsNothing checks that a failed
// precondition stops a write before any request reaches Things Cloud.
func TestCompleteTask_KI10_UnknownUUIDSendsNothing(t *testing.T) {
	prevTransport, prevMirror := http.DefaultTransport, writeMirror
	t.Cleanup(func() { http.DefaultTransport, writeMirror = prevTransport, prevMirror })
	http.DefaultTransport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		t.Errorf("unexpected request to %s", r.URL.Host)
		return nil, errors.New("no network in this test")
	})
	writeMirror = func() (mirrorLookup, error) { return newKI10Mirror(), nil }

	cases := map[string]func() error{
		"complete": func() error { return completeTask(generateUUID()) },
		"edit":     func() error { return editTask(EditTaskRequest{UUID: generateUUID(), Title: "x"}) },
		"create in missing project": func() error {
			_, err := createTask(CreateTaskRequest{Title: "x", Project: generateUUID()})
			return err
		},
		"checklist item": func() error { return deleteChecklistItem(generateUUID()) },
	}
	for name, run := range cases {
		err := run()
		if !isInvalidInput(err) || !strings.Contains(err.Error(), "does not exist") {
			t.Errorf("%s: error = %v, want an invalid-input 'does not exist' error", name, err)
		}
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func assertPrecondition(t *testing.T, err error, wantErr string) {
	t.Helper()
	if wantErr == "" {
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		return
	}
	if err == nil || !strings.Contains(err.Error(), wantErr) {
		t.Fatalf("error = %v, want one containing %q", err, wantErr)
	}
	if !isInvalidInput(err) {
		t.Errorf("error %v is not an invalid-input error (REST would answer 500, not 400)", err)
	}
}
