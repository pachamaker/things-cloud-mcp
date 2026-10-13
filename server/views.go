package main

import (
	"sort"

	things "github.com/arthursoares/things-cloud-sdk"

	"github.com/pachamaker/things-cloud-mcp/domain/civil"
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
//   - no start date and a deadline on or before today that the user has not
//     dismissed from Today (dds null), Inbox included.
//
// today is the owner's calendar day (ADR-0003); sr and dd are compared as the
// calendar dates they encode, never as instants. tir decides nothing: neither
// §5.1 nor things.py reads it (KI-02 open question).
func inToday(t *things.Task, today civil.Date) bool {
	if t.ScheduledDate != nil {
		started := t.Schedule == things.TaskScheduleAnytime || t.Schedule == things.TaskScheduleSomeday
		return started && !civil.FromWireTime(*t.ScheduledDate).After(today)
	}
	return t.DeadlineDate != nil && t.DeadlineSuppressionDate == nil &&
		!civil.FromWireTime(*t.DeadlineDate).After(today)
}

// eveningBucket is the sb value of Today's This Evening section; 0 is the
// main section (docs/protocol/things-cloud.md §5).
const eveningBucket = 1

// inEvening reports whether Today lists t under This Evening: Today ∩ sb=1
// (docs/protocol/things-cloud.md §5.1).
func inEvening(t *things.Task, today civil.Date) bool {
	return t.StartBucket == eveningBucket && inToday(t, today)
}

// inView reports view membership. Today and Anytime partition st=1; an Inbox or
// Someday to-do pulled into Today by its deadline stays in its own list too,
// as in Things.
func inView(t *things.Task, view listView, today civil.Date) bool {
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
			civil.FromWireTime(*t.ScheduledDate).After(today)
	}
	return false
}

// tasksInView filters to-dos already ordered by index and orders them as the
// Things lists do: Today's main section, then This Evening, each by today
// index; Upcoming by start date.
func tasksInView(tasks []*things.Task, view listView, today civil.Date) []*things.Task {
	var out []*things.Task
	for _, t := range tasks {
		if inView(t, view, today) {
			out = append(out, t)
		}
	}
	switch view {
	case viewToday:
		sort.SliceStable(out, func(i, j int) bool {
			if ei, ej := inEvening(out[i], today), inEvening(out[j], today); ei != ej {
				return ej
			}
			return out[i].TodayIndex < out[j].TodayIndex
		})
	case viewUpcoming:
		sort.SliceStable(out, func(i, j int) bool { return out[i].ScheduledDate.Before(*out[j].ScheduledDate) })
	}
	return out
}
