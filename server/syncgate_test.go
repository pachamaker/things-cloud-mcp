package main

import (
	"errors"
	"fmt"
	"net/http"
	"runtime"
	gosync "sync"
	"sync/atomic"
	"testing"
	"time"

	things "github.com/arthursoares/things-cloud-sdk"
	"github.com/arthursoares/things-cloud-sdk/sync"
)

type fakeClock struct {
	mu  gosync.Mutex
	now time.Time
}

func newFakeClock() *fakeClock {
	return &fakeClock{now: time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// fakeSyncer stands in for *sync.Syncer. Like upstream, it mutates plain
// fields without locking, so `go test -race` reports unserialised calls.
type fakeSyncer struct {
	calls       int
	errs        []error
	inFlight    atomic.Int32
	maxInFlight atomic.Int32
}

func (f *fakeSyncer) Sync() ([]sync.Change, error) {
	n := f.inFlight.Add(1)
	defer f.inFlight.Add(-1)
	for {
		seen := f.maxInFlight.Load()
		if n <= seen || f.maxInFlight.CompareAndSwap(seen, n) {
			break
		}
	}
	runtime.Gosched()

	f.calls++
	if len(f.errs) == 0 {
		return nil, nil
	}
	err := f.errs[0]
	f.errs = f.errs[1:]
	return nil, err
}

func runConcurrently(n int, fn func()) {
	var wg gosync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			fn()
		}()
	}
	close(start)
	wg.Wait()
}

func http429() error {
	return &things.HTTPError{StatusCode: http.StatusTooManyRequests, Status: "429 Too Many Requests"}
}

func TestSyncGate_KI27_ConcurrentReadsShareOneSync(t *testing.T) {
	t.Parallel()

	inner := &fakeSyncer{}
	gate := newSyncGate(inner, newFakeClock().Now)

	runConcurrently(32, func() {
		if _, err := gate.Sync(); err != nil {
			t.Errorf("Sync() error = %v", err)
		}
	})

	if inner.calls != 1 {
		t.Errorf("inner Sync calls = %d, want 1", inner.calls)
	}
	if got := inner.maxInFlight.Load(); got != 1 {
		t.Errorf("max concurrent inner Sync calls = %d, want 1", got)
	}
}

func TestSyncGate_B1_SyncNowSerialisesEveryCall(t *testing.T) {
	t.Parallel()

	inner := &fakeSyncer{}
	gate := newSyncGate(inner, newFakeClock().Now)

	runConcurrently(32, func() {
		if _, err := gate.SyncNow(); err != nil {
			t.Errorf("SyncNow() error = %v", err)
		}
	})

	if inner.calls != 32 {
		t.Errorf("inner Sync calls = %d, want 32", inner.calls)
	}
	if got := inner.maxInFlight.Load(); got != 1 {
		t.Errorf("max concurrent inner Sync calls = %d, want 1", got)
	}
}

func TestSyncGate_FreshnessWindow(t *testing.T) {
	t.Parallel()

	clock := newFakeClock()
	inner := &fakeSyncer{}
	gate := newSyncGate(inner, clock.Now)

	steps := []struct {
		advance   time.Duration
		wantCalls int
	}{
		{0, 1},
		{2*time.Second - time.Nanosecond, 1},
		{time.Nanosecond, 2},
		{time.Second, 2},
	}
	for i, step := range steps {
		clock.Advance(step.advance)
		if _, err := gate.Sync(); err != nil {
			t.Fatalf("step %d: Sync() error = %v", i, err)
		}
		if inner.calls != step.wantCalls {
			t.Fatalf("step %d: inner Sync calls = %d, want %d", i, inner.calls, step.wantCalls)
		}
	}
}

func TestSyncGate_SyncNowIgnoresFreshSync(t *testing.T) {
	t.Parallel()

	inner := &fakeSyncer{}
	gate := newSyncGate(inner, newFakeClock().Now)

	if _, err := gate.Sync(); err != nil {
		t.Fatalf("Sync() error = %v", err)
	}
	if _, err := gate.SyncNow(); err != nil {
		t.Fatalf("SyncNow() error = %v", err)
	}
	if inner.calls != 2 {
		t.Errorf("inner Sync calls = %d, want 2", inner.calls)
	}
}

