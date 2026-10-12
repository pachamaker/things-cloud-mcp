package main

import (
	"reflect"
	"slices"
	"sort"
	"testing"
	"time"

	things "github.com/arthursoares/things-cloud-sdk"
	"github.com/arthursoares/things-cloud-sdk/sync"
)

func TestPaginate(t *testing.T) {
	t.Parallel()

	items := []int{1, 2, 3, 4, 5}
	tests := []struct {
		name string
		opts listOpts
		want []int
	}{
		{"no limit no offset", listOpts{}, []int{1, 2, 3, 4, 5}},
		{"limit only", listOpts{Limit: 2}, []int{1, 2}},
		{"offset only", listOpts{Offset: 3}, []int{4, 5}},
		{"limit and offset", listOpts{Limit: 2, Offset: 1}, []int{2, 3}},
		{"limit past end", listOpts{Limit: 10, Offset: 4}, []int{5}},
		{"offset at end", listOpts{Offset: 5}, nil},
		{"offset past end", listOpts{Limit: 1, Offset: 9}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := paginate(items, tt.opts); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("paginate(%+v) = %v, want %v", tt.opts, got, tt.want)
			}
		})
	}
}

func TestCompletedInRange(t *testing.T) {
	t.Parallel()

	at := func(day int) *time.Time {
		v := time.Date(2026, 3, day, 12, 0, 0, 0, time.UTC)
		return &v
	}
	task := func(uuid string, status things.TaskStatus, done *time.Time) *things.Task {
		return &things.Task{UUID: uuid, Status: status, CompletionDate: done}
	}
	tasks := []*things.Task{
		task("open", things.TaskStatusPending, nil),
		task("canceled", things.TaskStatusCanceled, at(4)),
		task("no-date", things.TaskStatusCompleted, nil),
		task("d2", things.TaskStatusCompleted, at(2)),
		task("d5", things.TaskStatusCompleted, at(5)),
		task("d3", things.TaskStatusCompleted, at(3)),
	}
	uuids := func(ts []*things.Task) []string {
		var out []string
		for _, t := range ts {
			out = append(out, t.UUID)
		}
		return out
	}

	tests := []struct {
		name          string
		limit         int
		after, before *time.Time
		want          []string
	}{
		{"all completed, newest first", 0, nil, nil, []string{"d5", "d3", "d2"}},
		{"limit", 2, nil, nil, []string{"d5", "d3"}},
		{"after is inclusive", 0, at(3), nil, []string{"d5", "d3"}},
		{"before is exclusive", 0, nil, at(3), []string{"d2"}},
		{"window", 0, at(2), at(5), []string{"d3", "d2"}},
		{"empty window", 0, at(6), nil, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := uuids(completedInRange(tasks, tt.limit, tt.after, tt.before))
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("completedInRange = %v, want %v", got, tt.want)
			}
		})
	}
}

// listMirror answers the SDK state queries the read model makes from a fixed
// item list, with the SDK's status/trash filters and "index" order. Like the
// SDK, it builds a fresh Task for every row, so callers may change the result.
type listMirror []things.Task

func firstID(ids []string) string {
	if len(ids) == 0 {
		return ""
	}
	return ids[0]
}

func (f listMirror) query(tp things.TaskType, opts sync.QueryOpts, keep func(*things.Task) bool) ([]*things.Task, error) {
	var out []*things.Task
	for i := range f {
		it := f[i]
		if it.Type != tp || (!opts.IncludeCompleted && it.Status == things.TaskStatusCompleted) || (!opts.IncludeTrashed && it.InTrash) {
			continue
		}
		if keep == nil || keep(&it) {
			out = append(out, &it)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Index < out[j].Index })
	return out, nil
}

func (f listMirror) Task(uuid string) (*things.Task, error) {
	for i := range f {
		if f[i].UUID == uuid {
			it := f[i]
			return &it, nil
		}
	}
	return nil, nil
}

func (f listMirror) AllTasks(opts sync.QueryOpts) ([]*things.Task, error) {
	return f.query(things.TaskTypeTask, opts, nil)
}

func (f listMirror) AllProjects(opts sync.QueryOpts) ([]*things.Task, error) {
	return f.query(things.TaskTypeProject, opts, nil)
}

func (f listMirror) AllAreas() ([]*things.Area, error) { return nil, nil }

func (f listMirror) AllTags() ([]*things.Tag, error) { return nil, nil }

func (f listMirror) TasksInProject(projectUUID string, opts sync.QueryOpts) ([]*things.Task, error) {
	return f.query(things.TaskTypeTask, opts, func(t *things.Task) bool { return firstID(t.ParentTaskIDs) == projectUUID })
}

