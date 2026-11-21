package main

import (
	"testing"

	things "github.com/arthursoares/things-cloud-sdk"
)

func TestProjectHeadings_KI13_InheritedTrash(t *testing.T) {
	useProjectHeadingFixture(t, listMirror{
		{UUID: "p", Type: things.TaskTypeProject, InTrash: true},
		{UUID: "todo", Type: things.TaskTypeTask, ActionGroupIDs: []string{"h"}},
		{UUID: "h", Title: "Deleted project's heading", Type: things.TaskTypeHeading, ParentTaskIDs: []string{"p"}},
	})
	res := registeredRead(t, "things_list_project_tasks", map[string]any{"project_uuid": "p"})
	var page readPage[taskSummaryOutput]
	readJSON(t, res, &page)
	if res.IsError {
		t.Fatal("unexpected error")
	}
	if len(page.Items) != 0 || page.Total != 0 || page.Returned != 0 || page.HasMore || page.NextOffset != nil {
		t.Fatalf("trashed project exposes tasks or continuation: %+v", page)
	}
	if page.Headings == nil {
		t.Fatal("missing headings field")
	}
	if len(*page.Headings) != 0 {
		t.Fatalf("trashed project exposes headings: %+v", *page.Headings)
	}
}
