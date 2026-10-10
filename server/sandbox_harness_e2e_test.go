//go:build e2e

package main

// Sandbox e2e harness: runs the real server in this process against the
// sandbox Things account and drives it through /mcp with an MCP client.
// How to run it and what it guarantees: docs/testing/sandbox-e2e.md.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	gosync "sync"
	"testing"
	"time"

	things "github.com/arthursoares/things-cloud-sdk"
	"github.com/arthursoares/things-cloud-sdk/sync"
	mcpclient "github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"
)

const (
	envSandboxInterval     = "THINGS_E2E_REQUEST_INTERVAL"
	sandboxDefaultInterval = time.Second
	sandboxMinInterval     = 250 * time.Millisecond
	sandboxMidnightMargin  = 15 * time.Minute
	sandboxCallTimeout     = 3 * time.Minute
	sandboxRateLimitPause  = time.Minute
	thingsCloudHost        = "cloud.culturedcode.com"
)

// ---------------------------------------------------------------------------
// Things Cloud transport: throttle, write firewall, 409 injection
// ---------------------------------------------------------------------------

type committedWrite struct {
	Items     []wireItem
	HeadIndex int
}

type conflictInjection struct {
	target string
	write  func() error
	done   chan error
}

// cloudTransport replaces http.DefaultTransport for the test process. The
// SDK's http.Client has no Transport of its own, so every Things Cloud
// request of the server and of the observer passes through it.
type cloudTransport struct {
	base     http.RoundTripper
	interval time.Duration
	firewall *writeFirewall

	throttleMu gosync.Mutex
	last       time.Time

	mu            gosync.Mutex
	requests      int
	commitPosts   int
	conflicts     int
	rateLimited   bool
	commits       []committedWrite
	pendingInject *conflictInjection
}

func (c *cloudTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.Host != thingsCloudHost {
		return c.base.RoundTrip(req)
	}
	isCommit := req.Method == http.MethodPost && strings.HasSuffix(req.URL.Path, "/commit")
	var items []wireItem
	if isCommit {
		c.mu.Lock()
		c.commitPosts++
		c.mu.Unlock()

		body, err := io.ReadAll(req.Body)
		req.Body.Close()
		if err != nil {
			return nil, err
		}
		if items, err = parseCommitBody(body); err != nil {
			return nil, c.firewall.refuse(err.Error())
		}
		if err := c.firewall.check(items); err != nil {
			return nil, err
		}
		c.injectConflict(items)

		clone := req.Clone(req.Context())
		clone.Body = io.NopCloser(bytes.NewReader(body))
		clone.GetBody = func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(body)), nil }
		clone.ContentLength = int64(len(body))
		req = clone
	}

	c.throttle()
	resp, err := c.base.RoundTrip(req)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	c.requests++
	if resp.StatusCode == http.StatusTooManyRequests {
		c.rateLimited = true
	}
	if isCommit && resp.StatusCode == http.StatusConflict {
		c.conflicts++
	}
	c.mu.Unlock()

	if isCommit && resp.StatusCode == http.StatusOK {
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			return nil, err
		}
		resp.Body = io.NopCloser(bytes.NewReader(body))
		var head struct {
			ServerHeadIndex int `json:"server-head-index"`
		}
		_ = json.Unmarshal(body, &head)
		c.firewall.committed(items)
		c.mu.Lock()
		c.commits = append(c.commits, committedWrite{Items: items, HeadIndex: head.ServerHeadIndex})
		c.mu.Unlock()
	}
	return resp, nil
}

func (c *cloudTransport) throttle() {
	c.throttleMu.Lock()
	defer c.throttleMu.Unlock()
	if wait := c.interval - time.Since(c.last); wait > 0 {
		time.Sleep(wait)
	}
	c.last = time.Now()
}

// armConflict makes the next commit that modifies target wait until write
// (a second client's commit on the same history) has landed, so the server's
// commit carries a stale ancestor-index and Things Cloud answers 409.
func (c *cloudTransport) armConflict(target string, write func() error) <-chan error {
	done := make(chan error, 1)
	c.mu.Lock()
	c.pendingInject = &conflictInjection{target: target, write: write, done: done}
	c.mu.Unlock()
	return done
}

func (c *cloudTransport) disarmConflict() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.pendingInject = nil
}

func (c *cloudTransport) injectConflict(items []wireItem) {
	c.mu.Lock()
	inj := c.pendingInject
	if inj == nil || !modifies(items, inj.target) {
		c.mu.Unlock()
		return
	}
	c.pendingInject = nil
	c.mu.Unlock()
	inj.done <- inj.write()
}

