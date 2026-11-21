package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	things "github.com/arthursoares/things-cloud-sdk"

	"github.com/pachamaker/things-cloud-mcp/domain/civil"
)

// ---------------------------------------------------------------------------
// Output types for JSON serialization
// ---------------------------------------------------------------------------

type taskOutput struct {
	UUID         string   `json:"uuid"`
	Title        string   `json:"title"`
	Note         string   `json:"note,omitempty"`
	Status       string   `json:"status"`
	Type         string   `json:"type"`
	CompletedAt  string   `json:"completed_at,omitempty"`
	Deadline     string   `json:"deadline,omitempty"`
	ScheduledFor string   `json:"scheduled_for,omitempty"`
	ProjectID    string   `json:"project_id,omitempty"`
	HeadingID    string   `json:"heading_id,omitempty"`
	HeadingTitle string   `json:"heading_title,omitempty"`
	AreaID       string   `json:"area_id,omitempty"`
	Tags         []string `json:"tags,omitempty"`
	// Repeating is "template" for the hidden template of a repeating to-do or
	// project and "instance" for a to-do or project it generated (KI-05).
	Repeating string `json:"repeating,omitempty"`
	// Evening marks a to-do of things_list_today in its This Evening section
	// (KI-02); other lists leave it out.
	Evening bool `json:"evening,omitempty"`
}

type areaOutput struct {
	UUID  string `json:"uuid"`
	Title string `json:"title"`
}

type tagOutput struct {
	UUID      string `json:"uuid"`
	Title     string `json:"title"`
	Shorthand string `json:"shorthand,omitempty"`
}

type checklistOutput struct {
	UUID   string `json:"uuid"`
	Title  string `json:"title"`
	Status string `json:"status"`
}

// ---------------------------------------------------------------------------
// Format helpers
// ---------------------------------------------------------------------------

func formatTask(t *things.Task) taskOutput {
	o := taskOutput{
		UUID:  t.UUID,
		Title: t.Title,
		Note:  t.Note,
		Tags:  t.TagIDs,
	}
	switch t.Status {
	case things.TaskStatusPending:
		o.Status = "open"
	case things.TaskStatusCompleted:
		o.Status = "completed"
	case things.TaskStatusCanceled:
		o.Status = "canceled"
	}
	switch t.Type {
	case things.TaskTypeTask:
		o.Type = "task"
	case things.TaskTypeProject:
		o.Type = "project"
	case things.TaskTypeHeading:
		o.Type = "heading"
	}
	if d := calendarDay(t.DeadlineDate); d != nil {
		o.Deadline = d.Format("2006-01-02")
	}
	if t.CompletionDate != nil {
		o.CompletedAt = t.CompletionDate.UTC().Format(time.RFC3339)
	}
	if d := calendarDay(t.ScheduledDate); d != nil {
		o.ScheduledFor = d.Format("2006-01-02")
	}
	if len(t.ParentTaskIDs) > 0 {
		o.ProjectID = t.ParentTaskIDs[0]
	}
	if len(t.ActionGroupIDs) > 0 {
		o.HeadingID = t.ActionGroupIDs[0]
	}
	if len(t.AreaIDs) > 0 {
		o.AreaID = t.AreaIDs[0]
	}
	switch {
	case t.IsRepeatingTemplate():
		o.Repeating = "template"
	case t.IsRepeatingInstance():
		o.Repeating = "instance"
	}
	return o
}

func jsonToolResult(v any) *mcp.CallToolResult {
	return jsonToolResultWithIndent(v, false)
}

func jsonToolResultWithIndent(v any, indent bool) *mcp.CallToolResult {
	var (
		b   []byte
		err error
	)
	if indent {
		b, err = json.MarshalIndent(v, "", "  ")
	} else {
		b, err = json.Marshal(v)
	}
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to encode result JSON: %v", err))
	}
	return mcp.NewToolResultText(string(b))
}

func writeResult(fields map[string]string) *mcp.CallToolResult {
	return jsonToolResult(fields)
}

