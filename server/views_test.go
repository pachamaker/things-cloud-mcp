package main

import (
	"slices"
	"testing"
	"time"

	things "github.com/arthursoares/things-cloud-sdk"
	"github.com/arthursoares/things-cloud-sdk/sync"
)

// TestViews_KI02_ClassificationMatrix pins view membership for every case of the
// KI-02 table that the SDK mirror can represent, on a fixed calendar day. The
// model is docs/protocol/things-cloud.md §5.1 (things.py today/upcoming/someday).
// Anytime stays the complement of Today within st=1.
//
// Rows of the embedded-SDK version of this test that depend on tir cannot be
// represented, because the SDK mirror does not keep it (spike report §4 B4):
// st=1 placed in Today only through tir (was Today, now Anytime) and st=2 with
// no start date and a future tir (was Upcoming, now Someday).
func TestViews_KI02_ClassificationMatrix(t *testing.T) {
	t.Parallel()

	day := time.Date(2026, 10, 10, 0, 0, 0, 0, time.UTC)
	yesterday := day.AddDate(0, 0, -1)
	tomorrow := day.AddDate(0, 0, 1)
	never := time.Date(neverendingRepeatYear, time.January, 1, 0, 0, 0, 0, time.UTC)

	views := map[listView]string{
		viewInbox:    "inbox",
		viewToday:    "today",
		viewAnytime:  "anytime",
		viewSomeday:  "someday",
		viewUpcoming: "upcoming",
	}

	cases := []struct {
		uuid string
		task things.Task
		want []string
	}{
		// st=1 with a start date
		{"st1-start-today", things.Task{Schedule: things.TaskScheduleAnytime, ScheduledDate: &day}, []string{"today"}},
		{"st1-start-yesterday", things.Task{Schedule: things.TaskScheduleAnytime, ScheduledDate: &yesterday}, []string{"today"}},
		{"st1-no-dates", things.Task{Schedule: things.TaskScheduleAnytime}, []string{"anytime"}},

		// st=2: start date arrived but not yet confirmed by a Things client
		{"st2-start-today", things.Task{Schedule: things.TaskScheduleSomeday, ScheduledDate: &day}, []string{"today"}},
		{"st2-start-yesterday", things.Task{Schedule: things.TaskScheduleSomeday, ScheduledDate: &yesterday}, []string{"today"}},
		{"st2-start-tomorrow", things.Task{Schedule: things.TaskScheduleSomeday, ScheduledDate: &tomorrow}, []string{"upcoming"}},
		{"st2-no-dates", things.Task{Schedule: things.TaskScheduleSomeday}, []string{"someday"}},

		// Inbox
		{"st0-no-dates", things.Task{Schedule: things.TaskScheduleInbox}, []string{"inbox"}},
		{"st0-start-yesterday", things.Task{Schedule: things.TaskScheduleInbox, ScheduledDate: &yesterday}, []string{"inbox"}},

		// Deadlines pull only items without a start date into Today (dds is not mirrored)
		{"st1-deadline-today", things.Task{Schedule: things.TaskScheduleAnytime, DeadlineDate: &day}, []string{"today"}},
		{"st0-deadline-yesterday", things.Task{Schedule: things.TaskScheduleInbox, DeadlineDate: &yesterday}, []string{"inbox", "today"}},
		{"st2-deadline-today", things.Task{Schedule: things.TaskScheduleSomeday, DeadlineDate: &day}, []string{"today", "someday"}},
		{"st1-deadline-tomorrow", things.Task{Schedule: things.TaskScheduleAnytime, DeadlineDate: &tomorrow}, []string{"anytime"}},
		{"st2-start-tomorrow-deadline-yesterday", things.Task{Schedule: things.TaskScheduleSomeday, ScheduledDate: &tomorrow, DeadlineDate: &yesterday}, []string{"upcoming"}},
		{"st1-start-and-deadline-yesterday", things.Task{Schedule: things.TaskScheduleAnytime, ScheduledDate: &yesterday, DeadlineDate: &yesterday}, []string{"today"}},
		{"st1-deadline-never", things.Task{Schedule: things.TaskScheduleAnytime, DeadlineDate: &never}, []string{"anytime"}},
	}

	for _, tc := range cases {
		t.Run(tc.uuid, func(t *testing.T) {
			t.Parallel()
			var got []string
			for view, name := range views {
				if inView(&tc.task, view, day) {
					got = append(got, name)
				}
			}
			want := slices.Clone(tc.want)
			slices.Sort(got)
			slices.Sort(want)
			if !slices.Equal(got, want) {
				t.Errorf("views = %v, want %v", got, want)
			}
		})
	}
}

func TestOpenList_KI05_DropsClosedAndKeepsOrder(t *testing.T) {
	t.Parallel()

	tasks := []*things.Task{
		{UUID: "open-1", Status: things.TaskStatusPending},
		{UUID: "canceled", Status: things.TaskStatusCanceled},
		{UUID: "completed", Status: things.TaskStatusCompleted},
		{UUID: "open-2", Status: things.TaskStatusPending},
	}
	uuids := func(ts []*things.Task) []string {
		var out []string
		for _, t := range ts {
			out = append(out, t.UUID)
		}
		return out
	}

	open, err := openList(tasks, nil, listOpts{})
	if err != nil {
		t.Fatalf("openList: %v", err)
	}
	if got, want := uuids(open), []string{"open-1", "open-2"}; !slices.Equal(got, want) {
		t.Errorf("open list = %v, want %v", got, want)
	}

	page, _ := openList(tasks, nil, listOpts{Limit: 1, Offset: 1})
	if got, want := uuids(page), []string{"open-2"}; !slices.Equal(got, want) {
		t.Errorf("paginated after filtering = %v, want %v", got, want)
	}

	all, _ := openList(tasks, nil, listOpts{QueryOpts: sync.QueryOpts{IncludeCompleted: true}})
	if len(all) != len(tasks) {
		t.Errorf("IncludeCompleted kept %d of %d", len(all), len(tasks))
	}
}

func TestTasksInView_Ordering(t *testing.T) {
	t.Parallel()

	day := time.Date(2026, 10, 10, 0, 0, 0, 0, time.UTC)
	in3, in2 := day.AddDate(0, 0, 3), day.AddDate(0, 0, 2)
	tasks := []*things.Task{
		{UUID: "today-ti-2", Schedule: things.TaskScheduleAnytime, ScheduledDate: &day, TodayIndex: 2},
		{UUID: "upcoming-3", Schedule: things.TaskScheduleSomeday, ScheduledDate: &in3},
		{UUID: "today-ti-1", Schedule: things.TaskScheduleAnytime, ScheduledDate: &day, TodayIndex: 1},
		{UUID: "upcoming-2", Schedule: things.TaskScheduleSomeday, ScheduledDate: &in2},
		{UUID: "today-ti-1-later", Schedule: things.TaskScheduleAnytime, ScheduledDate: &day, TodayIndex: 1},
	}
	uuids := func(ts []*things.Task) []string {
		var out []string
		for _, t := range ts {
			out = append(out, t.UUID)
		}
		return out
	}

	if got, want := uuids(tasksInView(tasks, viewToday, day)), []string{"today-ti-1", "today-ti-1-later", "today-ti-2"}; !slices.Equal(got, want) {
		t.Errorf("today order = %v, want %v", got, want)
	}
	if got, want := uuids(tasksInView(tasks, viewUpcoming, day)), []string{"upcoming-2", "upcoming-3"}; !slices.Equal(got, want) {
		t.Errorf("upcoming order = %v, want %v", got, want)
	}
}
