package main

import (
	"context"
	"fmt"
	things "github.com/arthursoares/things-cloud-sdk"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/pachamaker/things-cloud-mcp/domain/civil"
	"strings"
	"testing"
	"time"
)

func useReadFixture(t *testing.T, count int) *readRevisionState {
	t.Helper()
	previous := mirror
	t.Cleanup(func() { mirror = previous })
	tasks := make(listMirror, count)
	for i := range tasks {
		tasks[i] = things.Task{UUID: fmt.Sprintf("task-%05d", i), Title: fmt.Sprintf("Task %d", i), Note: "private note", Status: things.TaskStatusPending, Type: things.TaskTypeTask, Schedule: things.TaskScheduleInbox, Index: i}
	}
	day, _ := civil.Parse("2026-11-14")
	state := &readRevisionState{Identity: mirrorIdentity{key: "synthetic", cursor: 1}, Today: day, Timezone: "UTC"}
	gate := newSyncGate(&fakeSyncer{}, time.Now)
	gate.readSource = func() (readModel, readRevisionState, error) {
		return readModel{mirror: catalogReadMirror{tasks}}, *state, nil
	}
	mirror = gate
	return state
}
func TestEveryCollectionReadUsesEnvelope(t *testing.T) {
	useReadFixture(t, 30)
	handlers := []struct {
		name string
		call func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error)
		args map[string]any
	}{
		{"inbox", mcpListInbox, nil}, {"today", mcpListToday, nil}, {"anytime", mcpListAnytime, nil}, {"someday", mcpListSomeday, nil}, {"upcoming", mcpListUpcoming, nil}, {"projects", mcpListProjects, nil}, {"areas", mcpListAreas, nil}, {"tags", mcpListTags, nil}, {"all", mcpListAllTasks, nil}, {"project", mcpListProjectTasks, map[string]any{"project_uuid": "project"}}, {"area", mcpListAreaTasks, map[string]any{"area_uuid": "area"}}, {"completed", mcpListCompleted, nil}, {"search", mcpSearchTasks, map[string]any{"query": "private"}},
	}
	for _, h := range handlers {
		t.Run(h.name, func(t *testing.T) {
			r, e := h.call(context.Background(), readRequest(h.args))
			if e != nil || r.IsError {
				t.Fatalf("%v %+v", e, r)
			}
			var p readPage[map[string]any]
			text := readJSON(t, r, &p)
			if strings.Contains(text, "private note") || p.Items == nil || p.Revision == "" {
				t.Fatal(text)
			}
			if (h.name == "inbox" || h.name == "all" || h.name == "search") && (p.Returned != 25 || p.Total != 30 || !p.HasMore) {
				t.Fatalf("bad page %+v", p)
			}
		})
	}
}
func TestMCPReadErrorsBounded(t *testing.T) {
	useReadFixture(t, 1)
	r, _ := mcpListInbox(context.Background(), readRequest(map[string]any{"limit": 0}))
	if !r.IsError {
		t.Fatal("zero accepted")
	}
	var out map[string]any
	readJSON(t, r, &out)
	r, _ = mcpSearchTasks(context.Background(), readRequest(map[string]any{"query": " "}))
	if !r.IsError {
		t.Fatal("blank search accepted")
	}
}

type catalogReadMirror struct{ listMirror }

func (c catalogReadMirror) AllAreas() ([]*things.Area, error) {
	return []*things.Area{{UUID: "area", Title: "Area"}}, nil
}
func (c catalogReadMirror) AllTags() ([]*things.Tag, error) {
	return []*things.Tag{{UUID: "tag", Title: "Tag"}}, nil
}
func (c catalogReadMirror) Area(id string) (*things.Area, error) {
	if id == "area" {
		return &things.Area{UUID: id, Title: "Area"}, nil
	}
	return nil, nil
}
func (c catalogReadMirror) Tag(id string) (*things.Tag, error) {
	if id == "tag" {
		return &things.Tag{UUID: id, Title: "Tag"}, nil
	}
	return nil, nil
}
func (c catalogReadMirror) ChecklistItems(id string) ([]*things.CheckListItem, error) {
	return []*things.CheckListItem{{UUID: "check", Title: "Check", Index: 1}}, nil
}

func TestReadAllEntryPointsAndArguments(t *testing.T) {
	useReadFixture(t, 30)
	tools := []struct {
		name string
		args map[string]any
	}{
		{"things_list_today", nil}, {"things_list_inbox", nil}, {"things_list_anytime", nil}, {"things_list_someday", nil}, {"things_list_upcoming", nil}, {"things_list_all_tasks", nil}, {"things_list_projects", nil}, {"things_list_areas", nil}, {"things_list_tags", nil}, {"things_list_project_tasks", map[string]any{"project_uuid": "project"}}, {"things_list_area_tasks", map[string]any{"area_uuid": "area"}}, {"things_list_completed", nil}, {"things_list_checklist_items", map[string]any{"task_uuid": "task-00000"}}, {"things_search_tasks", map[string]any{"query": "Task"}},
	}
	for _, tool := range tools {
		res := registeredRead(t, tool.name, tool.args)
		var p readPage[map[string]any]
		readJSON(t, res, &p)
		if res.IsError || p.Revision == "" || p.Items == nil {
			t.Fatal(tool.name)
		}
		args := map[string]any{"limit": 0}
		for k, v := range tool.args {
			args[k] = v
		}
		res = registeredRead(t, tool.name, args)
		var out map[string]any
		readJSON(t, res, &out)
		if !res.IsError {
			t.Fatal("invalid limit accepted", tool.name)
		}
	}
	for _, tool := range []string{"things_get_task", "things_get_area", "things_get_tag", "things_get_task_text"} {
		res := registeredRead(t, tool, map[string]any{"uuid": "missing"})
		var out map[string]any
		readJSON(t, res, &out)
		if !res.IsError {
			t.Fatal("missing entity accepted", tool)
		}
	}
}
