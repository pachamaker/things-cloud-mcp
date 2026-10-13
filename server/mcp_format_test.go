package main

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"

	things "github.com/arthursoares/things-cloud-sdk"

	"github.com/pachamaker/things-cloud-mcp/domain/civil"
)

func TestFormatTaskJSONShape(t *testing.T) {
	t.Parallel()

	t.Run("includes completed_at", func(t *testing.T) {
		t.Parallel()

		completedAt := time.Date(2026, 3, 5, 10, 30, 0, 0, time.FixedZone("UTC+1", 3600))
		task := &things.Task{
			UUID:           "task-1",
			Title:          "Completed task",
			Status:         things.TaskStatusCompleted,
			Type:           things.TaskTypeTask,
			CompletionDate: &completedAt,
		}

		b, err := json.Marshal(formatTask(task))
		if err != nil {
			t.Fatalf("marshal failed: %v", err)
		}

		var got map[string]any
		if err := json.Unmarshal(b, &got); err != nil {
			t.Fatalf("unmarshal failed: %v", err)
		}

		if got["completed_at"] != "2026-03-05T09:30:00Z" {
			t.Fatalf("expected completed_at to be normalized to UTC, got %v", got["completed_at"])
		}
		if _, ok := got["today_index_ref"]; ok {
			t.Fatal("did not expect today_index_ref: the SDK does not persist tir")
		}
	})

	t.Run("omits completed_at and today_index_ref when absent", func(t *testing.T) {
		t.Parallel()

		task := &things.Task{
			UUID:   "task-2",
			Title:  "Open task",
			Status: things.TaskStatusPending,
			Type:   things.TaskTypeTask,
		}

		b, err := json.Marshal(formatTask(task))
		if err != nil {
			t.Fatalf("marshal failed: %v", err)
		}

		var got map[string]any
		if err := json.Unmarshal(b, &got); err != nil {
			t.Fatalf("unmarshal failed: %v", err)
		}

		if _, ok := got["completed_at"]; ok {
			t.Fatal("did not expect completed_at in output")
		}
		if _, ok := got["today_index_ref"]; ok {
			t.Fatal("did not expect today_index_ref in output")
		}
	})

	t.Run("normalizes the never-ending sentinel deadline to absent", func(t *testing.T) {
		t.Parallel()

		sentinel := time.Date(neverendingRepeatYear, time.January, 1, 0, 0, 0, 0, time.UTC)
		task := &things.Task{
			UUID:         "task-3",
			Title:        "Task with sentinel deadline",
			Status:       things.TaskStatusPending,
			Type:         things.TaskTypeTask,
			DeadlineDate: &sentinel,
		}

		b, err := json.Marshal(formatTask(task))
		if err != nil {
			t.Fatalf("marshal failed: %v", err)
		}

		var got map[string]any
		if err := json.Unmarshal(b, &got); err != nil {
			t.Fatalf("unmarshal failed: %v", err)
		}

		if _, ok := got["deadline"]; ok {
			t.Fatalf("did not expect deadline in output for sentinel date, got %v", got["deadline"])
		}
	})

	t.Run("includes a real deadline", func(t *testing.T) {
		t.Parallel()

		deadline := time.Date(2026, 3, 10, 0, 0, 0, 0, time.UTC)
		task := &things.Task{
			UUID:         "task-4",
			Title:        "Task with real deadline",
			Status:       things.TaskStatusPending,
			Type:         things.TaskTypeTask,
			DeadlineDate: &deadline,
		}

		b, err := json.Marshal(formatTask(task))
		if err != nil {
			t.Fatalf("marshal failed: %v", err)
		}

		var got map[string]any
		if err := json.Unmarshal(b, &got); err != nil {
			t.Fatalf("unmarshal failed: %v", err)
		}

		if got["deadline"] != "2026-03-10" {
			t.Fatalf("expected deadline 2026-03-10, got %v", got["deadline"])
		}
	})
}

func TestFormatTask_KI04_NoSentinelDates(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		task things.Task
	}{
		{"start date sentinel", things.Task{ScheduledDate: dayAt(4001, time.January, 1)}},
		{"deadline in year 4000", things.Task{DeadlineDate: dayAt(4000, time.January, 1)}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := formatTask(&tt.task)
			if got.ScheduledFor != "" || got.Deadline != "" {
				t.Errorf("scheduled_for = %q, deadline = %q, want both empty", got.ScheduledFor, got.Deadline)
			}
		})
	}
}