func (f listMirror) TasksInArea(areaUUID string, opts sync.QueryOpts) ([]*things.Task, error) {
	return f.query(things.TaskTypeTask, opts, func(t *things.Task) bool { return firstID(t.AreaIDs) == areaUUID })
}

func (f listMirror) HeadingsInProject(projectUUID string, opts sync.QueryOpts) ([]*things.Task, error) {
	return f.query(things.TaskTypeHeading, opts, func(t *things.Task) bool { return firstID(t.ParentTaskIDs) == projectUUID })
}

func (f listMirror) TasksUnderHeading(headingUUID string, opts sync.QueryOpts) ([]*things.Task, error) {
	return f.query(things.TaskTypeTask, opts, func(t *things.Task) bool { return firstID(t.ActionGroupIDs) == headingUUID })
}

func dayAt(y int, m time.Month, d int) *time.Time {
	v := time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
	return &v
}

func dayString(d *time.Time) string {
	if d == nil {
		return ""
	}
	return d.Format("2006-01-02")
}

func TestClearSentinels_KI04(t *testing.T) {
	t.Parallel()

	done := time.Date(2026, 10, 9, 18, 30, 5, 0, time.UTC)
	tests := []struct {
		name                    string
		task                    things.Task
		wantScheduled, wantDead string
		wantCompleted           *time.Time
	}{
		{"deadline sentinel", things.Task{DeadlineDate: dayAt(4001, time.January, 1)}, "", "", nil},
		{"deadline in year 4000", things.Task{DeadlineDate: dayAt(4000, time.January, 1)}, "", "", nil},
		{"deadline on the last real day", things.Task{DeadlineDate: dayAt(3999, time.December, 31)}, "", "3999-12-31", nil},
		{"start date sentinel", things.Task{ScheduledDate: dayAt(4001, time.January, 1)}, "", "", nil},
		{"real start date and sentinel deadline", things.Task{ScheduledDate: dayAt(2026, time.October, 11), DeadlineDate: dayAt(4001, time.January, 1)}, "2026-10-11", "", nil},
		{"completed with sentinel deadline keeps its completion instant", things.Task{Status: things.TaskStatusCompleted, CompletionDate: &done, DeadlineDate: dayAt(4001, time.January, 1)}, "", "", &done},
		{"no dates", things.Task{}, "", "", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			task := tt.task
			got := clearSentinels(&task)
			if s := dayString(got.ScheduledDate); s != tt.wantScheduled {
				t.Errorf("start date = %q, want %q", s, tt.wantScheduled)
			}
			if d := dayString(got.DeadlineDate); d != tt.wantDead {
				t.Errorf("deadline = %q, want %q", d, tt.wantDead)
			}
			if !reflect.DeepEqual(got.CompletionDate, tt.wantCompleted) {
				t.Errorf("completion = %v, want %v", got.CompletionDate, tt.wantCompleted)
			}
		})
	}
}

// sentinelMirror holds one item per list, each with the year-4001 sentinel in
// a day field. Real dates are far enough from any test run's "today" to keep
// every item in the same view.
func sentinelMirror() listMirror {
	never := dayAt(4001, time.January, 1)
	done := time.Date(2026, 10, 9, 18, 30, 5, 0, time.UTC)
	return listMirror{
		{UUID: "inbox", Type: things.TaskTypeTask, Schedule: things.TaskScheduleInbox, DeadlineDate: never},
		{UUID: "today", Type: things.TaskTypeTask, Schedule: things.TaskScheduleAnytime, ScheduledDate: dayAt(2026, time.January, 1), DeadlineDate: never},
		{UUID: "anytime", Type: things.TaskTypeTask, Schedule: things.TaskScheduleAnytime, DeadlineDate: never, AreaIDs: []string{"area"}, ParentTaskIDs: []string{"project"}},
		{UUID: "someday-never", Type: things.TaskTypeTask, Schedule: things.TaskScheduleSomeday, ScheduledDate: never},
		{UUID: "upcoming", Type: things.TaskTypeTask, Schedule: things.TaskScheduleSomeday, ScheduledDate: dayAt(3000, time.January, 1), DeadlineDate: never},
		{UUID: "done", Type: things.TaskTypeTask, Status: things.TaskStatusCompleted, CompletionDate: &done, DeadlineDate: never},
		{UUID: "project", Type: things.TaskTypeProject, Schedule: things.TaskScheduleAnytime, DeadlineDate: never},
	}
}

