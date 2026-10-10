package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMCPServerInfo_KI16_ReportsBuildVersion(t *testing.T) {
	t.Parallel()

	body := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"test","version":"0"}}}`
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	rec := httptest.NewRecorder()
	newMCPHandler(false).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("initialize status = %d, body %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Result struct {
			ServerInfo struct {
				Version string `json:"version"`
			} `json:"serverInfo"`
		} `json:"result"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode initialize: %v (body %s)", err, rec.Body.String())
	}
	if got := resp.Result.ServerInfo.Version; got != version {
		t.Errorf("serverInfo.version = %q, want the build version %q", got, version)
	}
}
