package main

import (
	"errors"
	"net/http"
	"strings"
	gosync "sync"
	"testing"
	"time"

	things "github.com/arthursoares/things-cloud-sdk"
	"github.com/arthursoares/things-cloud-sdk/sync"
)

// stubSyncer returns err from every Sync.
type stubSyncer struct {
	err   error
	calls int
}

func (s *stubSyncer) Sync() ([]sync.Change, error) {
	s.calls++
	return nil, s.err
}

// fakeGuard answers history checks with reason (or checkErr) and runs
// rebuilds that fail with resyncErr. With hold set, a rebuild waits until the
// test closes it.
type fakeGuard struct {
	mu        gosync.Mutex
	reason    string
	checkErr  error
	resyncErr error
	hold      chan struct{}
	checks    int
	resyncs   []time.Time
	clock     *fakeClock
}

func (f *fakeGuard) stale() (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.checks++
	return f.reason, f.checkErr
}

func (f *fakeGuard) resync() error {
	f.mu.Lock()
	hold := f.hold
	f.resyncs = append(f.resyncs, f.clock.Now())
	f.mu.Unlock()
	if hold != nil {
		<-hold
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.resyncErr
}

func (f *fakeGuard) set(update func(*fakeGuard)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	update(f)
}

func (f *fakeGuard) counts() (checks, resyncs int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.checks, len(f.resyncs)
}

func http404() error {
	return &things.HTTPError{StatusCode: http.StatusNotFound, Status: "404 Not Found"}
}

// newGuardedGate returns a gate whose last history check is now.
func newGuardedGate(inner changeSyncer, guard *fakeGuard, clock *fakeClock) *syncGate {
	gate := newSyncGate(inner, clock.Now)
	gate.guard = guard
	gate.lastCheck = clock.Now()
	return gate
}

func TestStaleReason_KI28(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		stored  mirrorIdentity
		current cloudIdentity
		want    string
	}{
		{"nothing synced yet", mirrorIdentity{}, cloudIdentity{key: "B", head: 7}, ""},
		{"same history, up to date", mirrorIdentity{key: "A", cursor: 7}, cloudIdentity{key: "A", head: 7}, ""},
		{"same history, behind", mirrorIdentity{key: "A", cursor: 3}, cloudIdentity{key: "A", head: 7}, ""},
		{"history key changed", mirrorIdentity{key: "A", cursor: 3}, cloudIdentity{key: "B", head: 7}, "the account's history key changed (account reset?)"},
		{"cursor ahead of the head", mirrorIdentity{key: "A", cursor: 4408}, cloudIdentity{key: "A", head: 54}, "the mirror's cursor 4408 is ahead of the history head 54"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := staleReason(tc.stored, tc.current); got != tc.want {
				t.Errorf("staleReason() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestSyncGate_KI28_PeriodicCheckRebuildsStaleMirror(t *testing.T) {
	t.Parallel()

	clock := newFakeClock()
	inner := &stubSyncer{}
	guard := &fakeGuard{reason: "the account's history key changed (account reset?)", hold: make(chan struct{}), clock: clock}
	gate := newGuardedGate(inner, guard, clock)

	clock.Advance(historyCheckInterval - time.Second)
	if _, err := gate.Sync(); err != nil {
		t.Fatalf("Sync() before the check is due: %v", err)
	}
	if checks, _ := guard.counts(); checks != 0 {
		t.Fatalf("history checks = %d before the interval, want 0", checks)
	}

	clock.Advance(time.Second)
	if _, err := gate.Sync(); !errors.Is(err, errMirrorRebuilding) {
		t.Fatalf("Sync() with a stale mirror: error = %v, want errMirrorRebuilding", err)
	}
	calls := inner.calls
	for _, call := range []func() ([]sync.Change, error){gate.Sync, gate.SyncNow, gate.SyncForWrite} {
		clock.Advance(syncFreshness)
		if _, err := call(); !errors.Is(err, errMirrorRebuilding) {
			t.Fatalf("call during the rebuild: error = %v, want errMirrorRebuilding", err)
		}
	}
	if inner.calls != calls {
		t.Errorf("inner Sync calls during the rebuild = %d, want %d", inner.calls, calls)
	}

	guard.set(func(f *fakeGuard) { f.reason = "" })
	close(guard.hold)
	gate.waitRebuild()
	if _, err := gate.Sync(); err != nil {
		t.Fatalf("Sync() after the rebuild: %v", err)
	}
	if inner.calls != calls+1 {
		t.Errorf("inner Sync calls after the rebuild = %d, want %d", inner.calls, calls+1)
	}
	if checks, resyncs := guard.counts(); checks != 1 || resyncs != 1 {
		t.Errorf("checks, rebuilds = %d, %d, want 1, 1", checks, resyncs)
	}
}

func TestSyncGate_KI28_404ChecksTheHistoryAtMostOnceAMinute(t *testing.T) {
	t.Parallel()

	clock := newFakeClock()
	inner := &stubSyncer{err: http404()}
	guard := &fakeGuard{clock: clock}
	gate := newGuardedGate(inner, guard, clock)

	steps := []struct {
		advance    time.Duration
		wantChecks int
	}{
		{historyRecheckAfter404, 1},
		{syncFreshness, 1},
		{historyRecheckAfter404 - syncFreshness - time.Second, 1},
		{time.Second, 2},
	}
	for i, step := range steps {
		clock.Advance(step.advance)
		_, err := gate.Sync()
		if !errors.Is(err, errHistoryNotFound) {
			t.Fatalf("step %d: error = %v, want errHistoryNotFound", i, err)
		}
		if checks, resyncs := guard.counts(); checks != step.wantChecks || resyncs != 0 {
			t.Fatalf("step %d: checks, rebuilds = %d, %d, want %d, 0", i, checks, resyncs, step.wantChecks)
		}
	}
}

func TestSyncGate_KI28_404WithStaleHistoryRebuilds(t *testing.T) {
	t.Parallel()

	clock := newFakeClock()
	inner := &stubSyncer{err: http404()}
	guard := &fakeGuard{reason: "the account's history key changed (account reset?)", clock: clock}
	gate := newGuardedGate(inner, guard, clock)

	clock.Advance(historyRecheckAfter404)
	if _, err := gate.Sync(); !errors.Is(err, errMirrorRebuilding) {
		t.Fatalf("error = %v, want errMirrorRebuilding", err)
	}
	gate.waitRebuild()
	inner.err = nil
	if _, err := gate.Sync(); err != nil {
		t.Fatalf("Sync() after the rebuild: %v", err)
	}
}

func TestSyncGate_KI28_FailedHistoryCheckKeepsAGoodRead(t *testing.T) {
	t.Parallel()

	clock := newFakeClock()
	guard := &fakeGuard{checkErr: errors.New("verify the account: http response code: 503"), clock: clock}
	gate := newGuardedGate(&stubSyncer{}, guard, clock)

	clock.Advance(historyCheckInterval)
	if _, err := gate.Sync(); err != nil {
		t.Fatalf("Sync() with a failing history check: %v", err)
	}
	clock.Advance(syncFreshness)
	if _, err := gate.Sync(); err != nil {
		t.Fatalf("second Sync(): %v", err)
	}
	if checks, _ := guard.counts(); checks != 1 {
		t.Errorf("history checks = %d, want 1 (a failed check waits for the next interval)", checks)
	}
}

func TestSyncGate_KI28_WriteChecksAHistoryOlderThanAMinute(t *testing.T) {
	t.Parallel()

	clock := newFakeClock()
	guard := &fakeGuard{clock: clock}
	gate := newGuardedGate(&stubSyncer{}, guard, clock)

	clock.Advance(historyCheckBeforeWrite - time.Second)
	if _, err := gate.SyncForWrite(); err != nil {
		t.Fatal(err)
	}
	if checks, _ := guard.counts(); checks != 0 {
		t.Fatalf("history checks = %d within a minute of the last one, want 0", checks)
	}
	clock.Advance(time.Second)
	if _, err := gate.Sync(); err != nil {
		t.Fatal(err)
	}
	if checks, _ := guard.counts(); checks != 0 {
		t.Fatalf("a read checked the history after a minute (%d checks), want only writes", checks)
	}
	if _, err := gate.SyncForWrite(); err != nil {
		t.Fatal(err)
	}
	if checks, _ := guard.counts(); checks != 1 {
		t.Errorf("history checks = %d, want 1", checks)
	}
}

func TestSyncGate_KI28_FailedRebuildBacksOff(t *testing.T) {
	t.Parallel()

	clock := newFakeClock()
	inner := &stubSyncer{}
	guard := &fakeGuard{
		reason:    "the account's history key changed (account reset?)",
		resyncErr: errors.New("initial sync failed: http response code: 400 Bad Request"),
		clock:     clock,
	}
	gate := newGuardedGate(inner, guard, clock)

	clock.Advance(historyCheckInterval)
	if _, err := gate.Sync(); !errors.Is(err, errMirrorRebuilding) {
		t.Fatalf("error = %v, want errMirrorRebuilding", err)
	}
	gate.waitRebuild()
	calls := inner.calls

	wantWaits := []time.Duration{
		time.Minute, 2 * time.Minute, 4 * time.Minute, 8 * time.Minute,
		16 * time.Minute, 32 * time.Minute, time.Hour, time.Hour,
	}
	for i, wait := range wantWaits {
		_, err := gate.Sync()
		if !errors.Is(err, errMirrorRebuildFailed) {
			t.Fatalf("attempt %d: error = %v, want errMirrorRebuildFailed", i+1, err)
		}
		if want := "next rebuild attempt in " + wait.String(); !strings.Contains(err.Error(), want) {
			t.Fatalf("attempt %d: error %q does not say %q", i+1, err, want)
		}
		_, before := guard.counts()
		clock.Advance(wait - time.Second)
		if _, err := gate.Sync(); !errors.Is(err, errMirrorRebuildFailed) {
			t.Fatalf("attempt %d: during the backoff error = %v", i+1, err)
		}
		if _, after := guard.counts(); after != before {
			t.Fatalf("attempt %d: rebuilt during the backoff", i+1)
		}
		clock.Advance(time.Second)
		if _, err := gate.Sync(); !errors.Is(err, errMirrorRebuilding) {
			t.Fatalf("attempt %d: after the backoff error = %v, want errMirrorRebuilding", i+1, err)
		}
		gate.waitRebuild()
	}
	if inner.calls != calls {
		t.Errorf("inner Sync calls while the mirror was stale = %d, want %d", inner.calls, calls)
	}

	guard.set(func(f *fakeGuard) { f.resyncErr = nil })
	clock.Advance(time.Hour)
	if _, err := gate.Sync(); !errors.Is(err, errMirrorRebuilding) {
		t.Fatalf("error = %v, want errMirrorRebuilding", err)
	}
	gate.waitRebuild()
	if _, err := gate.Sync(); err != nil {
		t.Fatalf("Sync() after a successful rebuild: %v", err)
	}
}

// A rebuild that does not cure the mirror must not turn into a loop of full
// resyncs: rebuilds start at most once per backoff, which doubles up to an hour.
func TestSyncGate_KI28_NoRebuildLoop(t *testing.T) {
	t.Parallel()

	clock := newFakeClock()
	guard := &fakeGuard{reason: "the mirror's cursor 9 is ahead of the history head 2", clock: clock}
	gate := newGuardedGate(&stubSyncer{err: http404()}, guard, clock)

	for range 6 * 60 * 2 {
		clock.Advance(30 * time.Second)
		if _, err := gate.Sync(); err == nil {
			t.Fatal("Sync() succeeded on a mirror that stays stale")
		}
		gate.waitRebuild()
	}

	guard.mu.Lock()
	starts := append([]time.Time(nil), guard.resyncs...)
	guard.mu.Unlock()
	if len(starts) > 12 {
		t.Fatalf("%d rebuilds in 6 hours, want at most 12", len(starts))
	}
	for i := 1; i < len(starts); i++ {
		gap := starts[i].Sub(starts[i-1])
		want := min(rebuildBackoffMin<<(i-1), rebuildBackoffMax)
		if gap < want {
			t.Errorf("rebuild %d started %s after the previous one, want at least %s", i+1, gap, want)
		}
	}
}

func TestSyncGate_KI28_MatchingHistoryResetsTheRebuildBackoff(t *testing.T) {
	t.Parallel()

	const reset = "the account's history key changed (account reset?)"
	clock := newFakeClock()
	guard := &fakeGuard{reason: reset, clock: clock}
	gate := newGuardedGate(&stubSyncer{}, guard, clock)

	clock.Advance(historyCheckInterval)
	if _, err := gate.Sync(); !errors.Is(err, errMirrorRebuilding) {
		t.Fatalf("error = %v, want errMirrorRebuilding", err)
	}
	gate.waitRebuild()

	guard.set(func(f *fakeGuard) { f.reason = "" })
	clock.Advance(historyCheckInterval)
	if _, err := gate.Sync(); err != nil {
		t.Fatal(err)
	}

	guard.set(func(f *fakeGuard) { f.reason, f.resyncErr = reset, errors.New("initial sync failed: boom") })
	clock.Advance(historyCheckInterval)
	if _, err := gate.Sync(); !errors.Is(err, errMirrorRebuilding) {
		t.Fatalf("a new reset after a matching check: error = %v, want an immediate rebuild", err)
	}
	gate.waitRebuild()
	_, err := gate.Sync()
	if want := "next rebuild attempt in " + rebuildBackoffMin.String(); err == nil || !strings.Contains(err.Error(), want) {
		t.Errorf("error = %v, want the backoff to start again at %s", err, rebuildBackoffMin)
	}
}
