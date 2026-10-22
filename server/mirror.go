package main

import (
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"net/url"
	"os"
	"path/filepath"
	gosync "sync"

	things "github.com/arthursoares/things-cloud-sdk"
	"github.com/arthursoares/things-cloud-sdk/sync"
)

// mirrorBackupSuffix names the copy of the mirror a rebuild sets aside. Only
// the latest copy is kept: the cache is disposable, and the copy holds the
// tasks of a history the account no longer uses (KI-28).
const mirrorBackupSuffix = ".before-rebuild"

// sqliteFileSuffixes are the files of one SQLite database in WAL mode.
var sqliteFileSuffixes = []string{"", "-wal", "-shm"}

var errMirrorClosed = errors.New("the local Things mirror is closed")

// mirrorFile owns the SQLite mirror and the SDK Syncer that fills it. A
// rebuild replaces both (KI-28), so readers take the state through State and
// never keep the Syncer.
type mirrorFile struct {
	path   string
	client *things.Client

	mu     gosync.RWMutex
	syncer *sync.Syncer // the latest Syncer, closed while open is false
	open   bool
	closed bool // Close was called
}

func openMirrorFile(path string, c *things.Client) (*mirrorFile, error) {
	s, err := sync.Open(path, c)
	if err != nil {
		return nil, err
	}
	return &mirrorFile{path: path, client: c, syncer: s, open: true}, nil
}

// current returns the open Syncer, reopening the file if a rebuild could not.
func (f *mirrorFile) current() (*sync.Syncer, error) {
	f.mu.RLock()
	s, open, closed := f.syncer, f.open, f.closed
	f.mu.RUnlock()
	switch {
	case open:
		return s, nil
	case closed:
		return nil, errMirrorClosed
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return nil, errMirrorClosed
	}
	if !f.open {
		s, err := sync.Open(f.path, f.client)
		if err != nil {
			return nil, fmt.Errorf("open the mirror: %w", err)
		}
		f.syncer, f.open = s, true
	}
	return f.syncer, nil
}

// Sync runs without the lock: the sync gate never syncs while a rebuild
// replaces the file, and Close must not wait for a full resync.
func (f *mirrorFile) Sync() ([]sync.Change, error) {
	s, err := f.current()
	if err != nil {
		return nil, err
	}
	return s.Sync()
}

// State is the read side of the mirror. Queries on a State taken before a
// rebuild replaced the file fail with "database is closed".
func (f *mirrorFile) State() *sync.State {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.syncer.State()
}

func (f *mirrorFile) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closed = true
	if !f.open {
		return nil
	}
	f.open = false
	return f.syncer.Close()
}

// stored is the history the mirror was built from and its cursor.
func (f *mirrorFile) stored() (mirrorIdentity, error) {
	if _, err := f.current(); err != nil {
		return mirrorIdentity{}, err
	}
	return storedIdentity(f.path)
}

// reset sets the mirror aside as <path>.before-rebuild, replacing an older
// copy, and opens an empty mirror in its place.
func (f *mirrorFile) reset() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return errMirrorClosed
	}
	if f.open {
		if err := f.syncer.Close(); err != nil {
			log.Printf("[MIRROR] closing the old mirror: %v", err)
		}
		f.open = false
	}
	backup := f.path + mirrorBackupSuffix
	for _, suffix := range sqliteFileSuffixes {
		if err := os.Remove(backup + suffix); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("remove the previous backup: %w", err)
		}
	}
	for _, suffix := range sqliteFileSuffixes {
		if err := os.Rename(f.path+suffix, backup+suffix); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("set the mirror aside: %w", err)
		}
	}
	s, err := sync.Open(f.path, f.client)
	if err != nil {
		return fmt.Errorf("open a fresh mirror: %w", err)
	}
	f.syncer, f.open = s, true
	return nil
}

// mirrorIdentity is the history a mirror was built from and how far it got.
type mirrorIdentity struct {
	key    string
	cursor int
}

