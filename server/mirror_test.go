package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	gosync "sync"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
)

// The history keys stand in for real ones: each opens a whole task database
// without the password, so no error or log line may carry them (KI-35).
const (
	ki28Email    = "owner@example.com"
	ki28KeyA     = "KI28-history-key-before-the-reset"
	ki28KeyB     = "KI28-history-key-after-the-reset"
	ki28PageSize = 2
)

// ki28Cloud is an in-memory Things Cloud: the account's current history key
// and the histories it can serve. A history it does not hold answers 404
// AccountIssue, as Things Cloud does for the key of a reset account.
type ki28Cloud struct {
	mu        gosync.Mutex
	key       string
	histories map[string][]map[string]json.RawMessage
	broken    map[string]bool          // items of these histories answer 400
	hold      map[string]chan struct{} // items of these histories wait for the channel
	commits   int
}

func newKI28Cloud() *ki28Cloud {
	return &ki28Cloud{key: ki28KeyA, histories: map[string][]map[string]json.RawMessage{}, broken: map[string]bool{}, hold: map[string]chan struct{}{}}
}

// addTask appends a slot that creates an open to-do in Anytime.
func (c *ki28Cloud) addTask(key, title string) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	id := generateUUID()
	env, _ := json.Marshal(map[string]any{"t": 0, "e": "Task6", "p": map[string]any{"tt": title, "tp": 0, "ss": 0, "st": 1}})
	c.histories[key] = append(c.histories[key], map[string]json.RawMessage{id: env})
	return id
}

// resetAccount gives the account a new history; keepOld leaves the old one
// readable but frozen instead of deleting it.
func (c *ki28Cloud) resetAccount(newKey string, keepOld bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !keepOld {
		delete(c.histories, c.key)
	}
	c.key = newKey
	c.histories[newKey] = nil
}

func (c *ki28Cloud) update(fn func(*ki28Cloud)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	fn(c)
}

func (c *ki28Cloud) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	c.mu.Lock()
	reply := func(status int, v any) {
		c.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if status == http.StatusNotFound {
			w.Header().Set("Things-Response", "AccountIssue")
		}
		w.WriteHeader(status)
		json.NewEncoder(w).Encode(v)
	}

	path := r.URL.Path
	if path == "/version/1/account/"+ki28Email && r.Method == http.MethodGet {
		reply(http.StatusOK, map[string]any{"email": ki28Email, "history-key": c.key, "status": "SYAccountStatusActive"})
		return
	}
	rest, ok := strings.CutPrefix(path, "/version/1/history/")
	if !ok {
		reply(http.StatusNotFound, map[string]any{})
		return
	}
	key, sub, _ := strings.Cut(rest, "/")
	slots, exists := c.histories[key]
	switch {
	case !exists:
		reply(http.StatusNotFound, map[string]any{})
	case sub == "" && r.Method == http.MethodGet:
		reply(http.StatusOK, map[string]any{"latest-server-index": len(slots), "latest-schema-version": 301, "is-empty": len(slots) == 0})
	case sub == "items" && r.Method == http.MethodGet:
		if hold := c.hold[key]; hold != nil {
			c.mu.Unlock()
			<-hold
			c.mu.Lock()
			slots = c.histories[key]
		}
		if c.broken[key] {
			reply(http.StatusBadRequest, map[string]any{})
			return
		}
		start, err := strconv.Atoi(r.URL.Query().Get("start-index"))
		if err != nil || start < 0 || start > len(slots) {
			reply(http.StatusInternalServerError, map[string]any{})
			return
		}
		end := min(start+ki28PageSize, len(slots))
		reply(http.StatusOK, map[string]any{"items": slots[start:end], "current-item-index": len(slots), "schema": 301})
	case sub == "commit" && r.Method == http.MethodPost:
		c.commits++
		reply(http.StatusConflict, map[string]any{})
	default:
		reply(http.StatusNotFound, map[string]any{})
	}
}

