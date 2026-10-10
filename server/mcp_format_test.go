package main

import (
	"encoding/json"
	"testing"
	"time"

	things "github.com/arthursoares/things-cloud-sdk"
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
}