func TestReadModel_KI04_NoSentinelInAnyList(t *testing.T) {
	t.Parallel()

	r := readModel{mirror: sentinelMirror()}
	single := func(uuid string) func() ([]*things.Task, error) {
		return func() ([]*things.Task, error) {
			task, err := r.Task(uuid)
			return []*things.Task{task}, err
		}
	}
	project := func() ([]*things.Task, error) {
		tasks, _, err := r.TasksInProject("project", listOpts{})
		return tasks, err
	}
	withDone := listOpts{QueryOpts: sync.QueryOpts{IncludeCompleted: true}}
	lists := map[string]func() ([]*things.Task, error){
		"all tasks":           func() ([]*things.Task, error) { return r.AllTasks(listOpts{}) },
		"all tasks with done": func() ([]*things.Task, error) { return r.AllTasks(withDone) },
		"projects":            func() ([]*things.Task, error) { return r.AllProjects(listOpts{}) },
		"inbox":               func() ([]*things.Task, error) { return r.TasksInInbox(listOpts{}) },
		"today":               func() ([]*things.Task, error) { return r.TasksInToday(listOpts{}) },
		"anytime":             func() ([]*things.Task, error) { return r.TasksInAnytime(listOpts{}) },
		"someday":             func() ([]*things.Task, error) { return r.TasksInSomeday(listOpts{}) },
		"upcoming":            func() ([]*things.Task, error) { return r.TasksInUpcoming(listOpts{}) },
		"project":             project,
		"area":                func() ([]*things.Task, error) { return r.TasksInArea("area", listOpts{}) },
		"completed":           func() ([]*things.Task, error) { return r.CompletedTasksInRange(0, nil, nil) },
		"get project":         single("project"),
		"get completed":       single("done"),
		"get someday":         single("someday-never"),
	}
	for name, list := range lists {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			tasks, err := list()
			if err != nil {
				t.Fatal(err)
			}
			if len(tasks) == 0 {
				t.Fatal("list is empty; the check would prove nothing")
			}
			for _, task := range tasks {
				if task == nil {
					t.Fatal("nil task")
				}
				for field, d := range map[string]*time.Time{"start date": task.ScheduledDate, "deadline": task.DeadlineDate} {
					if d != nil && d.Year() >= 4000 {
						t.Errorf("%s: %s %s is the never sentinel", task.UUID, field, dayString(d))
					}
				}
			}
		})
	}
}