// lockedBuffer collects log output written by the rebuild goroutine too.
type lockedBuffer struct {
	mu  gosync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// ki28Server serves cloud over HTTP, keeps the server globals for the test
// and captures the log. Callers must not be parallel.
type ki28Server struct {
	t       *testing.T
	cloud   *ki28Cloud
	url     string
	dataDir string
	logs    *lockedBuffer
	close   func() error
}

func startKI28Server(t *testing.T, cloud *ki28Cloud) *ki28Server {
	t.Helper()
	srv := httptest.NewServer(cloud)
	prevClient, prevMirrorDB, prevMirror, prevHistory := client, mirrorDB, mirror, history
	prevLog := log.Writer()
	s := &ki28Server{t: t, cloud: cloud, url: srv.URL, dataDir: t.TempDir(), logs: &lockedBuffer{}}
	log.SetOutput(s.logs)
	t.Cleanup(func() {
		s.stop()
		srv.Close()
		log.SetOutput(prevLog)
		client, mirrorDB, mirror, history = prevClient, prevMirrorDB, prevMirror, prevHistory
		for _, key := range []string{ki28KeyA, ki28KeyB} {
			if strings.Contains(s.logs.String(), key) {
				t.Errorf("the log contains a history key:\n%s", s.logs.String())
			}
		}
	})
	return s
}

// connect starts the server the way main does.
func (s *ki28Server) connect() error {
	s.t.Helper()
	closeMirror, err := connectThingsAt(s.url, ki28Email, "not-a-real-password", s.dataDir)
	if err == nil {
		s.close = closeMirror
	}
	return err
}

func (s *ki28Server) mustConnect() {
	s.t.Helper()
	if err := s.connect(); err != nil {
		s.t.Fatalf("connectThings: %v", err)
	}
}

func (s *ki28Server) stop() {
	if mirror != nil {
		mirror.waitRebuild()
	}
	if s.close != nil {
		s.close()
		s.close = nil
	}
}

// useClock puts the running gate on clock, with the last history check now.
func (s *ki28Server) useClock(clock *fakeClock) {
	mirror.mu.Lock()
	defer mirror.mu.Unlock()
	mirror.now = clock.Now
	mirror.lastCheck = clock.Now()
}

func (s *ki28Server) backup() string {
	return syncDBPath(s.dataDir) + mirrorBackupSuffix
}

// getTask calls things_get_task and returns the tool error text, or "".
func getTask(t *testing.T, uuid string) string {
	t.Helper()
	var req mcp.CallToolRequest
	req.Params.Arguments = map[string]any{"uuid": uuid}
	res, err := mcpGetTask(context.Background(), req)
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if !res.IsError {
		return ""
	}
	return toolErrorText(t, res, nil)
}

func assertNoKI28Key(t *testing.T, where, s string) {
	t.Helper()
	for _, key := range []string{ki28KeyA, ki28KeyB} {
		if strings.Contains(s, key) {
			t.Errorf("%s contains a history key: %q", where, s)
		}
	}
}

func assertMirrorHas(t *testing.T, present, absent []string) {
	t.Helper()
	for _, id := range present {
		if got := getTask(t, id); got != "" {
			t.Errorf("task %s: %s, want it in the mirror", id, got)
		}
	}
	for _, id := range absent {
		if got := getTask(t, id); got != "task not found" {
			t.Errorf("task %s: %q, want \"task not found\"", id, got)
		}
	}
}

func assertWriterOn(t *testing.T, key string) {
	t.Helper()
	historyMu.Lock()
	defer historyMu.Unlock()
	if history == nil || history.ID != key {
		t.Errorf("the writer does not follow the account's current history")
	}
}

func assertBackupOf(t *testing.T, path, key string) {
	t.Helper()
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("backup: %v", err)
	}
	id, err := storedIdentity(path)
	if err != nil {
		t.Fatalf("read the backup: %v", err)
	}
	if id.key != key {
		t.Errorf("the backup is not the mirror of the old history")
	}
}

func TestConnectThings_KI28_RebuildsAfterTheHistoryKeyChanged(t *testing.T) {
	cloud := newKI28Cloud()
	old := cloud.addTask(ki28KeyA, "before the reset")
	s := startKI28Server(t, cloud)
	s.mustConnect()
	assertMirrorHas(t, []string{old}, nil)
	s.stop()

	cloud.resetAccount(ki28KeyB, false)
	fresh := cloud.addTask(ki28KeyB, "after the reset")
	s.mustConnect()

	assertMirrorHas(t, []string{fresh}, []string{old})
	assertWriterOn(t, ki28KeyB)
	assertBackupOf(t, s.backup(), ki28KeyA)
	if !strings.Contains(s.logs.String(), "the account's history key changed (account reset?)") {
		t.Errorf("the log does not say why the mirror was rebuilt:\n%s", s.logs.String())
	}
}

