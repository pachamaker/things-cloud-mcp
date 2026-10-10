package main

import (
	"errors"
	"fmt"
	"net/http"
	gosync "sync"
	"time"

	things "github.com/arthursoares/things-cloud-sdk"
	"github.com/arthursoares/things-cloud-sdk/sync"
)

const (
	syncFreshness  = 2 * time.Second
	syncBackoffMin = 10 * time.Second
	syncBackoffMax = 5 * time.Minute
)

var errSyncRateLimited = errors.New("sync rate limited by Things Cloud (HTTP 429)")

type changeSyncer interface {
	Sync() ([]sync.Change, error)
}

// syncGate serialises and coalesces Syncer.Sync calls. things-cloud-sdk
// v0.6.0 Syncer has no mutex: Sync swaps its executor for a transaction and
// mutates its History, so concurrent calls race (spike report B1). Reads via
// Syncer.State use the raw pool and do not go through the gate. Bursts of tool
// calls trip Things Cloud's 429 AbusePrevention (KI-27), so a 429 pauses all
// syncs with exponential backoff.
type syncGate struct {
	inner  changeSyncer
	now    func() time.Time
	window time.Duration

	mu          gosync.Mutex
	lastStart   time.Time
	lastErr     error
	backoff     time.Duration
	pausedUntil time.Time
}

func newSyncGate(inner changeSyncer, now func() time.Time) *syncGate {
	return &syncGate{inner: inner, now: now, window: syncFreshness}
}

// Sync is for reads: it returns the outcome of a sync that started less than
// the freshness window before the call, so a burst of reads costs one round
// trip. Coalesced calls return no changes.
func (g *syncGate) Sync() ([]sync.Change, error) {
	return g.sync(g.window)
}

// SyncNow is for read-after-write: only a sync that starts after the call can
// include the write that preceded it.
func (g *syncGate) SyncNow() ([]sync.Change, error) {
	return g.sync(0)
}

func (g *syncGate) sync(window time.Duration) ([]sync.Change, error) {
	arrived := g.now()
	g.mu.Lock()
	defer g.mu.Unlock()

	if g.lastStart.After(arrived.Add(-window)) {
		return nil, g.lastErr
	}
	start := g.now()
	if start.Before(g.pausedUntil) {
		return nil, fmt.Errorf("%w: syncs paused for another %s", errSyncRateLimited, g.pausedUntil.Sub(start).Round(time.Second))
	}

	g.lastStart = start
	changes, err := g.inner.Sync()
	err = redactRequestURL(err)
	if !isRateLimited(err) {
		g.backoff = 0
		g.lastErr = err
		return changes, err
	}
	g.backoff = min(max(2*g.backoff, syncBackoffMin), syncBackoffMax)
	g.pausedUntil = g.now().Add(g.backoff)
	g.lastErr = fmt.Errorf("%w: syncs paused for %s: %w", errSyncRateLimited, g.backoff, err)
	return nil, g.lastErr
}

func isRateLimited(err error) bool {
	var httpErr *things.HTTPError
	return errors.As(err, &httpErr) && httpErr.StatusCode == http.StatusTooManyRequests
}