// TestReadModel_KI04_NeverStartDateIsSomeday: a deferred to-do whose start
// date is the sentinel has no start date, so it is in Someday, not Upcoming.
func TestReadModel_KI04_NeverStartDateIsSomeday(t *testing.T) {
	t.Parallel()

	r := readModel{mirror: sentinelMirror()}
	someday, err := r.TasksInSomeday(listOpts{})
	if err != nil {
		t.Fatal(err)
	}
	upcoming, err := r.TasksInUpcoming(listOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(someday, func(t *things.Task) bool { return t.UUID == "someday-never" }) {
		t.Error("someday-never is not in Someday")
	}
	if slices.ContainsFunc(upcoming, func(t *things.Task) bool { return t.UUID == "someday-never" }) {
		t.Error("someday-never is in Upcoming")
	}
}

func TestReadModel_KI04_CompletedKeepsCompletionInstant(t *testing.T) {
	t.Parallel()

	r := readModel{mirror: sentinelMirror()}
	tasks, err := r.CompletedTasksInRange(0, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := time.Date(2026, 10, 9, 18, 30, 5, 0, time.UTC)
	if len(tasks) != 1 || tasks[0].CompletionDate == nil || !tasks[0].CompletionDate.Equal(want) || tasks[0].DeadlineDate != nil {
		t.Fatalf("completed = %+v, want done at %v without a deadline", tasks, want)
	}
}

// headingMirror is a project "p" with to-dos directly in it, under two
// headings, under a trashed and an archived heading, and items of another
// project "q". Index order differs from insertion order on purpose.
func headingMirror() listMirror {
	todo := func(uuid string, index int, project, heading string) things.Task {
		t := things.Task{UUID: uuid, Type: things.TaskTypeTask, Schedule: things.TaskScheduleAnytime, Index: index}
		if project != "" {
			t.ParentTaskIDs = []string{project}
		}
		if heading != "" {
			t.ActionGroupIDs = []string{heading}
		}
		return t
	}
	heading := func(uuid, title string, index int, project string) things.Task {
		return things.Task{UUID: uuid, Title: title, Type: things.TaskTypeHeading, Index: index, ParentTaskIDs: []string{project}}
	}
	with := func(t things.Task, change func(*things.Task)) things.Task {
		change(&t)
		return t
	}
	return listMirror{
		heading("h-second", "Second", 2, "p"),
		heading("h-first", "First", 1, "p"),
		with(heading("h-trashed", "Trashed", 3, "p"), func(t *things.Task) { t.InTrash = true }),
		with(heading("h-archived", "Archived", 4, "p"), func(t *things.Task) { t.Status = things.TaskStatusCompleted }),
		heading("q-heading", "Elsewhere", 0, "q"),
		{UUID: "p", Type: things.TaskTypeProject},
		todo("direct-b", 5, "p", ""),
		todo("direct-a", 1, "p", ""),
		todo("first-2", 7, "", "h-first"),
		todo("first-1", 2, "", "h-first"),
		todo("both", 4, "p", "h-first"),
		todo("second-1", 0, "", "h-second"),
		with(todo("first-done", 3, "", "h-first"), func(t *things.Task) { t.Status = things.TaskStatusCompleted }),
		with(todo("first-canceled", 5, "", "h-first"), func(t *things.Task) { t.Status = things.TaskStatusCanceled }),
		with(todo("first-trashed", 6, "", "h-first"), func(t *things.Task) { t.InTrash = true }),
		todo("under-trashed", 0, "", "h-trashed"),
		todo("under-archived", 0, "", "h-archived"),
		todo("under-q", 0, "", "q-heading"),
		todo("in-q", 0, "q", ""),
	}
}

// TestReadModel_KI13_ProjectIncludesHeadings: a project lists its own to-dos
// first, then each heading's to-dos in heading order, each group by index. A
// to-do with both pr and agr is listed once, under its heading. The to-do's
// own status decides open-list membership; a trashed heading's to-dos count
// as trashed. Pagination applies to the merged list.
func TestReadModel_KI13_ProjectIncludesHeadings(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		opts listOpts
		want []string
	}{
		{"open list", listOpts{},
			[]string{"direct-a", "direct-b", "first-1", "both", "first-2", "second-1", "under-archived"}},
		{"page across a heading boundary", listOpts{Limit: 2, Offset: 3},
			[]string{"both", "first-2"}},
		{"closed included", listOpts{QueryOpts: sync.QueryOpts{IncludeCompleted: true}},
			[]string{"direct-a", "direct-b", "first-1", "first-done", "both", "first-canceled", "first-2", "second-1", "under-archived"}},
		{"trash included", listOpts{QueryOpts: sync.QueryOpts{IncludeTrashed: true}},
			[]string{"direct-a", "direct-b", "first-1", "both", "first-trashed", "first-2", "second-1", "under-trashed", "under-archived"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			tasks, _, err := readModel{mirror: headingMirror()}.TasksInProject("p", tt.opts)
			if err != nil {
				t.Fatal(err)
			}
			var got []string
			for _, task := range tasks {
				got = append(got, task.UUID)
			}
			if !slices.Equal(got, tt.want) {
				t.Errorf("project tasks = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestReadModel_KI13_ProjectHeadings(t *testing.T) {
	t.Parallel()

	_, headings, err := readModel{mirror: headingMirror()}.TasksInProject("p", listOpts{})
	if err != nil {
		t.Fatal(err)
	}
	titles := map[string]string{}
	for _, h := range headings {
		titles[h.UUID] = h.Title
	}
	want := map[string]string{"h-first": "First", "h-second": "Second", "h-trashed": "Trashed", "h-archived": "Archived"}
	if !reflect.DeepEqual(titles, want) {
		t.Errorf("headings = %v, want %v", titles, want)
	}
}

// repeatingMirror holds a repeating template in every list a to-do or project
// can reach, a visible instance generated from one of them, a completed
// instance and an ordinary to-do. Every item is in project "p" or area "area".
func repeatingMirror() listMirror {
	rule := &things.RepeaterConfiguration{FrequencyUnit: things.FrequencyUnit(256)}
	todo := func(uuid string, schedule things.TaskSchedule, start *time.Time, change func(*things.Task)) things.Task {
		t := things.Task{UUID: uuid, Type: things.TaskTypeTask, Schedule: schedule, ScheduledDate: start, AreaIDs: []string{"area"}}
		if change != nil {
			change(&t)
		}
		return t
	}
	template := func(t *things.Task) { t.Repeater = rule }
	done := time.Date(2026, 10, 9, 18, 30, 5, 0, time.UTC)
	return listMirror{
		{UUID: "p", Type: things.TaskTypeProject, Schedule: things.TaskScheduleAnytime},
		{UUID: "h", Type: things.TaskTypeHeading, ParentTaskIDs: []string{"p"}},
		{UUID: "tpl-project", Type: things.TaskTypeProject, Schedule: things.TaskScheduleSomeday, Repeater: rule},
		todo("tpl-inbox", things.TaskScheduleInbox, nil, template),
		todo("tpl-today", things.TaskScheduleAnytime, dayAt(2026, time.January, 1), template),
		todo("tpl-anytime", things.TaskScheduleAnytime, nil, func(t *things.Task) {
			t.Repeater, t.ParentTaskIDs = rule, []string{"p"}
		}),
		todo("tpl-someday", things.TaskScheduleSomeday, nil, template),
		todo("tpl-upcoming", things.TaskScheduleSomeday, dayAt(3000, time.January, 1), template),
		todo("tpl-heading", things.TaskScheduleAnytime, nil, func(t *things.Task) {
			t.Repeater, t.ActionGroupIDs = rule, []string{"h"}
		}),
		todo("instance", things.TaskScheduleAnytime, nil, func(t *things.Task) {
			t.RecurrenceIDs, t.ParentTaskIDs = []string{"tpl-anytime"}, []string{"p"}
		}),
		todo("instance-done", things.TaskScheduleAnytime, nil, func(t *things.Task) {
			t.RecurrenceIDs, t.Status, t.CompletionDate = []string{"tpl-anytime"}, things.TaskStatusCompleted, &done
		}),
		todo("plain", things.TaskScheduleAnytime, nil, nil),
	}
}

// TestReadModel_KI05_HidesRepeatingTemplates: no open list shows the hidden
// template of a repeating to-do or project, closed items included or not
// (its next occurrence in Upcoming is KI-36); its generated instances are
// ordinary items. The Logbook query and lookups by UUID are unchanged.
func TestReadModel_KI05_HidesRepeatingTemplates(t *testing.T) {
	t.Parallel()

	r := readModel{mirror: repeatingMirror()}
	withDone := listOpts{QueryOpts: sync.QueryOpts{IncludeCompleted: true}}
	project := func(opts listOpts) func() ([]*things.Task, error) {
		return func() ([]*things.Task, error) {
			tasks, _, err := r.TasksInProject("p", opts)
			return tasks, err
		}
	}
	tests := map[string]struct {
		list func() ([]*things.Task, error)
		want []string
	}{
		"all tasks":             {func() ([]*things.Task, error) { return r.AllTasks(listOpts{}) }, []string{"instance", "plain"}},
		"all tasks with done":   {func() ([]*things.Task, error) { return r.AllTasks(withDone) }, []string{"instance", "instance-done", "plain"}},
		"projects":              {func() ([]*things.Task, error) { return r.AllProjects(listOpts{}) }, []string{"p"}},
		"projects with done":    {func() ([]*things.Task, error) { return r.AllProjects(withDone) }, []string{"p"}},
		"inbox":                 {func() ([]*things.Task, error) { return r.TasksInInbox(listOpts{}) }, nil},
		"today":                 {func() ([]*things.Task, error) { return r.TasksInToday(listOpts{}) }, nil},
		"anytime":               {func() ([]*things.Task, error) { return r.TasksInAnytime(listOpts{}) }, []string{"instance", "plain"}},
		"someday":               {func() ([]*things.Task, error) { return r.TasksInSomeday(listOpts{}) }, nil},
		"upcoming":              {func() ([]*things.Task, error) { return r.TasksInUpcoming(listOpts{}) }, nil},
		"project":               {project(listOpts{}), []string{"instance"}},
		"project with done":     {project(withDone), []string{"instance"}},
		"area":                  {func() ([]*things.Task, error) { return r.TasksInArea("area", listOpts{}) }, []string{"instance", "plain"}},
		"page after a template": {func() ([]*things.Task, error) { return r.AllTasks(listOpts{Limit: 1, Offset: 1}) }, []string{"plain"}},
		"completed (logbook)":   {func() ([]*things.Task, error) { return r.CompletedTasksInRange(0, nil, nil) }, []string{"instance-done"}},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			tasks, err := tt.list()
			if err != nil {
				t.Fatal(err)
			}
			var got []string
			for _, task := range tasks {
				got = append(got, task.UUID)
			}
			if !slices.Equal(got, tt.want) {
				t.Errorf("list = %v, want %v", got, tt.want)
			}
		})
	}

	for _, uuid := range []string{"tpl-someday", "tpl-project"} {
		task, err := r.Task(uuid)
		if err != nil || task == nil || !task.IsRepeatingTemplate() {
			t.Errorf("Task(%q) = %+v, %v; want the template itself", uuid, task, err)
		}
	}
}
