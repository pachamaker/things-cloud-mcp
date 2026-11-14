package main

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	things "github.com/arthursoares/things-cloud-sdk"
	"github.com/mark3labs/mcp-go/mcp"
)

// Call registered tools through HTTP to include middleware, tool registration,
// decoding and the same entry point remote clients use, without opening a socket.
func registeredRead(t *testing.T, tool string, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{"name": tool, "arguments": args}})
	req := httptest.NewRequest("POST", "/mcp", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req = req.WithContext(withAccess(req.Context(), access{}))
	rec := httptest.NewRecorder()
	newMCPHandler(false).ServeHTTP(rec, req)
	text := rec.Body.String()
	if strings.Contains(text, "data: ") {
		for _, line := range strings.Split(text, "\n") {
			if strings.HasPrefix(line, "data: ") {
				text = strings.TrimPrefix(line, "data: ")
				break
			}
		}
	}
	var response struct {
		Result json.RawMessage `json:"result"`
		Error  any             `json:"error"`
	}
	if err := json.Unmarshal([]byte(text), &response); err != nil || response.Error != nil {
		t.Fatalf("HTTP %d: %s (%v)", rec.Code, text, err)
	}
	var wire struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	}
	if err := json.Unmarshal(response.Result, &wire); err != nil {
		t.Fatal(err)
	}
	if len(wire.Content) != 1 {
		t.Fatalf("missing tool result: %s", text)
	}
	res := mcp.NewToolResultText(wire.Content[0].Text)
	res.IsError = wire.IsError
	return res
}

func TestReadPageWalk247And10000(t *testing.T) {
	for _, n := range []int{247, 10000} {
		t.Run(fmt.Sprint(n), func(t *testing.T) {
			useReadFixture(t, n)
			seen := map[string]bool{}
			offset := 0
			rev := ""
			for {
				res := registeredRead(t, "things_list_all_tasks", map[string]any{"offset": offset, "revision": rev, "limit": 100})
				var p readPage[taskSummaryOutput]
				text := readJSON(t, res, &p)
				if res.IsError || p.Total != n || strings.Contains(text, "private note") {
					t.Fatal(text)
				}
				for _, x := range p.Items {
					if seen[x.UUID] {
						t.Fatal("duplicate", x.UUID)
					}
					seen[x.UUID] = true
				}
				if !p.HasMore {
					break
				}
				if p.Returned == 0 || p.NextOffset == nil {
					t.Fatal("no progress")
				}
				offset = *p.NextOffset
				rev = p.Revision
			}
			if len(seen) != n {
				t.Fatalf("got %d want %d", len(seen), n)
			}
		})
	}
}
func TestReadWalkRestartsAfterMutation(t *testing.T) {
	s := useReadFixture(t, 30)
	first := registeredRead(t, "things_list_inbox", nil)
	var p readPage[taskSummaryOutput]
	readJSON(t, first, &p)
	s.Identity.cursor++
	res := registeredRead(t, "things_list_inbox", map[string]any{"offset": *p.NextOffset, "revision": p.Revision})
	var e struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	readJSON(t, res, &e)
	if !res.IsError || e.Error.Code != "stale_page" {
		t.Fatalf("bad stale error %+v", e)
	}
}
func TestCoherentReadDuringSyncAndRebuild(t *testing.T) {
	useReadFixture(t, 1)
	entered, release := make(chan struct{}), make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- mirror.withRead(func(readModel, readRevisionState) error { close(entered); <-release; return nil })
	}()
	<-entered
	var acquired atomic.Bool
	locked := make(chan struct{})
	go func() {
		mirror.mu.Lock()
		acquired.Store(true)
		mirror.rebuilding = true
		mirror.mu.Unlock()
		close(locked)
	}()
	// The capture must still own the same mutex until its query finishes.
	if mirror.mu.TryLock() {
		mirror.mu.Unlock()
		t.Fatal("read did not exclude sync/rebuild")
	}
	if acquired.Load() {
		t.Fatal("writer entered capture")
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	<-locked
	called := false
	if err := mirror.withRead(func(readModel, readRevisionState) error { called = true; return nil }); err == nil || called {
		t.Fatal("read allowed during rebuild")
	}
}

func TestReadSearchScopesAndVisibility(t *testing.T) {
	useReadFixture(t, 0)
	at := time.Now()
	tasks := listMirror{
		{UUID: "open", Title: "needle", Status: things.TaskStatusPending},
		{UUID: "done", Note: "needle", Status: things.TaskStatusCompleted, CompletionDate: &at},
		{UUID: "canceled", Title: "needle", Status: things.TaskStatusCanceled},
		{UUID: "trash", Title: "needle", Status: things.TaskStatusPending, InTrash: true},
	}
	r := readModel{mirror: tasks}
	for scope, want := range map[string]int{"open": 1, "completed": 1, "all": 2} {
		got, _, err := readTaskSelection(r, readQuery{Tool: "things_search_tasks", Filters: map[string]string{"query": "needle", "scope": scope}}, ownerToday())
		if err != nil || len(got) != want {
			t.Fatalf("%s %+v %v", scope, got, err)
		}
	}
}
