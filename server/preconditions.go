package main

import (
	"fmt"
	"strings"

	things "github.com/arthursoares/things-cloud-sdk"
	"github.com/arthursoares/things-cloud-sdk/sync"
)

// mirrorLookup is the part of the SDK state the write preconditions read
// (ADR-0004 §3, KI-10). *sync.State implements it.
type mirrorLookup interface {
	Task(uuid string) (*things.Task, error)
	Area(uuid string) (*things.Area, error)
	Tag(uuid string) (*things.Tag, error)
	AllTasks(opts sync.QueryOpts) ([]*things.Task, error)
	ChecklistItems(taskUUID string) ([]*things.CheckListItem, error)
}

// writeMirror syncs and returns the mirror the preconditions check, so a
// write is judged against the account as it is now, not as of the last read.
// Tests replace it.
var writeMirror = func() (mirrorLookup, error) {
	if err := syncForRead(); err != nil {
		return nil, fmt.Errorf("sync before write failed: %w", err)
	}
	return syncer.State(), nil
}

// precheck runs one write's preconditions against a fresh mirror before any
// byte is sent (ADR-0004 §3).
func precheck(check func(mirrorLookup) error) error {
	m, err := writeMirror()
	if err != nil {
		return err
	}
	return check(m)
}

func taskTypeName(t things.TaskType) string {
	switch t {
	case things.TaskTypeTask:
		return "a to-do"
	case things.TaskTypeProject:
		return "a project"
	case things.TaskTypeHeading:
		return "a heading"
	}
	return fmt.Sprintf("an item of unknown type %d", t)
}

// lookupTask resolves a task-like object and checks its type. The SDK hides
// deleted objects, so a missing one is either deleted or a wrong UUID.
func lookupTask(m mirrorLookup, field, uuid string, want things.TaskType) (*things.Task, error) {
	t, err := m.Task(uuid)
	if err != nil {
		return nil, fmt.Errorf("look up %s %s: %w", field, uuid, err)
	}
	if t == nil {
		return nil, invalidInputf("%s %s does not exist (deleted, or a wrong UUID); list items again to get a current UUID (KI-10)", field, uuid)
	}
	if t.Type != want {
		return nil, invalidInputf("%s %s is %s, expected %s (KI-10)", field, uuid, taskTypeName(t.Type), taskTypeName(want))
	}
	return t, nil
}

// requireToDo resolves the to-do a *_task tool changes. Only to-dos are
// accepted: these tools' payloads are verified for to-dos, not for projects
// or headings (AGENTS.md rule 2).
func requireToDo(m mirrorLookup, uuid string) (*things.Task, error) {
	return lookupTask(m, "to-do", uuid, things.TaskTypeTask)
}

// requireActiveToDo additionally refuses to-dos in the trash.
func requireActiveToDo(m mirrorLookup, uuid string) (*things.Task, error) {
	t, err := requireToDo(m, uuid)
	if err != nil {
		return nil, err
	}
	if t.InTrash {
		return nil, invalidInputf("to-do %s is in the trash; restore it with things_untrash_task first (KI-10)", uuid)
	}
	return t, nil
}

func checkComplete(m mirrorLookup, uuid string) error {
	t, err := requireActiveToDo(m, uuid)
	if err != nil {
		return err
	}
	switch t.Status {
	case things.TaskStatusCompleted:
		return invalidInputf("to-do %s is already completed (KI-10)", uuid)
	case things.TaskStatusCanceled:
		return invalidInputf("to-do %s is canceled; reopen it with things_uncomplete_task first (KI-10)", uuid)
	}
	return nil
}

func checkUncomplete(m mirrorLookup, uuid string) error {
	t, err := requireActiveToDo(m, uuid)
	if err != nil {
		return err
	}
	if t.Status != things.TaskStatusCompleted && t.Status != things.TaskStatusCanceled {
		return invalidInputf("to-do %s is not completed or canceled (KI-10)", uuid)
	}
	return nil
}

