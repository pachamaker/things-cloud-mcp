package sync

import (
	"path/filepath"
	"slices"
	"testing"
	"time"

	things "github.com/arthursoares/things-cloud-sdk"
)

// TestViews_KI02_ClassificationMatrix pins view membership for every case of the
// KI-02 table that the mirror can represent, on a fixed calendar day. The model
// is docs/protocol/things-cloud.md §5.1 (things.py today/upcoming/someday).
// Anytime stays the complement of Today within st=1.
func TestViews_KI02_ClassificationMatrix(t *testing.T) {
	t.Parallel()

	syncer, err := Open(filepath.Join(t.TempDir(), "test.db"), nil)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer syncer.Close()

	day := time.Date(2026, 10, 10, 0, 0, 0, 0, time.UTC)
	yesterday := day.AddDate(0, 0, -1)
	tomorrow := day.AddDate(0, 0, 1)
	never := time.Date(things.NeverendingRepeatYear, time.January, 1, 0, 0, 0, 0, time.UTC)

	const (
		inbox    = "inbox"
		today    = "today"
		anytime  = "anytime"
		someday  = "someday"
		upcoming = "upcoming"
	)

	cases := []struct {
		uuid string
		task things.Task
		want []string
	}{
		// st=1 with a start date
		{"st1-start-today", things.Task{Schedule: things.TaskScheduleAnytime, ScheduledDate: &day}, []string{today}},
		{"st1-start-yesterday", things.Task{Schedule: things.TaskScheduleAnytime, ScheduledDate: &yesterday}, []string{today}},
		{"st1-start-and-tir-yesterday", things.Task{Schedule: things.TaskScheduleAnytime, ScheduledDate: &yesterday, TodayIndexReference: &yesterday}, []string{today}},
		{"st1-tir-today", things.Task{Schedule: things.TaskScheduleAnytime, TodayIndexReference: &day}, []string{today}},
		{"st1-no-dates", things.Task{Schedule: things.TaskScheduleAnytime}, []string{anytime}},

		// st=2: start date arrived but not yet confirmed by a Things client
		{"st2-start-today", things.Task{Schedule: things.TaskScheduleSomeday, ScheduledDate: &day}, []string{today}},
		{"st2-start-yesterday", things.Task{Schedule: things.TaskScheduleSomeday, ScheduledDate: &yesterday}, []string{today}},
		{"st2-start-tomorrow", things.Task{Schedule: things.TaskScheduleSomeday, ScheduledDate: &tomorrow}, []string{upcoming}},
		{"st2-tir-tomorrow", things.Task{Schedule: things.TaskScheduleSomeday, TodayIndexReference: &tomorrow}, []string{upcoming}},
		{"st2-no-dates", things.Task{Schedule: things.TaskScheduleSomeday}, []string{someday}},

		// Inbox
		{"st0-no-dates", things.Task{Schedule: things.TaskScheduleInbox}, []string{inbox}},
		{"st0-tir-today", things.Task{Schedule: things.TaskScheduleInbox, TodayIndexReference: &day}, []string{inbox}},

		// Deadlines pull only items without a start date into Today (dds is not mirrored)
		{"st1-deadline-today", things.Task{Schedule: things.TaskScheduleAnytime, DeadlineDate: &day}, []string{today}},
		{"st0-deadline-yesterday", things.Task{Schedule: things.TaskScheduleInbox, DeadlineDate: &yesterday}, []string{inbox, today}},
		{"st2-deadline-today", things.Task{Schedule: things.TaskScheduleSomeday, DeadlineDate: &day}, []string{today, someday}},
		{"st1-deadline-tomorrow", things.Task{Schedule: things.TaskScheduleAnytime, DeadlineDate: &tomorrow}, []string{anytime}},
		{"st2-start-tomorrow-deadline-yesterday", things.Task{Schedule: things.TaskScheduleSomeday, ScheduledDate: &tomorrow, DeadlineDate: &yesterday}, []string{upcoming}},
		{"st1-start-and-deadline-yesterday", things.Task{Schedule: things.TaskScheduleAnytime, ScheduledDate: &yesterday, DeadlineDate: &yesterday}, []string{today}},
		{"st1-deadline-never", things.Task{Schedule: things.TaskScheduleAnytime, DeadlineDate: &never}, []string{anytime}},

		// Closed or trashed items are in no open view (KI-05)
		{"canceled-st1-start-today", things.Task{Schedule: things.TaskScheduleAnytime, ScheduledDate: &day, Status: things.TaskStatusCanceled}, nil},
		{"completed-st2-start-today", things.Task{Schedule: things.TaskScheduleSomeday, ScheduledDate: &day, Status: things.TaskStatusCompleted}, nil},
		{"canceled-st0-deadline-today", things.Task{Schedule: things.TaskScheduleInbox, DeadlineDate: &day, Status: things.TaskStatusCanceled}, nil},
		{"trashed-st1-start-today", things.Task{Schedule: things.TaskScheduleAnytime, ScheduledDate: &day, InTrash: true}, nil},
	}

	for _, tc := range cases {
		task := tc.task
		task.UUID = tc.uuid
		task.Title = tc.uuid
		if err := syncer.saveTask(&task); err != nil {
			t.Fatalf("saveTask(%s): %v", tc.uuid, err)
		}
	}

	state := syncer.State()
	dayStart, dayEnd := day.Unix(), tomorrow.Unix()
	queries := map[string]func() ([]*things.Task, error){
		inbox:    func() ([]*things.Task, error) { return state.TasksInInbox(QueryOpts{}) },
		today:    func() ([]*things.Task, error) { return state.tasksInToday(QueryOpts{}, dayStart, dayEnd) },
		anytime:  func() ([]*things.Task, error) { return state.tasksInAnytime(QueryOpts{}, dayStart, dayEnd) },
		someday:  func() ([]*things.Task, error) { return state.tasksInSomeday(QueryOpts{}, dayEnd) },
		upcoming: func() ([]*things.Task, error) { return state.tasksInUpcoming(QueryOpts{}, dayEnd) },
	}

	views := map[string][]string{}
	for view, query := range queries {
		tasks, err := query()
		if err != nil {
			t.Fatalf("%s query failed: %v", view, err)
		}
		for _, task := range tasks {
			if slices.Contains(views[task.UUID], view) {
				t.Errorf("%s returned %s twice", view, task.UUID)
			}
			views[task.UUID] = append(views[task.UUID], view)
		}
	}

	for _, tc := range cases {
		t.Run(tc.uuid, func(t *testing.T) {
			got := slices.Clone(views[tc.uuid])
			want := slices.Clone(tc.want)
			slices.Sort(got)
			slices.Sort(want)
			if !slices.Equal(got, want) {
				t.Errorf("views = %v, want %v", got, want)
			}
		})
	}
}
