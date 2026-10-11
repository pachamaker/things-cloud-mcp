package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
)

var writeToolNames = []string{
	"things_create_task", "things_complete_task", "things_edit_task", "things_trash_task",
	"things_move_to_today", "things_move_to_anytime", "things_move_to_someday", "things_move_to_inbox",
	"things_uncomplete_task", "things_untrash_task",
	"things_create_area", "things_create_tag", "things_create_heading", "things_create_project",
	"things_create_checklist_item", "things_complete_checklist_item",
	"things_uncomplete_checklist_item", "things_delete_checklist_item",
}

func listToolNames(t *testing.T, writes bool) []string {
	t.Helper()
	body := `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	rec := httptest.NewRecorder()
	newMCPHandler(writes).ServeHTTP(rec, withAccessRequest(req, access{write: true}))
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
	var names []string
	for _, tool := range resp.Result.Tools {
		names = append(names, tool.Name)
	}
	return names
}

func TestNewMCPHandler_ADR0004_ReadOnlyListsNoWriteTools(t *testing.T) {
	t.Parallel()

	readOnly := listToolNames(t, false)
	for _, name := range writeToolNames {
		if slices.Contains(readOnly, name) {
			t.Errorf("read-only server lists write tool %s", name)
		}
	}
	if !slices.Contains(readOnly, "things_list_today") || !slices.Contains(readOnly, "things_search_tasks") {
		t.Errorf("read-only server lost read tools: %v", readOnly)
	}

	all := listToolNames(t, true)
	for _, name := range writeToolNames {
		if !slices.Contains(all, name) {
			t.Errorf("server with writes enabled does not list %s", name)
		}
	}
	if len(all) != len(readOnly)+len(writeToolNames) {
		t.Errorf("tools with writes = %d, want %d read + %d write", len(all), len(readOnly), len(writeToolNames))
	}
}

func TestWritesEnabled_ADR0004(t *testing.T) {
	t.Parallel()

	tests := []struct {
		raw     string
		want    bool
		wantErr bool
	}{
		{"", false, false},
		{"false", false, false},
		{"0", false, false},
		{"true", true, false},
		{"TRUE", true, false},
		{"1", true, false},
		{"yes", false, true},
		{"on", false, true},
	}
	for _, tt := range tests {
		t.Run(tt.raw, func(t *testing.T) {
			t.Parallel()
			got, err := writesEnabled(tt.raw)
			if (err != nil) != tt.wantErr {
				t.Fatalf("writesEnabled(%q) error = %v, wantErr %v", tt.raw, err, tt.wantErr)
			}
			if got != tt.want {
				t.Errorf("writesEnabled(%q) = %v, want %v", tt.raw, got, tt.want)
			}
		})
	}
}
