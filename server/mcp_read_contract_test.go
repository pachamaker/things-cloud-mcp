package main

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/pachamaker/things-cloud-mcp/domain/civil"
)

func readRequest(args map[string]any) mcp.CallToolRequest {
	var req mcp.CallToolRequest
	req.Params.Arguments = args
	return req
}

func readJSON(t *testing.T, res *mcp.CallToolResult, dst any) string {
	t.Helper()
	if len(res.Content) != 1 {
		t.Fatalf("content blocks: %d", len(res.Content))
	}
	c, ok := res.Content[0].(mcp.TextContent)
	if !ok {
		t.Fatalf("content type %T", res.Content[0])
	}
	if len(c.Text) > 24576 || !utf8.ValidString(c.Text) {
		t.Fatal("invalid size/UTF-8")
	}
	if err := json.Unmarshal([]byte(c.Text), dst); err != nil {
		t.Fatal(err)
	}
	return c.Text
}

func TestReadArgumentsStrict(t *testing.T) {
	p, err := parsePageOptions(readRequest(nil))
	if err != nil || p.Limit != 25 || p.Offset != 0 {
		t.Fatalf("defaults %+v %v", p, err)
	}
	for _, v := range []any{0, -1, 1.5, math.NaN(), math.Inf(1), "2", true, nil, 1e30, 101, json.Number("1.5")} {
		if _, err := parsePageOptions(readRequest(map[string]any{"limit": v})); err == nil {
			t.Errorf("accepted %v (%T)", v, v)
		}
	}
	for _, v := range []any{-1, 0.5, "0", nil, 1e30} {
		if _, err := parsePageOptions(readRequest(map[string]any{"offset": v})); err == nil {
			t.Errorf("accepted offset %v", v)
		}
	}
	if _, err := parsePageOptions(readRequest(map[string]any{"offset": 1})); err == nil {
		t.Fatal("missing revision")
	}
	if _, err := parsePageOptions(readRequest(map[string]any{"limit": 100})); err != nil {
		t.Fatal(err)
	}
	p, err = parseTextOptions(readRequest(nil))
	if err != nil || p.Limit != 4000 {
		t.Fatal(p, err)
	}
	if _, err := parseTextOptions(readRequest(map[string]any{"limit": 8001})); err == nil {
		t.Fatal("text max")
	}
}

func TestReadEnvelopeByteBudget(t *testing.T) {
	day, _ := civil.Parse("2026-11-14")
	items := []string{strings.Repeat("界\n\"", 2000), strings.Repeat("界\n\"", 2000), "tail"}
	res := encodeCollectionPage(items, pageOptions{Limit: 25}, "rev", day, "Europe/Moscow")
	var page readPage[string]
	readJSON(t, res, &page)
	if res.IsError || page.Total != 3 || page.Returned < 1 || !page.HasMore || page.NextOffset == nil || *page.NextOffset != page.Returned {
		t.Fatalf("bad page %+v", page)
	}
	var empty readPage[string]
	text := readJSON(t, encodeCollectionPage(items, pageOptions{Limit: 25, Offset: 99}, "rev", day, "UTC"), &empty)
	if empty.Total != 3 || empty.HasMore || !strings.Contains(text, `"items":[]`) || !strings.Contains(text, `"next_offset":null`) {
		t.Fatal(text)
	}
	if !encodeCollectionPage([]string{strings.Repeat("x", 30000)}, pageOptions{Limit: 25}, "rev", day, "UTC").IsError {
		t.Fatal("oversize item")
	}
	var out map[string]any
	readJSON(t, readError("bad", strings.Repeat("\"界", 30000)), &out)
	readJSON(t, boundedReadResult(map[string]float64{"n": math.NaN()}), &out)
}