func TestProjectTasksResult_KI13_HeadingFields(t *testing.T) {
	t.Parallel()

	tasks := []*things.Task{
		{UUID: "direct", Type: things.TaskTypeTask, ParentTaskIDs: []string{"p"}},
		{UUID: "under", Type: things.TaskTypeTask, ActionGroupIDs: []string{"h1"}},
	}
	headings := []*things.Task{{UUID: "h1", Title: "Errands", Type: things.TaskTypeHeading}}

	res := projectTasksResult(tasks, headings)
	if res.IsError {
		t.Fatalf("result is an error: %+v", res)
	}
	var got []map[string]any
	if err := json.Unmarshal([]byte(res.Content[0].(mcp.TextContent).Text), &got); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d tasks, want 2", len(got))
	}
	if _, ok := got[0]["heading_id"]; ok {
		t.Errorf("direct to-do has heading_id %v", got[0]["heading_id"])
	}
	if _, ok := got[0]["heading_title"]; ok {
		t.Errorf("direct to-do has heading_title %v", got[0]["heading_title"])
	}
	if got[1]["heading_id"] != "h1" || got[1]["heading_title"] != "Errands" {
		t.Errorf("under heading: heading_id = %v, heading_title = %v, want h1, Errands", got[1]["heading_id"], got[1]["heading_title"])
	}
	if got[0]["project_id"] != "p" {
		t.Errorf("direct to-do project_id = %v, want p", got[0]["project_id"])
	}
}

// TestFormatTask_KI05_RepeatingMarker: a template, reachable only through
// things_get_task, says so; an instance names its role; an ordinary to-do
// has no "repeating" key at all.
func TestFormatTask_KI05_RepeatingMarker(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		task things.Task
		want any
	}{
		{"template", things.Task{Repeater: &things.RepeaterConfiguration{}}, "template"},
		{"instance", things.Task{RecurrenceIDs: []string{"template"}}, "instance"},
		{"ordinary", things.Task{}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			b, err := json.Marshal(formatTask(&tt.task))
			if err != nil {
				t.Fatal(err)
			}
			var got map[string]any
			if err := json.Unmarshal(b, &got); err != nil {
				t.Fatal(err)
			}
			if got["repeating"] != tt.want {
				t.Errorf("repeating = %v, want %v (JSON %s)", got["repeating"], tt.want, b)
			}
		})
	}
}

// TestTodayTasksResult_KI02_EveningFlag: things_list_today marks This Evening
// to-dos with "evening": true and leaves the key out for the rest; other
// lists never carry it, because This Evening is a section of Today only.
func TestTodayTasksResult_KI02_EveningFlag(t *testing.T) {
	t.Parallel()

	day := time.Date(2026, 10, 10, 0, 0, 0, 0, time.UTC)
	today := civil.DateOf(day)
	tasks := []*things.Task{
		{UUID: "main", Type: things.TaskTypeTask, Schedule: things.TaskScheduleAnytime, ScheduledDate: &day},
		{UUID: "evening", Type: things.TaskTypeTask, Schedule: things.TaskScheduleAnytime, ScheduledDate: &day, StartBucket: 1},
	}
	decode := func(res *mcp.CallToolResult) []map[string]any {
		t.Helper()
		if res.IsError {
			t.Fatalf("result is an error: %+v", res)
		}
		var got []map[string]any
		if err := json.Unmarshal([]byte(res.Content[0].(mcp.TextContent).Text), &got); err != nil {
			t.Fatal(err)
		}
		return got
	}

	got := decode(todayTasksResult(tasks, today))
	if len(got) != 2 {
		t.Fatalf("got %d tasks, want 2", len(got))
	}
	if v, ok := got[0]["evening"]; ok {
		t.Errorf("main section to-do has evening %v", v)
	}
	if got[1]["evening"] != true {
		t.Errorf("This Evening to-do: evening = %v, want true", got[1]["evening"])
	}

	for _, o := range decode(tasksResult(tasks)) {
		if v, ok := o["evening"]; ok {
			t.Errorf("%v: a list other than Today has evening %v", o["uuid"], v)
		}
	}
}