// requireTrashable resolves the target of trash/untrash. The trash flag `tr`
// is verified for every task type (docs/protocol/things-cloud.md §5), so
// projects and headings are accepted here, unlike in the other *_task tools.
func requireTrashable(m mirrorLookup, uuid string) (*things.Task, error) {
	t, err := m.Task(uuid)
	if err != nil {
		return nil, fmt.Errorf("look up item %s: %w", uuid, err)
	}
	if t == nil {
		return nil, invalidInputf("item %s does not exist (deleted, or a wrong UUID); list items again to get a current UUID (KI-10)", uuid)
	}
	return t, nil
}

func checkTrash(m mirrorLookup, uuid string) error {
	t, err := requireTrashable(m, uuid)
	if err != nil {
		return err
	}
	if t.InTrash {
		return invalidInputf("%s %s is already in the trash (KI-10)", strings.TrimPrefix(taskTypeName(t.Type), "a "), uuid)
	}
	return nil
}

func checkUntrash(m mirrorLookup, uuid string) error {
	t, err := requireTrashable(m, uuid)
	if err != nil {
		return err
	}
	if !t.InTrash {
		return invalidInputf("%s %s is not in the trash (KI-10)", strings.TrimPrefix(taskTypeName(t.Type), "a "), uuid)
	}
	return nil
}

// taskRefs are the objects a create or edit links to; empty fields are not
// referenced.
type taskRefs struct {
	Project    string
	ParentTask string
	Area       string
	Tags       []string
}

// checkRefs resolves every referenced object. A project or parent to-do in
// the trash is refused too: Things would show the new link under a trashed
// container.
func checkRefs(m mirrorLookup, refs taskRefs) error {
	if refs.Project != "" {
		p, err := lookupTask(m, "project", refs.Project, things.TaskTypeProject)
		if err != nil {
			return err
		}
		if p.InTrash {
			return invalidInputf("project %s is in the trash (KI-10)", refs.Project)
		}
	}
	if refs.ParentTask != "" {
		p, err := lookupTask(m, "parent_task", refs.ParentTask, things.TaskTypeTask)
		if err != nil {
			return err
		}
		if p.InTrash {
			return invalidInputf("parent_task %s is in the trash (KI-10)", refs.ParentTask)
		}
	}
	if refs.Area != "" {
		a, err := m.Area(refs.Area)
		if err != nil {
			return fmt.Errorf("look up area %s: %w", refs.Area, err)
		}
		if a == nil {
			return invalidInputf("area %s does not exist (deleted, or a wrong UUID); use things_list_areas (KI-10)", refs.Area)
		}
	}
	for _, id := range refs.Tags {
		tag, err := m.Tag(id)
		if err != nil {
			return fmt.Errorf("look up tag %s: %w", id, err)
		}
		if tag == nil {
			return invalidInputf("tag %s does not exist (deleted, or a wrong UUID); use things_list_tags (KI-10)", id)
		}
	}
	return nil
}

// findChecklistItem looks a checklist item up by its own UUID. The SDK can
// only list items per to-do, so this scans every to-do, trashed and closed
// ones included; it runs only for checklist writes.
func findChecklistItem(m mirrorLookup, uuid string) (*things.CheckListItem, error) {
	tasks, err := m.AllTasks(sync.QueryOpts{IncludeCompleted: true, IncludeTrashed: true})
	if err != nil {
		return nil, fmt.Errorf("look up checklist item %s: %w", uuid, err)
	}
	for _, t := range tasks {
		items, err := m.ChecklistItems(t.UUID)
		if err != nil {
			return nil, fmt.Errorf("look up checklist item %s: %w", uuid, err)
		}
		for _, item := range items {
			if item.UUID == uuid {
				return item, nil
			}
		}
	}
	return nil, invalidInputf("checklist item %s does not exist (deleted, or a wrong UUID); use things_list_checklist_items (KI-10)", uuid)
}

func checkChecklistStatus(m mirrorLookup, uuid string, want things.TaskStatus) error {
	item, err := findChecklistItem(m, uuid)
	if err != nil {
		return err
	}
	switch want {
	case things.TaskStatusCompleted:
		if item.Status == things.TaskStatusCompleted {
			return invalidInputf("checklist item %s is already completed (KI-10)", uuid)
		}
	case things.TaskStatusPending:
		if item.Status == things.TaskStatusPending {
			return invalidInputf("checklist item %s is not completed (KI-10)", uuid)
		}
	}
	return nil
}
