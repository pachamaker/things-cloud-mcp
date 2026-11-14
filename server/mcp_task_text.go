package main

import (
	"context"
	"encoding/json"
	"fmt"
	"unicode/utf8"

	things "github.com/arthursoares/things-cloud-sdk"
	"github.com/mark3labs/mcp-go/mcp"
)

type taskTextOutput struct {
	UUID               string `json:"uuid"`
	Field              string `json:"field"`
	Text               string `json:"text"`
	TotalCharacters    int    `json:"total_characters"`
	ReturnedCharacters int    `json:"returned_characters"`
	NextOffset         *int   `json:"next_offset"`
	HasMore            bool   `json:"has_more"`
	Revision           string `json:"revision"`
}

func boundedTaskDetail(task *things.Task, rev string, s readRevisionState) *mcp.CallToolResult {
	full := formatTask(task)
	b, err := json.Marshal(full)
	if err == nil && len(b) <= maxReadBytes {
		return boundedReadResult(full)
	}
	card := summarizeTask(task)
	if task.Note != "" {
		card.TruncatedFields = []string{"note"}
	}
	detail := func(card taskSummaryOutput) any {
		return struct {
			taskSummaryOutput
			NoteCharacters     int    `json:"note_characters"`
			Revision           string `json:"revision"`
			DetailInstructions string `json:"detail_instructions"`
		}{card, utf8.RuneCountInString(task.Note), rev, "Use things_get_task_text(uuid, field=note|title). Start at offset 0 and continue with its returned revision and next_offset. A truncated field is incomplete and must not be used to overwrite stored text."}
	}
	card = fitTaskTitle(card, func(candidate taskSummaryOutput) bool {
		b, e := json.Marshal(detail(candidate))
		return e == nil && len(b) <= maxReadBytes
	})
	return boundedReadResult(detail(card))
}

func mcpGetTaskText(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	uuid, err := requiredReadString(req, "uuid")
	if err != nil {
		return readError("invalid_arguments", err.Error()), nil
	}
	field := "note"
	if v, ok := req.GetArguments()["field"]; ok {
		var good bool
		field, good = v.(string)
		if !good || (field != "note" && field != "title") {
			return readError("invalid_arguments", "field must be note or title"), nil
		}
	}
	opts, err := parseTextOptions(req)
	if err != nil {
		return readError("invalid_arguments", err.Error()), nil
	}
	var render func() *mcp.CallToolResult
	err = mirror.withRead(func(r readModel, s readRevisionState) error {
		q := readQuery{Tool: "things_get_task_text", Filters: map[string]string{"uuid": uuid, "field": field}, Order: "unicode-code-points"}
		rev, e := validateReadRevision(s, q, opts)
		if e != nil {
			return e
		}
		task, e := r.Task(uuid)
		if e != nil {
			return e
		}
		if task == nil {
			render = func() *mcp.CallToolResult { return readError("not_found", "task not found") }
			return nil
		}
		render = func() *mcp.CallToolResult {
			text := task.Note
			if field == "title" {
				text = task.Title
			}
			runes := []rune(text)
			out := taskTextOutput{UUID: uuid, Field: field, TotalCharacters: len(runes), Revision: rev}
			start := min(opts.Offset, len(runes))
			maximum := min(opts.Limit, len(runes)-start)
			build := func(n int) taskTextOutput {
				v := out
				v.Text = string(runes[start : start+n])
				v.ReturnedCharacters = n
				v.HasMore = start+n < len(runes)
				if v.HasMore {
					next := start + n
					v.NextOffset = &next
				}
				return v
			}
			low, high := 0, maximum
			for low < high {
				mid := low + (high-low+1)/2
				b, e := json.Marshal(build(mid))
				if e == nil && len(b) <= maxReadBytes {
					low = mid
				} else {
					high = mid - 1
				}
			}
			if low == 0 && maximum > 0 {
				return oversizedReadItem(map[string]string{"uuid": uuid})
			}
			return boundedReadResult(build(low))
		}

		return nil
	})
	if err != nil {
		return readFailure(err), nil
	}
	return render(), nil
}

func mcpReadEntity(tool string, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	uuid, err := requiredReadString(req, "uuid")
	if err != nil {
		return readError("invalid_arguments", err.Error()), nil
	}
	var render func() *mcp.CallToolResult
	err = mirror.withRead(func(r readModel, s readRevisionState) error {
		rev, e := makeReadRevision(s, readQuery{Tool: tool, Filters: map[string]string{"uuid": uuid}})
		if e != nil {
			return e
		}
		switch tool {
		case "things_get_task":
			task, e := r.Task(uuid)
			if e != nil {
				return e
			}
			if task == nil {
				render = func() *mcp.CallToolResult { return readError("not_found", "task not found") }
			} else {
				render = func() *mcp.CallToolResult { return boundedTaskDetail(task, rev, s) }
			}
		case "things_get_area":
			reader, ok := r.mirror.(interface {
				Area(string) (*things.Area, error)
			})
			if !ok {
				return fmt.Errorf("area reader unavailable")
			}
			area, e := reader.Area(uuid)
			if e != nil {
				return e
			}
			if area == nil {
				render = func() *mcp.CallToolResult { return readError("not_found", "area not found") }
			} else {
				render = func() *mcp.CallToolResult { return boundedReadResult(areaOutput{UUID: area.UUID, Title: area.Title}) }
			}
		case "things_get_tag":
			reader, ok := r.mirror.(interface {
				Tag(string) (*things.Tag, error)
			})
			if !ok {
				return fmt.Errorf("tag reader unavailable")
			}
			tag, e := reader.Tag(uuid)
			if e != nil {
				return e
			}
			if tag == nil {
				render = func() *mcp.CallToolResult { return readError("not_found", "tag not found") }
			} else {
				render = func() *mcp.CallToolResult {
					return boundedReadResult(tagOutput{UUID: tag.UUID, Title: tag.Title, Shorthand: tag.ShortHand})
				}
			}
		}
		return nil
	})
	if err != nil {
		return readFailure(fmt.Errorf("pre-read sync or query failed: %w", err)), nil
	}
	return render(), nil
}
