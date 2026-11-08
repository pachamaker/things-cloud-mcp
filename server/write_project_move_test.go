package main

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"

	things "github.com/arthursoares/things-cloud-sdk"
)

func TestEditTaskProjectMoveClearsHeading(t *testing.T) {
	data, err := os.ReadFile("../testdata/captures/project-move-heading-2026-11-07.json")
	if err != nil {
		t.Fatal(err)
	}
	var capture struct {
		Items []struct {
			Payload map[string]json.RawMessage `json:"p"`
		} `json:"items"`
	}
	if err := json.Unmarshal(data, &capture); err != nil || len(capture.Items) != 1 {
		t.Fatalf("invalid project move capture: %v", err)
	}
	want := capture.Items[0].Payload
	var projects []string
	if err := json.Unmarshal(want["pr"], &projects); err != nil || len(projects) != 1 {
		t.Fatalf("invalid captured project: %v", err)
	}
	id, project := generateUUID(), projects[0]
	originalTransport, originalMirror, originalHistory := http.DefaultTransport, writeMirror, history
	t.Cleanup(func() {
		http.DefaultTransport, writeMirror, history = originalTransport, originalMirror, originalHistory
	})
	writeMirror = func() (mirrorLookup, error) {
		return fakeMirror{tasks: map[string]*things.Task{
			id:      {Type: things.TaskTypeTask, Schedule: things.TaskScheduleSomeday, ActionGroupIDs: []string{generateUUID()}},
			project: {Type: things.TaskTypeProject},
		}}, nil
	}
	stop := errors.New("captured test commit")
	var payload map[string]json.RawMessage
	http.DefaultTransport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.Method == http.MethodGet && strings.HasSuffix(req.URL.Path, "/items") {
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"items":[],"current-item-index":5,"schema":301}`))}, nil
		}
		if req.Method != http.MethodPost || !strings.HasSuffix(req.URL.Path, "/commit") {
			t.Fatalf("unexpected request: %s", req.Method)
		}
		var commit map[string]struct {
			Payload map[string]json.RawMessage `json:"p"`
		}
		if err := json.NewDecoder(req.Body).Decode(&commit); err != nil {
			t.Fatal(err)
		}
		payload = commit[id].Payload
		return nil, stop
	})
	history = things.New(things.APIEndpoint, ki35Email, "not-a-real-password").HistoryWithID(ki35HistoryKey)
	if err := editTask(EditTaskRequest{UUID: id, Project: project}); !errors.Is(err, stop) {
		t.Fatalf("expected to intercept the commit: %v", err)
	}
	if string(payload["agr"]) != string(want["agr"]) {
		t.Errorf("agr = %s, want %s", payload["agr"], want["agr"])
	}
	var gotProjects []string
	if err := json.Unmarshal(payload["pr"], &gotProjects); err != nil || len(gotProjects) != 1 || gotProjects[0] != project {
		t.Errorf("pr = %s, want [%q]", payload["pr"], project)
	}
	for _, key := range []string{"st", "sr", "tir"} {
		if _, ok := payload[key]; ok {
			t.Errorf("project-only edit unexpectedly writes %s", key)
		}
	}
}
