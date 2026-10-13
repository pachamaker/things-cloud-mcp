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

// newKI01Mirror holds a repeating to-do template, an open, a completed and a
// trashed instance of it, a repeating project template and an instance of
// it, each with a checklist where a to-do has one. IDs are canonical, so
// the write functions get past input validation.
func newKI01Mirror() (fakeMirror, map[string]string) {
	ids := map[string]string{}
	for _, name := range []string{"template", "instance", "instance-done", "instance-trashed", "project-template", "project-instance", "item-template", "item-instance"} {
		ids[name] = generateUUID()
	}
	rule := &things.RepeaterConfiguration{FrequencyUnit: things.FrequencyUnit(256)}
	todo := func(name string, change func(*things.Task)) *things.Task {
		t := &things.Task{UUID: ids[name], Type: things.TaskTypeTask, Status: things.TaskStatusPending, RecurrenceIDs: []string{ids["template"]}}
		if change != nil {
			change(t)
		}
		return t
	}
	m := newKI10Mirror()
	for name, t := range map[string]*things.Task{
		"template":         todo("template", func(t *things.Task) { t.Repeater, t.RecurrenceIDs = rule, nil }),
		"instance":         todo("instance", nil),
		"instance-done":    todo("instance-done", func(t *things.Task) { t.Status = things.TaskStatusCompleted }),
		"instance-trashed": todo("instance-trashed", func(t *things.Task) { t.InTrash = true }),
		"project-template": {UUID: ids["project-template"], Type: things.TaskTypeProject, Repeater: rule},
		"project-instance": {UUID: ids["project-instance"], Type: things.TaskTypeProject, RecurrenceIDs: []string{ids["project-template"]}},
	} {
		m.tasks[ids[name]] = t
	}
	m.checklists[ids["template"]] = []*things.CheckListItem{{UUID: ids["item-template"], Status: things.TaskStatusPending}}
	m.checklists[ids["instance"]] = []*things.CheckListItem{{UUID: ids["item-instance"], Status: things.TaskStatusPending}}
	return m, ids
}

// TestPreconditions_KI01_RepeatingTargetsRefused: no write changes a
// repeating template or one of its instances, whatever its status, and
// nothing is linked to a hidden template. An instance may still contain new
// items, like any visible project or to-do.
func TestPreconditions_KI01_RepeatingTargetsRefused(t *testing.T) {
	t.Parallel()

	m, ids := newKI01Mirror()
	active := func(m mirrorLookup, id string) error { _, err := requireActiveToDo(m, id); return err }
	checklist := func(want things.TaskStatus) func(mirrorLookup, string) error {
		return func(m mirrorLookup, id string) error { return checkChecklistStatus(m, id, want) }
	}
	deleteItem := func(m mirrorLookup, id string) error { _, err := findChecklistItem(m, id); return err }
	refs := func(r func(string) taskRefs) func(mirrorLookup, string) error {
		return func(m mirrorLookup, id string) error { return checkRefs(m, r(id)) }
	}
	project := refs(func(id string) taskRefs { return taskRefs{Project: id} })
	parent := refs(func(id string) taskRefs { return taskRefs{ParentTask: id} })

	const template, instance = "hidden template of a repeating", "generated by the repeating"
	tests := []struct {
		name    string
		check   func(mirrorLookup, string) error
		target  string
		wantErr string
	}{
		{"complete template", checkComplete, "template", template + " to-do"},
		{"complete instance", checkComplete, "instance", instance + " to-do " + ids["template"]},
		{"uncomplete instance", checkUncomplete, "instance-done", instance},
		{"edit or move template", active, "template", template},
		{"edit or move instance", active, "instance", instance},
		{"edit or move trashed instance", active, "instance-trashed", instance},
		{"trash template", checkTrash, "template", template},
		{"trash instance", checkTrash, "instance", instance},
		{"trash project template", checkTrash, "project-template", template + " project"},
		{"trash project instance", checkTrash, "project-instance", instance + " project"},
		{"untrash instance", checkUntrash, "instance-trashed", instance},
		{"add checklist item to template", active, "template", template},
		{"add checklist item to instance", active, "instance", instance},
		{"complete template's checklist item", checklist(things.TaskStatusCompleted), "item-template", "belongs to to-do " + ids["template"] + ", the " + template},
		{"complete instance's checklist item", checklist(things.TaskStatusCompleted), "item-instance", instance},
		{"delete instance's checklist item", deleteItem, "item-instance", instance},
		{"project is a template", project, "project-template", template + " project"},
		{"parent is a template", parent, "template", template + " to-do"},
		{"project is an instance", project, "project-instance", ""},
		{"parent is an instance", parent, "instance", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := tt.check(m, ids[tt.target])
			assertPrecondition(t, err, tt.wantErr)
			if tt.wantErr != "" && !strings.Contains(err.Error(), "KI-") {
				t.Errorf("error %q does not name its KI", err)
			}
		})
	}
}

// TestWrites_KI01_RepeatingSendsNothing checks that a refused write to a
// repeating item stops before any request reaches Things Cloud.
func TestWrites_KI01_RepeatingSendsNothing(t *testing.T) {
	prevTransport, prevMirror := http.DefaultTransport, writeMirror
	t.Cleanup(func() { http.DefaultTransport, writeMirror = prevTransport, prevMirror })
	http.DefaultTransport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		t.Errorf("unexpected request to %s", r.URL.Host)
		return nil, errors.New("no network in this test")
	})
	m, ids := newKI01Mirror()
	writeMirror = func() (mirrorLookup, error) { return m, nil }

	cases := map[string]func() error{
		"complete template":   func() error { return completeTask(ids["template"]) },
		"complete instance":   func() error { return completeTask(ids["instance"]) },
		"uncomplete instance": func() error { return uncompleteTask(ids["instance-done"]) },
		"edit instance":       func() error { return editTask(EditTaskRequest{UUID: ids["instance"], Title: "x"}) },
		"today instance":      func() error { return moveTaskToToday(ids["instance"]) },
		"someday template":    func() error { return moveTaskToSomeday(ids["template"]) },
		"trash project":       func() error { return trashTask(ids["project-template"]) },
		"untrash instance":    func() error { return untrashTask(ids["instance-trashed"]) },
		"checklist add":       func() error { _, err := createChecklistItem("x", ids["instance"]); return err },
		"checklist complete":  func() error { return completeChecklistItem(ids["item-instance"]) },
		"checklist delete":    func() error { return deleteChecklistItem(ids["item-template"]) },
		"create in template": func() error {
			_, err := createTask(CreateTaskRequest{Title: "x", Project: ids["project-template"]})
			return err
		},
	}
	for name, run := range cases {
		err := run()
		if !isInvalidInput(err) || !strings.Contains(err.Error(), "repeating") {
			t.Errorf("%s: error = %v, want an invalid-input error about the repeating item", name, err)
		}
	}
}
