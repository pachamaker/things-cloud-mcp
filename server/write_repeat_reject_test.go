package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
)

// Recurrence writes are forbidden (KI-01). The server globals `history` and
// `syncer` stay nil in this test binary, so any request that slipped past the
// guard and reached historyWrite() would panic instead of returning an error.

var ki01RepeatValues = []string{
	"daily",
	"every 3 weeks",
	"daily until 2026-02-24 after completion",
	"garbage-value",
	"none",
	"None",
	" ",
}

func assertRepeatRejected(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !isInvalidInput(err) {
		t.Fatalf("expected invalid-input error, got: %v", err)
	}
	msg := err.Error()
	if !strings.Contains(msg, "repeat") || !strings.Contains(msg, "KI-01") {
		t.Fatalf("expected error naming repeat and KI-01, got: %v", err)
	}
}

func TestCreateTask_KI01_RejectsAnyRepeat(t *testing.T) {
	t.Parallel()

	for _, repeat := range ki01RepeatValues {
		t.Run(repeat, func(t *testing.T) {
			t.Parallel()

			uuid, err := createTask(CreateTaskRequest{Title: "Test task", When: "anytime", Repeat: repeat})
			assertRepeatRejected(t, err)
			if uuid != "" {
				t.Fatalf("expected empty uuid on rejection, got %q", uuid)
			}
		})
	}
}

func TestEditTask_KI01_RejectsAnyRepeat(t *testing.T) {
	t.Parallel()

	for _, repeat := range ki01RepeatValues {
		t.Run(repeat, func(t *testing.T) {
			t.Parallel()

			err := editTask(EditTaskRequest{UUID: generateUUID(), Title: "Renamed", Repeat: repeat})
			assertRepeatRejected(t, err)
		})
	}
}

func TestRESTWrites_KI01_RejectRepeat(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		handler http.HandlerFunc
		body    string
	}{
		{"create daily", handleCreateTask, `{"title":"Test task","repeat":"daily"}`},
		{"create none", handleCreateTask, `{"title":"Test task","repeat":"none"}`},
		{"edit weekly", handleEditTask, `{"uuid":"` + generateUUID() + `","repeat":"weekly"}`},
		{"edit none", handleEditTask, `{"uuid":"` + generateUUID() + `","repeat":"none"}`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			req := httptest.NewRequest(http.MethodPost, "/api/tasks", strings.NewReader(tc.body))
			rec := httptest.NewRecorder()
			tc.handler(rec, req)

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d (body %s)", rec.Code, http.StatusBadRequest, rec.Body.String())
			}
			var resp map[string]string
			if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if !strings.Contains(resp["error"], "KI-01") {
				t.Fatalf("expected KI-01 in error, got %q", resp["error"])
			}
		})
	}
}

func TestMCPWrites_KI01_RejectRepeat(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		handler func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error)
		args    map[string]any
	}{
		{"create daily", mcpCreateTask, map[string]any{"title": "Test task", "repeat": "daily"}},
		{"create none", mcpCreateTask, map[string]any{"title": "Test task", "repeat": "none"}},
		{"edit monthly", mcpEditTask, map[string]any{"uuid": generateUUID(), "repeat": "monthly"}},
		{"edit none", mcpEditTask, map[string]any{"uuid": generateUUID(), "repeat": "none"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var req mcp.CallToolRequest
			req.Params.Arguments = tc.args
			result, err := tc.handler(context.Background(), req)
			if err != nil {
				t.Fatalf("handler returned error: %v", err)
			}
			if !result.IsError {
				t.Fatal("expected error result")
			}
			text, ok := result.Content[0].(mcp.TextContent)
			if !ok {
				t.Fatalf("expected text content, got %T", result.Content[0])
			}
			if !strings.Contains(text.Text, "KI-01") {
				t.Fatalf("expected KI-01 in error, got %q", text.Text)
			}
		})
	}
}

func TestMCPToolSchemas_KI01_NoRepeatParameter(t *testing.T) {
	t.Parallel()

	body := `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	rec := httptest.NewRecorder()
	newMCPHandler(true).ServeHTTP(rec, withAccessRequest(req, access{write: true}))

	if rec.Code != http.StatusOK {
		t.Fatalf("tools/list status = %d, body %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Result struct {
			Tools []mcp.Tool `json:"tools"`
		} `json:"result"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode tools/list: %v (body %s)", err, rec.Body.String())
	}

	seen := map[string]bool{}
	for _, tool := range resp.Result.Tools {
		if _, ok := tool.InputSchema.Properties["repeat"]; ok {
			t.Errorf("tool %s still exposes a repeat parameter", tool.Name)
		}
		seen[tool.Name] = true
	}
	for _, name := range []string{"things_create_task", "things_edit_task"} {
		if !seen[name] {
			t.Errorf("tools/list did not return %s", name)
		}
	}
}
