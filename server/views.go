package main

import (
	"sort"
	"time"

	things "github.com/arthursoares/things-cloud-sdk"
)

// listView names a Things list the server derives locally; Things Cloud has
// no view endpoints (ADR-0005).
type listView int

const (
	viewInbox listView = iota
	viewToday
	viewAnytime
	viewSomeday
	viewUpcoming
)

// isClosed reports whether a to-do belongs only to the Logbook: Things treats
// canceled like completed for open lists (KI-05).
func isClosed(t *things.Task) bool {
	return t.Status == things.TaskStatusCompleted || t.Status == things.TaskStatusCanceled
}

// inToday follows docs/protocol/things-cloud.md §5.1 (things.py today()):
//   - started (st=1) or deferred (st=2) with a start date on or before today:
//     overdue carry-over and scheduled items whose date has arrived but that no
//     Things client has confirmed yet;
//   - no start date and a deadline on or before today, Inbox included.
//
// today is the civil day encoded as midnight UTC, as Things stores sr and dd.
// The SDK mirror does not keep tir, dds or sb (spike report §4 B4), so to-dos
// placed in Today only through tir are missed, dismissed deadlines still show,
// and This Evening is not flagged.
func inToday(t *things.Task, today time.Time) bool {
	tomorrow := today.AddDate(0, 0, 1)
	if t.ScheduledDate != nil {
		started := t.Schedule == things.TaskScheduleAnytime || t.Schedule == things.TaskScheduleSomeday
		return started && t.ScheduledDate.Before(tomorrow)
	}
	return t.DeadlineDate != nil && t.DeadlineDate.Before(tomorrow)
}

// inView reports view membership. Today and Anytime partition st=1; an Inbox or
// Someday to-do pulled into Today by its deadline stays in its own list too,
// as in Things.
func inView(t *things.Task, view listView, today time.Time) bool {
	switch view {
	case viewInbox:
		return t.Schedule == things.TaskScheduleInbox
	case viewToday:
		return inToday(t, today)
	case viewAnytime:
		return t.Schedule == things.TaskScheduleAnytime && !inToday(t, today)
	case viewSomeday:
		return t.Schedule == things.TaskScheduleSomeday && t.ScheduledDate == nil
	case viewUpcoming:
		return t.Schedule == things.TaskScheduleSomeday && t.ScheduledDate != nil &&
			!t.ScheduledDate.Before(today.AddDate(0, 0, 1))
	}
	return false
}

// tasksInView filters to-dos already ordered by index and orders them as the
// Things lists do: Today by today index, Upcoming by start date.
func tasksInView(tasks []*things.Task, view listView, today time.Time) []*things.Task {
	var out []*things.Task
	for _, t := range tasks {
		if inView(t, view, today) {
			out = append(out, t)
		}
	}
	switch view {
	case viewToday:
		sort.SliceStable(out, func(i, j int) bool { return out[i].TodayIndex < out[j].TodayIndex })
	case viewUpcoming:
		sort.SliceStable(out, func(i, j int) bool { return out[i].ScheduledDate.Before(*out[j].ScheduledDate) })
	}
	return out
}
