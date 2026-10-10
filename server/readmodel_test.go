package main

import (
	"reflect"
	"testing"
	"time"

	things "github.com/arthursoares/things-cloud-sdk"
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