func TestConnectThings_KI28_RebuildsWhenTheCursorIsAheadOfTheHead(t *testing.T) {
	cloud := newKI28Cloud()
	var old []string
	for i := range 5 {
		old = append(old, cloud.addTask(ki28KeyA, fmt.Sprintf("old %d", i)))
	}
	s := startKI28Server(t, cloud)
	s.mustConnect()
	s.stop()

	cloud.update(func(c *ki28Cloud) { c.histories[ki28KeyA] = nil })
	fresh := cloud.addTask(ki28KeyA, "replaced history")
	s.mustConnect()

	assertMirrorHas(t, []string{fresh}, old)
	assertBackupOf(t, s.backup(), ki28KeyA)
	if !strings.Contains(s.logs.String(), "the mirror's cursor 5 is ahead of the history head 1") {
		t.Errorf("the log does not say why the mirror was rebuilt:\n%s", s.logs.String())
	}
}

func TestConnectThings_KI28_KeepsAMatchingMirror(t *testing.T) {
	cloud := newKI28Cloud()
	first := cloud.addTask(ki28KeyA, "first")
	s := startKI28Server(t, cloud)
	s.mustConnect()
	s.stop()

	second := cloud.addTask(ki28KeyA, "second")
	s.mustConnect()

	assertMirrorHas(t, []string{first, second}, nil)
	if _, err := os.Stat(s.backup()); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("a matching mirror was set aside (stat error %v)", err)
	}
}

func TestConnectThings_KI28_NoHistoryKey(t *testing.T) {
	cloud := newKI28Cloud()
	cloud.addTask(ki28KeyA, "task")
	s := startKI28Server(t, cloud)
	s.mustConnect()
	s.stop()

	cloud.update(func(c *ki28Cloud) { c.key = "" })
	err := s.connect()
	if err == nil || !strings.Contains(err.Error(), "Things Cloud returned no history key for the account") {
		t.Fatalf("connectThings error = %v, want the missing history key", err)
	}
	if _, err := os.Stat(s.backup()); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the mirror was set aside without a history to rebuild from (stat error %v)", err)
	}
}

func TestMirror_KI28_RebuildsAfter404WhileRunning(t *testing.T) {
	cloud := newKI28Cloud()
	old := cloud.addTask(ki28KeyA, "before the reset")
	s := startKI28Server(t, cloud)
	s.mustConnect()
	clock := newFakeClock()
	s.useClock(clock)

	cloud.resetAccount(ki28KeyB, false)
	fresh := cloud.addTask(ki28KeyB, "after the reset")
	hold := make(chan struct{})
	cloud.update(func(c *ki28Cloud) { c.hold[ki28KeyB] = hold })

	clock.Advance(historyRecheckAfter404)
	got := getTask(t, fresh)
	if !strings.Contains(got, errMirrorRebuilding.Error()) {
		t.Fatalf("tool error = %q, want the rebuild notice", got)
	}
	assertNoKI28Key(t, "tool error", got)
	clock.Advance(syncFreshness)
	if got := getTask(t, fresh); !strings.Contains(got, errMirrorRebuilding.Error()) {
		t.Fatalf("tool error during the rebuild = %q, want the rebuild notice", got)
	}

	close(hold)
	mirror.waitRebuild()
	assertMirrorHas(t, []string{fresh}, []string{old})
	assertWriterOn(t, ki28KeyB)
	assertBackupOf(t, s.backup(), ki28KeyA)
}

func TestMirror_KI28_PeriodicCheckFindsAFrozenOldHistory(t *testing.T) {
	cloud := newKI28Cloud()
	old := cloud.addTask(ki28KeyA, "before the reset")
	s := startKI28Server(t, cloud)
	s.mustConnect()
	clock := newFakeClock()
	s.useClock(clock)

	cloud.resetAccount(ki28KeyB, true)
	fresh := cloud.addTask(ki28KeyB, "after the reset")
	clock.Advance(historyCheckInterval - time.Second)
	if got := getTask(t, old); got != "" {
		t.Fatalf("read before the check is due: %q", got)
	}

	clock.Advance(time.Second)
	if got := getTask(t, old); !strings.Contains(got, errMirrorRebuilding.Error()) {
		t.Fatalf("tool error = %q, want the rebuild notice", got)
	}
	mirror.waitRebuild()
	assertMirrorHas(t, []string{fresh}, []string{old})
	assertWriterOn(t, ki28KeyB)
}

