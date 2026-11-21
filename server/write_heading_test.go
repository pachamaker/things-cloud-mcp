package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	things "github.com/arthursoares/things-cloud-sdk"
	"github.com/mark3labs/mcp-go/mcp"
)

// Intercept before a commit can leave the process. These are contract tests,
// not a native-client capture or a Things Cloud round trip.
func interceptHeadingWrite(t *testing.T, tasks map[string]*things.Task, call func() error) map[string]json.RawMessage {
	t.Helper()
	oldTransport, oldMirror, oldHistory := http.DefaultTransport, writeMirror, history
	t.Cleanup(func() { http.DefaultTransport, writeMirror, history = oldTransport, oldMirror, oldHistory })
	writeMirror = func() (mirrorLookup, error) { return fakeMirror{tasks: tasks}, nil }
	stop := errors.New("offline heading commit intercepted")
	var payload map[string]json.RawMessage
	http.DefaultTransport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.Method == http.MethodGet && strings.HasSuffix(req.URL.Path, "/items") {
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"items":[],"current-item-index":5,"schema":301}`))}, nil
		}
		if req.Method != http.MethodPost || !strings.HasSuffix(req.URL.Path, "/commit") {
			t.Fatalf("unexpected request: %s", req.Method)
		}
		var commit map[string]struct {
			Payload map[string]json.RawMessage `json:"p"`
		}
		if err := json.NewDecoder(req.Body).Decode(&commit); err != nil {
			t.Fatal(err)
		}
		if len(commit) != 1 {
			t.Fatalf("commit entities = %d", len(commit))
		}
		for _, entity := range commit {
			payload = entity.Payload
		}
		return nil, stop
	})
	history = things.New(things.APIEndpoint, ki35Email, "not-a-real-password").HistoryWithID(ki35HistoryKey)
	if err := call(); !errors.Is(err, stop) && (err == nil || !strings.Contains(err.Error(), stop.Error())) {
		t.Fatalf("expected intercepted commit, got %v", err)
	}
	if payload == nil {
		t.Fatal("missing intercepted payload")
	}
	return payload
}

func headingFixtures() (map[string]*things.Task, string, string, string) {
	task, project, heading := generateUUID(), generateUUID(), generateUUID()
	date := time.Date(2026, 12, 2, 0, 0, 0, 0, time.UTC)
	return map[string]*things.Task{
		task:    {UUID: task, Type: things.TaskTypeTask, Schedule: things.TaskScheduleSomeday, ScheduledDate: &date, DeadlineDate: &date, Note: "keep note", TagIDs: []string{generateUUID()}, ParentTaskIDs: []string{generateUUID()}, ActionGroupIDs: []string{generateUUID()}},
		project: {UUID: project, Type: things.TaskTypeProject},
		heading: {UUID: heading, Type: things.TaskTypeHeading, ParentTaskIDs: []string{project}},
	}, task, project, heading
}

func assertHeadingParent(t *testing.T, p map[string]json.RawMessage, key, id string) {
	t.Helper()
	var got []string
	if err := json.Unmarshal(p[key], &got); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, []string{id}) {
		t.Fatalf("%s=%s, want [%q]", key, p[key], id)
	}
}

func TestHeadingEditPreservesUnspecifiedFields(t *testing.T) {
	for _, placement := range []string{"same project", "other project", "inbox", "today", "anytime", "someday"} {
		t.Run(placement, func(t *testing.T) {
			tasks, id, project, heading := headingFixtures()
			switch placement {
			case "same project":
				tasks[id].ParentTaskIDs = []string{project}
			case "inbox":
				tasks[id].Schedule = things.TaskScheduleInbox
			case "today":
				tasks[id].Schedule = things.TaskScheduleAnytime
			case "anytime":
				tasks[id].Schedule = things.TaskScheduleAnytime
				tasks[id].ScheduledDate = nil
			case "someday":
				tasks[id].ScheduledDate = nil
			}
			p := interceptHeadingWrite(t, tasks, func() error { return editTask(EditTaskRequest{UUID: id, Heading: heading}) })
			assertHeadingParent(t, p, "agr", heading)
			assertHeadingParent(t, p, "pr", project)
			for _, key := range []string{"st", "sr", "tir", "dd", "dds", "sb", "tg", "nt", "tt", "ix", "ti", "rr", "rt"} {
				if _, ok := p[key]; ok {
					t.Errorf("heading-only edit unexpectedly writes %s", key)
				}
			}
			if len(p) != 4 {
				t.Errorf("payload keys = %v; want only agr, pr, ar, md", p)
			}
			if string(p["ar"]) != "[]" {
				t.Errorf("area not cleared: %s", p["ar"])
			}
		})
	}
}

func TestHeadingCreate(t *testing.T) {
	for _, explicitProject := range []bool{false, true} {
		t.Run(map[bool]string{false: "infer project", true: "matching project"}[explicitProject], func(t *testing.T) {
			tasks, _, project, heading := headingFixtures()
			req := CreateTaskRequest{Title: "example", Note: "note", When: "someday", Heading: heading}
			if explicitProject {
				req.Project = project
			}
			p := interceptHeadingWrite(t, tasks, func() error { _, err := createTask(req); return err })
			assertHeadingParent(t, p, "agr", heading)
			assertHeadingParent(t, p, "pr", project)
			if string(p["st"]) != "2" {
				t.Errorf("explicit schedule changed: %s", p["st"])
			}
			var note wireNote
			if err := json.Unmarshal(p["nt"], &note); err != nil || note.Value != "note" {
				t.Fatalf("note = %s", p["nt"])
			}
		})
	}
	t.Run("default anytime", func(t *testing.T) {
		tasks, _, _, heading := headingFixtures()
		p := interceptHeadingWrite(t, tasks, func() error { _, err := createTask(CreateTaskRequest{Title: "example", Heading: heading}); return err })
		if string(p["st"]) != "1" {
			t.Errorf("default schedule=%s", p["st"])
		}
	})
}

func TestHeadingRejectsInvalidPlacementBeforeCommit(t *testing.T) {
	for _, name := range []string{"malformed", "none", "missing heading", "wrong type", "trashed heading", "completed heading", "orphan heading", "missing project", "trashed project", "wrong project type", "conflicting project", "parent task", "area", "repeating target"} {
		t.Run(name, func(t *testing.T) {
			tasks, id, project, heading := headingFixtures()
			req := EditTaskRequest{UUID: id, Heading: heading}
			switch name {
			case "malformed":
				req.Heading = "not-a-uuid"
			case "none":
				req.Heading = "none"
			case "missing heading":
				delete(tasks, heading)
			case "wrong type":
				tasks[heading].Type = things.TaskTypeTask
			case "trashed heading":
				tasks[heading].InTrash = true
			case "completed heading":
				tasks[heading].Status = things.TaskStatusCompleted
			case "orphan heading":
				tasks[heading].ParentTaskIDs = nil
			case "missing project":
				delete(tasks, project)
			case "trashed project":
				tasks[project].InTrash = true
			case "wrong project type":
				tasks[project].Type = things.TaskTypeTask
			case "conflicting project":
				req.Project = generateUUID()
				tasks[req.Project] = &things.Task{Type: things.TaskTypeProject}
			case "parent task":
				req.ParentTask = id
			case "area":
				req.Area = generateUUID()
			case "repeating target":
				tasks[id].RecurrenceIDs = []string{generateUUID()}
			}
			oldMirror, oldTransport := writeMirror, http.DefaultTransport
			t.Cleanup(func() { writeMirror, http.DefaultTransport = oldMirror, oldTransport })
			writeMirror = func() (mirrorLookup, error) { return fakeMirror{tasks: tasks}, nil }
			http.DefaultTransport = roundTripFunc(func(*http.Request) (*http.Response, error) {
				t.Fatal("invalid heading attempted network write")
				return nil, errors.New("forbidden")
			})
			if err := editTask(req); err == nil || !isInvalidInput(err) {
				t.Errorf("edit error=%v", err)
			}
			if name != "repeating target" && name != "area" {
				_, err := createTask(CreateTaskRequest{Title: "example", Heading: req.Heading, Project: req.Project, ParentTask: req.ParentTask})
				if name != "area" && (err == nil || !isInvalidInput(err)) {
					t.Errorf("create error=%v", err)
				}
			}
		})
	}
}

func TestHeadingMCPForwarding(t *testing.T) {
	for _, name := range []string{"create", "edit"} {
		t.Run(name, func(t *testing.T) {
			tasks, id, _, heading := headingFixtures()
			req := mcp.CallToolRequest{}
			req.Params.Arguments = map[string]any{"uuid": id, "title": "example", "heading": heading}
			p := interceptHeadingWrite(t, tasks, func() error {
				var result *mcp.CallToolResult
				var err error
				if name == "create" {
					result, err = mcpCreateTask(context.Background(), req)
				} else {
					result, err = mcpEditTask(context.Background(), req)
				}
				if err != nil {
					return err
				}
				if result == nil || !result.IsError {
					return errors.New("expected intercepted MCP write error")
				}
				for _, c := range result.Content {
					if text, ok := c.(mcp.TextContent); ok {
						return errors.New(text.Text)
					}
				}
				return errors.New("missing MCP error text")
			})
			assertHeadingParent(t, p, "agr", heading)
		})
	}
}

func TestHeadingEditExplicitChanges(t *testing.T) {
	tasks, id, _, heading := headingFixtures()
	tag := generateUUID()
	// The interceptor's mirror is extended for explicitly requested tags.
	p := interceptHeadingWrite(t, tasks, func() error {
		writeMirror = func() (mirrorLookup, error) {
			return fakeMirror{tasks: tasks, tags: map[string]*things.Tag{tag: {UUID: tag}}}, nil
		}
		return editTask(EditTaskRequest{UUID: id, Heading: heading, When: "someday", Note: "replacement", Tags: tag, Deadline: "none"})
	})
	if string(p["st"]) != "2" || string(p["dd"]) != "null" {
		t.Errorf("explicit dates ignored: %v", p)
	}
	assertHeadingParent(t, p, "tg", tag)
	var note wireNote
	if err := json.Unmarshal(p["nt"], &note); err != nil || note.Value != "replacement" {
		t.Fatalf("note=%s", p["nt"])
	}
}

func TestHeadingMCPSchema(t *testing.T) {
	t.Parallel()
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	rec := httptest.NewRecorder()
	newMCPHandler(true).ServeHTTP(rec, withAccessRequest(req, access{write: true}))
	var resp struct {
		Result struct {
			Tools []mcp.Tool `json:"tools"`
		} `json:"result"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	found := 0
	for _, tool := range resp.Result.Tools {
		if tool.Name != "things_create_task" && tool.Name != "things_edit_task" {
			continue
		}
		found++
		prop, ok := tool.InputSchema.Properties["heading"].(map[string]any)
		if !ok || prop["type"] != "string" {
			t.Errorf("%s missing heading string schema", tool.Name)
		}
		for _, field := range tool.InputSchema.Required {
			if field == "heading" {
				t.Errorf("%s heading unexpectedly required", tool.Name)
			}
		}
	}
	if found != 2 {
		t.Fatalf("found %d create/edit tools", found)
	}
}