// parseDateOrRFC3339 reads an instant; a bare YYYY-MM-DD means the start of
// that day in loc, the owner's timezone (ADR-0003).
func parseDateOrRFC3339(raw, name string, loc *time.Location) (*time.Time, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	if t, err := time.Parse(time.RFC3339, raw); err == nil {
		tt := t.UTC()
		return &tt, nil
	}
	if d, err := civil.Parse(raw); err == nil {
		tt := d.Start(loc).UTC()
		return &tt, nil
	}
	return nil, fmt.Errorf("%s must be RFC3339 or YYYY-MM-DD", name)
}

// todayNote names the calendar day the date rules use, so the assistant can
// notice when the user is in another timezone (ADR-0003).
func todayNote() string {
	return fmt.Sprintf("\"Today\" is the calendar day in %s, the timezone configured on the server.", ownerZone)
}

// ---------------------------------------------------------------------------
// Read tool handlers
// ---------------------------------------------------------------------------

func mcpListToday(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return mcpReadCollection("things_list_today", req)
}

func mcpListAnytime(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return mcpReadCollection("things_list_anytime", req)
}

func mcpListInbox(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return mcpReadCollection("things_list_inbox", req)
}

func mcpListSomeday(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return mcpReadCollection("things_list_someday", req)
}

func mcpListUpcoming(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return mcpReadCollection("things_list_upcoming", req)
}

func mcpListProjects(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return mcpReadCollection("things_list_projects", req)
}

func mcpListAreas(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return mcpReadCollection("things_list_areas", req)
}

func mcpListTags(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return mcpReadCollection("things_list_tags", req)
}

func mcpListAllTasks(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return mcpReadCollection("things_list_all_tasks", req)
}

func mcpListProjectTasks(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return mcpReadCollection("things_list_project_tasks", req)
}

func mcpListAreaTasks(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return mcpReadCollection("things_list_area_tasks", req)
}

func mcpListCompleted(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return mcpReadCollection("things_list_completed", req)
}

func mcpListChecklistItems(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return mcpReadCollection("things_list_checklist_items", req)
}

func mcpGetTask(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return mcpReadEntity("things_get_task", req)
}

func mcpGetArea(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return mcpReadEntity("things_get_area", req)
}

func mcpGetTag(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return mcpReadEntity("things_get_tag", req)
}

// ---------------------------------------------------------------------------
// Write tool handlers
// ---------------------------------------------------------------------------

func mcpCreateTask(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	title, err := req.RequireString("title")
	if err != nil {
		return mcp.NewToolResultError("title is required"), nil
	}
	uuid, err := createTask(CreateTaskRequest{
		Title:      title,
		Note:       req.GetString("note", ""),
		When:       req.GetString("when", ""),
		Deadline:   req.GetString("deadline", ""),
		Project:    req.GetString("project", ""),
		Heading:    req.GetString("heading", ""),
		ParentTask: req.GetString("parent_task", ""),
		Tags:       req.GetString("tags", ""),
		Repeat:     req.GetString("repeat", ""),
	})
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return writeResult(map[string]string{"status": "created", "uuid": uuid, "title": title}), nil
}

func mcpCompleteTask(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	uuid, err := req.RequireString("uuid")
	if err != nil {
		return mcp.NewToolResultError("uuid is required"), nil
	}
	if err := completeTask(uuid); err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return writeResult(map[string]string{"status": "completed", "uuid": uuid}), nil
}

func mcpEditTask(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	uuid, err := req.RequireString("uuid")
	if err != nil {
		return mcp.NewToolResultError("uuid is required"), nil
	}
	if err := editTask(EditTaskRequest{
		UUID:       uuid,
		Title:      req.GetString("title", ""),
		Note:       req.GetString("note", ""),
		When:       req.GetString("when", ""),
		Deadline:   req.GetString("deadline", ""),
		Project:    req.GetString("project", ""),
		Heading:    req.GetString("heading", ""),
		ParentTask: req.GetString("parent_task", ""),
		Area:       req.GetString("area", ""),
		Tags:       req.GetString("tags", ""),
		Repeat:     req.GetString("repeat", ""),
	}); err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return writeResult(map[string]string{"status": "updated", "uuid": uuid}), nil
}

func mcpTrashTask(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	uuid, err := req.RequireString("uuid")
	if err != nil {
		return mcp.NewToolResultError("uuid is required"), nil
	}
	if err := trashTask(uuid); err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return writeResult(map[string]string{"status": "trashed", "uuid": uuid}), nil
}

