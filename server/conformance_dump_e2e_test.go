//go:build e2e

package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"testing"
	"time"

	things "github.com/arthursoares/things-cloud-sdk"
	"github.com/arthursoares/things-cloud-sdk/sync"
)

const (
	// envConformanceDir names the snapshot directory that already holds
	// expected.json; scripts/conformance/snapshot.sh sets it.
	envConformanceDir = "THINGS_CONFORMANCE_DIR"
	// envConformanceKeyFile names the snapshot's one-off title key.
	envConformanceKeyFile = "THINGS_CONFORMANCE_TITLE_KEY_FILE"
)

// TestSandboxConformanceDump writes the server's half of a conformance
// snapshot (docs/testing/conformance.md): input.json beside the expected.json
// export_things.py wrote from the Things app on this Mac. It reads the sandbox
// account only and sends no commit. It skips unless envConformanceDir is set.
func TestSandboxConformanceDump(t *testing.T) {
	dir := os.Getenv(envConformanceDir)
	if dir == "" {
		t.Skipf("%s is not set; scripts/conformance/snapshot.sh runs this test", envConformanceDir)
	}
	writeConformanceInput(t, dir, os.Getenv(envConformanceKeyFile))
}

func writeConformanceInput(t *testing.T, dir, keyFile string) {
	t.Helper()
	rawKey, err := os.ReadFile(keyFile)
	if err != nil {
		t.Fatalf("%s: %v", envConformanceKeyFile, err)
	}
	key, err := parseConformanceTitleKey(rawKey)
	if err != nil {
		t.Fatal(err)
	}
	exp, err := readConformanceExpected(filepath.Join(dir, conformanceExpectedFile))
	if err != nil {
		t.Fatal(err)
	}
	if exp.TitleKeyID != conformanceKeyID(key) {
		t.Fatal("the title key is not the one expected.json was written with")
	}
	out := filepath.Join(dir, conformanceInputFile)
	if _, err := os.Stat(out); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("%s exists: each snapshot is taken once", out)
	}

	h := startSandboxHarness(t, harnessSnapshot)
	in := dumpConformanceInput(t, h, exp, key)
	raw, err := encodeConformanceJSON(in)
	if err != nil {
		t.Fatal(err)
	}
	if s := string(raw); h.red.redact(s) != s || strings.Contains(s, "@") {
		t.Fatal("input.json would carry a credential, the history key or an e-mail address; nothing was written")
	}
	if err := os.WriteFile(out, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	mirrored, wireOnly := 0, 0
	for _, task := range in.Tasks {
		if task.Mirror != nil {
			mirrored++
		} else {
			wireOnly++
		}
	}
	t.Logf("wrote %s: %d tasks (%d in the mirror, %d in history only), history head %d, day %s in %s",
		out, len(in.Tasks), mirrored, wireOnly, in.HistoryHead, in.Today, in.Timezone)
	if posts := h.cloud.stats().CommitPosts; posts != 0 {
		t.Errorf("the read-only dump attempted %d commits", posts)
	}
}

// mirrorCursor is the history index the server's mirror has synced up to.
func mirrorCursor(t *testing.T) int {
	t.Helper()
	id, err := mirrorDB.stored()
	if err != nil {
		t.Fatalf("read the mirror's cursor: %v", err)
	}
	return id.cursor
}

// dumpConformanceInput reads the server's mirror and the raw history of the
// account at one history head, after checking that it is the account and the
// day the export was taken on.
func dumpConformanceInput(t *testing.T, h *sandboxHarness, exp conformanceExpected, key []byte) conformanceInput {
	t.Helper()
	if h.zone.String() != exp.Timezone {
		t.Fatalf("THINGS_TIMEZONE is %q, the export was taken in %q; snapshot.sh passes it on", h.zone, exp.Timezone)
	}
	if h.today.String() != exp.Today {
		t.Fatalf("the server's day is %s, the export's %s: the snapshot straddles midnight; take it again", h.today, exp.Today)
	}

	head := mirrorCursor(t)
	items, skipped, err := h.observer.itemsSince(0)
	if err != nil {
		t.Fatalf("read history: %v", err)
	}
	if skipped > 0 {
		t.Logf("%d history items with a non-object payload skipped", skipped)
	}
	if _, err := mirrorDB.Sync(); err != nil {
		t.Fatalf("sync the mirror: %s", h.red.redact(err.Error()))
	}
	if moved := mirrorCursor(t); moved != head {
		t.Fatalf("history moved from %d to %d during the dump: a Things app synced; keep them idle and take the snapshot again", head, moved)
	}
	live := foldHistory(items)
	if marker := live[exp.MarkerArea]; marker == nil || !hasKindPrefix(marker, "Area") || marker.title() != sandboxMarkerArea {
		t.Fatalf("the export's marker area %s is not this account's %q area: the Mac's Things app and the sandbox credentials are on different accounts", exp.MarkerArea, sandboxMarkerArea)
	}

	state := mirrorState()
	all := sync.QueryOpts{IncludeCompleted: true, IncludeTrashed: true}
	var mirror []*things.Task
	for _, list := range []func(sync.QueryOpts) ([]*things.Task, error){state.AllTasks, state.AllProjects, state.AllHeadings} {
		tasks, err := list(all)
		if err != nil {
			t.Fatalf("read the mirror: %v", err)
		}
		mirror = append(mirror, tasks...)
	}

	return conformanceInput{
		Format:      conformanceFormat,
		Source:      "the server's SDK mirror and the raw history of the sandbox account, read by TestSandboxConformanceDump",
		SDK:         sdkModuleVersion(),
		DumpedAt:    time.Now().In(h.zone).Format(time.RFC3339),
		Today:       h.today.String(),
		Timezone:    h.zone.String(),
		HistoryHead: head,
		TitleKeyID:  conformanceKeyID(key),
		MarkerArea:  exp.MarkerArea,
		Tasks:       buildConformanceInput(mirror, live, key),
	}
}

// sdkModuleVersion names the SDK build that filled the mirror, replace
// directives included.
func sdkModuleVersion() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "unknown"
	}
	for _, m := range info.Deps {
		if m.Path != "github.com/arthursoares/things-cloud-sdk" {
			continue
		}
		if m.Replace != nil {
			return fmt.Sprintf("%s %s => %s %s", m.Path, m.Version, m.Replace.Path, m.Replace.Version)
		}
		return m.Path + " " + m.Version
	}
	return "unknown"
}