func modifies(items []wireItem, uuid string) bool {
	for _, it := range items {
		if it.Action == 1 && it.UUID == uuid {
			return true
		}
	}
	return false
}

type cloudStats struct {
	Requests, CommitPosts, Conflicts int
	RateLimited                      bool
}

func (c *cloudTransport) stats() cloudStats {
	c.mu.Lock()
	defer c.mu.Unlock()
	return cloudStats{Requests: c.requests, CommitPosts: c.commitPosts, Conflicts: c.conflicts, RateLimited: c.rateLimited}
}

func (c *cloudTransport) committed() []committedWrite {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]committedWrite(nil), c.commits...)
}

func (c *cloudTransport) clearRateLimited() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.rateLimited = false
}

// ---------------------------------------------------------------------------
// Harness
// ---------------------------------------------------------------------------

type harnessMode int

const (
	harnessRun      harnessMode = iota // writes with this run's title prefix
	harnessCleanup                     // trashes leftovers of earlier runs
	harnessReadOnly                    // seed checks: no commit leaves the process
)

type sandboxHarness struct {
	creds    sandboxCredentials
	runID    string
	prefix   string
	today    time.Time // the server's civil day: the UTC day until ADR-0003 (KI-03)
	red      *redactor
	cloud    *cloudTransport
	firewall *writeFirewall
	mcp      *mcpclient.Client
	observer *sandboxObserver

	// startIndex is the history head before the run's first write.
	startIndex int

	mu      gosync.Mutex
	covered map[string]bool
	cleaned bool
}

func startSandboxHarness(t *testing.T, mode harnessMode) *sandboxHarness {
	t.Helper()

	creds, err := loadSandboxCredentials(os.Getenv, os.ReadFile)
	if errors.Is(err, errSandboxNotConfigured) {
		t.Skip(err.Error())
	}
	if err != nil {
		t.Fatalf("sandbox credentials: %v", err)
	}
	warnAboutEnvFile(t)

	now := time.Now().UTC()
	midnight := now.Truncate(24 * time.Hour)
	if now.Sub(midnight) < sandboxMidnightMargin || midnight.Add(24*time.Hour).Sub(now) < sandboxMidnightMargin {
		t.Skip("within 15 minutes of 00:00 UTC: the server's day is the UTC day (KI-03), so a run here could straddle two days; retry later")
	}

	interval := sandboxDefaultInterval
	if raw := os.Getenv(envSandboxInterval); raw != "" {
		if interval, err = time.ParseDuration(raw); err != nil || interval < sandboxMinInterval {
			t.Fatalf("%s=%q: want a Go duration of at least %s", envSandboxInterval, raw, sandboxMinInterval)
		}
	}

	runID := os.Getenv(envSandboxRunID)
	if runID != "" {
		if err := validateSandboxRunID(runID); err != nil {
			t.Fatal(err)
		}
	} else if runID, err = newSandboxRunIDNow(); err != nil {
		t.Fatal(err)
	}
	h := &sandboxHarness{
		creds:   creds,
		runID:   runID,
		prefix:  sandboxRunPrefix(runID),
		red:     &redactor{},
		covered: map[string]bool{},
	}
	h.red.add(creds.Username)
	h.red.add(creds.Password)

	prevLog := log.Writer()
	log.SetOutput(redactingWriter{r: h.red, w: prevLog})
	t.Cleanup(func() { log.SetOutput(prevLog) })

	var prefixes []string
	switch mode {
	case harnessRun:
		prefixes = []string{h.prefix, smokeTestTitlePrefix}
	case harnessCleanup:
		prefixes = []string{sandboxRunTitleRoot, smokeTestTitlePrefix}
	}
	h.firewall = newWriteFirewall(time.Now, prefixes...)

	direct := http.DefaultTransport
	h.cloud = &cloudTransport{base: direct, interval: interval, firewall: h.firewall}
	http.DefaultTransport = h.cloud
	t.Cleanup(func() { http.DefaultTransport = direct })

	closeMirror, err := connectThings(creds.Username, creds.Password, t.TempDir())
	if err != nil {
		t.Fatalf("start the server against the sandbox account: %s", h.red.redact(err.Error()))
	}
	t.Cleanup(func() { closeMirror() })
	h.red.add(history.ID)
	h.today = time.Unix(todayMidnightUTC(), 0).UTC()

	mux := http.NewServeMux()
	mux.Handle("/mcp", newMCPHandler())
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	h.mcp, err = mcpclient.NewStreamableHttpClient(srv.URL+"/mcp", transport.WithHTTPBasicClient(&http.Client{Transport: direct}))
	if err != nil {
		t.Fatalf("MCP client: %v", err)
	}
	t.Cleanup(func() { h.mcp.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), sandboxCallTimeout)
	defer cancel()
	if err := h.mcp.Start(ctx); err != nil {
		t.Fatalf("MCP client start: %v", err)
	}
	var initReq mcp.InitializeRequest
	initReq.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
	initReq.Params.ClientInfo = mcp.Implementation{Name: "things-sandbox-e2e", Version: "1"}
	if _, err := h.mcp.Initialize(ctx, initReq); err != nil {
		t.Fatalf("MCP initialize: %v", err)
	}

	h.guard(t, mode)
	h.observer = newSandboxObserver(t, h)
	t.Logf("sandbox harness ready: run %s, %d Things Cloud requests so far, one every %s at most", h.runID, h.cloud.stats().Requests, interval)
	return h
}

