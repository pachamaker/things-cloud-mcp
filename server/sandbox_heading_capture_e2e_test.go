//go:build e2e

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSandboxHeadingCapture(t *testing.T) {
	h := startSandboxHarness(t, harnessReadOnly)
	items, _, err := h.observer.itemsSince(0)
	if err != nil {
		t.Fatalf("read sandbox history: %s", h.red.redact(err.Error()))
	}
	fx, err := buildHeadingCapture(items)
	if err != nil {
		t.Fatal(err)
	}
	fx.Source = "sandbox seed history read by TestSandboxHeadingCapture; verify native-client provenance before treating as protocol evidence"
	fx.CapturedOn = h.today.String()
	fx.HistoryHead = h.startIndex
	var buf bytes.Buffer
	if err := encodeCaptureJSON(&buf, fx, "  "); err != nil {
		t.Fatal(err)
	}
	out := buf.Bytes()
	if s := string(out); h.red.redact(s) != s || strings.Contains(s, "@") {
		t.Fatal("capture failed secret redaction; nothing exported")
	}
	if posts := h.cloud.stats().CommitPosts; posts != 0 {
		t.Fatalf("read-only capture attempted %d commits", posts)
	}
	path := os.Getenv(envSandboxCaptureOut)
	if path == "" {
		path = filepath.Join(t.TempDir(), "heading-capture.json")
	}
	if err := os.WriteFile(path, out, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Logf("exported %d seed objects, %d history items, %d redactions; zero commits", len(fx.Objects), len(fx.Items), len(fx.Redacted))
	// Missing native operations is reported without suppressing the useful raw capture.
	if err := checkHeadingEvidence(fx); err != nil {
		t.Logf("evidence incomplete: %s", err)
	}
}
