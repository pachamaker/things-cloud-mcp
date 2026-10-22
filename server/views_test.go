package main

import (
	"slices"
	"testing"
	"time"

	things "github.com/arthursoares/things-cloud-sdk"
	"github.com/arthursoares/things-cloud-sdk/sync"

	"github.com/pachamaker/things-cloud-mcp/domain/civil"
)

var viewNames = map[listView]string{
	viewInbox:    "inbox",
	viewToday:    "today",
	viewAnytime:  "anytime",
	viewSomeday:  "someday",
	viewUpcoming: "upcoming",
}

// viewsOf lists the views a to-do is in on day today, sorted by name.
func viewsOf(task *things.Task, today civil.Date) []string {
	var got []string
	for view, name := range viewNames {
		if inView(task, view, today) {
			got = append(got, name)
		}
	}
	slices.Sort(got)
	return got
}

// TestViews_KI02_ClassificationMatrix pins view membership for every case of the
// KI-02 table, on a fixed calendar day. The model is
// docs/protocol/things-cloud.md §5.1 (things.py today/upcoming/someday).
// Anytime stays the complement of Today within st=1. Dismissed deadlines, This
// Evening and tir have tests of their own below.
func TestViews_KI02_ClassificationMatrix(t *testing.T) {
	t.Parallel()

	day := time.Date(2026, 10, 10, 0, 0, 0, 0, time.UTC)
	today := civil.DateOf(day)
	yesterday := day.AddDate(0, 0, -1)
	tomorrow := day.AddDate(0, 0, 1)
	never := time.Date(neverendingRepeatYear, time.January, 1, 0, 0, 0, 0, time.UTC)

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

		// Deadlines pull only items without a start date into Today
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
			want := slices.Clone(tc.want)
			slices.Sort(want)
			if got := viewsOf(&tc.task, today); !slices.Equal(got, want) {
				t.Errorf("views = %v, want %v", got, want)
			}
		})
	}
}

