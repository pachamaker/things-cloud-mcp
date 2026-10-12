package main

import (
	"errors"
	"fmt"
	"log"
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

	// History checks (KI-28): one account verify and one history head read.
	historyCheckInterval    = 15 * time.Minute
	historyCheckBeforeWrite = time.Minute
	historyRecheckAfter404  = time.Minute
	rebuildBackoffMin       = time.Minute
	rebuildBackoffMax       = time.Hour
)

var (
	errSyncRateLimited = errors.New("sync rate limited by Things Cloud (HTTP 429)")

	errHistoryNotFound     = errors.New("Things Cloud answered 404 for the mirrored history (account reset or account issue, KI-28)")
	errMirrorRebuilding    = errors.New("the local Things mirror is being rebuilt because the account's Things Cloud history changed (KI-28); try again in a minute")
	errMirrorStale         = errors.New("the local Things mirror no longer follows the account's Things Cloud history (KI-28)")
	errMirrorRebuildFailed = errors.New("rebuilding the local Things mirror failed (KI-28)")
)

type changeSyncer interface {
	Sync() ([]sync.Change, error)
}

// mirrorGuard compares the mirror with the account's current history and
// rebuilds it (KI-28). *historyGuard implements it.
type mirrorGuard interface {
	stale() (reason string, err error)
	resync() error
}

// syncGate serialises and coalesces Syncer.Sync calls. things-cloud-sdk
// v0.6.0 Syncer has no mutex: Sync swaps its executor for a transaction and
// mutates its History, so concurrent calls race (spike report B1). Reads via
// Syncer.State use the raw pool and do not go through the gate. Bursts of tool
// calls trip Things Cloud's 429 AbusePrevention (KI-27), so a 429 pauses all
// syncs with exponential backoff.
//
// With a guard, the gate also checks that the mirror still follows the
// account's history and rebuilds it in the background when it does not
// (KI-28). Calls fail fast while a rebuild runs, and rebuilds back off, so a
// mirror that stays stale cannot loop on full resyncs.
type syncGate struct {
	inner  changeSyncer
	guard  mirrorGuard
	now    func() time.Time
	window time.Duration

	mu          gosync.Mutex
	lastStart   time.Time
	lastErr     error
	backoff     time.Duration
	pausedUntil time.Time

	lastCheck      time.Time
	staleReason    string
	mirrorErr      error // set while the mirror waits for a rebuild or after a failed one
	rebuilding     bool
	rebuildDone    chan struct{}
	rebuildBackoff time.Duration
	nextRebuild    time.Time
}

func newSyncGate(inner changeSyncer, now func() time.Time) *syncGate {
	return &syncGate{inner: inner, now: now, window: syncFreshness}
}

// Sync is for reads: it returns the outcome of a sync that started less than
// the freshness window before the call, so a burst of reads costs one round
// trip. Coalesced calls return no changes.
func (g *syncGate) Sync() ([]sync.Change, error) {
	return g.sync(g.window, historyCheckInterval)
}

// SyncNow is for read-after-write: only a sync that starts after the call can
// include the write that preceded it.
func (g *syncGate) SyncNow() ([]sync.Change, error) {
	return g.sync(0, historyCheckInterval)
}

// SyncForWrite is for write preconditions: it also checks the account's
// history unless a check ran within the last minute, so a write does not land
// in a history an account reset left behind (KI-28).
func (g *syncGate) SyncForWrite() ([]sync.Change, error) {
	return g.sync(g.window, historyCheckBeforeWrite)
}

func (g *syncGate) sync(window, checkAge time.Duration) ([]sync.Change, error) {
	arrived := g.now()
	g.mu.Lock()
	defer g.mu.Unlock()

	if err := g.mirrorUnavailable(arrived); err != nil {
		return nil, err
	}
	changes, err := g.syncInner(arrived, window)
	if g.guard == nil {
		return changes, err
	}
	return g.checkHistory(changes, err, checkAge)
}

