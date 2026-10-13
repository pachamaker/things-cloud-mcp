package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// addItem appends a slot with one history item of kind with payload p and
// returns its UUID.
func (c *ki28Cloud) addItem(key, kind, p string) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	id := generateUUID()
	env, _ := json.Marshal(map[string]any{"t": 0, "e": kind, "p": json.RawMessage(p)})
	c.histories[key] = append(c.histories[key], map[string]json.RawMessage{id: env})
	return id
}

// ki29Tombstones appends the two tombstone shapes of arthursoares#37 at
// slots 2 and 3: one deletes doomed but has a non-numeric dld, the other names
// no deletion target. It returns the UUID of the target-less one.
func ki29Tombstones(cloud *ki28Cloud, doomed string) string {
	cloud.addItem(ki28KeyA, "Tombstone2", `{"dloid":"`+doomed+`","dld":"not-a-number"}`)
	return cloud.addItem(ki28KeyA, "Tombstone2", `{"dld":1770000123.5}`)
}

func TestMirror_KI29_SkippedTombstoneIsLoggedAndTheSyncGoesOn(t *testing.T) {
	for _, tc := range []struct {
		name    string
		running bool // the tombstones arrive after startup, through the sync gate
	}{
		{"at startup", false},
		{"while running", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cloud := newKI28Cloud()
			doomed := cloud.addTask(ki28KeyA, "deleted by a tombstone with a bad dld")
			kept := cloud.addTask(ki28KeyA, "kept")
			var skipped string
			if !tc.running {
				skipped = ki29Tombstones(cloud, doomed)
			}
			s := startKI28Server(t, cloud)
			s.mustConnect()
			if tc.running {
				clock := newFakeClock()
				s.useClock(clock)
				assertMirrorHas(t, []string{doomed, kept}, nil)
				skipped = ki29Tombstones(cloud, doomed)
				clock.Advance(syncFreshness)
			}

			assertMirrorHas(t, []string{kept}, []string{doomed})
			logs := s.logs.String()
			want := fmt.Sprintf("[SYNC] skipped history item 3 (Tombstone2 %s): tombstone names no deletion target (dloid)", skipped)
			if n := strings.Count(logs, "[SYNC] skipped history item"); n != 1 || !strings.Contains(logs, want) {
				t.Errorf("the log has %d skip lines, want exactly %q:\n%s", n, want, logs)
			}
			for _, payload := range []string{"1770000123", "not-a-number", "dld"} {
				if strings.Contains(logs, payload) {
					t.Errorf("the log carries tombstone payload %q:\n%s", payload, logs)
				}
			}
		})
	}
}
