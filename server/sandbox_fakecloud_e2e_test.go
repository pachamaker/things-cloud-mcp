//go:build e2e && fakecloud

package main

// Offline self-test of the sandbox harness: with the fakecloud tag every
// Things Cloud request is answered by an in-memory history instead of the
// network, so the harness logic (guard, firewall, 409 injection, wire checks,
// cleanup, seed checks) can be exercised without an account:
//
//	go test -tags 'e2e fakecloud' -run '^TestSandbox' -count=1 ./server
//
// It proves nothing about Things Cloud or the Things apps.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	gosync "sync"
	"time"
)

const (
	fakeHistoryKey = "fake-history-key"
	fakePageSize   = 40
)

func init() {
	fake := &fakeThingsCloud{loopback: http.DefaultTransport}
	fake.seed()
	http.DefaultTransport = fake
	for key, value := range map[string]string{
		envSandboxUsername: "sandbox@example.invalid",
		envSandboxPassword: "fake-password",
		envSandboxInterval: sandboxMinInterval.String(),
	} {
		if os.Getenv(key) == "" {
			os.Setenv(key, value)
		}
	}
}

type fakeThingsCloud struct {
	loopback http.RoundTripper

	mu    gosync.Mutex
	slots []map[string]json.RawMessage
}

func (f *fakeThingsCloud) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.Host != thingsCloudHost {
		switch req.URL.Hostname() {
		case "127.0.0.1", "localhost", "::1":
			return f.loopback.RoundTrip(req)
		}
		return nil, fmt.Errorf("fake cloud: refusing a request to %s", req.URL.Host)
	}
	f.mu.Lock()
	defer f.mu.Unlock()

	history := "/version/1/history/" + fakeHistoryKey
	q := req.URL.Query()
	switch path := req.URL.Path; {
	case req.Method == http.MethodGet && strings.HasPrefix(path, "/version/1/account/"):
		return fakeJSON(req, http.StatusOK, map[string]any{
			"email": strings.TrimPrefix(path, "/version/1/account/"), "history-key": fakeHistoryKey,
			"status": "SYAccountStatusActive", "SLA-version-accepted": "1", "issues": []any{}, "maildrop-email": "",
		})
	case req.Method == http.MethodGet && path == history:
		return fakeJSON(req, http.StatusOK, map[string]any{
			"latest-server-index": len(f.slots), "latest-schema-version": 301,
			"is-empty": len(f.slots) == 0, "latest-total-content-size": 0,
		})
	case req.Method == http.MethodGet && path == history+"/items":
		start, err := strconv.Atoi(q.Get("start-index"))
		if err != nil || start < 0 || start > len(f.slots) {
			return fakeJSON(req, http.StatusInternalServerError, map[string]any{"error": "start-index"})
		}
		end := min(start+fakePageSize, len(f.slots))
		return fakeJSON(req, http.StatusOK, map[string]any{
			"items": f.slots[start:end], "current-item-index": len(f.slots), "schema": 301,
			"latest-total-content-size": 0, "start-total-content-size": 0, "end-total-content-size": 0,
		})
	case req.Method == http.MethodPost && path == history+"/commit":
		ancestor, err := strconv.Atoi(q.Get("ancestor-index"))
		if err != nil || ancestor != len(f.slots) {
			return fakeJSON(req, http.StatusConflict, map[string]any{"error": "OutdatedAncestor"})
		}
		body, err := io.ReadAll(req.Body)
		if err != nil {
			return nil, err
		}
		var slot map[string]json.RawMessage
		if err := json.Unmarshal(body, &slot); err != nil {
			return fakeJSON(req, http.StatusBadRequest, map[string]any{"error": "body"})
		}
		f.slots = append(f.slots, slot)
		return fakeJSON(req, http.StatusOK, map[string]any{"server-head-index": len(f.slots)})
	}
	return fakeJSON(req, http.StatusNotFound, map[string]any{"error": "not found"})
}

func fakeJSON(req *http.Request, status int, v any) (*http.Response, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return &http.Response{
		StatusCode: status, Status: fmt.Sprintf("%d %s", status, http.StatusText(status)),
		Proto: "HTTP/1.1", ProtoMajor: 1, ProtoMinor: 1,
		Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(bytes.NewReader(b)),
		ContentLength: int64(len(b)), Request: req,
	}, nil
}

