package main

import (
	"sort"
	"time"

	things "github.com/arthursoares/things-cloud-sdk"
	"github.com/arthursoares/things-cloud-sdk/sync"
)

// listOpts adds pagination to the SDK query options; the SDK's QueryOpts has
// no Limit/Offset.
type listOpts struct {
	sync.QueryOpts
	Limit  int
	Offset int
}

// readModel wraps the SDK state with the list queries the server exposes.
// Methods not redefined here are the SDK's own (Task, Area, Tag, ChecklistItems).
type readModel struct {
	*sync.State
}

func readState() readModel {
	return readModel{State: syncer.State()}
}

func paginate[T any](items []T, opts listOpts) []T {
	if opts.Offset >= len(items) {
		return nil
	}
	items = items[opts.Offset:]
	if opts.Limit > 0 && opts.Limit < len(items) {
		items = items[:opts.Limit]
	}
	return items
}

// openList drops canceled items unless closed ones were asked for: the SDK's
// IncludeCompleted gate only excludes completed (status 3), Things hides both
// from open lists (KI-05).
func openList(tasks []*things.Task, err error, opts listOpts) ([]*things.Task, error) {
	if err != nil {
		return nil, err
	}
	if opts.IncludeCompleted {
		return paginate(tasks, opts), nil
	}
	open := tasks[:0:0]
	for _, t := range tasks {
		if !isClosed(t) {
			open = append(open, t)
		}
	}
	return paginate(open, opts), nil
}

func paged[T any](items []T, err error, opts listOpts) ([]T, error) {
	if err != nil {
		return nil, err
	}
	return paginate(items, opts), nil
}

func (r readModel) AllTasks(opts listOpts) ([]*things.Task, error) {
	tasks, err := r.State.AllTasks(opts.QueryOpts)
	return openList(tasks, err, opts)
}

func (r readModel) AllProjects(opts listOpts) ([]*things.Task, error) {
	tasks, err := r.State.AllProjects(opts.QueryOpts)
	return openList(tasks, err, opts)
}

func (r readModel) AllAreasWithOpts(opts listOpts) ([]*things.Area, error) {
	areas, err := r.State.AllAreas()
	return paged(areas, err, opts)
}

func (r readModel) AllTagsWithOpts(opts listOpts) ([]*things.Tag, error) {
	tags, err := r.State.AllTags()
	return paged(tags, err, opts)
}

// tasksInView classifies every to-do locally instead of using the SDK's
// TasksIn* queries, which drop overdue and arrived-but-unconfirmed items from
// every list (spike report §4 B3). "Today" is the UTC day until KI-03.
func (r readModel) tasksInView(view listView, opts listOpts) ([]*things.Task, error) {
	tasks, err := r.State.AllTasks(opts.QueryOpts)
	if err != nil {
		return nil, err
	}
	tasks = tasksInView(tasks, view, time.Unix(todayMidnightUTC(), 0).UTC())
	return openList(tasks, nil, opts)
}

func (r readModel) TasksInInbox(opts listOpts) ([]*things.Task, error) {
	return r.tasksInView(viewInbox, opts)
}

func (r readModel) TasksInToday(opts listOpts) ([]*things.Task, error) {
	return r.tasksInView(viewToday, opts)
}

func (r readModel) TasksInAnytime(opts listOpts) ([]*things.Task, error) {
	return r.tasksInView(viewAnytime, opts)
}

func (r readModel) TasksInSomeday(opts listOpts) ([]*things.Task, error) {
	return r.tasksInView(viewSomeday, opts)
}

func (r readModel) TasksInUpcoming(opts listOpts) ([]*things.Task, error) {
	return r.tasksInView(viewUpcoming, opts)
}

func (r readModel) TasksInProject(projectUUID string, opts listOpts) ([]*things.Task, error) {
	tasks, err := r.State.TasksInProject(projectUUID, opts.QueryOpts)
	return openList(tasks, err, opts)
}

func (r readModel) TasksInArea(areaUUID string, opts listOpts) ([]*things.Task, error) {
	tasks, err := r.State.TasksInArea(areaUUID, opts.QueryOpts)
	return openList(tasks, err, opts)
}

// CompletedTasksInRange returns completed to-dos, most recent first.
// completedAfter is inclusive, completedBefore is exclusive.
func (r readModel) CompletedTasksInRange(limit int, completedAfter, completedBefore *time.Time) ([]*things.Task, error) {
	tasks, err := r.State.AllTasks(sync.QueryOpts{IncludeCompleted: true})
	if err != nil {
		return nil, err
	}
	return completedInRange(tasks, limit, completedAfter, completedBefore), nil
}

func completedInRange(tasks []*things.Task, limit int, completedAfter, completedBefore *time.Time) []*things.Task {
	if limit <= 0 {
		limit = 50
	}
	var out []*things.Task
	for _, t := range tasks {
		if t.Status != things.TaskStatusCompleted || t.CompletionDate == nil {
			continue
		}
		done := t.CompletionDate.Unix()
		if completedAfter != nil && done < completedAfter.Unix() {
			continue
		}
		if completedBefore != nil && done >= completedBefore.Unix() {
			continue
		}
		out = append(out, t)
	}
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].CompletionDate.After(*out[j].CompletionDate)
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}
