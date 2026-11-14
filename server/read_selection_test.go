package main

import (
	things "github.com/arthursoares/things-cloud-sdk"
	"testing"
	"time"
)

func TestReadOrderingTieBreaks(t *testing.T) {
	tasks := []*things.Task{{UUID: "b", Index: 1}, {UUID: "a", Index: 1}}
	orderReadTasks(tasks)
	if tasks[0].UUID != "a" {
		t.Fatal("unstable index tie")
	}
	at := time.Date(2026, 11, 14, 0, 0, 0, 0, time.UTC)
	tasks = []*things.Task{{UUID: "b", Status: things.TaskStatusCompleted, CompletionDate: &at}, {UUID: "a", Status: things.TaskStatusCompleted, CompletionDate: &at}}
	done := completedInRange(tasks, 100, nil, nil)
	if done[0].UUID != "a" {
		t.Fatal("unstable completed tie")
	}
}

func TestCompletedReadHidesTemplate(t *testing.T) {
	at := time.Date(2026, 11, 14, 0, 0, 0, 0, time.UTC)
	template := &things.Task{UUID: "template", Status: things.TaskStatusCompleted, CompletionDate: &at, Repeater: &things.RepeaterConfiguration{}}
	if !template.IsRepeatingTemplate() {
		t.Fatal("invalid template fixture")
	}
	if len(completedInRange([]*things.Task{template}, 100, nil, nil)) != 0 {
		t.Fatal("hidden template exposed")
	}
}
