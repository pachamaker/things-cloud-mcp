package main

import (
	"github.com/pachamaker/things-cloud-mcp/domain/civil"
	"testing"
	"time"
)

func TestReadRevisionBinding(t *testing.T) {
	d, _ := civil.Parse("2026-11-14")
	s := readRevisionState{Identity: mirrorIdentity{key: "private", cursor: 42}, Generation: 1, Today: d, Timezone: "UTC"}
	q := readQuery{Tool: "things_list_inbox", Filters: map[string]string{"scope": "open"}, Order: "index,uuid"}
	a, e := makeReadRevision(s, q)
	if e != nil {
		t.Fatal(e)
	}
	b, _ := makeReadRevision(s, q)
	if a != b || len(a) != 64 {
		t.Fatal("unstable revision")
	}
	s.Identity.cursor++
	b, _ = makeReadRevision(s, q)
	if a == b {
		t.Fatal("cursor ignored")
	}
	s.Identity.cursor--
	s.Generation++
	b, _ = makeReadRevision(s, q)
	if a == b {
		t.Fatal("generation ignored")
	}
	s.Generation--
	q.Filters["scope"] = "all"
	b, _ = makeReadRevision(s, q)
	if a == b {
		t.Fatal("scope ignored")
	}
	q.Filters["scope"] = "open"
	q.Tool = "things_list_anytime"
	b, _ = makeReadRevision(s, q)
	if a == b {
		t.Fatal("tool ignored")
	}
	q.Tool = "things_list_inbox"
	s.Today = civil.DateOf(time.Date(2026, 11, 15, 0, 0, 0, 0, time.UTC))
	b, _ = makeReadRevision(s, q)
	if a == b {
		t.Fatal("day ignored")
	}
}