// storedIdentity reads the history key and cursor the SDK keeps in its
// sync_state table. The SDK Syncer has no getter for the key; its own replay
// backup reads the same row (SDK sync/replay.go, readReplayBackupState). An
// empty key means nothing has been synced yet.
func storedIdentity(path string) (mirrorIdentity, error) {
	dsn := (&url.URL{Scheme: "file", Path: path, RawQuery: "mode=ro"}).String()
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return mirrorIdentity{}, err
	}
	defer db.Close()
	var id mirrorIdentity
	err = db.QueryRow(`SELECT history_id, server_index FROM sync_state WHERE id = 1`).Scan(&id.key, &id.cursor)
	if errors.Is(err, sql.ErrNoRows) {
		return mirrorIdentity{}, nil
	}
	if err != nil {
		return mirrorIdentity{}, fmt.Errorf("read the mirror's sync state: %w", err)
	}
	return id, nil
}

// cloudIdentity is the account's current history and its head.
type cloudIdentity struct {
	key  string
	head int
}

// fetchCloudIdentity asks Things Cloud for the account's history key and that
// history's head index.
func fetchCloudIdentity(c *things.Client) (cloudIdentity, error) {
	v, err := c.Verify()
	if err != nil {
		return cloudIdentity{}, fmt.Errorf("verify the account: %w", redactRequestURL(err))
	}
	if v.HistoryKey == "" {
		return cloudIdentity{}, errors.New("the account has no history key in Things Cloud")
	}
	h, err := c.History(v.HistoryKey)
	if err != nil {
		return cloudIdentity{}, fmt.Errorf("read the history head: %w", redactRequestURL(err))
	}
	return cloudIdentity{key: v.HistoryKey, head: h.LatestServerIndex}, nil
}

// staleReason says why a mirror built from stored no longer follows the
// account's current history, or "" when it does. After an account reset the
// account gets a new history key, and the SDK keeps reading the old one from
// its stored cursor (KI-28). A cursor ahead of the head means the history it
// pointed into was replaced; the SDK then reports no changes, forever.
func staleReason(stored mirrorIdentity, current cloudIdentity) string {
	switch {
	case stored.key == "":
		return ""
	case stored.key != current.key:
		return "the account's history key changed (account reset?)"
	case stored.cursor > current.head:
		return fmt.Sprintf("the mirror's cursor %d is ahead of the history head %d", stored.cursor, current.head)
	}
	return ""
}

// historyGuard keeps the mirror and the writer on the account's current
// history (KI-28).
type historyGuard struct {
	file      *mirrorFile
	cloud     func() (cloudIdentity, error)
	useWriter func(key string) error
}

func newHistoryGuard(file *mirrorFile, c *things.Client) *historyGuard {
	return &historyGuard{
		file:      file,
		cloud:     func() (cloudIdentity, error) { return fetchCloudIdentity(c) },
		useWriter: func(key string) error { return useHistory(c, key) },
	}
}

// stale compares the mirror with the account's current history.
func (g *historyGuard) stale() (string, error) {
	current, err := g.cloud()
	if err != nil {
		return "", err
	}
	stored, err := g.file.stored()
	if err != nil {
		return "", err
	}
	return staleReason(stored, current), nil
}

// resync brings the mirror onto the account's current history: it sets a
// stale mirror aside, syncs the mirror to the head and points the writer at
// the same history. It runs at startup and for every rebuild. A mirror left
// half-built by a failed rebuild already follows the current history, so a
// retry resumes it instead of starting over and replacing the backup.
func (g *historyGuard) resync() error {
	current, err := g.cloud()
	if err != nil {
		return fmt.Errorf("check the account's history: %w", err)
	}
	stored, err := g.file.stored()
	if err != nil {
		return err
	}
	if reason := staleReason(stored, current); reason != "" {
		log.Printf("[MIRROR] %s; setting the mirror aside as %s", reason, filepath.Base(g.file.path+mirrorBackupSuffix))
		if err := g.file.reset(); err != nil {
			return fmt.Errorf("replace the mirror: %w", err)
		}
	}
	if err := runInitialSync(g.file); err != nil {
		return err
	}
	return g.useWriter(current.key)
}
