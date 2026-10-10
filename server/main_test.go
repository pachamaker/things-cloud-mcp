package main

import (
	"bytes"
	"context"
	"errors"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/arthursoares/things-cloud-sdk/sync"
)

type stubInitialSyncer struct {
	changes []sync.Change
	err     error
}

func (s stubInitialSyncer) Sync() ([]sync.Change, error) {
	return s.changes, s.err
}

func TestRunInitialSync(t *testing.T) {
	t.Parallel()

	t.Run("success", func(t *testing.T) {
		t.Parallel()

		if err := runInitialSync(stubInitialSyncer{changes: []sync.Change{}}); err != nil {
			t.Fatalf("runInitialSync returned error: %v", err)
		}
	})

	t.Run("failure", func(t *testing.T) {
		t.Parallel()

		err := runInitialSync(stubInitialSyncer{err: errors.New("boom")})
		if err == nil {
			t.Fatal("expected error")
		}
		if got, want := err.Error(), "initial sync failed: boom"; got != want {
			t.Fatalf("runInitialSync error = %q, want %q", got, want)
		}
	})
}

type stubShutdownServer struct {
	listenStarted  chan struct{}
	shutdownCalled chan struct{}
	listenErr      error
	shutdownErr    error
	waitForStop    bool
}

func (s *stubShutdownServer) ListenAndServe() error {
	if s.listenStarted != nil {
		close(s.listenStarted)
	}
	if s.waitForStop {
		<-s.shutdownCalled
	}
	return s.listenErr
}

func (s *stubShutdownServer) Shutdown(context.Context) error {
	if s.shutdownCalled != nil {
		select {
		case <-s.shutdownCalled:
		default:
			close(s.shutdownCalled)
		}
	}
	return s.shutdownErr
}

func TestServeWithGracefulShutdown(t *testing.T) {
	t.Parallel()

	t.Run("shuts down cleanly after context cancellation", func(t *testing.T) {
		t.Parallel()

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		srv := &stubShutdownServer{
			listenStarted:  make(chan struct{}),
			shutdownCalled: make(chan struct{}),
			listenErr:      http.ErrServerClosed,
			waitForStop:    true,
		}

		errCh := make(chan error, 1)
		go func() {
			errCh <- serveWithGracefulShutdown(ctx, srv, time.Second)
		}()

		<-srv.listenStarted
		cancel()

		select {
		case err := <-errCh:
			if err != nil {
				t.Fatalf("serveWithGracefulShutdown returned error: %v", err)
			}
		case <-time.After(time.Second):
			t.Fatal("timed out waiting for shutdown")
		}

		select {
		case <-srv.shutdownCalled:
		default:
			t.Fatal("expected Shutdown to be called")
		}
	})

	t.Run("returns listen errors directly", func(t *testing.T) {
		t.Parallel()

		srv := &stubShutdownServer{
			shutdownCalled: make(chan struct{}),
			listenErr:      errors.New("listen failed"),
		}

		err := serveWithGracefulShutdown(context.Background(), srv, time.Second)
		if err == nil {
			t.Fatal("expected error")
		}
		if got, want := err.Error(), "listen failed"; got != want {
			t.Fatalf("serveWithGracefulShutdown error = %q, want %q", got, want)
		}

		select {
		case <-srv.shutdownCalled:
			t.Fatal("did not expect Shutdown to be called")
		default:
		}
	})
}

func TestSyncDBPath_KI16(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		dataDir string
		want    string
	}{
		{"default data dir", "", "/data/things-upstream.db"},
		{"absolute DATA_DIR", "/var/lib/things-mcp", "/var/lib/things-mcp/things-upstream.db"},
		{"DATA_DIR with trailing slash", "/srv/things/", "/srv/things/things-upstream.db"},
		{"relative DATA_DIR", "state", "state/things-upstream.db"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := syncDBPath(tt.dataDir); got != tt.want {
				t.Errorf("syncDBPath(%q) = %q, want %q", tt.dataDir, got, tt.want)
			}
		})
	}
}

func TestNewThingsClient_KI09_DebugEnvDoesNotDumpPassword(t *testing.T) {
	t.Setenv("DEBUG", "true")
	var logs bytes.Buffer
	prev := log.Writer()
	log.SetOutput(&logs)
	t.Cleanup(func() { log.SetOutput(prev) })

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	const fakePassword = "not-a-real-password-ki09"
	c := newThingsClient(srv.URL, "owner@example.com", fakePassword)
	if _, err := c.Verify(); err != nil {
		t.Fatalf("Verify() error = %v", err)
	}
	if strings.Contains(logs.String(), fakePassword) {
		t.Error("log output contains the Things password")
	}
}
