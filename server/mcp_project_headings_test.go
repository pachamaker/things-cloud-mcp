package main

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	things "github.com/arthursoares/things-cloud-sdk"
)

func useProjectHeadingFixture(t *testing.T, tasks listMirror) *readRevisionState {
	s := useReadFixture(t, 0)
	mirror.readSource = func() (readModel, readRevisionState, error) {
		return readModel{mirror: catalogReadMirror{tasks}}, *s, nil
	}
	return s
}

func TestProjectHeadings_KI13_Pagination(t *testing.T) {
	fixture := headingMirror()
	fixture = append(fixture, things.Task{UUID: "empty", Title: "Empty", Type: things.TaskTypeHeading, ParentTaskIDs: []string{"p"}, Index: 0})
	state := useProjectHeadingFixture(t, fixture)
	tasks, _, err := (readModel{mirror: catalogReadMirror{fixture}}).TasksInProject("p", listOpts{})
	if err != nil {
		t.Fatal(err)
	}
	want := []areaOutput{{UUID: "empty", Title: "Empty"}, {UUID: "h-first", Title: "First"}, {UUID: "h-second", Title: "Second"}, {UUID: "h-archived", Title: "Archived"}}
	seen := []string{}
	offset, rev := 0, ""
	for {
		res := registeredRead(t, "things_list_project_tasks", map[string]any{"project_uuid": "p", "limit": 2, "offset": offset, "revision": rev})
		var page readPage[taskSummaryOutput]
		text := readJSON(t, res, &page)
		var extra struct {
			Headings []areaOutput `json:"headings"`
		}
		if err := json.Unmarshal([]byte(text), &extra); err != nil {
			t.Fatal(err)
		}
		if res.IsError || !reflect.DeepEqual(extra.Headings, want) || page.Total != len(tasks) || page.Returned != len(page.Items) {
			t.Fatal(text)
		}
		if rev != "" && page.Revision != rev {
			t.Fatal("revision changed")
		}
		for _, task := range page.Items {
			seen = append(seen, task.UUID)
		}
		if !page.HasMore {
			break
		}
		if page.NextOffset == nil || *page.NextOffset != offset+page.Returned {
			t.Fatal(text)
		}
		offset, rev = *page.NextOffset, page.Revision
	}
	expected := []string{}
	for _, task := range tasks {
		expected = append(expected, task.UUID)
	}
	if !reflect.DeepEqual(seen, expected) {
		t.Fatalf("got %v want %v", seen, expected)
	}
	state.Identity.cursor++
	res := registeredRead(t, "things_list_project_tasks", map[string]any{"project_uuid": "p", "offset": 2, "revision": rev})
	var failure map[string]any
	text := readJSON(t, res, &failure)
	if !res.IsError || !strings.Contains(text, "stale_page") {
		t.Fatal(text)
	}
}

func TestProjectHeadings_KI13_EmptyAndOtherTools(t *testing.T) {
	useProjectHeadingFixture(t, listMirror{{UUID: "empty", Title: "Empty", Type: things.TaskTypeHeading, ParentTaskIDs: []string{"p"}}})
	for _, tc := range []struct{ tool, project, want string }{
		{"things_list_project_tasks", "p", `[{"uuid":"empty","title":"Empty"}]`},
		{"things_list_project_tasks", "missing", `[]`},
		{"things_list_all_tasks", "", ``},
	} {
		args := map[string]any{}
		if tc.project != "" {
			args["project_uuid"] = tc.project
		}
		res := registeredRead(t, tc.tool, args)
		var got map[string]json.RawMessage
		text := readJSON(t, res, &got)
		if res.IsError {
			t.Fatal(text)
		}
		if string(got["headings"]) != tc.want {
			t.Fatalf("headings=%s want %s", got["headings"], tc.want)
		}
	}
}

func TestProjectHeadings_KI13_ByteBudget(t *testing.T) {
	for _, oversized := range []bool{false, true} {
		title := strings.Repeat("界", 1000)
		if oversized {
			title = strings.Repeat("界", 10000)
		}
		useProjectHeadingFixture(t, listMirror{
			{UUID: "h", Title: title, Type: things.TaskTypeHeading, ParentTaskIDs: []string{"p"}},
			{UUID: "todo", Title: strings.Repeat("長", 10000), Type: things.TaskTypeTask, ParentTaskIDs: []string{"p"}},
		})
		res := registeredRead(t, "things_list_project_tasks", map[string]any{"project_uuid": "p"})
		var got map[string]json.RawMessage
		text := readJSON(t, res, &got)
		if oversized {
			if !res.IsError || !strings.Contains(text, `"field":"headings"`) {
				t.Fatal(text)
			}
		} else {
			if res.IsError || !strings.Contains(text, `"truncated_fields":["title"]`) {
				t.Fatal(text)
			}
		}
	}
}

func TestProjectHeadings_KI13_ByteLimitedWalk(t *testing.T) {
	fixture := listMirror{{UUID: "h", Title: strings.Repeat("H", 2000), Type: things.TaskTypeHeading, ParentTaskIDs: []string{"p"}}}
	for i := 0; i < 30; i++ {
		fixture = append(fixture, things.Task{UUID: fmt.Sprintf("t%02d", i), Title: strings.Repeat("T", 1000), Type: things.TaskTypeTask, ParentTaskIDs: []string{"p"}, Index: i})
	}
	useProjectHeadingFixture(t, fixture)
	offset, rev := 0, ""
	seen := map[string]bool{}
	byteLimited := false
	for {
		res := registeredRead(t, "things_list_project_tasks", map[string]any{"project_uuid": "p", "limit": 100, "offset": offset, "revision": rev})
		var page readPage[taskSummaryOutput]
		text := readJSON(t, res, &page)
		if res.IsError || page.Total != 30 || page.Returned == 0 {
			t.Fatal(text)
		}
		for _, task := range page.Items {
			if seen[task.UUID] {
				t.Fatal("duplicate", task.UUID)
			}
			seen[task.UUID] = true
		}
		if !page.HasMore {
			break
		}
		if page.PageLimitedBy == nil || *page.PageLimitedBy != "bytes" || page.NextOffset == nil || *page.NextOffset != offset+page.Returned {
			t.Fatal(text)
		}
		byteLimited = true
		offset, rev = *page.NextOffset, page.Revision
	}
	if !byteLimited || len(seen) != 30 {
		t.Fatalf("byteLimited=%v tasks=%d", byteLimited, len(seen))
	}
}