func TestSyncGate_FailedSyncIsSharedWithinWindow(t *testing.T) {
	t.Parallel()

	clock := newFakeClock()
	boom := errors.New("boom")
	inner := &fakeSyncer{errs: []error{boom}}
	gate := newSyncGate(inner, clock.Now)

	if _, err := gate.Sync(); !errors.Is(err, boom) {
		t.Fatalf("first Sync() error = %v, want boom", err)
	}
	if _, err := gate.Sync(); !errors.Is(err, boom) {
		t.Fatalf("coalesced Sync() error = %v, want boom", err)
	}
	if inner.calls != 1 {
		t.Fatalf("inner Sync calls = %d, want 1", inner.calls)
	}

	clock.Advance(2 * time.Second)
	if _, err := gate.Sync(); err != nil {
		t.Fatalf("Sync() after window error = %v", err)
	}
	if inner.calls != 2 {
		t.Errorf("inner Sync calls = %d, want 2", inner.calls)
	}
}

func TestSyncGate_KI27_BacksOffAfter429(t *testing.T) {
	t.Parallel()

	clock := newFakeClock()
	inner := &fakeSyncer{}
	gate := newSyncGate(inner, clock.Now)

	wantPauses := []time.Duration{
		10 * time.Second,
		20 * time.Second,
		40 * time.Second,
		80 * time.Second,
		160 * time.Second,
		5 * time.Minute,
		5 * time.Minute,
	}
	for i, pause := range wantPauses {
		inner.errs = []error{fmt.Errorf("fetch items: %w", http429())}
		if _, err := gate.Sync(); !errors.Is(err, errSyncRateLimited) {
			t.Fatalf("429 #%d: Sync() error = %v, want errSyncRateLimited", i+1, err)
		}
		calls := inner.calls

		clock.Advance(pause - time.Nanosecond)
		if _, err := gate.Sync(); !errors.Is(err, errSyncRateLimited) {
			t.Fatalf("429 #%d: Sync() during pause error = %v, want errSyncRateLimited", i+1, err)
		}
		if _, err := gate.SyncNow(); !errors.Is(err, errSyncRateLimited) {
			t.Fatalf("429 #%d: SyncNow() during pause error = %v, want errSyncRateLimited", i+1, err)
		}
		if inner.calls != calls {
			t.Fatalf("429 #%d: inner Sync called during pause (%d calls, want %d)", i+1, inner.calls, calls)
		}
		clock.Advance(time.Nanosecond)
	}

	if _, err := gate.Sync(); err != nil {
		t.Fatalf("Sync() after pause error = %v", err)
	}

	clock.Advance(2 * time.Second)
	inner.errs = []error{http429()}
	if _, err := gate.Sync(); !errors.Is(err, errSyncRateLimited) {
		t.Fatalf("429 after success: Sync() error = %v, want errSyncRateLimited", err)
	}
	clock.Advance(10 * time.Second)
	if _, err := gate.Sync(); err != nil {
		t.Errorf("success resets the backoff: Sync() 10s after a new 429 error = %v", err)
	}
}

func TestSyncGate_OtherHTTPErrorsDoNotPause(t *testing.T) {
	t.Parallel()

	clock := newFakeClock()
	unavailable := &things.HTTPError{StatusCode: http.StatusServiceUnavailable, Status: "503 Service Unavailable"}
	inner := &fakeSyncer{errs: []error{unavailable}}
	gate := newSyncGate(inner, clock.Now)

	if _, err := gate.Sync(); !errors.Is(err, unavailable) || errors.Is(err, errSyncRateLimited) {
		t.Fatalf("Sync() error = %v, want the 503 without errSyncRateLimited", err)
	}
	clock.Advance(2 * time.Second)
	if _, err := gate.Sync(); err != nil {
		t.Fatalf("Sync() after window error = %v", err)
	}
	if inner.calls != 2 {
		t.Errorf("inner Sync calls = %d, want 2", inner.calls)
	}
}