// guard is the account check of AGENTS.md rule 1; the firewall stays locked
// until it passes.
func (h *sandboxHarness) guard(t *testing.T, mode harnessMode) {
	t.Helper()
	verified, err := client.Verify()
	if err != nil {
		t.Fatalf("verify the sandbox account: %s", h.red.redact(err.Error()))
	}
	areas, err := readState().AllAreasWithOpts(listOpts{})
	if err != nil {
		t.Fatalf("read areas: %v", err)
	}
	titles := make([]string, len(areas))
	for i, a := range areas {
		titles[i] = a.Title
	}
	if err := checkSandboxAccount(h.creds, os.Getenv("THINGS_USERNAME"), verified.Email, titles); err != nil {
		t.Fatal(err)
	}
	if mode == harnessReadOnly {
		h.firewall.setMode(firewallReadOnly)
	} else {
		h.firewall.setMode(firewallWrites)
	}
}

func warnAboutEnvFile(t *testing.T) {
	path := os.Getenv(envSandboxEnvFile)
	if path == "" {
		return
	}
	if info, err := os.Stat(path); err == nil && info.Mode().Perm()&0o077 != 0 {
		t.Logf("warning: %s is readable by group or others; chmod 600 it", envSandboxEnvFile)
	}
	abs, err := filepath.Abs(path)
	wd, wdErr := os.Getwd()
	if err == nil && wdErr == nil && strings.HasPrefix(abs, filepath.Dir(wd)+string(filepath.Separator)) {
		t.Logf("warning: %s points inside the repository; keep sandbox credentials outside it", envSandboxEnvFile)
	}
}

// call invokes a tool through /mcp. It stops the test when the firewall
// refused a commit or Things Cloud rate-limited the run.
func (h *sandboxHarness) call(t *testing.T, name string, args map[string]any) (string, bool) {
	t.Helper()
	h.mu.Lock()
	h.covered[name] = true
	h.mu.Unlock()

	before := h.firewall.violationCount()
	var req mcp.CallToolRequest
	req.Params.Name = name
	req.Params.Arguments = args
	ctx, cancel := context.WithTimeout(context.Background(), sandboxCallTimeout)
	defer cancel()
	res, err := h.mcp.CallTool(ctx, req)
	if v := h.firewall.violationsSince(before); len(v) > 0 {
		t.Fatalf("%s: the sandbox firewall refused a commit the server tried to send:\n  %s", name, strings.Join(v, "\n  "))
	}
	if h.cloud.stats().RateLimited {
		t.Fatalf("%s: Things Cloud answered 429; stop, wait, and retry with a larger %s", name, envSandboxInterval)
	}
	if err != nil {
		t.Fatalf("%s: %s", name, h.red.redact(err.Error()))
	}
	return h.red.redact(resultText(res)), res.IsError
}

func (h *sandboxHarness) mustCall(t *testing.T, name string, args map[string]any) string {
	t.Helper()
	text, isErr := h.call(t, name, args)
	if isErr {
		t.Fatalf("%s %v failed: %s", name, args, text)
	}
	return text
}

func resultText(res *mcp.CallToolResult) string {
	var parts []string
	for _, c := range res.Content {
		switch tc := c.(type) {
		case mcp.TextContent:
			parts = append(parts, tc.Text)
		case *mcp.TextContent:
			parts = append(parts, tc.Text)
		}
	}
	return strings.Join(parts, "\n")
}

func decodeAs[T any](t *testing.T, what, text string) T {
	t.Helper()
	var v T
	if err := json.Unmarshal([]byte(text), &v); err != nil {
		t.Fatalf("%s: cannot decode the %d-byte result: %v", what, len(text), err)
	}
	return v
}