func mcpMoveToToday(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	uuid, err := req.RequireString("uuid")
	if err != nil {
		return mcp.NewToolResultError("uuid is required"), nil
	}
	if err := moveTaskToToday(uuid); err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return writeResult(map[string]string{"status": "moved_to_today", "uuid": uuid}), nil
}

func mcpMoveToAnytime(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	uuid, err := req.RequireString("uuid")
	if err != nil {
		return mcp.NewToolResultError("uuid is required"), nil
	}
	if err := moveTaskToAnytime(uuid); err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return writeResult(map[string]string{"status": "moved_to_anytime", "uuid": uuid}), nil
}

func mcpMoveToSomeday(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	uuid, err := req.RequireString("uuid")
	if err != nil {
		return mcp.NewToolResultError("uuid is required"), nil
	}
	if err := moveTaskToSomeday(uuid); err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return writeResult(map[string]string{"status": "moved_to_someday", "uuid": uuid}), nil
}

func mcpMoveToInbox(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	uuid, err := req.RequireString("uuid")
	if err != nil {
		return mcp.NewToolResultError("uuid is required"), nil
	}
	if err := moveTaskToInbox(uuid); err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return writeResult(map[string]string{"status": "moved_to_inbox", "uuid": uuid}), nil
}

func mcpUncompleteTask(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	uuid, err := req.RequireString("uuid")
	if err != nil {
		return mcp.NewToolResultError("uuid is required"), nil
	}
	if err := uncompleteTask(uuid); err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return writeResult(map[string]string{"status": "uncompleted", "uuid": uuid}), nil
}

func mcpUntrashTask(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	uuid, err := req.RequireString("uuid")
	if err != nil {
		return mcp.NewToolResultError("uuid is required"), nil
	}
	if err := untrashTask(uuid); err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return writeResult(map[string]string{"status": "restored", "uuid": uuid}), nil
}

func mcpCreateArea(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	title, err := req.RequireString("title")
	if err != nil {
		return mcp.NewToolResultError("title is required"), nil
	}
	var tagUUIDs []string
	if tagStr := req.GetString("tags", ""); tagStr != "" {
		tagUUIDs = strings.Split(tagStr, ",")
	}
	uuid, err := createArea(title, tagUUIDs)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return writeResult(map[string]string{"status": "created", "uuid": uuid, "title": title}), nil
}

func mcpCreateTag(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	title, err := req.RequireString("title")
	if err != nil {
		return mcp.NewToolResultError("title is required"), nil
	}
	uuid, err := createTag(title, req.GetString("shorthand", ""), req.GetString("parent", ""))
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return writeResult(map[string]string{"status": "created", "uuid": uuid, "title": title}), nil
}

func mcpCreateHeading(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	title, err := req.RequireString("title")
	if err != nil {
		return mcp.NewToolResultError("title is required"), nil
	}
	uuid, err := createHeading(title, req.GetString("project", ""))
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return writeResult(map[string]string{"status": "created", "uuid": uuid, "title": title}), nil
}

func mcpCreateProject(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	title, err := req.RequireString("title")
	if err != nil {
		return mcp.NewToolResultError("title is required"), nil
	}
	uuid, err := createProject(
		title,
		req.GetString("note", ""),
		req.GetString("when", ""),
		req.GetString("deadline", ""),
		req.GetString("area", ""),
	)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return writeResult(map[string]string{"status": "created", "uuid": uuid, "title": title}), nil
}

// ---------------------------------------------------------------------------
// Search handler
// ---------------------------------------------------------------------------

func mcpSearchTasks(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return mcpReadCollection("things_search_tasks", req)
}

// ---------------------------------------------------------------------------
// Checklist item handlers
// ---------------------------------------------------------------------------

func mcpCreateChecklistItem(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	title, err := req.RequireString("title")
	if err != nil {
		return mcp.NewToolResultError("title is required"), nil
	}
	taskUUID, err := req.RequireString("task_uuid")
	if err != nil {
		return mcp.NewToolResultError("task_uuid is required"), nil
	}
	uuid, err := createChecklistItem(title, taskUUID)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return writeResult(map[string]string{"status": "created", "uuid": uuid, "title": title}), nil
}

