package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	things "github.com/arthursoares/things-cloud-sdk"
	"github.com/arthursoares/things-cloud-sdk/sync"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/pachamaker/things-cloud-mcp/domain/civil"
)

type taskSummaryOutput struct {
	taskOutput
	HasNote         bool     `json:"has_note"`
	TruncatedFields []string `json:"truncated_fields,omitempty"`
}

func summarizeTask(t *things.Task) taskSummaryOutput {
	o := formatTask(t)
	o.Note = ""
	return taskSummaryOutput{taskOutput: o, HasNote: t.Note != ""}
}

func requiredReadString(req mcp.CallToolRequest, key string) (string, error) {
	v, ok := req.GetArguments()[key].(string)
	if !ok || strings.TrimSpace(v) == "" {
		return "", fmt.Errorf("%s must be a non-empty string", key)
	}
	return v, nil
}

func readFailure(err error) *mcp.CallToolResult {
	code := "read_failed"
	if errors.Is(err, errStalePage) {
		code = "stale_page"
	}
	return readError(code, redactRequestURL(err).Error())
}

func readTaskSelection(r readModel, q readQuery, today civil.Date) (tasks, headings []*things.Task, err error) {
	switch q.Tool {
	case "things_list_today":
		tasks, err = r.tasksInViewOn(viewToday, listOpts{}, today)
	case "things_list_inbox":
		tasks, err = r.tasksInViewOn(viewInbox, listOpts{}, today)
	case "things_list_anytime":
		tasks, err = r.tasksInViewOn(viewAnytime, listOpts{}, today)
	case "things_list_someday":
		tasks, err = r.tasksInViewOn(viewSomeday, listOpts{}, today)
	case "things_list_upcoming":
		tasks, err = r.tasksInViewOn(viewUpcoming, listOpts{}, today)
	case "things_list_projects":
		tasks, err = r.AllProjects(listOpts{})
	case "things_list_all_tasks":
		tasks, err = r.AllTasks(listOpts{})
	case "things_list_project_tasks":
		tasks, headings, err = r.TasksInProject(q.Filters["project_uuid"], listOpts{})
	case "things_list_area_tasks":
		tasks, err = r.TasksInArea(q.Filters["area_uuid"], listOpts{})
	case "things_list_completed":
		var after, before *time.Time
		after, err = parseDateOrRFC3339(q.Filters["completed_after"], "completed_after", ownerZone)
		if err != nil {
			return
		}
		before, err = parseDateOrRFC3339(q.Filters["completed_before"], "completed_before", ownerZone)
		if err != nil {
			return
		}
		if after != nil && before != nil && !after.Before(*before) {
			err = fmt.Errorf("completed_after must be earlier than completed_before")
			return
		}
		tasks, err = r.CompletedTasksInRange(int(^uint(0)>>1), after, before)
	case "things_search_tasks":
		scope := q.Filters["scope"]
		tasks, err = r.AllTasks(listOpts{QueryOpts: sync.QueryOpts{IncludeCompleted: scope != "open"}})
		if err != nil {
			return
		}
		query := strings.ToLower(q.Filters["query"])
		matches := tasks[:0:0]
		for _, task := range tasks {
			if task.Status == things.TaskStatusCanceled || (scope == "completed" && task.Status != things.TaskStatusCompleted) {
				continue
			}
			if strings.Contains(strings.ToLower(task.Title), query) || strings.Contains(strings.ToLower(task.Note), query) {
				matches = append(matches, task)
			}
		}
		tasks = matches
		sort.Slice(tasks, func(i, j int) bool { return tasks[i].UUID < tasks[j].UUID })
	default:
		err = fmt.Errorf("unknown task collection")
	}
	return
}

func collectionQuery(tool string, req mcp.CallToolRequest) (readQuery, error) {
	q := readQuery{Tool: tool, Filters: map[string]string{}, Order: "view-order-with-uuid-tiebreak"}
	key := ""
	switch tool {
	case "things_list_project_tasks":
		key = "project_uuid"
	case "things_list_area_tasks":
		key = "area_uuid"
	case "things_list_checklist_items":
		key = "task_uuid"
	}
	if key != "" {
		v, err := requiredReadString(req, key)
		if err != nil {
			return q, err
		}
		q.Filters[key] = v
	}
	if tool == "things_search_tasks" {
		query, err := requiredReadString(req, "query")
		if err != nil {
			return q, err
		}
		q.Filters["query"] = strings.ToLower(query)
		q.Filters["scope"] = "open"
		q.Order = "uuid"
		if v, ok := req.GetArguments()["scope"]; ok {
			scope, good := v.(string)
			if !good || (scope != "open" && scope != "completed" && scope != "all") {
				return q, fmt.Errorf("scope must be open, completed, or all")
			}
			q.Filters["scope"] = scope
		}
	}
	if tool == "things_list_completed" {
		q.Order = "completed-desc,uuid"
		for _, key := range []string{"completed_after", "completed_before"} {
			v, ok := req.GetArguments()[key]
			if !ok {
				continue
			}
			s, good := v.(string)
			if !good {
				return q, fmt.Errorf("%s must be a date string", key)
			}
			d, err := parseDateOrRFC3339(s, key, ownerZone)
			if err != nil {
				return q, err
			}
			if d != nil {
				q.Filters[key] = d.Format(time.RFC3339Nano)
			}
		}
		a, b := q.Filters["completed_after"], q.Filters["completed_before"]
		if a != "" && b != "" {
			after, _ := time.Parse(time.RFC3339Nano, a)
			before, _ := time.Parse(time.RFC3339Nano, b)
			if !after.Before(before) {
				return q, fmt.Errorf("completed_after must be earlier than completed_before")
			}
		}
	}
	return q, nil
}