// create calls a create tool and returns the new UUID.
func (h *sandboxHarness) create(t *testing.T, tool string, args map[string]any) string {
	t.Helper()
	out := decodeAs[map[string]string](t, tool, h.mustCall(t, tool, args))
	if out["status"] != "created" || !isSafeGeneratedID(out["uuid"]) {
		t.Fatalf("%s returned %v", tool, out)
	}
	return out["uuid"]
}

// act calls a write tool and checks the status it reports.
func (h *sandboxHarness) act(t *testing.T, tool string, args map[string]any, wantStatus string) {
	t.Helper()
	out := decodeAs[map[string]string](t, tool, h.mustCall(t, tool, args))
	if out["status"] != wantStatus {
		t.Fatalf("%s status = %q, want %q", tool, out["status"], wantStatus)
	}
}

func (h *sandboxHarness) getTask(t *testing.T, uuid string) taskOutput {
	t.Helper()
	return decodeAs[taskOutput](t, "things_get_task", h.mustCall(t, "things_get_task", map[string]any{"uuid": uuid}))
}

func (h *sandboxHarness) listTasks(t *testing.T, tool string, args map[string]any) []taskOutput {
	t.Helper()
	if args == nil {
		args = map[string]any{}
	}
	return decodeAs[[]taskOutput](t, tool, h.mustCall(t, tool, args))
}

func containsTask(list []taskOutput, uuid string) bool {
	for _, o := range list {
		if o.UUID == uuid {
			return true
		}
	}
	return false
}

var viewTools = []struct {
	name string
	tool string
	view listView
}{
	{"inbox", "things_list_inbox", viewInbox},
	{"today", "things_list_today", viewToday},
	{"anytime", "things_list_anytime", viewAnytime},
	{"someday", "things_list_someday", viewSomeday},
	{"upcoming", "things_list_upcoming", viewUpcoming},
}

// viewsOf asks every list tool whether it shows uuid.
func (h *sandboxHarness) viewsOf(t *testing.T, uuid string) []string {
	t.Helper()
	var in []string
	for _, v := range viewTools {
		if containsTask(h.listTasks(t, v.tool, nil), uuid) {
			in = append(in, v.name)
		}
	}
	return in
}

func viewName(v listView) string {
	for _, vt := range viewTools {
		if vt.view == v {
			return vt.name
		}
	}
	return fmt.Sprintf("view %d", v)
}

func (h *sandboxHarness) assertOnlyIn(t *testing.T, uuid, view string) {
	t.Helper()
	if got := h.viewsOf(t, uuid); len(got) != 1 || got[0] != view {
		t.Errorf("%s is listed in %v, want only %s", uuid, got, view)
	}
}

func (h *sandboxHarness) coveredTools() map[string]bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make(map[string]bool, len(h.covered))
	for k, v := range h.covered {
		out[k] = v
	}
	return out
}

// ---------------------------------------------------------------------------
// Cleanup
// ---------------------------------------------------------------------------

type createdObject struct {
	UUID  string
	Kind  string
	Type  int64 // tp for Task6
	Title string
}

func (h *sandboxHarness) createdObjects() []createdObject {
	var out []createdObject
	for _, c := range h.cloud.committed() {
		for _, it := range c.Items {
			if it.Action != 0 || it.Kind == "Tombstone2" {
				continue
			}
			tp, _ := jsonInt(it.Payload["tp"])
			title, _ := jsonString(it.Payload["tt"])
			out = append(out, createdObject{UUID: it.UUID, Kind: it.Kind, Type: tp, Title: title})
		}
	}
	return out
}

// trashOrder trashes to-dos, then headings, then projects.
func trashOrder(tp int64) int {
	switch tp {
	case 0:
		return 0
	case 2:
		return 1
	default:
		return 2
	}
}