func mcpCompleteChecklistItem(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	uuid, err := req.RequireString("uuid")
	if err != nil {
		return mcp.NewToolResultError("uuid is required"), nil
	}
	if err := completeChecklistItem(uuid); err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return writeResult(map[string]string{"status": "completed", "uuid": uuid}), nil
}

func mcpUncompleteChecklistItem(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	uuid, err := req.RequireString("uuid")
	if err != nil {
		return mcp.NewToolResultError("uuid is required"), nil
	}
	if err := uncompleteChecklistItem(uuid); err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return writeResult(map[string]string{"status": "uncompleted", "uuid": uuid}), nil
}

func mcpDeleteChecklistItem(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	uuid, err := req.RequireString("uuid")
	if err != nil {
		return mcp.NewToolResultError("uuid is required"), nil
	}
	if err := deleteChecklistItem(uuid); err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return writeResult(map[string]string{"status": "deleted", "uuid": uuid}), nil
}

// ---------------------------------------------------------------------------
// MCP server setup
// ---------------------------------------------------------------------------

// newMCPHandler registers the write tools only when writes is true
// (ENABLE_WRITES, ADR-0004); a read-only server does not even list them.
// Mount it behind a gate: each request needs an access value in its
// context, and write tools also need write access (KI-07).
func newMCPHandler(writes bool) http.Handler {
	writeTools := map[string]bool{}
	s := server.NewMCPServer("Things Cloud", version,
		server.WithToolFilter(toolFilter(writeTools)),
		server.WithToolHandlerMiddleware(toolAccess(writeTools)),
	)
	addWrite := func(tool mcp.Tool, handler server.ToolHandlerFunc) {
		if writes {
			writeTools[tool.Name] = true
			s.AddTool(tool, handler)
		}
	}

	addRead := func(tool mcp.Tool, handler server.ToolHandlerFunc) {
		if strings.HasPrefix(tool.Name, "things_list_") || tool.Name == "things_search_tasks" {
			tool.Description += " Returns compact summary pages without notes, default 25/max 100 items and at most 24 KiB. total is the full selection, returned is this page. Fetch more only when needed. Continue with next_offset and revision. On stale_page restart at zero and replace earlier results."
			mcp.WithNumber("limit", mcp.Description("Page size, integer 1..100, default 25"), mcp.Min(1), mcp.Max(100))(&tool)
			mcp.WithNumber("offset", mcp.Description("Non-negative integer offset, default 0"), mcp.Min(0))(&tool)
			mcp.WithString("revision", mcp.Description("Required for offset > 0, copied from the first page"))(&tool)
		}
		if tool.Name == "things_search_tasks" {
			mcp.WithString("scope", mcp.Description("open (default), completed, or their union all. Excludes canceled, trash and hidden templates."), mcp.Enum("open", "completed", "all"))(&tool)
		}
		for _, name := range []string{"limit", "offset"} {
			if prop, ok := tool.InputSchema.Properties[name].(map[string]any); ok {
				prop["type"] = "integer"
			}
		}

		s.AddTool(tool, handler)
	}

	addRead(mcp.NewTool("things_get_task_text",
		mcp.WithDescription("Read a task title or note in bounded Unicode chunks. Default 4000/max 8000 code points and at most 24 KiB JSON. Continue with next_offset and the returned revision. On stale_page restart at zero and replace the previous text."),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithString("uuid", mcp.Required()),
		mcp.WithString("field", mcp.Enum("note", "title"), mcp.Description("note (default) or title")),
		mcp.WithNumber("offset", mcp.Min(0)),
		mcp.WithNumber("limit", mcp.Min(1), mcp.Max(8000)),
		mcp.WithString("revision", mcp.Description("Required for continuation offset > 0")),
	), mcpGetTaskText)

	// --- Read tools ---

	addRead(mcp.NewTool("things_list_today",
		mcp.WithDescription("List to-dos in the Things Today view: to-dos with a start date today or earlier (including overdue ones and scheduled ones whose date has arrived), to-dos placed in Today, and to-dos without a start date whose deadline is today or earlier (Inbox included). "+todayNote()+" A deadline no longer pulls a to-do in once the user dismissed it from Today in the Things app. To-dos in the This Evening section come last and carry \"evening\": true. Projects scheduled for today are not listed."),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithNumber("limit",
			mcp.Description("Maximum number of tasks to return"),
			mcp.Min(0),
		),
		mcp.WithNumber("offset",
			mcp.Description("Number of tasks to skip before returning results"),
			mcp.Min(0),
		),
	), mcpListToday)

	addRead(mcp.NewTool("things_list_anytime",
		mcp.WithDescription("List started to-dos in the Things Anytime view, including started to-dos also shown in Today. "+todayNote()),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithNumber("limit",
			mcp.Description("Maximum number of tasks to return"),
			mcp.Min(0),
		),
		mcp.WithNumber("offset",
			mcp.Description("Number of tasks to skip before returning results"),
			mcp.Min(0),
		),
	), mcpListAnytime)

	addRead(mcp.NewTool("things_list_inbox",
		mcp.WithDescription("List tasks in the Things inbox"),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithNumber("limit",
			mcp.Description("Maximum number of tasks to return"),
			mcp.Min(0),
		),
		mcp.WithNumber("offset",
			mcp.Description("Number of tasks to skip before returning results"),
			mcp.Min(0),
		),
	), mcpListInbox)

	addRead(mcp.NewTool("things_list_someday",
		mcp.WithDescription("List to-dos in the Things Someday view: deferred to-dos without a start date. Those with a deadline today or earlier also appear in things_list_today."),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithNumber("limit",
			mcp.Description("Maximum number of tasks to return"),
			mcp.Min(0),
		),
		mcp.WithNumber("offset",
			mcp.Description("Number of tasks to skip before returning results"),
			mcp.Min(0),
		),
	), mcpListSomeday)

	addRead(mcp.NewTool("things_list_upcoming",
		mcp.WithDescription("List to-dos in the Things Upcoming view: deferred to-dos with a start date after today. Scheduled to-dos whose date has arrived are in things_list_today instead. "+todayNote()),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithNumber("limit",
			mcp.Description("Maximum number of tasks to return"),
			mcp.Min(0),
		),
		mcp.WithNumber("offset",
			mcp.Description("Number of tasks to skip before returning results"),
			mcp.Min(0),
		),
	), mcpListUpcoming)

	addRead(mcp.NewTool("things_list_projects",
		mcp.WithDescription("List all projects in Things"),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithNumber("limit",
			mcp.Description("Maximum number of projects to return"),
			mcp.Min(0),
		),
		mcp.WithNumber("offset",
			mcp.Description("Number of projects to skip before returning results"),
			mcp.Min(0),
		),
	), mcpListProjects)

	addRead(mcp.NewTool("things_list_areas",
		mcp.WithDescription("List all areas in Things"),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithNumber("limit",
			mcp.Description("Maximum number of areas to return"),
			mcp.Min(0),
		),
		mcp.WithNumber("offset",
			mcp.Description("Number of areas to skip before returning results"),
			mcp.Min(0),
		),
	), mcpListAreas)

	addRead(mcp.NewTool("things_list_tags",
		mcp.WithDescription("List all tags in Things"),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithNumber("limit",
			mcp.Description("Maximum number of tags to return"),
			mcp.Min(0),
		),
		mcp.WithNumber("offset",
			mcp.Description("Number of tags to skip before returning results"),
			mcp.Min(0),
		),
	), mcpListTags)

	addRead(mcp.NewTool("things_list_all_tasks",
		mcp.WithDescription("List all open tasks in Things"),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithNumber("limit",
			mcp.Description("Maximum number of tasks to return"),
			mcp.Min(0),
		),
		mcp.WithNumber("offset",
			mcp.Description("Number of tasks to skip before returning results"),
			mcp.Min(0),
		),
	), mcpListAllTasks)

	addRead(mcp.NewTool("things_list_project_tasks",
		mcp.WithDescription("List to-dos in a specific Things project, including those under its headings: first the to-dos directly in the project, then each heading's to-dos in heading order. To-dos under a heading carry heading_id and heading_title."),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithString("project_uuid",
			mcp.Required(),
			mcp.Description("UUID of the project"),
		),
		mcp.WithNumber("limit",
			mcp.Description("Maximum number of tasks to return"),
			mcp.Min(0),
		),
		mcp.WithNumber("offset",
			mcp.Description("Number of tasks to skip before returning results"),
			mcp.Min(0),
		),
	), mcpListProjectTasks)

	addRead(mcp.NewTool("things_list_area_tasks",
		mcp.WithDescription("List tasks in a specific Things area"),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithString("area_uuid",
			mcp.Required(),
			mcp.Description("UUID of the area"),
		),
		mcp.WithNumber("limit",
			mcp.Description("Maximum number of tasks to return"),
			mcp.Min(0),
		),
		mcp.WithNumber("offset",
			mcp.Description("Number of tasks to skip before returning results"),
			mcp.Min(0),
		),
	), mcpListAreaTasks)

	addRead(mcp.NewTool("things_list_completed",
		mcp.WithDescription("List recently completed tasks, ordered by completion date (most recent first)"),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithNumber("limit",
			mcp.Description("Maximum number of tasks to return (default 50)"),
		),
		mcp.WithString("completed_after",
			mcp.Description("Optional lower bound for completion time (inclusive). Accepts RFC3339, or YYYY-MM-DD for the start of that day in the server's timezone."),
		),
		mcp.WithString("completed_before",
			mcp.Description("Optional upper bound for completion time (exclusive). Accepts RFC3339, or YYYY-MM-DD for the start of that day in the server's timezone."),
		),
	), mcpListCompleted)

	addRead(mcp.NewTool("things_list_checklist_items",
		mcp.WithDescription("List checklist items (lightweight checkboxes) within a task. These are different from subtasks — checklist items live inside the task's detail view and cannot have their own dates, tags, or notes. Use things_list_project_tasks with the task UUID to find subtasks instead."),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithString("task_uuid",
			mcp.Required(),
			mcp.Description("UUID of the task"),
		),
	), mcpListChecklistItems)

	addRead(mcp.NewTool("things_get_task",
		mcp.WithDescription("Get a single Things task by UUID. Normal-sized detail includes the full note. Oversized task text is explicitly marked truncated and can be retrieved with things_get_task_text. Never overwrite a stored field using incomplete text."),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithString("uuid",
			mcp.Required(),
			mcp.Description("UUID of the task"),
		),
	), mcpGetTask)

	addRead(mcp.NewTool("things_get_area",
		mcp.WithDescription("Get a single Things area by UUID"),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithString("uuid",
			mcp.Required(),
			mcp.Description("UUID of the area"),
		),
	), mcpGetArea)

	addRead(mcp.NewTool("things_get_tag",
		mcp.WithDescription("Get a single Things tag by UUID"),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithString("uuid",
			mcp.Required(),
			mcp.Description("UUID of the tag"),
		),
	), mcpGetTag)

	// --- Write tools ---

	addWrite(mcp.NewTool("things_create_task",
		mcp.WithDescription("Create a new task in Things. Recurrence cannot be set through this server (KI-01); ask the user to set it up in the Things app."),
		mcp.WithString("title",
			mcp.Required(),
			mcp.Description("Task title"),
		),
		mcp.WithString("note",
			mcp.Description("Task notes"),
		),
		mcp.WithString("when",
			mcp.Description("When to start the task. Use this for most date-related requests. Accepts: 'today', 'anytime' (triaged, no date), 'someday' (deferred), 'inbox' (default), or a YYYY-MM-DD date. A future date puts the task in Upcoming and auto-surfaces it on that day. Today's date or past dates go to Today view."),
		),
		mcp.WithString("deadline",
			mcp.Description("Hard deadline in YYYY-MM-DD format. Only use when the user explicitly mentions a deadline or due date — not for general scheduling. Most date requests should use 'when' instead."),
		),
		mcp.WithString("project",
			mcp.Description("Project UUID to add the task to"),
		),
		mcp.WithString("heading",
			mcp.Description("Heading UUID. Its project is inferred; an explicit project must match. Cannot be combined with parent_task or area. Moving an existing task preserves dates, tags, and notes unless explicitly changed. To leave a heading, move to a project without heading."),
		),
		mcp.WithString("parent_task",
			mcp.Description("Parent task UUID to create this as a subtask — a full nested task with its own dates, tags, and notes. For simple checkboxes within a task, use things_create_checklist_item instead. Takes precedence over project."),
		),
		mcp.WithString("tags",
			mcp.Description("Comma-separated tag UUIDs"),
		),
	), mcpCreateTask)

	addWrite(mcp.NewTool("things_complete_task",
		mcp.WithDescription("Mark a Things task as completed"),
		mcp.WithString("uuid",
			mcp.Required(),
			mcp.Description("UUID of the task to complete"),
		),
	), mcpCompleteTask)

	addWrite(mcp.NewTool("things_edit_task",
		mcp.WithDescription("Edit an existing Things task. Recurrence cannot be set, changed or removed through this server (KI-01); ask the user to do it in the Things app."),
		mcp.WithString("uuid",
			mcp.Required(),
			mcp.Description("UUID of the task to edit"),
		),
		mcp.WithString("title",
			mcp.Description("New title"),
		),
		mcp.WithString("note",
			mcp.Description("New notes, or 'none' to clear existing notes"),
		),
		mcp.WithString("when",
			mcp.Description("When to start the task. Use this for most date-related requests. Accepts: 'today', 'anytime' (triaged, no date), 'someday' (deferred), 'inbox' (clears schedule), 'none' (strip dates, keep in project/area), or a YYYY-MM-DD date. A future date puts the task in Upcoming. Today's date or past dates go to Today view."),
		),
		mcp.WithString("deadline",
			mcp.Description("Hard deadline in YYYY-MM-DD format, or 'none' to clear an existing deadline. Only use when the user explicitly mentions a deadline or due date — not for general scheduling."),
		),
		mcp.WithString("project",
			mcp.Description("New project UUID"),
		),
		mcp.WithString("heading",
			mcp.Description("Heading UUID. Its project is inferred; an explicit project must match. Cannot be combined with parent_task or area. Moving an existing task preserves dates, tags, and notes unless explicitly changed. To leave a heading, move to a project without heading."),
		),
		mcp.WithString("parent_task",
			mcp.Description("Move task under a parent task (make it a subtask). Takes precedence over project."),
		),
		mcp.WithString("area",
			mcp.Description("Area UUID to assign the task to, or 'none' to remove from area"),
		),
		mcp.WithString("tags",
			mcp.Description("New comma-separated tag UUIDs (replaces existing)"),
		),
	), mcpEditTask)

	addWrite(mcp.NewTool("things_trash_task",
		mcp.WithDescription("Move a Things task to the trash"),
		mcp.WithDestructiveHintAnnotation(true),
		mcp.WithString("uuid",
			mcp.Required(),
			mcp.Description("UUID of the task to trash"),
		),
	), mcpTrashTask)

	addWrite(mcp.NewTool("things_move_to_today",
		mcp.WithDescription("Move a Things task to the Today view"),
		mcp.WithString("uuid",
			mcp.Required(),
			mcp.Description("UUID of the task to move"),
		),
	), mcpMoveToToday)

	addWrite(mcp.NewTool("things_move_to_anytime",
		mcp.WithDescription("Move a Things task to the Anytime view"),
		mcp.WithString("uuid",
			mcp.Required(),
			mcp.Description("UUID of the task to move"),
		),
	), mcpMoveToAnytime)

	addWrite(mcp.NewTool("things_move_to_someday",
		mcp.WithDescription("Move a Things task to the Someday view"),
		mcp.WithString("uuid",
			mcp.Required(),
			mcp.Description("UUID of the task to move"),
		),
	), mcpMoveToSomeday)

	addWrite(mcp.NewTool("things_move_to_inbox",
		mcp.WithDescription("Move a Things task back to the Inbox"),
		mcp.WithString("uuid",
			mcp.Required(),
			mcp.Description("UUID of the task to move"),
		),
	), mcpMoveToInbox)

	addWrite(mcp.NewTool("things_uncomplete_task",
		mcp.WithDescription("Mark a completed Things task as open again"),
		mcp.WithString("uuid",
			mcp.Required(),
			mcp.Description("UUID of the task to uncomplete"),
		),
	), mcpUncompleteTask)

	addWrite(mcp.NewTool("things_untrash_task",
		mcp.WithDescription("Restore a Things task from the trash"),
		mcp.WithString("uuid",
			mcp.Required(),
			mcp.Description("UUID of the task to restore"),
		),
	), mcpUntrashTask)

	addWrite(mcp.NewTool("things_create_area",
		mcp.WithDescription("Create a new area in Things"),
		mcp.WithString("title",
			mcp.Required(),
			mcp.Description("Area title"),
		),
		mcp.WithString("tags",
			mcp.Description("Comma-separated tag UUIDs to associate"),
		),
	), mcpCreateArea)

	addWrite(mcp.NewTool("things_create_tag",
		mcp.WithDescription("Create a new tag in Things"),
		mcp.WithString("title",
			mcp.Required(),
			mcp.Description("Tag title"),
		),
		mcp.WithString("shorthand",
			mcp.Description("Tag keyboard shortcut"),
		),
		mcp.WithString("parent",
			mcp.Description("Parent tag UUID for nesting"),
		),
	), mcpCreateTag)

	addWrite(mcp.NewTool("things_create_heading",
		mcp.WithDescription("Create a heading within a project in Things"),
		mcp.WithString("title",
			mcp.Required(),
			mcp.Description("Heading title"),
		),
		mcp.WithString("project",
			mcp.Description("Project UUID to add the heading to"),
		),
	), mcpCreateHeading)

	addWrite(mcp.NewTool("things_create_project",
		mcp.WithDescription("Create a new project in Things"),
		mcp.WithString("title",
			mcp.Required(),
			mcp.Description("Project title"),
		),
		mcp.WithString("note",
			mcp.Description("Project notes"),
		),
		mcp.WithString("when",
			mcp.Description("Schedule: today, anytime (default), or someday"),
			mcp.Enum("today", "anytime", "someday"),
		),
		mcp.WithString("deadline",
			mcp.Description("Deadline in YYYY-MM-DD format"),
		),
		mcp.WithString("area",
			mcp.Description("Area UUID to assign the project to"),
		),
	), mcpCreateProject)

	// --- Search tools ---

	addRead(mcp.NewTool("things_search_tasks",
		mcp.WithDescription("Search for tasks by title or note content (case-insensitive substring match)"),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithString("query",
			mcp.Required(),
			mcp.Description("Text to search for in task titles and notes"),
		),
	), mcpSearchTasks)

	// --- Checklist item tools ---

	addWrite(mcp.NewTool("things_create_checklist_item",
		mcp.WithDescription("Add a checklist item (lightweight checkbox) to a task. Checklist items are simple checkboxes within a task's detail view — they cannot have their own dates, tags, or notes. For full nested tasks with independent scheduling, use things_create_task with parent_task instead."),
		mcp.WithString("title",
			mcp.Required(),
			mcp.Description("Checklist item text"),
		),
		mcp.WithString("task_uuid",
			mcp.Required(),
			mcp.Description("UUID of the task to add the checklist item to"),
		),
	), mcpCreateChecklistItem)

	addWrite(mcp.NewTool("things_complete_checklist_item",
		mcp.WithDescription("Mark a checklist item (checkbox) as completed"),
		mcp.WithString("uuid",
			mcp.Required(),
			mcp.Description("UUID of the checklist item to complete"),
		),
	), mcpCompleteChecklistItem)

	addWrite(mcp.NewTool("things_uncomplete_checklist_item",
		mcp.WithDescription("Mark a completed checklist item (checkbox) as open again"),
		mcp.WithString("uuid",
			mcp.Required(),
			mcp.Description("UUID of the checklist item to uncomplete"),
		),
	), mcpUncompleteChecklistItem)

	addWrite(mcp.NewTool("things_delete_checklist_item",
		mcp.WithDescription("Delete a checklist item (checkbox) from a task"),
		mcp.WithDestructiveHintAnnotation(true),
		mcp.WithString("uuid",
			mcp.Required(),
			mcp.Description("UUID of the checklist item to delete"),
		),
	), mcpDeleteChecklistItem)

	return server.NewStreamableHTTPServer(s,
		server.WithEndpointPath("/mcp"),
		server.WithStateLess(true),
	)
}