// TestViews_KI02_DismissedDeadline: once the user dismisses an overdue
// deadline from Today (dds set), the deadline alone no longer puts a to-do
// there; a start date still does (docs/protocol/things-cloud.md §5.1, things.py
// today(): deadline_suppressed=False).
func TestViews_KI02_DismissedDeadline(t *testing.T) {
	t.Parallel()

	day := time.Date(2026, 10, 10, 0, 0, 0, 0, time.UTC)
	today := civil.DateOf(day)
	yesterday := day.AddDate(0, 0, -1)
	dismissed := day.AddDate(0, 0, -1)

	cases := []struct {
		name string
		task things.Task
		want []string
	}{
		{"st1-deadline-yesterday-dismissed", things.Task{Schedule: things.TaskScheduleAnytime, DeadlineDate: &yesterday, DeadlineSuppressionDate: &dismissed}, []string{"anytime"}},
		{"st1-deadline-today-dismissed", things.Task{Schedule: things.TaskScheduleAnytime, DeadlineDate: &day, DeadlineSuppressionDate: &dismissed}, []string{"anytime"}},
		{"st0-deadline-yesterday-dismissed", things.Task{Schedule: things.TaskScheduleInbox, DeadlineDate: &yesterday, DeadlineSuppressionDate: &dismissed}, []string{"inbox"}},
		{"st2-deadline-yesterday-dismissed", things.Task{Schedule: things.TaskScheduleSomeday, DeadlineDate: &yesterday, DeadlineSuppressionDate: &dismissed}, []string{"someday"}},
		{"st1-start-today-deadline-dismissed", things.Task{Schedule: things.TaskScheduleAnytime, ScheduledDate: &day, DeadlineDate: &yesterday, DeadlineSuppressionDate: &dismissed}, []string{"today"}},
		{"st2-start-yesterday-deadline-dismissed", things.Task{Schedule: things.TaskScheduleSomeday, ScheduledDate: &yesterday, DeadlineDate: &yesterday, DeadlineSuppressionDate: &dismissed}, []string{"today"}},
		{"st1-deadline-yesterday-not-dismissed", things.Task{Schedule: things.TaskScheduleAnytime, DeadlineDate: &yesterday}, []string{"today"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := viewsOf(&tc.task, today); !slices.Equal(got, tc.want) {
				t.Errorf("views = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestViews_KI02_ThisEvening: This Evening is Today ∩ sb=1
// (docs/protocol/things-cloud.md §5.1). sb=1 outside Today flags nothing, and
// the bucket never moves a to-do between views.
func TestViews_KI02_ThisEvening(t *testing.T) {
	t.Parallel()

	day := time.Date(2026, 10, 10, 0, 0, 0, 0, time.UTC)
	today := civil.DateOf(day)
	yesterday := day.AddDate(0, 0, -1)
	tomorrow := day.AddDate(0, 0, 1)

	cases := []struct {
		name    string
		task    things.Task
		views   []string
		evening bool
	}{
		{"st1-start-today-evening", things.Task{Schedule: things.TaskScheduleAnytime, ScheduledDate: &day, StartBucket: 1}, []string{"today"}, true},
		{"st1-start-today-main", things.Task{Schedule: things.TaskScheduleAnytime, ScheduledDate: &day}, []string{"today"}, false},
		{"st1-start-yesterday-evening", things.Task{Schedule: things.TaskScheduleAnytime, ScheduledDate: &yesterday, StartBucket: 1}, []string{"today"}, true},
		{"st2-start-today-evening", things.Task{Schedule: things.TaskScheduleSomeday, ScheduledDate: &day, StartBucket: 1}, []string{"today"}, true},
		{"st1-deadline-today-evening", things.Task{Schedule: things.TaskScheduleAnytime, DeadlineDate: &day, StartBucket: 1}, []string{"today"}, true},
		{"st1-no-dates-evening", things.Task{Schedule: things.TaskScheduleAnytime, StartBucket: 1}, []string{"anytime"}, false},
		{"st2-start-tomorrow-evening", things.Task{Schedule: things.TaskScheduleSomeday, ScheduledDate: &tomorrow, StartBucket: 1}, []string{"upcoming"}, false},
		{"st0-evening", things.Task{Schedule: things.TaskScheduleInbox, StartBucket: 1}, []string{"inbox"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := viewsOf(&tc.task, today); !slices.Equal(got, tc.views) {
				t.Errorf("views = %v, want %v", got, tc.views)
			}
			if got := inEvening(&tc.task, today); got != tc.evening {
				t.Errorf("inEvening = %v, want %v", got, tc.evening)
			}
		})
	}
}

// TestViews_KI02_TodayIndexReferenceDate pins that tir decides no view: the
// rules of docs/protocol/things-cloud.md §5.1 and things.py, the conformance
// reference, read sr only. Whether the Things app ever lists a to-do by tir
// alone is an open question in KI-02; a template's tir is its next occurrence
// (KI-36), and templates are in no list (KI-05).
func TestViews_KI02_TodayIndexReferenceDate(t *testing.T) {
	t.Parallel()

	day := time.Date(2026, 10, 10, 0, 0, 0, 0, time.UTC)
	today := civil.DateOf(day)
	yesterday := day.AddDate(0, 0, -1)
	tomorrow := day.AddDate(0, 0, 1)

	cases := []struct {
		name string
		task things.Task
		want []string
	}{
		{"st1-no-start-tir-today", things.Task{Schedule: things.TaskScheduleAnytime, TodayIndexReferenceDate: &day}, []string{"anytime"}},
		{"st2-no-start-tir-tomorrow", things.Task{Schedule: things.TaskScheduleSomeday, TodayIndexReferenceDate: &tomorrow}, []string{"someday"}},
		{"st2-no-start-tir-today", things.Task{Schedule: things.TaskScheduleSomeday, TodayIndexReferenceDate: &day}, []string{"someday"}},
		{"st2-start-tomorrow-tir-today", things.Task{Schedule: things.TaskScheduleSomeday, ScheduledDate: &tomorrow, TodayIndexReferenceDate: &day}, []string{"upcoming"}},
		{"st1-start-yesterday-tir-yesterday", things.Task{Schedule: things.TaskScheduleAnytime, ScheduledDate: &yesterday, TodayIndexReferenceDate: &yesterday}, []string{"today"}},
		{"st0-no-start-tir-today", things.Task{Schedule: things.TaskScheduleInbox, TodayIndexReferenceDate: &day}, []string{"inbox"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := viewsOf(&tc.task, today); !slices.Equal(got, tc.want) {
				t.Errorf("views = %v, want %v", got, tc.want)
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
	today := civil.DateOf(day)
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

	if got, want := uuids(tasksInView(tasks, viewToday, today)), []string{"today-ti-1", "today-ti-1-later", "today-ti-2"}; !slices.Equal(got, want) {
		t.Errorf("today order = %v, want %v", got, want)
	}
	if got, want := uuids(tasksInView(tasks, viewUpcoming, today)), []string{"upcoming-2", "upcoming-3"}; !slices.Equal(got, want) {
		t.Errorf("upcoming order = %v, want %v", got, want)
	}
}

// TestTasksInView_KI02_EveningLast: Today lists its main section first and
// This Evening after it, each by today index, as the Things app shows them.
func TestTasksInView_KI02_EveningLast(t *testing.T) {
	t.Parallel()

	day := time.Date(2026, 10, 10, 0, 0, 0, 0, time.UTC)
	today := civil.DateOf(day)
	tasks := []*things.Task{
		{UUID: "evening-ti-1", Schedule: things.TaskScheduleAnytime, ScheduledDate: &day, TodayIndex: 1, StartBucket: 1},
		{UUID: "main-ti-3", Schedule: things.TaskScheduleAnytime, ScheduledDate: &day, TodayIndex: 3},
		{UUID: "evening-ti-0", Schedule: things.TaskScheduleAnytime, ScheduledDate: &day, TodayIndex: 0, StartBucket: 1},
		{UUID: "main-ti-2", Schedule: things.TaskScheduleAnytime, ScheduledDate: &day, TodayIndex: 2},
	}
	var got []string
	for _, t := range tasksInView(tasks, viewToday, today) {
		got = append(got, t.UUID)
	}
	if want := []string{"main-ti-2", "main-ti-3", "evening-ti-0", "evening-ti-1"}; !slices.Equal(got, want) {
		t.Errorf("today order = %v, want %v", got, want)
	}
}