func TestMirror_KI28_WriteChecksTheHistoryFirst(t *testing.T) {
	cloud := newKI28Cloud()
	old := cloud.addTask(ki28KeyA, "before the reset")
	s := startKI28Server(t, cloud)
	s.mustConnect()
	clock := newFakeClock()
	s.useClock(clock)

	cloud.resetAccount(ki28KeyB, true)
	clock.Advance(historyCheckBeforeWrite)
	var req mcp.CallToolRequest
	req.Params.Arguments = map[string]any{"uuid": old}
	res, err := mcpCompleteTask(context.Background(), req)
	if got := toolErrorText(t, res, err); !strings.Contains(got, errMirrorRebuilding.Error()) {
		t.Fatalf("tool error = %q, want the rebuild notice", got)
	}
	mirror.waitRebuild()
	cloud.update(func(c *ki28Cloud) {
		if c.commits != 0 {
			t.Errorf("%d commits reached Things Cloud, want none", c.commits)
		}
	})
	assertWriterOn(t, ki28KeyB)
}

func TestMirror_KI28_FailedRebuildIsReportedAndResumed(t *testing.T) {
	cloud := newKI28Cloud()
	old := cloud.addTask(ki28KeyA, "before the reset")
	s := startKI28Server(t, cloud)
	s.mustConnect()
	clock := newFakeClock()
	s.useClock(clock)

	cloud.resetAccount(ki28KeyB, false)
	fresh := cloud.addTask(ki28KeyB, "after the reset")
	cloud.update(func(c *ki28Cloud) { c.broken[ki28KeyB] = true })
	clock.Advance(historyRecheckAfter404)
	if got := getTask(t, fresh); !strings.Contains(got, errMirrorRebuilding.Error()) {
		t.Fatalf("tool error = %q, want the rebuild notice", got)
	}
	mirror.waitRebuild()

	got := getTask(t, fresh)
	for _, want := range []string{errMirrorRebuildFailed.Error(), "400 Bad Request", "next rebuild attempt in 1m0s"} {
		if !strings.Contains(got, want) {
			t.Errorf("tool error = %q, want it to contain %q", got, want)
		}
	}
	assertNoKI28Key(t, "tool error", got)

	cloud.update(func(c *ki28Cloud) { c.broken[ki28KeyB] = false })
	clock.Advance(rebuildBackoffMin)
	if got := getTask(t, fresh); !strings.Contains(got, errMirrorRebuilding.Error()) {
		t.Fatalf("tool error after the backoff = %q, want the rebuild notice", got)
	}
	mirror.waitRebuild()
	assertMirrorHas(t, []string{fresh}, []string{old})
	assertWriterOn(t, ki28KeyB)
	assertBackupOf(t, s.backup(), ki28KeyA)
	if n := strings.Count(s.logs.String(), "setting the mirror aside"); n != 1 {
		t.Errorf("the mirror was set aside %d times, want once: the retry resumes the new mirror", n)
	}
}

func TestMirrorFile_KI28_ResetMovesTheWholeDatabase(t *testing.T) {
	cloud := newKI28Cloud()
	cloud.addTask(ki28KeyA, "task")
	s := startKI28Server(t, cloud)
	s.mustConnect()

	path := syncDBPath(s.dataDir)
	if err := os.WriteFile(s.backup()+"-wal", []byte("stale"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := mirrorDB.reset(); err != nil {
		t.Fatalf("reset: %v", err)
	}
	if _, err := os.Stat(s.backup() + "-wal"); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("an older backup's WAL survived next to the new backup (stat error %v)", err)
	}
	assertBackupOf(t, s.backup(), ki28KeyA)
	id, err := mirrorDB.stored()
	if err != nil || id != (mirrorIdentity{}) {
		t.Errorf("stored() after reset = %+v, %v, want an empty mirror", id, err)
	}
	entries, _ := filepath.Glob(path + "*")
	for _, e := range entries {
		info, err := os.Stat(e)
		if err == nil && info.Mode().Perm()&0o077 != 0 && filepath.Dir(e) != s.dataDir {
			t.Errorf("%s is readable by others", e)
		}
	}
}
