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

// mirrorReader is the part of the SDK state the read model queries.
// *sync.State implements it; tests use a fake.
type mirrorReader interface {
	Task(uuid string) (*things.Task, error)
	AllTasks(opts sync.QueryOpts) ([]*things.Task, error)
	AllProjects(opts sync.QueryOpts) ([]*things.Task, error)
	AllAreas() ([]*things.Area, error)
	AllTags() ([]*things.Tag, error)
	TasksInProject(projectUUID string, opts sync.QueryOpts) ([]*things.Task, error)
	TasksInArea(areaUUID string, opts sync.QueryOpts) ([]*things.Task, error)
}

// readModel wraps the SDK state with the list queries the server exposes.
type readModel struct {
	mirror mirrorReader
}

func readState() readModel {
	return readModel{mirror: syncer.State()}
}

// neverYear starts the years of Things' "never" sentinel: it stores 4001-01-01
// (64092211200) where a day value means "no date" (docs/protocol/things-cloud.md §4, KI-04).
const neverYear = 4000

// calendarDay returns nil for a day value that holds the "never" sentinel.
func calendarDay(d *time.Time) *time.Time {
	if d == nil || d.Year() >= neverYear {
		return nil
	}
	return d
}

// clearSentinels turns sentinel start dates and deadlines into "no date", so
// no list, view rule or output sees 4001-01-01 (KI-04). The completion date is
// an instant, not a day value, and is kept. The SDK builds a fresh Task per
// read, so changing it in place is safe.
func clearSentinels(t *things.Task) *things.Task {
	t.ScheduledDate = calendarDay(t.ScheduledDate)
	t.DeadlineDate = calendarDay(t.DeadlineDate)
	return t
}

func withoutSentinels(tasks []*things.Task, err error) ([]*things.Task, error) {
	if err != nil {
		return nil, err
	}
	for _, t := range tasks {
		clearSentinels(t)
	}
	return tasks, nil
}

func (r readModel) Task(uuid string) (*things.Task, error) {
	t, err := r.mirror.Task(uuid)
	if err != nil || t == nil {
		return t, err
	}
	return clearSentinels(t), nil
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
	tasks, err := withoutSentinels(r.mirror.AllTasks(opts.QueryOpts))
	return openList(tasks, err, opts)
}

func (r readModel) AllProjects(opts listOpts) ([]*things.Task, error) {
	tasks, err := withoutSentinels(r.mirror.AllProjects(opts.QueryOpts))
	return openList(tasks, err, opts)
}

func (r readModel) AllAreasWithOpts(opts listOpts) ([]*things.Area, error) {
	areas, err := r.mirror.AllAreas()
	return paged(areas, err, opts)
}

func (r readModel) AllTagsWithOpts(opts listOpts) ([]*things.Tag, error) {
	tags, err := r.mirror.AllTags()
	return paged(tags, err, opts)
}

// tasksInView classifies every to-do locally instead of using the SDK's
// TasksIn* queries, which drop overdue and arrived-but-unconfirmed items from
// every list (spike report §4 B3). "Today" is the owner's day (ADR-0003).
func (r readModel) tasksInView(view listView, opts listOpts) ([]*things.Task, error) {
	tasks, err := withoutSentinels(r.mirror.AllTasks(opts.QueryOpts))
	if err != nil {
		return nil, err
	}
	tasks = tasksInView(tasks, view, ownerToday())
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
	tasks, err := withoutSentinels(r.mirror.TasksInProject(projectUUID, opts.QueryOpts))
	return openList(tasks, err, opts)
}

func (r readModel) TasksInArea(areaUUID string, opts listOpts) ([]*things.Task, error) {
	tasks, err := withoutSentinels(r.mirror.TasksInArea(areaUUID, opts.QueryOpts))
	return openList(tasks, err, opts)
}

// CompletedTasksInRange returns completed to-dos, most recent first.
// completedAfter is inclusive, completedBefore is exclusive.
func (r readModel) CompletedTasksInRange(limit int, completedAfter, completedBefore *time.Time) ([]*things.Task, error) {
	tasks, err := withoutSentinels(r.mirror.AllTasks(sync.QueryOpts{IncludeCompleted: true}))
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