func mcpReadCollection(tool string, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	opts, err := parsePageOptions(req)
	if err != nil {
		return readError("invalid_arguments", err.Error()), nil
	}
	q, err := collectionQuery(tool, req)
	if err != nil {
		return readError("invalid_arguments", err.Error()), nil
	}
	var render func() *mcp.CallToolResult
	err = mirror.withRead(func(r readModel, s readRevisionState) error {
		rev, e := validateReadRevision(s, q, opts)
		if e != nil {
			return e
		}
		switch tool {
		case "things_list_areas":
			items, e := r.mirror.AllAreas()
			if e != nil {
				return e
			}
			sort.Slice(items, func(i, j int) bool { return items[i].UUID < items[j].UUID })
			out := make([]areaOutput, len(items))
			for i, x := range items {
				out[i] = areaOutput{UUID: x.UUID, Title: x.Title}
			}
			render = func() *mcp.CallToolResult { return encodeCollectionPage(out, opts, rev, s.Today, s.Timezone) }
		case "things_list_tags":
			items, e := r.mirror.AllTags()
			if e != nil {
				return e
			}
			sort.Slice(items, func(i, j int) bool { return items[i].UUID < items[j].UUID })
			out := make([]tagOutput, len(items))
			for i, x := range items {
				out[i] = tagOutput{UUID: x.UUID, Title: x.Title, Shorthand: x.ShortHand}
			}
			render = func() *mcp.CallToolResult { return encodeCollectionPage(out, opts, rev, s.Today, s.Timezone) }
		case "things_list_checklist_items":
			reader, ok := r.mirror.(interface {
				ChecklistItems(string) ([]*things.CheckListItem, error)
			})
			if !ok {
				return fmt.Errorf("checklist reader unavailable")
			}
			items, e := reader.ChecklistItems(q.Filters["task_uuid"])
			if e != nil {
				return e
			}
			sort.Slice(items, func(i, j int) bool {
				if items[i].Index == items[j].Index {
					return items[i].UUID < items[j].UUID
				}
				return items[i].Index < items[j].Index
			})
			out := make([]checklistOutput, len(items))
			for i, x := range items {
				status := "open"
				if x.Status == things.TaskStatusCompleted {
					status = "completed"
				}
				out[i] = checklistOutput{UUID: x.UUID, Title: x.Title, Status: status}
			}
			render = func() *mcp.CallToolResult { return encodeCollectionPage(out, opts, rev, s.Today, s.Timezone) }
		default:
			tasks, headings, e := readTaskSelection(r, q, s.Today)
			if e != nil {
				return e
			}
			render = func() *mcp.CallToolResult {
				out := summarizeTasks(tasks, headings, tool, s.Today)
				// Only inspect/project returned candidates for the expensive encoded-title fallback.
				end := min(len(out), opts.Offset+min(opts.Limit, max(0, len(out)-opts.Offset)))
				if opts.Offset < len(out) {
					for i := opts.Offset; i < end; i++ {
						out[i] = fitSummaryTitle(out[i], rev, s)
					}
				}
				return encodeCollectionPage(out, opts, rev, s.Today, s.Timezone)
			}

		}
		return nil
	})
	if err != nil {
		return readFailure(fmt.Errorf("pre-read sync or query failed: %w", err)), nil
	}
	return render(), nil
}

func fitSummaryTitle(o taskSummaryOutput, rev string, s readRevisionState) taskSummaryOutput {
	// Fit against a real one-item envelope with worst-case pagination metadata.
	fits := func(x taskSummaryOutput) bool {
		next := int(^uint(0) >> 1)
		reason := "bytes"
		p := readPage[taskSummaryOutput]{Items: []taskSummaryOutput{x}, Returned: 1, Total: next, HasMore: true, NextOffset: &next, Revision: rev, Today: s.Today.String(), Timezone: s.Timezone, PageLimitedBy: &reason}
		b, err := json.Marshal(p)
		return err == nil && len(b) <= maxReadBytes
	}
	return fitTaskTitle(o, fits)
}

func fitTaskTitle(o taskSummaryOutput, fits func(taskSummaryOutput) bool) taskSummaryOutput {
	if fits(o) {
		return o
	}
	title := []rune(o.Title)
	o.Title = ""
	o.TruncatedFields = append(append([]string(nil), o.TruncatedFields...), "title")
	if !fits(o) {
		return o
	} // Encoder returns an explicit metadata overflow error.
	low, high := 0, len(title)
	for low < high {
		mid := low + (high-low+1)/2
		o.Title = string(title[:mid])
		if fits(o) {
			low = mid
		} else {
			high = mid - 1
		}
	}
	o.Title = string(title[:low])
	return o
}

func summarizeTasks(tasks, headings []*things.Task, tool string, today civil.Date) []taskSummaryOutput {
	names := make(map[string]string, len(headings))
	for _, h := range headings {
		names[h.UUID] = h.Title
	}
	out := make([]taskSummaryOutput, len(tasks))
	for i, task := range tasks {
		out[i] = summarizeTask(task)
		if tool == "things_list_today" {
			out[i].Evening = inEvening(task, today)
		}
		out[i].HeadingTitle = names[out[i].HeadingID]
	}
	return out
}
