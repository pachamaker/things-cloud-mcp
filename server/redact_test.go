package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	things "github.com/arthursoares/things-cloud-sdk"
	"github.com/mark3labs/mcp-go/mcp"
)

// ki35HistoryKey stands in for the history key, which opens the whole task
// database without the password (docs/protocol/things-cloud.md §1).
const (
	ki35HistoryKey = "KI35-not-a-real-history-key"
	ki35Email      = "owner@example.com"
	ki35HistoryURL = "/version/1/history/" + ki35HistoryKey
)

var errConnRefused = errors.New("dial tcp 192.0.2.1:443: connect: connection refused")

type stubResponse struct {
	status int
	body   string
}

func stubOK(body string) stubResponse { return stubResponse{http.StatusOK, body} }

// cloudStub answers the listed requests, keyed by "path?query" or by path,
// and refuses the connection for every other request, the way a dropped
// network does.
type cloudStub map[string]stubResponse

func (c cloudStub) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Body != nil {
		req.Body.Close()
	}
	r, ok := c[req.URL.Path+"?"+req.URL.RawQuery]
	if !ok {
		r, ok = c[req.URL.Path]
	}
	if !ok {
		return nil, errConnRefused
	}
	return &http.Response{
		StatusCode: r.status,
		Status:     fmt.Sprintf("%d %s", r.status, http.StatusText(r.status)),
		Header:     http.Header{"Content-Type": {"application/json"}},
		Body:       io.NopCloser(strings.NewReader(r.body)),
		Request:    req,
	}, nil
}

// useCloudStub routes the SDK client (its http.Client uses the default
// transport) to stub, lets the write preconditions pass and captures the log.
// Callers must not be parallel.
func useCloudStub(t *testing.T, stub cloudStub) *bytes.Buffer {
	t.Helper()
	prevTransport := http.DefaultTransport
	prevClient, prevMirrorDB, prevMirror, prevHistory := client, mirrorDB, mirror, history
	prevWriteMirror := writeMirror
	prevLog := log.Writer()
	var logs bytes.Buffer
	http.DefaultTransport = stub
	// The write preconditions (KI-10) pass: these tests are about what happens
	// after them.
	writeMirror = func() (mirrorLookup, error) { return fakeMirror{anyOpenToDo: true}, nil }
	log.SetOutput(&logs)
	t.Cleanup(func() {
		http.DefaultTransport = prevTransport
		client, mirrorDB, mirror, history = prevClient, prevMirrorDB, prevMirror, prevHistory
		writeMirror = prevWriteMirror
		log.SetOutput(prevLog)
	})
	return &logs
}

