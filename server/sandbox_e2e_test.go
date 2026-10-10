//go:build e2e

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"sort"
	"strings"
	"testing"

	things "github.com/arthursoares/things-cloud-sdk"
	"github.com/arthursoares/things-cloud-sdk/sync"
	"github.com/mark3labs/mcp-go/mcp"
)

// TestSandboxE2E drives every MCP tool against the sandbox account, checks
// read-after-write view membership, the 409 retry, and what landed on the
// wire, then trashes what it created. docs/testing/sandbox-e2e.md.
func TestSandboxE2E(t *testing.T) {
	h := startSandboxHarness(t, harnessRun)
	t.Cleanup(func() { h.cleanupRun(t) })
	t.Logf("title prefix %q", h.prefix)

	day := func(offset int) string { return h.today.AddDate(0, 0, offset).Format("2006-01-02") }
	today, upcoming, deadline := day(0), day(7), day(30)
	title := func(s string) string { return h.prefix + s }
	id := map[string]string{}

	if !t.Run("setup", func(t *testing.T) {
		id["tag"] = h.create(t, "things_create_tag", map[string]any{"title": title("tag")})
		id["area"] = h.create(t, "things_create_area", map[string]any{"title": title("area"), "tags": id["tag"]})
		id["project"] = h.create(t, "things_create_project", map[string]any{
			"title": title("project"), "note": "e2e project note", "when": "anytime", "deadline": deadline, "area": id["area"],
		})
		id["heading"] = h.create(t, "things_create_heading", map[string]any{"title": title("heading"), "project": id["project"]})
		id["task"] = h.create(t, "things_create_task", map[string]any{
			"title": title("task in project"), "note": "e2e task note", "when": "today",
			"deadline": deadline, "project": id["project"], "tags": id["tag"],
		})
		id["subtask"] = h.create(t, "things_create_task", map[string]any{"title": title("subtask"), "parent_task": id["task"]})

		task := h.getTask(t, id["task"])
		want := taskOutput{
			UUID: id["task"], Title: title("task in project"), Note: "e2e task note", Status: "open", Type: "task",
			Deadline: deadline, ScheduledFor: today, ProjectID: id["project"], Tags: []string{id["tag"]},
		}
		if !taskOutputsEqual(task, want) {
			t.Errorf("task = %s, want %s", publicTask(task), publicTask(want))
		}
		if sub := h.getTask(t, id["subtask"]); sub.ProjectID != id["task"] || sub.Status != "open" {
			t.Errorf("subtask = %s, want parent %s", publicTask(sub), id["task"])
		}
		if p := h.getTask(t, id["project"]); p.Type != "project" || p.AreaID != id["area"] || p.Deadline != deadline || p.Note != "e2e project note" {
			t.Errorf("project = %s", publicTask(p))
		}
		if hd := h.getTask(t, id["heading"]); hd.Type != "heading" || hd.ProjectID != id["project"] {
			t.Errorf("heading = %s", publicTask(hd))
		}
		area := decodeAs[areaOutput](t, "things_get_area", h.mustCall(t, "things_get_area", map[string]any{"uuid": id["area"]}))
		if area.Title != title("area") {
			t.Errorf("area %s: title differs from the one written", id["area"])
		}
		tag := decodeAs[tagOutput](t, "things_get_tag", h.mustCall(t, "things_get_tag", map[string]any{"uuid": id["tag"]}))
		if tag.Title != title("tag") {
			t.Errorf("tag %s: title differs from the one written", id["tag"])
		}
	}) {
		return
	}

	t.Run("placement", func(t *testing.T) {
		for _, tc := range []struct{ label, when, view, scheduled string }{
			{"inbox", "", "inbox", ""},
			{"today", "today", "today", today},
			{"anytime", "anytime", "anytime", ""},
			{"someday", "someday", "someday", ""},
			{"upcoming", upcoming, "upcoming", upcoming},
		} {
			args := map[string]any{"title": title("placement " + tc.label)}
			if tc.when != "" {
				args["when"] = tc.when
			}
			uuid := h.create(t, "things_create_task", args)
			id["placement "+tc.label] = uuid
			h.assertOnlyIn(t, uuid, tc.view)
			if got := h.getTask(t, uuid).ScheduledFor; got != tc.scheduled {
				t.Errorf("placement %s: scheduled_for = %q, want %q", tc.label, got, tc.scheduled)
			}
		}

		moved := h.create(t, "things_create_task", map[string]any{"title": title("moved")})
		id["moved"] = moved
		for _, step := range []struct {
			tool   string
			args   map[string]any
			status string
			view   string
		}{
			{"things_move_to_today", map[string]any{"uuid": moved}, "moved_to_today", "today"},
			{"things_move_to_anytime", map[string]any{"uuid": moved}, "moved_to_anytime", "anytime"},
			{"things_move_to_someday", map[string]any{"uuid": moved}, "moved_to_someday", "someday"},
			{"things_edit_task", map[string]any{"uuid": moved, "when": upcoming}, "updated", "upcoming"},
			{"things_move_to_inbox", map[string]any{"uuid": moved}, "moved_to_inbox", "inbox"},
		} {
			h.act(t, step.tool, step.args, step.status)
			h.assertOnlyIn(t, moved, step.view)
		}
	})

	t.Run("edit", func(t *testing.T) {
		e := h.create(t, "things_create_task", map[string]any{"title": title("edit"), "when": "anytime"})
		id["edit"] = e
		edit := func(args map[string]any) taskOutput {
			t.Helper()
			args["uuid"] = e
			h.act(t, "things_edit_task", args, "updated")
			return h.getTask(t, e)
		}
		if got := edit(map[string]any{"title": title("edit renamed"), "note": "e2e edited note"}); got.Title != title("edit renamed") || got.Note != "e2e edited note" {
			t.Errorf("after title and note: %s", publicTask(got))
		}
		if got := edit(map[string]any{"note": "none"}); got.Note != "" {
			t.Errorf("note not cleared: %s", publicTask(got))
		}
		if got := edit(map[string]any{"deadline": deadline}); got.Deadline != deadline {
			t.Errorf("deadline = %q, want %q", got.Deadline, deadline)
		}
		if got := edit(map[string]any{"deadline": "none"}); got.Deadline != "" {
			t.Errorf("deadline not cleared: %s", publicTask(got))
		}
		if got := edit(map[string]any{"area": id["area"]}); got.AreaID != id["area"] {
			t.Errorf("area_id = %q, want %q", got.AreaID, id["area"])
		}
		if !containsTask(h.listTasks(t, "things_list_area_tasks", map[string]any{"area_uuid": id["area"]}), e) {
			t.Error("things_list_area_tasks does not list the task moved into the area")
		}
		if got := edit(map[string]any{"area": "none"}); got.AreaID != "" {
			t.Errorf("area not cleared: %s", publicTask(got))
		}
		if got := edit(map[string]any{"tags": id["tag"]}); !slices.Equal(got.Tags, []string{id["tag"]}) {
			t.Errorf("tags = %v, want [%s]", got.Tags, id["tag"])
		}
		if got := edit(map[string]any{"project": id["project"]}); got.ProjectID != id["project"] {
			t.Errorf("project_id = %q, want %q", got.ProjectID, id["project"])
		}
		if !containsTask(h.listTasks(t, "things_list_project_tasks", map[string]any{"project_uuid": id["project"]}), e) {
			t.Error("things_list_project_tasks does not list the task moved into the project")
		}
	})

	t.Run("lifecycle", func(t *testing.T) {
		c := h.create(t, "things_create_task", map[string]any{"title": title("lifecycle"), "when": "anytime"})
		id["lifecycle"] = c

		h.act(t, "things_complete_task", map[string]any{"uuid": c}, "completed")
		if got := h.getTask(t, c); got.Status != "completed" || got.CompletedAt == "" {
			t.Errorf("after complete: %s", publicTask(got))
		}
		done := h.listTasks(t, "things_list_completed", map[string]any{"limit": 200, "completed_after": today})
		if !containsTask(done, c) {
			t.Error("things_list_completed does not list the completed task")
		}
		if views := h.viewsOf(t, c); len(views) != 0 {
			t.Errorf("completed task still listed in %v", views)
		}

		h.act(t, "things_uncomplete_task", map[string]any{"uuid": c}, "uncompleted")
		if got := h.getTask(t, c); got.Status != "open" || got.CompletedAt != "" {
			t.Errorf("after uncomplete: %s", publicTask(got))
		}
		h.assertOnlyIn(t, c, "anytime")

		h.act(t, "things_trash_task", map[string]any{"uuid": c}, "trashed")
		if views := h.viewsOf(t, c); len(views) != 0 {
			t.Errorf("trashed task still listed in %v", views)
		}
		if containsTask(h.listTasks(t, "things_list_all_tasks", nil), c) {
			t.Error("things_list_all_tasks lists a trashed task")
		}

		h.act(t, "things_untrash_task", map[string]any{"uuid": c}, "restored")
		h.assertOnlyIn(t, c, "anytime")
		if !containsTask(h.listTasks(t, "things_list_all_tasks", nil), c) {
			t.Error("things_list_all_tasks does not list the restored task")
		}
	})

	t.Run("checklist", func(t *testing.T) {
		item := h.create(t, "things_create_checklist_item", map[string]any{"title": title("checklist item"), "task_uuid": id["task"]})
		id["checklist"] = item
		status := func() string {
			t.Helper()
			list := decodeAs[[]checklistOutput](t, "things_list_checklist_items",
				h.mustCall(t, "things_list_checklist_items", map[string]any{"task_uuid": id["task"]}))
			for _, c := range list {
				if c.UUID == item {
					return c.Status
				}
			}
			return "absent"
		}
		if got := status(); got != "open" {
			t.Errorf("new checklist item is %s", got)
		}
		h.act(t, "things_complete_checklist_item", map[string]any{"uuid": item}, "completed")
		if got := status(); got != "completed" {
			t.Errorf("completed checklist item is %s", got)
		}
		h.act(t, "things_uncomplete_checklist_item", map[string]any{"uuid": item}, "uncompleted")
		if got := status(); got != "open" {
			t.Errorf("reopened checklist item is %s", got)
		}
		h.act(t, "things_delete_checklist_item", map[string]any{"uuid": item}, "deleted")
		if got := status(); got != "absent" {
			t.Errorf("deleted checklist item is %s", got)
		}
	})

	t.Run("repeat_rejected", func(t *testing.T) {
		before := h.cloud.stats().CommitPosts
		for _, tc := range []struct {
			tool string
			args map[string]any
		}{
			{"things_create_task", map[string]any{"title": title("repeat"), "repeat": "daily"}},
			{"things_edit_task", map[string]any{"uuid": id["task"], "repeat": "none"}},
		} {
			text, isErr := h.call(t, tc.tool, tc.args)
			if !isErr || !strings.Contains(text, "KI-01") {
				t.Errorf("%s with repeat: error=%v, %q; want a KI-01 rejection", tc.tool, isErr, text)
			}
		}
		if after := h.cloud.stats().CommitPosts; after != before {
			t.Errorf("a rejected repeat request still sent %d commits", after-before)
		}
	})

	t.Run("read_tools", func(t *testing.T) {
		if !containsTask(h.listTasks(t, "things_list_projects", nil), id["project"]) {
			t.Error("things_list_projects does not list the project")
		}
		areas := decodeAs[[]areaOutput](t, "things_list_areas", h.mustCall(t, "things_list_areas", map[string]any{}))
		if !slices.ContainsFunc(areas, func(a areaOutput) bool { return a.UUID == id["area"] }) ||
			!slices.ContainsFunc(areas, func(a areaOutput) bool { return a.Title == sandboxMarkerArea }) {
			t.Error("things_list_areas lacks the run's area or the marker area")
		}
		tags := decodeAs[[]tagOutput](t, "things_list_tags", h.mustCall(t, "things_list_tags", map[string]any{}))
		if !slices.ContainsFunc(tags, func(tg tagOutput) bool { return tg.UUID == id["tag"] }) {
			t.Error("things_list_tags does not list the tag")
		}
		projectTasks := h.listTasks(t, "things_list_project_tasks", map[string]any{"project_uuid": id["project"]})
		if !containsTask(projectTasks, id["task"]) {
			t.Error("things_list_project_tasks does not list the task created in the project")
		}
		h.listTasks(t, "things_list_area_tasks", map[string]any{"area_uuid": id["area"]})
		all := h.listTasks(t, "things_list_all_tasks", nil)
		for _, key := range []string{"task", "edit", "lifecycle"} {
			if !containsTask(all, id[key]) {
				t.Errorf("things_list_all_tasks does not list %s", key)
			}
		}
		found := h.listTasks(t, "things_search_tasks", map[string]any{"query": h.runID})
		if !containsTask(found, id["task"]) {
			t.Error("things_search_tasks does not find the run's task")
		}
		for _, o := range found {
			if !strings.Contains(o.Title, h.runID) {
				t.Errorf("search for the run id returned %s, which lacks it", o.UUID)
			}
		}
	})

	t.Run("conflict_409", func(t *testing.T) {
		k := h.create(t, "things_create_task", map[string]any{"title": title("conflict"), "when": "anytime"})
		id["conflict"] = k
		observerTitle := title("conflict renamed by the second client")
		before := h.cloud.stats().Conflicts
		done := h.cloud.armConflict(k, func() error { return h.observer.retitle(k, observerTitle) })
		defer h.cloud.disarmConflict()

		h.act(t, "things_edit_task", map[string]any{"uuid": k, "note": "written after a 409"}, "updated")
		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("second client's write: %s", h.red.redact(err.Error()))
			}
		default:
			t.Fatal("the server sent no commit for the task, so no conflict was injected")
		}
		if got := h.cloud.stats().Conflicts - before; got < 1 {
			t.Fatal("Things Cloud accepted a commit with a stale ancestor-index; the 409 retry was not exercised")
		}
		if got := h.getTask(t, k); got.Title != observerTitle || got.Note != "written after a 409" {
			t.Errorf("after the retried write: title from the second client %t, note from the server %t", got.Title == observerTitle, got.Note == "written after a 409")
		}
	})

	t.Run("observer_mirror", func(t *testing.T) {
		st := h.observer.state(t)
		task := func(key string) *things.Task {
			t.Helper()
			got, err := st.Task(id[key])
			if err != nil || got == nil {
				t.Fatalf("observer mirror lacks %s (%s): %v", key, id[key], err)
			}
			return got
		}
		if x := task("task"); x.Title != title("task in project") || !slices.Equal(x.ParentTaskIDs, []string{id["project"]}) ||
			!slices.Equal(x.TagIDs, []string{id["tag"]}) || x.Status != things.TaskStatusPending || x.InTrash {
			t.Errorf("task in the observer mirror: %s", publicSDKTask(x))
		}
		if s := task("subtask"); !slices.Equal(s.ParentTaskIDs, []string{id["task"]}) {
			t.Errorf("subtask parents = %v", s.ParentTaskIDs)
		}
		if p := task("project"); p.Type != things.TaskTypeProject || !slices.Equal(p.AreaIDs, []string{id["area"]}) {
			t.Errorf("project in the observer mirror: %s", publicSDKTask(p))
		}
		if hd := task("heading"); hd.Type != things.TaskTypeHeading || !slices.Equal(hd.ParentTaskIDs, []string{id["project"]}) {
			t.Errorf("heading in the observer mirror: %s", publicSDKTask(hd))
		}
		if e := task("edit"); e.Title != title("edit renamed") || e.Note != "" || e.DeadlineDate != nil || len(e.AreaIDs) != 0 ||
			!slices.Equal(e.ParentTaskIDs, []string{id["project"]}) || !slices.Equal(e.TagIDs, []string{id["tag"]}) {
			t.Errorf("edited task in the observer mirror: %s", publicSDKTask(e))
		}
		if c := task("lifecycle"); c.Status != things.TaskStatusPending || c.InTrash {
			t.Errorf("lifecycle task in the observer mirror: %s", publicSDKTask(c))
		}
		if k := task("conflict"); k.Title != title("conflict renamed by the second client") || k.Note != "written after a 409" {
			t.Errorf("conflict task in the observer mirror: title from the second client %t, note from the server %t", k.Title == title("conflict renamed by the second client"), k.Note == "written after a 409")
		}
		if a, err := st.Area(id["area"]); err != nil || a == nil || a.Title != title("area") {
			t.Errorf("area %s in the observer mirror: found %t, title as written %t, %v", id["area"], a != nil, a != nil && a.Title == title("area"), err)
		}
		if tg, err := st.Tag(id["tag"]); err != nil || tg == nil || tg.Title != title("tag") {
			t.Errorf("tag %s in the observer mirror: found %t, title as written %t, %v", id["tag"], tg != nil, tg != nil && tg.Title == title("tag"), err)
		}
		items, err := st.ChecklistItems(id["task"])
		if err != nil {
			t.Fatal(err)
		}
		for _, c := range items {
			if c.UUID == id["checklist"] {
				t.Error("the deleted checklist item is still in the observer mirror")
			}
		}

		// The same classifier over an independent mirror must agree with what
		// the list tools returned.
		for key, want := range map[string]listView{
			"placement inbox": viewInbox, "placement today": viewToday, "placement anytime": viewAnytime,
			"placement someday": viewSomeday, "placement upcoming": viewUpcoming, "moved": viewInbox,
		} {
			x := task(key)
			var in []string
			for _, v := range viewTools {
				if inView(x, v.view, h.today) {
					in = append(in, v.name)
				}
			}
			if len(in) != 1 || in[0] != viewName(want) {
				t.Errorf("%s: observer classifies it as %v, want %s", key, in, viewName(want))
			}
		}
	})

	t.Run("wire", func(t *testing.T) {
		h.verifyWire(t)
	})

	t.Run("cleanup", func(t *testing.T) {
		h.cleanupRun(t)
		st := h.observer.state(t)
		for _, o := range h.createdObjects() {
			if o.Kind != "Task6" {
				continue
			}
			if task, err := st.Task(o.UUID); err != nil || task == nil || !task.InTrash {
				t.Errorf("%s (Task6 tp=%d) is not in the trash after cleanup", o.UUID, o.Type)
			}
		}
	})

	t.Run("tool_coverage", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), sandboxCallTimeout)
		defer cancel()
		res, err := h.mcp.ListTools(ctx, mcp.ListToolsRequest{})
		if err != nil {
			t.Fatal(err)
		}
		covered := h.coveredTools()
		for _, tool := range res.Tools {
			if !covered[tool.Name] {
				t.Errorf("tool %s is not exercised by the sandbox e2e run; add it to TestSandboxE2E", tool.Name)
			}
		}
	})

	s := h.cloud.stats()
	t.Logf("Things Cloud requests: %d (commits posted %d, 409s %d)", s.Requests, s.CommitPosts, s.Conflicts)
}

