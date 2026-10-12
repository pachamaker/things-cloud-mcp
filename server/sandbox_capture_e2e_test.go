//go:build e2e

package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// envSandboxCaptureOut names the file TestSandboxRecurrenceCapture writes;
// unset, it writes into the test's temp directory.
const envSandboxCaptureOut = "THINGS_E2E_CAPTURE_OUT"

// TestSandboxRecurrenceCapture reads every history item the official Things
// apps wrote for the repeating seeds (templates, generated instances and what
// hangs off them) and writes them, sanitized, as a capture fixture
// (docs/protocol/things-cloud.md §8, testdata/captures/README.md). It sends no
// commit.
func TestSandboxRecurrenceCapture(t *testing.T) {
	h := startSandboxHarness(t, harnessReadOnly)
	items, skipped, err := h.observer.itemsSince(0)
	if err != nil {
		t.Fatalf("observer: read history: %v", err)
	}
	if skipped > 0 {
		t.Logf("%d history items with a non-object payload skipped", skipped)
	}
	fx, err := buildRecurrenceCapture(items, recurrenceSeedTitles)
	if err != nil {
		t.Fatal(err)
	}
	fx.Source = "history of the sandbox account (docs/testing/sandbox-e2e.md) as written by the official Things apps, read back by TestSandboxRecurrenceCapture"
	fx.CapturedOn = h.today.String()
	fx.HistoryHead = h.startIndex

	var buf bytes.Buffer
	if err := encodeCaptureJSON(&buf, fx, "  "); err != nil {
		t.Fatal(err)
	}
	out := buf.Bytes()
	if s := string(out); h.red.redact(s) != s || strings.Contains(s, "@") {
		t.Fatal("the sanitized capture still carries a credential, the history key or an e-mail address; nothing was written")
	}
	path := os.Getenv(envSandboxCaptureOut)
	if path == "" {
		path = filepath.Join(t.TempDir(), "recurrence-capture.json")
	}
	if err := os.WriteFile(path, out, 0o600); err != nil {
		t.Fatal(err)
	}

	for _, o := range fx.Objects {
		t.Logf("object %s: %s", o.UUID, o.Role)
	}
	for _, it := range fx.Items {
		var p map[string]json.RawMessage
		if err := json.Unmarshal(it.Payload, &p); err != nil {
			t.Fatal(err)
		}
		t.Logf("index %d (%d in slot) %s t=%d %s keys %v", it.Index, it.SlotItems, it.UUID, it.Action, it.Kind, sortedKeys(p))
	}
	t.Logf("%d objects, %d items, %d values redacted (paths in the file); written to %s", len(fx.Objects), len(fx.Items), len(fx.Redacted), path)
	if posts := h.cloud.stats().CommitPosts; posts != 0 {
		t.Errorf("read-only capture attempted %d commits", posts)
	}
}
