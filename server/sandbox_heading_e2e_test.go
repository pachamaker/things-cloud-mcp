//go:build e2e && heading

package main

import (
	"encoding/json"
	things "github.com/arthursoares/things-cloud-sdk"
	"github.com/arthursoares/things-cloud-sdk/sync"
	"os"
	"reflect"
	"slices"
	"sort"
	"strings"
	"testing"
)

var headingCaptureCheck = func() error {
	data, err := os.ReadFile("../testdata/captures/heading-assignment-native.json")
	if err != nil {
		return err
	}
	var fx captureFixture
	if err := json.Unmarshal(data, &fx); err != nil {
		return err
	}
	return checkHeadingEvidence(fx)
}

// This suite is separate from nightly writes. No credentials are read until
// reviewed native evidence for both membership operations is checked.
func TestSandboxHeadingE2E(t *testing.T) {
	if err := headingCaptureCheck(); err != nil {
		t.Fatalf("KI-45: obtain/review capture-heading artifact before live heading writes: %v", err)
	}
	h := startSandboxHarness(t, harnessRun)
	t.Cleanup(func() { h.cleanupRun(t) })
	title := func(s string) string { return h.prefix + s }
	tag := h.create(t, "things_create_tag", map[string]any{"title": title("heading tag")})
	p1 := h.create(t, "things_create_project", map[string]any{"title": title("heading project A")})
	p2 := h.create(t, "things_create_project", map[string]any{"title": title("heading project B")})
	headings := []string{}
	for i, p := range []string{p1, p1, p2} {
		id := h.create(t, "things_create_heading", map[string]any{"title": title("heading " + string(rune('A'+i))), "project": p})
		headings = append(headings, id)
		got := h.getTask(t, id)
		if got.Type != "heading" || got.ProjectID != p {
			t.Fatal("created heading has wrong parent")
		}
	}
	when, deadline := h.today.AddDays(7).String(), h.today.AddDays(30).String()
	id := h.create(t, "things_create_task", map[string]any{"title": title("task under heading"), "heading": headings[0], "when": when, "deadline": deadline, "tags": tag, "note": "heading sandbox note"})
	check := func(task, hd, project string) {
		t.Helper()
		got := h.getTask(t, task)
		if got.HeadingID != hd || got.ProjectID != project || got.ScheduledFor != when || got.Deadline != deadline || got.Note != "heading sandbox note" || !slices.Equal(got.Tags, []string{tag}) {
			t.Fatalf("heading or preserved fields differ: %s", publicTask(got))
		}
		if !containsTask(h.listTasks(t, "things_list_project_tasks", map[string]any{"project_uuid": project}), task) {
			t.Fatal("task missing from destination project")
		}
	}
	check(id, headings[0], p1)
	for i, hd := range []string{headings[1], headings[2]} {
		project := []string{p1, p2}[i]
		h.act(t, "things_edit_task", map[string]any{"uuid": id, "heading": hd}, "updated")
		check(id, hd, project)
		if project == p2 && containsTask(h.listTasks(t, "things_list_project_tasks", map[string]any{"project_uuid": p1}), id) {
			t.Fatal("cross-project move left task in old project")
		}
	}
	root := h.create(t, "things_create_task", map[string]any{"title": title("root task"), "project": p1, "when": when, "deadline": deadline, "tags": tag, "note": "heading sandbox note"})
	h.act(t, "things_edit_task", map[string]any{"uuid": root, "heading": headings[0]}, "updated")
	check(root, headings[0], p1)

	st := h.observer.state(t)
	for _, tc := range []struct{ id, heading, project string }{{id, headings[2], p2}, {root, headings[0], p1}} {
		got, err := st.Task(tc.id)
		if err != nil || got == nil {
			t.Fatal("observer could not read task")
		}
		if !slices.Equal(got.ActionGroupIDs, []string{tc.heading}) || !slices.Equal(got.ParentTaskIDs, []string{tc.project}) || got.Schedule != things.TaskScheduleSomeday || calendarDay(got.ScheduledDate).Format("2006-01-02") != when || calendarDay(got.DeadlineDate).Format("2006-01-02") != deadline || got.Note != "heading sandbox note" || !slices.Equal(got.TagIDs, []string{tag}) {
			t.Fatalf("observer found incorrect membership or data: %s", publicSDKTask(got))
		}
	}
	// Exact update keys protect fields not exposed by the compact MCP response.
	for _, commit := range h.cloud.committed() {
		for _, item := range commit.Items {
			if item.Action == 1 && len(jsonIDs(item.Payload["agr"])) > 0 {
				if !reflect.DeepEqual(sortedKeys(item.Payload), []string{"agr", "ar", "md", "pr"}) {
					t.Error("heading move wrote unrelated fields")
				}
			}
		}
	}
	h.verifyWire(t)
	before := h.cloud.stats().CommitPosts
	_, isErr := h.call(t, "things_edit_task", map[string]any{"uuid": id, "heading": headings[0], "project": p2})
	if !isErr || h.cloud.stats().CommitPosts != before {
		t.Fatal("conflicting project was not refused before commit")
	}
}

func TestHeadingNativeEvidence(t *testing.T) {
	if err := headingCaptureCheck(); err != nil {
		t.Fatalf("KI-45: reviewed native fixture required before credentials: %v", err)
	}
}

// Unlike the general legacy cleanup, this entry point never includes old
// smoke-test leftovers or an automatically generated run ID.
func TestSandboxHeadingCleanup(t *testing.T) {
	runID := os.Getenv(envSandboxRunID)
	if err := validateSandboxRunID(runID); err != nil || runID == "" {
		t.Fatal("heading cleanup requires an explicit valid THINGS_E2E_RUN_ID")
	}
	h := startSandboxHarness(t, harnessCleanup)
	st := mirrorState()
	var leftovers []*things.Task
	for _, query := range []func(sync.QueryOpts) ([]*things.Task, error){st.AllTasks, st.AllHeadings, st.AllProjects} {
		tasks, err := query(sync.QueryOpts{IncludeCompleted: true})
		if err != nil {
			t.Fatal(err)
		}
		for _, task := range tasks {
			if !task.InTrash && headingRunTitle(task.Title, runID) {
				leftovers = append(leftovers, task)
			}
		}
	}
	sort.SliceStable(leftovers, func(i, j int) bool {
		return trashOrder(int64(leftovers[i].Type)) < trashOrder(int64(leftovers[j].Type))
	})
	for _, task := range leftovers {
		h.firewall.own(task.UUID, "Task6")
		h.act(t, "things_trash_task", map[string]any{"uuid": task.UUID}, "trashed")
	}
	t.Logf("heading cleanup: trashed %d objects of this exact run", len(leftovers))
}

func headingRunTitle(title, runID string) bool {
	if err := validateSandboxRunID(runID); err != nil || runID == "" {
		return false
	}
	return strings.HasPrefix(title, sandboxRunPrefix(runID))
}

func TestHeadingCleanupScope(t *testing.T) {
	t.Parallel()
	run := "e2e-heading-test"
	for _, tc := range []struct {
		title string
		want  bool
	}{{"[e2e-heading-test] task", true}, {"[e2e-heading-test-other] task", false}, {"[smoke-test] task", false}, {"SEED under heading", false}, {"ordinary private task", false}} {
		if got := headingRunTitle(tc.title, run); got != tc.want {
			t.Errorf("cleanup match = %t, want %t", got, tc.want)
		}
	}
}