// seed writes the marker area and the seed list of docs/testing/sandbox-e2e.md
// the way current Things apps would: to-dos as Task7.
func (f *fakeThingsCloud) seed() {
	civil := func(y int, m time.Month, d int) *int64 {
		v := time.Date(y, m, d, 0, 0, 0, 0, time.UTC).Unix()
		return &v
	}
	ids := map[string]string{}
	newID := func(key string) string {
		ids[key] = generateUUID()
		return ids[key]
	}
	add := func(id, kind string, payload any) {
		p, _ := json.Marshal(payload)
		env, _ := json.Marshal(map[string]any{"t": 0, "e": kind, "p": json.RawMessage(p)})
		f.slots = append(f.slots, map[string]json.RawMessage{id: env})
	}
	todo := func(key, title string, mutate func(*taskCreatePayload)) {
		p := taskCreatePayload{
			Tp: 0, St: 1, Rt: []string{}, Dl: []string{}, Ar: []string{}, Tt: title, Tg: []string{},
			Agr: []string{}, Cd: 1791676800, Nt: emptyNote(), Pr: []string{}, Xx: defaultExtension(),
		}
		if mutate != nil {
			mutate(&p)
		}
		add(newID(key), "Task7", p)
	}
	rule := func(fu, tp int) *json.RawMessage {
		raw := json.RawMessage(fmt.Sprintf(`{"fu":%d,"fa":1,"tp":%d,"of":[{"dy":0}],"rrv":4,"ia":1791676800,"sr":1791676800,"ed":64092211200,"rc":0,"ts":0}`, fu, tp))
		return &raw
	}

	add(newID("marker"), "Area3", map[string]any{"ix": 0, "tt": sandboxMarkerArea, "tg": []string{}, "xx": defaultExtension()})
	marker := ids["marker"]
	inMarker := func(p *taskCreatePayload) { p.Ar = []string{marker} }
	todo("anytime", "SEED anytime", inMarker)
	todo("someday", "SEED someday", func(p *taskCreatePayload) { p.St = 2 })
	todo("upcoming", "SEED upcoming", func(p *taskCreatePayload) { p.St, p.Sr = 2, civil(2030, time.January, 1) })
	todo("deadline", "SEED deadline", func(p *taskCreatePayload) { p.Dd = civil(2030, time.June, 1) })
	todo("checklist", "SEED checklist", nil)
	for i, title := range []string{"one", "two"} {
		ss, sp := 0, any(nil)
		if i == 0 {
			ss, sp = 3, 1791680000.5
		}
		add(newID("check "+title), "ChecklistItem3", map[string]any{
			"tt": title, "ts": []string{ids["checklist"]}, "ix": i, "cd": 1791676800, "md": nil,
			"ss": ss, "sp": sp, "lt": false, "xx": defaultExtension(),
		})
	}
	add(newID("deleted tag"), "Tag4", map[string]any{"ix": 1, "tt": "SEED tag", "sh": nil, "pn": []string{}, "xx": defaultExtension()})
	add(newID("tombstone"), "Tombstone2", map[string]any{"dloid": ids["deleted tag"], "dld": 1791677000.5})
	add(newID("tag"), "Tag4", map[string]any{"ix": 0, "tt": "SEED tag", "sh": nil, "pn": []string{}, "xx": defaultExtension()})
	todo("tagged", "SEED tagged", func(p *taskCreatePayload) { p.Tg = []string{ids["tag"]} })
	// Decoys the seed lookup must not take for the tag seed (KI-34): the
	// deleted tag above and a checklist item named like the tag.
	add(newID("check tag"), "ChecklistItem3", map[string]any{
		"tt": "SEED tag", "ts": []string{ids["tagged"]}, "ix": 0, "cd": 1791676800, "md": nil,
		"ss": 0, "sp": nil, "lt": false, "xx": defaultExtension(),
	})
	todo("project", "SEED project", func(p *taskCreatePayload) { p.Tp, p.Ar = 1, []string{marker} })
	todo("heading", "SEED heading", func(p *taskCreatePayload) { p.Tp, p.Pr = 2, []string{ids["project"]} })
	todo("in project", "SEED in project", func(p *taskCreatePayload) { p.Pr = []string{ids["project"]} })
	todo("under heading", "SEED under heading", func(p *taskCreatePayload) { p.Agr = []string{ids["heading"]} })
	todo("weekly", "SEED repeat weekly", func(p *taskCreatePayload) { p.St, p.Rr = 2, rule(256, 0) })
	todo("weekly instance", "SEED repeat weekly", func(p *taskCreatePayload) { p.Rt = []string{ids["weekly"]} })
	todo("monthly", "SEED repeat monthly after completion", func(p *taskCreatePayload) { p.St, p.Rr = 2, rule(8, 1) })
	stop := 1791680000.5
	todo("completed", "SEED completed", func(p *taskCreatePayload) { p.Ss, p.Sp = 3, &stop })
	todo("canceled", "SEED canceled", func(p *taskCreatePayload) { p.Ss, p.Sp = 2, &stop })
}