// cleanupRun trashes every to-do, heading and project the run created. It is
// idempotent and runs from t.Cleanup too, so a failed run still cleans up;
// leftovers of runs killed by a timeout go to TestSandboxCleanupLeftovers.
func (h *sandboxHarness) cleanupRun(t *testing.T) {
	h.mu.Lock()
	if h.cleaned {
		h.mu.Unlock()
		return
	}
	h.cleaned = true
	h.mu.Unlock()

	if h.cloud.stats().RateLimited {
		t.Logf("cleanup: waiting %s after a 429", sandboxRateLimitPause)
		time.Sleep(sandboxRateLimitPause)
		h.cloud.clearRateLimited()
	}
	if _, err := mirror.SyncNow(); err != nil {
		t.Logf("cleanup: refresh before trashing failed: %s", h.red.redact(err.Error()))
	}

	var tasks []createdObject
	var manual []string
	for _, o := range h.createdObjects() {
		switch o.Kind {
		case "Task6":
			tasks = append(tasks, o)
		case "Area3":
			manual = append(manual, "area "+o.UUID)
		case "Tag4":
			manual = append(manual, "tag "+o.UUID)
		}
	}
	sort.SliceStable(tasks, func(i, j int) bool { return trashOrder(tasks[i].Type) < trashOrder(tasks[j].Type) })

	trashed, failed := 0, 0
	for _, o := range tasks {
		if task, err := syncer.State().Task(o.UUID); err == nil && task != nil && task.InTrash {
			continue
		}
		if err := h.trash(o.UUID); err != nil {
			failed++
			t.Errorf("cleanup: could not trash %s: %s", o.UUID, h.red.redact(err.Error()))
			continue
		}
		trashed++
	}
	t.Logf("cleanup: trashed %d objects, %d failed (run TestSandboxCleanupLeftovers later for any left)", trashed, failed)
	if len(manual) > 0 {
		t.Logf("cleanup: areas and tags cannot be deleted through the server (no verified write); delete the ones titled %q… in the Things app when they pile up: %s", h.prefix, strings.Join(manual, ", "))
	}
}

// trash goes through /mcp and falls back to the write function directly.
func (h *sandboxHarness) trash(uuid string) error {
	var req mcp.CallToolRequest
	req.Params.Name = "things_trash_task"
	req.Params.Arguments = map[string]any{"uuid": uuid}
	ctx, cancel := context.WithTimeout(context.Background(), sandboxCallTimeout)
	defer cancel()
	res, err := h.mcp.CallTool(ctx, req)
	if err == nil && !res.IsError {
		return nil
	}
	return trashTask(uuid)
}

// ---------------------------------------------------------------------------
// Observer: a second, independent SDK client on the same account
// ---------------------------------------------------------------------------

type sandboxObserver struct {
	client  *things.Client
	history *things.History
	dbPath  string
	syncer  *sync.Syncer
	red     *redactor
}

func newSandboxObserver(t *testing.T, h *sandboxHarness) *sandboxObserver {
	t.Helper()
	c := things.New(things.APIEndpoint, h.creds.Username, h.creds.Password)
	hist, err := c.OwnHistory()
	if err != nil {
		t.Fatalf("observer: %s", h.red.redact(err.Error()))
	}
	if hist.ID != history.ID {
		t.Fatal("observer: the second client sees a different history than the server")
	}
	if err := hist.Sync(); err != nil {
		t.Fatalf("observer: history head: %s", h.red.redact(err.Error()))
	}
	h.startIndex = hist.LatestServerIndex
	o := &sandboxObserver{client: c, history: hist, dbPath: filepath.Join(t.TempDir(), "observer.db"), red: h.red}
	t.Cleanup(func() {
		if o.syncer != nil {
			o.syncer.Close()
		}
	})
	return o
}

// state syncs the observer's own mirror (a temp SQLite DB) and returns it.
func (o *sandboxObserver) state(t *testing.T) *sync.State {
	t.Helper()
	if o.syncer == nil {
		s, err := sync.Open(o.dbPath, o.client)
		if err != nil {
			t.Fatalf("observer: open mirror: %v", err)
		}
		o.syncer = s
	}
	if _, err := o.syncer.Sync(); err != nil {
		t.Fatalf("observer: sync mirror: %s", o.red.redact(err.Error()))
	}
	return o.syncer.State()
}

// itemsSince reads raw history from start to the head. Items whose payload is
// not an object (very old kinds) are skipped and counted.
func (o *sandboxObserver) itemsSince(start int) ([]wireItem, int, error) {
	h := o.client.HistoryWithID(o.history.ID)
	var out []wireItem
	skipped := 0
	for idx := start; ; {
		items, more, err := h.Items(things.ItemsOptions{StartIndex: idx})
		if err != nil {
			return nil, skipped, errors.New(o.red.redact(err.Error()))
		}
		for _, it := range items {
			w, err := wireItemFromHistory(it)
			if err != nil {
				skipped++
				continue
			}
			out = append(out, w)
		}
		if !more || h.LoadedServerIndex <= idx {
			return out, skipped, nil
		}
		idx = h.LoadedServerIndex
	}
}

// retitle commits a title-only Task6 modify, a verified write
// (docs/protocol/things-cloud.md §5), from the observer's own history cursor.
func (o *sandboxObserver) retitle(uuid, title string) error {
	if err := o.history.Sync(); err != nil {
		return err
	}
	return o.history.Write(writeEnvelope{id: uuid, action: 1, kind: "Task6", payload: newTaskUpdate().title(title).build()})
}