// refusedRequest returns the error net/http produces for a request whose
// connection is refused.
func refusedRequest(t *testing.T, method, rawURL string) error {
	t.Helper()
	req, err := http.NewRequest(method, rawURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := (&http.Client{Transport: cloudStub{}}).Do(req)
	if err == nil {
		resp.Body.Close()
		t.Fatal("expected the request to fail")
	}
	return err
}

func toolErrorText(t *testing.T, res *mcp.CallToolResult, err error) string {
	t.Helper()
	if err != nil {
		t.Fatalf("handler returned error: %v", err)
	}
	if !res.IsError || len(res.Content) != 1 {
		t.Fatalf("expected one error content item, got %+v", res)
	}
	text, ok := res.Content[0].(mcp.TextContent)
	if !ok {
		t.Fatalf("expected text content, got %T", res.Content[0])
	}
	return text.Text
}

func restErrorText(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var body struct {
		Error string `json:"error"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode REST error body: %v", err)
	}
	return body.Error
}

func assertNoHistoryKey(t *testing.T, where, s string) {
	t.Helper()
	if strings.Contains(s, ki35HistoryKey) {
		t.Errorf("%s contains the history key: %q", where, s)
	}
}

func TestHistoryWrite_KI35_ErrorHidesHistoryKey(t *testing.T) {
	const itemsAt5 = `{"items":[],"current-item-index":5,"schema":301}`
	cases := []struct {
		name string
		stub cloudStub
		want string
	}{
		{
			name: "history sync refused",
			stub: cloudStub{},
			want: `history sync failed: Get "https://cloud.culturedcode.com/<redacted>": ` + errConnRefused.Error(),
		},
		{
			name: "commit refused",
			stub: cloudStub{ki35HistoryURL + "/items": stubOK(itemsAt5)},
			want: `write failed: Post "https://cloud.culturedcode.com/<redacted>": ` + errConnRefused.Error(),
		},
		{
			name: "re-sync after 409 refused",
			stub: cloudStub{
				ki35HistoryURL + "/items?start-index=0": stubOK(itemsAt5),
				ki35HistoryURL + "/commit":              {http.StatusConflict, `{}`},
			},
			want: `history re-sync failed: Get "https://cloud.culturedcode.com/<redacted>": ` + errConnRefused.Error(),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			logs := useCloudStub(t, tc.stub)
			id := generateUUID()
			resetHistory := func() {
				history = things.New(things.APIEndpoint, ki35Email, "not-a-real-password").HistoryWithID(ki35HistoryKey)
			}

			resetHistory()
			var req mcp.CallToolRequest
			req.Params.Arguments = map[string]any{"uuid": id}
			res, err := mcpCompleteTask(context.Background(), req)
			if got := toolErrorText(t, res, err); got != tc.want {
				t.Errorf("tool error = %q, want %q", got, tc.want)
			}

			resetHistory()
			rec := httptest.NewRecorder()
			handleCompleteTask(rec, httptest.NewRequest(http.MethodPost, "/api/tasks/complete", strings.NewReader(`{"uuid":"`+id+`"}`)))
			if rec.Code != http.StatusInternalServerError {
				t.Errorf("REST status = %d, want 500", rec.Code)
			}
			if got := restErrorText(t, rec); got != tc.want {
				t.Errorf("REST error = %q, want %q", got, tc.want)
			}

			assertNoHistoryKey(t, "log", logs.String())
		})
	}
}

func TestReadSync_KI35_ErrorHidesHistoryKey(t *testing.T) {
	logs := useCloudStub(t, cloudStub{})
	syncErr := refusedRequest(t, http.MethodGet, "https://cloud.culturedcode.com"+ki35HistoryURL+"/items?start-index=0")
	cause := `Get "https://cloud.culturedcode.com/<redacted>": ` + errConnRefused.Error()

	reads := []struct {
		name string
		read func(t *testing.T) string
		want string
	}{
		{"MCP read tool", func(t *testing.T) string {
			res, err := mcpListToday(context.Background(), mcp.CallToolRequest{})
			return toolErrorText(t, res, err)
		}, "pre-read sync failed: " + cause},
		{"REST read", func(t *testing.T) string {
			rec := httptest.NewRecorder()
			handleToday(rec, httptest.NewRequest(http.MethodGet, "/api/tasks/today", nil))
			return restErrorText(t, rec)
		}, "pre-read sync failed: " + cause},
		{"REST sync", func(t *testing.T) string {
			rec := httptest.NewRecorder()
			handleSync(rec, httptest.NewRequest(http.MethodPost, "/api/sync", nil))
			return restErrorText(t, rec)
		}, "sync failed: " + cause},
	}
	for _, r := range reads {
		t.Run(r.name, func(t *testing.T) {
			mirror = newSyncGate(&fakeSyncer{errs: []error{syncErr}}, newFakeClock().Now)
			if got := r.read(t); got != r.want {
				t.Errorf("error = %q, want %q", got, r.want)
			}
		})
	}
	assertNoHistoryKey(t, "log", logs.String())
}

func TestConnectThings_KI35_ErrorHidesHistoryKey(t *testing.T) {
	logs := useCloudStub(t, cloudStub{
		"/version/1/account/" + ki35Email: stubOK(`{"history-key":"` + ki35HistoryKey + `"}`),
		ki35HistoryURL:                    stubOK(`{"latest-server-index":0,"is-empty":true}`),
	})

	closeMirror, err := connectThings(ki35Email, "not-a-real-password", t.TempDir())
	if err == nil {
		closeMirror()
		t.Fatal("expected connectThings to fail")
	}
	want := `failed to sync history: Get "https://cloud.culturedcode.com/<redacted>": ` + errConnRefused.Error()
	if got := err.Error(); got != want {
		t.Errorf("error = %q, want %q", got, want)
	}
	assertNoHistoryKey(t, "log", logs.String())
}

func TestRedactRequestURL_KI35(t *testing.T) {
	t.Parallel()

	refused := &url.Error{Op: "Post", URL: "https://cloud.culturedcode.com" + ki35HistoryURL + "/commit?ancestor-index=7&_cnt=1", Err: errConnRefused}
	const redacted = `Post "https://cloud.culturedcode.com/<redacted>": ` + "dial tcp 192.0.2.1:443: connect: connection refused"

	t.Run("nil", func(t *testing.T) {
		t.Parallel()
		if err := redactRequestURL(nil); err != nil {
			t.Fatalf("redactRequestURL(nil) = %v", err)
		}
	})

	t.Run("no transport error", func(t *testing.T) {
		t.Parallel()
		err := &things.HTTPError{StatusCode: http.StatusConflict, Status: "409 Conflict"}
		if got := redactRequestURL(err); got != error(err) {
			t.Fatalf("redactRequestURL changed %v into %v", err, got)
		}
	})

	t.Run("wrapped transport error", func(t *testing.T) {
		t.Parallel()
		err := redactRequestURL(fmt.Errorf("write failed: %w", refused))
		if got, want := err.Error(), "write failed: "+redacted; got != want {
			t.Fatalf("error = %q, want %q", got, want)
		}
		if !errors.Is(err, errConnRefused) {
			t.Error("the cause is no longer reachable with errors.Is")
		}
		var ue *url.Error
		if !errors.As(err, &ue) {
			t.Fatal("the *url.Error is no longer reachable with errors.As")
		}
		assertNoHistoryKey(t, "unwrapped URL", ue.URL)
		if ue.Op != "Post" {
			t.Errorf("Op = %q, want Post", ue.Op)
		}
	})

	t.Run("idempotent", func(t *testing.T) {
		t.Parallel()
		once := redactRequestURL(refused)
		if got, want := redactRequestURL(once).Error(), once.Error(); got != want {
			t.Fatalf("second redaction = %q, want %q", got, want)
		}
	})

	t.Run("empty URL", func(t *testing.T) {
		t.Parallel()
		err := &url.Error{Op: "Get", URL: "", Err: errConnRefused}
		if got := redactRequestURL(err); got != error(err) {
			t.Fatalf("redactRequestURL changed %v into %v", err, got)
		}
	})

	t.Run("URL without host", func(t *testing.T) {
		t.Parallel()
		err := redactRequestURL(&url.Error{Op: "Get", URL: ki35HistoryURL + "/items", Err: errConnRefused})
		if got, want := err.Error(), `Get "<redacted>": `+errConnRefused.Error(); got != want {
			t.Fatalf("error = %q, want %q", got, want)
		}
	})
}