func (g *syncGate) syncInner(arrived time.Time, window time.Duration) ([]sync.Change, error) {
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

// checkHistory compares the mirror with the account's history when a check
// is due: after a successful sync once the last check is older than
// checkAge, and after a 404 on the mirrored history at most once a minute.
// A failed check never fails a successful read.
func (g *syncGate) checkHistory(changes []sync.Change, err error, checkAge time.Duration) ([]sync.Change, error) {
	notFound := isHistoryNotFound(err)
	if notFound {
		err = fmt.Errorf("%w: %w", errHistoryNotFound, err)
	}
	now := g.now()
	due := (err == nil && !now.Before(g.lastCheck.Add(checkAge))) ||
		(notFound && !now.Before(g.lastCheck.Add(historyRecheckAfter404)))
	if !due {
		return changes, err
	}

	g.lastCheck = now
	reason, checkErr := g.guard.stale()
	if checkErr != nil {
		checkErr = redactRequestURL(checkErr)
		log.Printf("[MIRROR] history check failed: %v", checkErr)
		if notFound {
			return nil, fmt.Errorf("%w (history check failed: %v)", err, checkErr)
		}
		return changes, err
	}
	if reason == "" {
		g.rebuildBackoff = 0
		return changes, err
	}
	g.staleReason = reason
	return nil, g.startRebuild(now)
}

// mirrorUnavailable is the error for a call while the mirror is being
// rebuilt, waits for a rebuild, or a rebuild failed. Once the backoff has
// passed, it starts the next rebuild.
func (g *syncGate) mirrorUnavailable(now time.Time) error {
	switch {
	case g.rebuilding:
		return errMirrorRebuilding
	case g.mirrorErr == nil:
		return nil
	case now.Before(g.nextRebuild):
		return fmt.Errorf("%w; next rebuild attempt in %s", g.mirrorErr, g.nextRebuild.Sub(now).Round(time.Second))
	}
	return g.startRebuild(now)
}

// startRebuild rebuilds the mirror in the background unless the previous
// rebuild started less than the rebuild backoff ago. The backoff doubles with
// every rebuild up to an hour and resets once a check finds the mirror on the
// account's history.
func (g *syncGate) startRebuild(now time.Time) error {
	if now.Before(g.nextRebuild) {
		g.mirrorErr = fmt.Errorf("%w: %s", errMirrorStale, g.staleReason)
		return g.mirrorUnavailable(now)
	}
	g.rebuildBackoff = min(max(2*g.rebuildBackoff, rebuildBackoffMin), rebuildBackoffMax)
	g.nextRebuild = now.Add(g.rebuildBackoff)
	g.rebuilding = true
	g.mirrorErr = nil
	done := make(chan struct{})
	g.rebuildDone = done
	log.Printf("[MIRROR] rebuilding the local mirror: %s", g.staleReason)

	go func() {
		defer close(done)
		err := redactRequestURL(g.guard.resync())

		g.mu.Lock()
		defer g.mu.Unlock()
		g.rebuilding = false
		g.lastStart = time.Time{}
		g.lastErr = nil
		if err != nil {
			log.Printf("[MIRROR] rebuild failed: %v", err)
			g.mirrorErr = fmt.Errorf("%w: %w", errMirrorRebuildFailed, err)
			return
		}
		log.Printf("[MIRROR] rebuild complete")
		g.staleReason = ""
		g.lastCheck = g.now()
	}()
	return errMirrorRebuilding
}

// waitRebuild blocks until the rebuild that is running, if any, ends.
func (g *syncGate) waitRebuild() {
	g.mu.Lock()
	done := g.rebuildDone
	g.mu.Unlock()
	if done != nil {
		<-done
	}
}

func isRateLimited(err error) bool {
	var httpErr *things.HTTPError
	return errors.As(err, &httpErr) && httpErr.StatusCode == http.StatusTooManyRequests
}

func isHistoryNotFound(err error) bool {
	var httpErr *things.HTTPError
	return errors.As(err, &httpErr) && httpErr.StatusCode == http.StatusNotFound
}
