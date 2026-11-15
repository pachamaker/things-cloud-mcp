package main

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/pachamaker/things-cloud-mcp/domain/civil"
)

type readQuery struct {
	Tool    string
	Filters map[string]string
	Order   string
}
type readRevisionState struct {
	Identity   mirrorIdentity
	Generation uint64
	Today      civil.Date
	Timezone   string
}

var readRevisionSecret = func() []byte {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		panic("cannot initialize read revision key")
	}
	return key
}()

func makeReadRevision(s readRevisionState, q readQuery) (string, error) {
	// mirrorIdentity has private fields. Include them explicitly in the signed input.
	b, err := json.Marshal(struct {
		Query      readQuery
		History    string
		Cursor     int
		Generation uint64
		Day        string
		Zone       string
	}{q, s.Identity.key, s.Identity.cursor, s.Generation, s.Today.String(), s.Timezone})
	if err != nil {
		return "", err
	}
	h := hmac.New(sha256.New, readRevisionSecret)
	h.Write(b)
	return hex.EncodeToString(h.Sum(nil)), nil
}

var errStalePage = errors.New("stale_page")

func validateReadRevision(s readRevisionState, q readQuery, p pageOptions) (string, error) {
	revision, err := makeReadRevision(s, q)
	if err != nil {
		return "", err
	}
	if p.Revision != "" && !hmac.Equal([]byte(p.Revision), []byte(revision)) {
		return "", fmt.Errorf("%w: data or query changed; restart at offset zero and replace previous results", errStalePage)
	}
	return revision, nil
}

// withRead excludes sync and asynchronous rebuild startup/completion while all
// parent/heading queries run. Rebuild's unavailable flag is set before it releases mu.
func (g *syncGate) withRead(fn func(readModel, readRevisionState) error) error {
	if _, err := g.Sync(); err != nil {
		return err
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if err := g.mirrorUnavailable(g.now()); err != nil {
		return err
	}
	var r readModel
	var s readRevisionState
	var err error
	if g.readSource != nil {
		r, s, err = g.readSource()
	} else {
		id, e := mirrorDB.stored()
		if e != nil {
			return e
		}
		mirrorDB.mu.RLock()
		r = readModel{mirror: mirrorDB.syncer.State()}
		s = readRevisionState{Identity: id, Generation: mirrorDB.generation, Today: ownerToday(), Timezone: ownerZone.String()}
		mirrorDB.mu.RUnlock()
	}
	if err != nil {
		return err
	}
	return fn(r, s)
}