func taskOutputsEqual(a, b taskOutput) bool {
	return a.UUID == b.UUID && a.Title == b.Title && a.Note == b.Note && a.Status == b.Status && a.Type == b.Type &&
		a.CompletedAt == b.CompletedAt && a.Deadline == b.Deadline && a.ScheduledFor == b.ScheduledFor &&
		a.ProjectID == b.ProjectID && a.AreaID == b.AreaID && slices.Equal(a.Tags, b.Tags)
}

// verifyWire reads the history the run wrote with the observer's client and
// checks every committed item as it is stored: the same rules the firewall
// applied before sending, plus "stored equals sent".
func (h *sandboxHarness) verifyWire(t *testing.T) {
	t.Helper()
	items, _, err := h.observer.itemsSince(h.startIndex)
	if err != nil {
		t.Fatalf("observer: read history: %v", err)
	}
	type slot struct {
		index int
		uuid  string
	}
	matched := map[slot]bool{}
	find := func(head int, sent wireItem) (wireItem, bool) {
		for _, it := range items {
			if it.ServerIndex == head-1 && it.UUID == sent.UUID && !matched[slot{it.ServerIndex, it.UUID}] {
				return it, true
			}
		}
		for _, it := range items {
			if it.UUID == sent.UUID && it.Action == sent.Action && it.Kind == sent.Kind &&
				!matched[slot{it.ServerIndex, it.UUID}] && len(payloadDifferences(sent.Payload, it.Payload)) == 0 {
				return it, true
			}
		}
		return wireItem{}, false
	}

	kinds := map[string]int{}
	verified := 0
	for _, c := range h.cloud.committed() {
		for _, sent := range c.Items {
			stored, ok := find(c.HeadIndex, sent)
			if !ok {
				t.Errorf("%s (%s, t=%d): committed with head %d but not found in history", sent.UUID, sent.Kind, sent.Action, c.HeadIndex)
				continue
			}
			matched[slot{stored.ServerIndex, stored.UUID}] = true
			if stored.Kind != sent.Kind || stored.Action != sent.Action {
				t.Errorf("%s: sent %s t=%d, stored %s t=%d", sent.UUID, sent.Kind, sent.Action, stored.Kind, stored.Action)
			}
			for _, v := range wireViolations(stored, itemCivilToday(stored.Payload, h.today)) {
				t.Errorf("stored item at index %d: %s", stored.ServerIndex, v)
			}
			for _, d := range payloadDifferences(sent.Payload, stored.Payload) {
				t.Errorf("%s at index %d: %s", stored.UUID, stored.ServerIndex, d)
			}
			kinds[stored.Kind]++
			verified++
		}
	}
	if verified == 0 {
		t.Fatal("no committed item was verified")
	}

	// Things apps on the account may write housekeeping changes to the run's
	// objects (docs/protocol/things-cloud.md §7); report them, they are not ours.
	for _, it := range items {
		target := it.UUID
		if it.Kind == "Tombstone2" {
			target, _ = jsonString(it.Payload["dloid"])
		}
		if h.firewall.owns(target) && !matched[slot{it.ServerIndex, it.UUID}] {
			keys := make([]string, 0, len(it.Payload))
			for k := range it.Payload {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			t.Logf("foreign write on a run object at index %d: %s t=%d keys %v", it.ServerIndex, it.Kind, it.Action, keys)
		}
	}
	t.Logf("wire: %d committed items verified in history %v", verified, kinds)
}

// TestSandboxCleanupLeftovers trashes to-dos, headings and projects left by
// earlier runs (titles starting with "[e2e-" or "[smoke-test] "), e.g. after a
// run was killed by -timeout. With THINGS_E2E_RUN_ID set it trashes only that
// run's objects (and smoke-test leftovers). Areas and tags are only listed.
func TestSandboxCleanupLeftovers(t *testing.T) {
	h := startSandboxHarness(t, harnessCleanup)
	leftover := isHarnessTitle
	if os.Getenv(envSandboxRunID) != "" {
		leftover = func(title string) bool {
			return strings.HasPrefix(title, h.prefix) || strings.HasPrefix(title, smokeTestTitlePrefix)
		}
		t.Logf("cleaning up run %s only", h.runID)
	}
	st := syncer.State()
	opts := sync.QueryOpts{IncludeCompleted: true}

	var leftovers []*things.Task
	for _, query := range []func(sync.QueryOpts) ([]*things.Task, error){st.AllTasks, st.AllHeadings, st.AllProjects} {
		tasks, err := query(opts)
		if err != nil {
			t.Fatal(err)
		}
		for _, task := range tasks {
			if !task.InTrash && leftover(task.Title) {
				leftovers = append(leftovers, task)
			}
		}
	}
	for _, task := range leftovers {
		h.firewall.own(task.UUID, "Task6")
		h.act(t, "things_trash_task", map[string]any{"uuid": task.UUID}, "trashed")
	}

	var manual []string
	areas, err := readState().AllAreasWithOpts(listOpts{})
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range areas {
		if leftover(a.Title) {
			manual = append(manual, fmt.Sprintf("area %s of run %s", a.UUID, runOfTitle(a.Title)))
		}
	}
	tags, err := readState().AllTagsWithOpts(listOpts{})
	if err != nil {
		t.Fatal(err)
	}
	for _, tg := range tags {
		if leftover(tg.Title) {
			manual = append(manual, fmt.Sprintf("tag %s of run %s", tg.UUID, runOfTitle(tg.Title)))
		}
	}
	t.Logf("trashed %d leftover to-dos, headings and projects", len(leftovers))
	if len(manual) > 0 {
		t.Logf("delete these in the Things app (no verified delete write for areas and tags): %s", strings.Join(manual, ", "))
	}
}

func isHarnessTitle(title string) bool {
	return strings.HasPrefix(title, sandboxRunTitleRoot) || strings.HasPrefix(title, smokeTestTitlePrefix)
}

// TestSandboxSeedChecks reads the items the owner created once in the Things
// app (docs/testing/sandbox-e2e.md, "Seed list") and checks how the server
// reads Task7 items, repeating to-dos and other shapes it never writes. It
// sends no commit.
func TestSandboxSeedChecks(t *testing.T) {
	h := startSandboxHarness(t, harnessReadOnly)
	items, skipped, err := h.observer.itemsSince(0)
	if err != nil {
		t.Fatalf("observer: read history: %v", err)
	}
	if skipped > 0 {
		t.Logf("%d history items with a non-object payload skipped", skipped)
	}
	folded := foldHistory(items)

	var missing []string
	// lookup returns the live entities titled title; repeating to-dos share
	// the title between the template (rr set) and its instances (rt set).
	lookup := func(title string, isTask bool, keep func(*foldedEntity) bool) []*foldedEntity {
		var found []*foldedEntity
		for _, e := range folded {
			if e.title() == title && isTaskEntity(e) == isTask && string(e.Fields["tr"]) != "true" && keep(e) {
				found = append(found, e)
			}
		}
		return found
	}
	one := func(title string, found []*foldedEntity) *foldedEntity {
		t.Helper()
		switch len(found) {
		case 0:
			missing = append(missing, title)
			return nil
		case 1:
			return found[0]
		default:
			t.Errorf("seed %q exists %d times; keep exactly one", title, len(found))
			return found[0]
		}
	}
	isTemplate := func(e *foldedEntity) bool { return !isJSONNull(e.Fields["rr"]) }
	isInstance := func(e *foldedEntity) bool { return !isEmptyJSONArrayOrNull(e.Fields["rt"]) }
	seed := func(title string, isTask bool) *foldedEntity {
		t.Helper()
		return one(title, lookup(title, isTask, func(e *foldedEntity) bool { return !isInstance(e) }))
	}
	template := func(title string) *foldedEntity {
		t.Helper()
		e := one(title, lookup(title, true, isTemplate))
		if e != nil {
			t.Logf("seed %q: %d generated instances in history", title, len(lookup(title, true, isInstance)))
		}
		return e
	}

	marker := seed(sandboxMarkerArea, false)
	anytime := seed("SEED anytime", true)
	someday := seed("SEED someday", true)
	upcoming := seed("SEED upcoming", true)
	deadline := seed("SEED deadline", true)
	checklist := seed("SEED checklist", true)
	tag := seed("SEED tag", false)
	tagged := seed("SEED tagged", true)
	project := seed("SEED project", true)
	inProject := seed("SEED in project", true)
	underHeading := seed("SEED under heading", true)
	repeatWeekly := template("SEED repeat weekly")
	repeatAfter := template("SEED repeat monthly after completion")
	completed := seed("SEED completed", true)
	canceled := seed("SEED canceled", true)
	if len(missing) > 0 {
		t.Fatalf("seed items missing, create them once in the Things app (docs/testing/sandbox-e2e.md): %s", strings.Join(missing, ", "))
	}

	task7 := 0
	for _, e := range []*foldedEntity{anytime, someday, upcoming, deadline, checklist, tagged, project, inProject, underHeading, repeatWeekly, repeatAfter, completed, canceled} {
		if e.Kinds["Task7"] {
			task7++
		}
		t.Logf("seed %q kinds %v", e.title(), sortedKeys(e.Kinds))
	}
	if task7 == 0 {
		t.Error("no seed to-do was written as Task7; create the seeds with Things 3.23 or later (KI-25)")
	}

	views := func(e *foldedEntity) []string { return h.viewsOf(t, e.UUID) }
	if v := views(anytime); !slices.Contains(v, "anytime") {
		t.Errorf("SEED anytime is listed in %v, want anytime", v)
	}
	if !containsTask(h.listTasks(t, "things_list_area_tasks", map[string]any{"area_uuid": marker.UUID}), anytime.UUID) {
		t.Errorf("SEED anytime is not listed in the %s area", sandboxMarkerArea)
	}
	if v := views(someday); !slices.Contains(v, "someday") {
		t.Errorf("SEED someday is listed in %v, want someday", v)
	}
	if v := views(upcoming); !slices.Contains(v, "upcoming") {
		t.Errorf("SEED upcoming is listed in %v, want upcoming", v)
	}
	if got := h.getTask(t, upcoming.UUID).ScheduledFor; got != "2030-01-01" {
		t.Errorf("SEED upcoming scheduled_for = %q, want 2030-01-01", got)
	}
	if got := h.getTask(t, deadline.UUID).Deadline; got != "2030-06-01" {
		t.Errorf("SEED deadline deadline = %q, want 2030-06-01", got)
	}

	list := decodeAs[[]checklistOutput](t, "things_list_checklist_items",
		h.mustCall(t, "things_list_checklist_items", map[string]any{"task_uuid": checklist.UUID}))
	done := 0
	for _, c := range list {
		if c.Status == "completed" {
			done++
		}
	}
	if len(list) != 2 || done != 1 {
		t.Errorf("SEED checklist has %d items, %d ticked; want two with one ticked", len(list), done)
	}

	tags := decodeAs[[]tagOutput](t, "things_list_tags", h.mustCall(t, "things_list_tags", map[string]any{}))
	if !slices.ContainsFunc(tags, func(tg tagOutput) bool { return tg.UUID == tag.UUID }) {
		t.Error("things_list_tags does not list SEED tag")
	}
	if got := h.getTask(t, tagged.UUID).Tags; !slices.Contains(got, tag.UUID) {
		t.Errorf("SEED tagged tags = %v, want SEED tag", got)
	}

	if !containsTask(h.listTasks(t, "things_list_projects", nil), project.UUID) {
		t.Error("things_list_projects does not list SEED project")
	}
	projectTasks := h.listTasks(t, "things_list_project_tasks", map[string]any{"project_uuid": project.UUID})
	if !containsTask(projectTasks, inProject.UUID) {
		t.Error("things_list_project_tasks does not list SEED in project")
	}
	if !containsTask(projectTasks, underHeading.UUID) {
		t.Log("KNOWN ISSUE KI-13: things_list_project_tasks misses SEED under heading")
	}

	for _, tc := range []struct {
		e      *foldedEntity
		fu, tp float64
	}{
		{repeatWeekly, 256, 0},
		{repeatAfter, 8, 1},
	} {
		var rr map[string]any
		if err := json.Unmarshal(tc.e.Fields["rr"], &rr); err != nil {
			t.Errorf("%q: recurrence rule is not an object: %v", tc.e.title(), err)
			continue
		}
		if rr["fu"] != tc.fu || rr["tp"] != tc.tp {
			t.Errorf("%q rr fu=%v tp=%v, want fu=%v tp=%v; if the seed is right, correct docs/protocol/things-cloud.md §5.3", tc.e.title(), rr["fu"], rr["tp"], tc.fu, tc.tp)
		}
		if v := views(tc.e); len(v) > 0 {
			t.Logf("KNOWN ISSUE KI-05: repeating template %q is listed in %v", tc.e.title(), v)
		}
		if _, isErr := h.call(t, "things_get_task", map[string]any{"uuid": tc.e.UUID}); isErr {
			t.Errorf("things_get_task fails on %q", tc.e.title())
		}
	}

	logbook := h.listTasks(t, "things_list_completed", map[string]any{"limit": 200})
	if !containsTask(logbook, completed.UUID) {
		t.Error("things_list_completed does not list SEED completed")
	}
	all := h.listTasks(t, "things_list_all_tasks", nil)
	for _, e := range []*foldedEntity{completed, canceled} {
		if v := views(e); len(v) > 0 {
			t.Errorf("%q is listed in %v; closed to-dos belong to the Logbook only (KI-05)", e.title(), v)
		}
		if containsTask(all, e.UUID) {
			t.Errorf("things_list_all_tasks lists the closed %q", e.title())
		}
	}

	if posts := h.cloud.stats().CommitPosts; posts != 0 {
		t.Errorf("read-only seed checks attempted %d commits", posts)
	}
}

func isTaskEntity(e *foldedEntity) bool {
	for k := range e.Kinds {
		if strings.HasPrefix(k, "Task") {
			return true
		}
	}
	return false
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
