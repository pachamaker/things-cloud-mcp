//go:build e2e && fakecloud

package main

import (
	"encoding/hex"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	things "github.com/arthursoares/things-cloud-sdk"

	"github.com/pachamaker/things-cloud-mcp/domain/civil"
)

// TestSandboxConformanceDumpOffline runs the conformance dump against the fake
// cloud: input.json must load, carry no titles, and agree with history for
// every seed, so that a real snapshot's "mirror disagrees with history"
// failures are not artefacts of the dump.
func TestSandboxConformanceDumpOffline(t *testing.T) {
	fake, ok := http.DefaultTransport.(*fakeThingsCloud)
	if !ok {
		t.Fatal("the fake cloud is not installed")
	}
	marker := fakeMarkerArea(t, fake)
	zone, err := ownerTimezone(os.Getenv("THINGS_TIMEZONE"))
	if err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	keyFile := filepath.Join(dir, "title-key")
	if err := os.WriteFile(keyFile, []byte(hex.EncodeToString(conformanceTestKey)), 0o600); err != nil {
		t.Fatal(err)
	}
	exp := conformanceExpected{
		Format: conformanceFormat, Today: civil.Today(civil.SystemClock{}, zone).String(), Timezone: zone.String(),
		TitleKeyID: conformanceKeyID(conformanceTestKey), MarkerArea: marker, Tasks: []expectedTask{},
	}
	writeConformanceJSON(t, filepath.Join(dir, conformanceExpectedFile), exp)

	writeConformanceInput(t, dir, keyFile)

	raw, err := os.ReadFile(filepath.Join(dir, conformanceInputFile))
	if err != nil {
		t.Fatal(err)
	}
	var in conformanceInput
	if err := decodeStrict(raw, &in); err != nil {
		t.Fatal(err)
	}
	byHash := map[string]inputTask{}
	for _, task := range in.Tasks {
		byHash[task.TitleHash] = task
	}
	for _, title := range []string{"SEED anytime", "SEED project", "SEED heading", "SEED repeat weekly", "SEED canceled"} {
		task, ok := byHash[conformanceTitleHashOf(conformanceTestKey, title)]
		if !ok || task.Mirror == nil || task.Wire == nil {
			t.Errorf("%s: not dumped from both the mirror and history: %+v", title, task)
		}
	}

	// The app's side is made to agree with the server, so any problem left
	// is the dump's own: the mirror and history disagreeing.
	snap := conformanceSnapshot{Input: in, Expected: exp}
	snap.Today, _ = civil.Parse(exp.Today)
	var mirror []things.Task
	for _, task := range in.Tasks {
		if task.Mirror != nil {
			mirror = append(mirror, *task.Mirror)
		}
	}
	views, err := serverViews(mirror, snap.Today)
	if err != nil {
		t.Fatal(err)
	}
	for _, task := range in.Tasks {
		if task.Wire == nil || *task.Wire.Type == 2 {
			continue
		}
		typ := "to-do"
		if *task.Wire.Type == 1 {
			typ = "project"
		}
		snap.Expected.Tasks = append(snap.Expected.Tasks, expectedTask{
			UUID: task.UUID, Type: typ, TitleHash: task.TitleHash, Views: views[task.UUID], App: *task.Wire,
		})
	}
	writeConformanceJSON(t, filepath.Join(dir, conformanceExpectedFile), snap.Expected)
	loaded, err := loadConformanceSnapshot(dir)
	if err != nil {
		t.Fatal(err)
	}
	cases, err := conformanceCases(loaded)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range cases {
		if v := judgeConformanceCase(c); len(v.Problems) > 0 {
			t.Errorf("%s: %q", c.UUID, v.Problems)
		}
	}
	t.Logf("%d tasks dumped, %d cases judged", len(in.Tasks), len(cases))
}

func fakeMarkerArea(t *testing.T, fake *fakeThingsCloud) string {
	t.Helper()
	fake.mu.Lock()
	defer fake.mu.Unlock()
	for _, slot := range fake.slots {
		for id, env := range slot {
			var item struct {
				E string `json:"e"`
				P struct {
					Tt string `json:"tt"`
				} `json:"p"`
			}
			if json.Unmarshal(env, &item) == nil && item.E == "Area3" && item.P.Tt == sandboxMarkerArea {
				return id
			}
		}
	}
	t.Fatal("the fake cloud has no marker area")
	return ""
}

func writeConformanceJSON(t *testing.T, path string, v any) {
	t.Helper()
	raw, err := encodeConformanceJSON(v)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
}
