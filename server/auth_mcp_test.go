package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
)

// withAccessRequest stands in for the HTTP gate in tests that drive the MCP
// handler directly.
func withAccessRequest(req *http.Request, a access) *http.Request {
	return req.WithContext(withAccess(req.Context(), a))
}

func mcpPost(t *testing.T, h http.Handler, body string, a *access) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if a != nil {
		req = withAccessRequest(req, *a)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func toolNamesFor(t *testing.T, a *access) []string {
	t.Helper()
	rec := mcpPost(t, newMCPHandler(true), `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`, a)
	if rec.Code != http.StatusOK {
		t.Fatalf("tools/list status = %d, body %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Result struct {
			Tools []mcp.Tool `json:"tools"`
		} `json:"result"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode tools/list: %v (body %s)", err, rec.Body.String())
	}
	var names []string
	for _, tool := range resp.Result.Tools {
		names = append(names, tool.Name)
	}
	return names
}

func callToolText(t *testing.T, name string, a *access) (string, bool) {
	t.Helper()
	body := `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"` + name + `","arguments":{"title":"x","uuid":"x"}}}`
	rec := mcpPost(t, newMCPHandler(true), body, a)
	if rec.Code != http.StatusOK {
		t.Fatalf("tools/call status = %d, body %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Result struct {
			IsError bool `json:"isError"`
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"result"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode tools/call: %v (body %s)", err, rec.Body.String())
	}
	if len(resp.Result.Content) == 0 {
		t.Fatalf("tools/call returned no content: %s", rec.Body.String())
	}
	return resp.Result.Content[0].Text, resp.Result.IsError
}

func TestMCPToolFilter_KI07_ReadScopeHidesWriteTools(t *testing.T) {
	t.Parallel()

	readOnly := toolNamesFor(t, &access{write: false})
	for _, name := range writeToolNames {
		if slices.Contains(readOnly, name) {
			t.Errorf("a things.read token sees write tool %s", name)
		}
	}
	if !slices.Contains(readOnly, "things_list_today") {
		t.Errorf("a things.read token lost read tools: %v", readOnly)
	}

	full := toolNamesFor(t, &access{write: true})
	for _, name := range writeToolNames {
		if !slices.Contains(full, name) {
			t.Errorf("a things.write token does not see %s", name)
		}
	}

	if none := toolNamesFor(t, nil); len(none) != 0 {
		t.Errorf("a request without access lists tools: %v", none)
	}
}

// The handlers behind these calls touch the package globals, which are nil
// here: reaching one would panic, so a clean error proves the call was stopped.
func TestMCPToolAccess_KI07_CallsAreCheckedNotJustListed(t *testing.T) {
	t.Parallel()

	text, isErr := callToolText(t, "things_create_task", &access{write: false})
	if !isErr || !strings.Contains(text, scopeWrite) {
		t.Errorf("write tool with a things.read token: isError=%v text=%q", isErr, text)
	}

	for _, name := range []string{"things_list_today", "things_trash_task"} {
		text, isErr := callToolText(t, name, nil)
		if !isErr || !strings.Contains(text, "not authenticated") {
			t.Errorf("%s without access: isError=%v text=%q", name, isErr, text)
		}
	}
}

var (
	restReadRoutes = []string{
		"/api/verify", "/api/sync", "/api/tasks/inbox", "/api/tasks/today", "/api/tasks/anytime",
		"/api/tasks/someday", "/api/tasks/upcoming", "/api/projects", "/api/areas", "/api/tags",
	}
	restWriteRoutes = []string{"/api/tasks/create", "/api/tasks/complete", "/api/tasks/trash", "/api/tasks/edit"}
)

func serveRouter(h http.Handler, method, path, authz, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Host = "127.0.0.1:8080"
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if authz != "" {
		req.Header.Set("Authorization", authz)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// Handlers behind these routes touch nil package globals, so only requests
// the gate must stop are sent with a token.
func TestRouter_KI07(t *testing.T) {
	t.Parallel()

	const toolsList = `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`

	t.Run("oauth", func(t *testing.T) {
		t.Parallel()
		is := newTestIssuer(t, newFakeClock())
		readToken := "Bearer " + is.mint(t, mintOpts{claims: func(m map[string]any) { m["scope"] = scopeRead }})
		writeToken := "Bearer " + is.mint(t, mintOpts{})

		for _, restAPI := range []bool{false, true} {
			h := newRouter(newTestGate(t, is, true), true, restAPI)

			if rec := serveRouter(h, http.MethodGet, "/", "", ""); rec.Code != http.StatusOK {
				t.Errorf("GET / = %d, want 200 (health check stays public)", rec.Code)
			}
			if rec := serveRouter(h, http.MethodGet, "/.well-known/oauth-protected-resource/mcp", "", ""); rec.Code != http.StatusOK {
				t.Errorf("metadata = %d, want 200", rec.Code)
			}
			for _, method := range []string{http.MethodPost, http.MethodGet, http.MethodDelete} {
				if rec := serveRouter(h, method, "/mcp", "", toolsList); rec.Code != http.StatusUnauthorized {
					t.Errorf("%s /mcp without a token = %d, want 401", method, rec.Code)
				}
			}

			rec := serveRouter(h, http.MethodPost, "/mcp", readToken, toolsList)
			if rec.Code != http.StatusOK {
				t.Fatalf("tools/list with a things.read token = %d", rec.Code)
			}
			for _, name := range writeToolNames {
				if strings.Contains(rec.Body.String(), `"`+name+`"`) {
					t.Errorf("tools/list through the router shows %s to a things.read token", name)
				}
			}
			if rec := serveRouter(h, http.MethodPost, "/mcp", writeToken, toolsList); !strings.Contains(rec.Body.String(), `"things_create_task"`) {
				t.Errorf("tools/list with a things.write token lacks write tools: %d", rec.Code)
			}

			for _, path := range append(slices.Clone(restReadRoutes), restWriteRoutes...) {
				want := http.StatusNotFound
				if restAPI {
					want = http.StatusUnauthorized
				}
				if rec := serveRouter(h, http.MethodPost, path, "", `{}`); rec.Code != want {
					t.Errorf("restAPI=%v %s without a token = %d, want %d", restAPI, path, rec.Code, want)
				}
			}
			if restAPI {
				for _, path := range restWriteRoutes {
					if rec := serveRouter(h, http.MethodPost, path, readToken, `{"uuid":"x","title":"x"}`); rec.Code != http.StatusForbidden {
						t.Errorf("%s with a things.read token = %d, want 403", path, rec.Code)
					}
				}
			}
		}
	})

	t.Run("ENABLE_WRITES caps the REST API too", func(t *testing.T) {
		t.Parallel()
		is := newTestIssuer(t, newFakeClock())
		writeToken := "Bearer " + is.mint(t, mintOpts{})
		h := newRouter(newTestGate(t, is, false), false, true)
		for _, path := range restWriteRoutes {
			if rec := serveRouter(h, http.MethodPost, path, writeToken, `{"uuid":"x","title":"x"}`); rec.Code != http.StatusNotFound {
				t.Errorf("read-only server: %s with a things.write token = %d, want 404", path, rec.Code)
			}
		}
	})

	t.Run("local only", func(t *testing.T) {
		t.Parallel()
		h := newRouter(localGate{}, true, false)
		rec := serveRouter(h, http.MethodPost, "/mcp", "", toolsList)
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "things_create_task") {
			t.Errorf("local /mcp tools/list = %d %s", rec.Code, rec.Body.String())
		}
		if rec := serveRouter(h, http.MethodGet, "/.well-known/oauth-protected-resource/mcp", "", ""); rec.Code != http.StatusNotFound {
			t.Errorf("local metadata = %d, want 404", rec.Code)
		}
	})
}

// mcp-go v0.44 runs task-augmented tool calls without the tool handler
// middleware, so toolAccess would not see them. No tool may opt in.
func TestMCPTools_KI07_NoTaskSupportBypassesToolAccess(t *testing.T) {
	t.Parallel()

	rec := mcpPost(t, newMCPHandler(true), `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`, &access{write: true})
	var resp struct {
		Result struct {
			Tools []mcp.Tool `json:"tools"`
		} `json:"result"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode tools/list: %v", err)
	}
	if len(resp.Result.Tools) == 0 {
		t.Fatal("no tools listed")
	}
	for _, tool := range resp.Result.Tools {
		if tool.Execution != nil {
			t.Errorf("%s declares execution %+v; task-augmented calls skip the things.write check", tool.Name, *tool.Execution)
		}
	}
}
