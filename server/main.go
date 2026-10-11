package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	things "github.com/arthursoares/things-cloud-sdk"
	"github.com/arthursoares/things-cloud-sdk/sync"
)

// version is set at build time: go build -ldflags "-X main.version=v2.0.0" (KI-16).
var version = "dev"

var (
	client  *things.Client
	syncer  *sync.Syncer
	mirror  *syncGate
	history *things.History
)

type shutdownServer interface {
	ListenAndServe() error
	Shutdown(context.Context) error
}

const shutdownTimeout = 10 * time.Second

const (
	defaultDataDir = "/data"
	// syncDBFile differs from the embedded SDK's things.db (schema v4):
	// upstream migrate() treats v4 as newer than its own v2 and would skip its
	// migrations on that file (spike report B5). The cache is disposable.
	syncDBFile = "things-upstream.db"
)

func syncDBPath(dataDir string) string {
	if dataDir == "" {
		dataDir = defaultDataDir
	}
	return filepath.Join(dataDir, syncDBFile)
}

func jsonResponse(w http.ResponseWriter, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(data)
}

func jsonError(w http.ResponseWriter, msg string, code int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

func handleVerify(w http.ResponseWriter, r *http.Request) {
	resp, err := client.Verify()
	if err != nil {
		jsonError(w, fmt.Sprintf("verification failed: %v", err), 401)
		return
	}
	jsonResponse(w, resp)
}

func paginationQueryOpts(r *http.Request) (listOpts, error) {
	opts := listOpts{}

	if raw := r.URL.Query().Get("limit"); raw != "" {
		limit, err := strconv.Atoi(raw)
		if err != nil || limit < 0 {
			return opts, fmt.Errorf("limit must be a non-negative integer")
		}
		opts.Limit = limit
	}
	if raw := r.URL.Query().Get("offset"); raw != "" {
		offset, err := strconv.Atoi(raw)
		if err != nil || offset < 0 {
			return opts, fmt.Errorf("offset must be a non-negative integer")
		}
		opts.Offset = offset
	}

	return opts, nil
}

func handleSync(w http.ResponseWriter, r *http.Request) {
	changes, err := mirror.Sync()
	if err != nil {
		jsonError(w, fmt.Sprintf("sync failed: %v", err), 500)
		return
	}
	jsonResponse(w, map[string]interface{}{
		"changes_count": len(changes),
	})
}

func handleInbox(w http.ResponseWriter, r *http.Request) {
	opts, err := paginationQueryOpts(r)
	if err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := syncForRead(); err != nil {
		jsonError(w, fmt.Sprintf("pre-read sync failed: %v", err), http.StatusServiceUnavailable)
		return
	}
	state := readState()
	tasks, err := state.TasksInInbox(opts)
	if err != nil {
		jsonError(w, fmt.Sprintf("failed to get inbox: %v", err), 500)
		return
	}
	if tasks == nil {
		jsonResponse(w, []*things.Task{})
		return
	}
	jsonResponse(w, tasks)
}

func handleToday(w http.ResponseWriter, r *http.Request) {
	opts, err := paginationQueryOpts(r)
	if err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := syncForRead(); err != nil {
		jsonError(w, fmt.Sprintf("pre-read sync failed: %v", err), http.StatusServiceUnavailable)
		return
	}
	state := readState()
	tasks, err := state.TasksInToday(opts)
	if err != nil {
		jsonError(w, fmt.Sprintf("failed to get today: %v", err), 500)
		return
	}
	if tasks == nil {
		jsonResponse(w, []*things.Task{})
		return
	}
	jsonResponse(w, tasks)
}

func handleAnytime(w http.ResponseWriter, r *http.Request) {
	opts, err := paginationQueryOpts(r)
	if err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := syncForRead(); err != nil {
		jsonError(w, fmt.Sprintf("pre-read sync failed: %v", err), http.StatusServiceUnavailable)
		return
	}
	state := readState()
	tasks, err := state.TasksInAnytime(opts)
	if err != nil {
		jsonError(w, fmt.Sprintf("failed to get anytime: %v", err), 500)
		return
	}
	if tasks == nil {
		jsonResponse(w, []*things.Task{})
		return
	}
	jsonResponse(w, tasks)
}

func handleSomeday(w http.ResponseWriter, r *http.Request) {
	opts, err := paginationQueryOpts(r)
	if err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := syncForRead(); err != nil {
		jsonError(w, fmt.Sprintf("pre-read sync failed: %v", err), http.StatusServiceUnavailable)
		return
	}
	state := readState()
	tasks, err := state.TasksInSomeday(opts)
	if err != nil {
		jsonError(w, fmt.Sprintf("failed to get someday: %v", err), 500)
		return
	}
	if tasks == nil {
		jsonResponse(w, []*things.Task{})
		return
	}
	jsonResponse(w, tasks)
}

func handleUpcoming(w http.ResponseWriter, r *http.Request) {
	opts, err := paginationQueryOpts(r)
	if err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := syncForRead(); err != nil {
		jsonError(w, fmt.Sprintf("pre-read sync failed: %v", err), http.StatusServiceUnavailable)
		return
	}
	state := readState()
	tasks, err := state.TasksInUpcoming(opts)
	if err != nil {
		jsonError(w, fmt.Sprintf("failed to get upcoming: %v", err), 500)
		return
	}
	if tasks == nil {
		jsonResponse(w, []*things.Task{})
		return
	}
	jsonResponse(w, tasks)
}

func handleProjects(w http.ResponseWriter, r *http.Request) {
	opts, err := paginationQueryOpts(r)
	if err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := syncForRead(); err != nil {
		jsonError(w, fmt.Sprintf("pre-read sync failed: %v", err), http.StatusServiceUnavailable)
		return
	}
	state := readState()
	projects, err := state.AllProjects(opts)
	if err != nil {
		jsonError(w, fmt.Sprintf("failed to get projects: %v", err), 500)
		return
	}
	if projects == nil {
		jsonResponse(w, []*things.Task{})
		return
	}
	jsonResponse(w, projects)
}

func handleAreas(w http.ResponseWriter, r *http.Request) {
	opts, err := paginationQueryOpts(r)
	if err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := syncForRead(); err != nil {
		jsonError(w, fmt.Sprintf("pre-read sync failed: %v", err), http.StatusServiceUnavailable)
		return
	}
	state := readState()
	areas, err := state.AllAreasWithOpts(opts)
	if err != nil {
		jsonError(w, fmt.Sprintf("failed to get areas: %v", err), 500)
		return
	}
	if areas == nil {
		jsonResponse(w, []*things.Area{})
		return
	}
	jsonResponse(w, areas)
}

func handleTags(w http.ResponseWriter, r *http.Request) {
	opts, err := paginationQueryOpts(r)
	if err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := syncForRead(); err != nil {
		jsonError(w, fmt.Sprintf("pre-read sync failed: %v", err), http.StatusServiceUnavailable)
		return
	}
	state := readState()
	tags, err := state.AllTagsWithOpts(opts)
	if err != nil {
		jsonError(w, fmt.Sprintf("failed to get tags: %v", err), 500)
		return
	}
	if tags == nil {
		jsonResponse(w, []*things.Tag{})
		return
	}
	jsonResponse(w, tags)
}

// writesEnabled parses ENABLE_WRITES. Writes are off unless explicitly enabled,
// so a new install cannot change the account by accident (ADR-0004).
func writesEnabled(raw string) (bool, error) {
	return parseSwitch("ENABLE_WRITES", raw)
}

// newRouter mounts every data route behind g: /mcp always, the REST API only
// when restAPI is set (ADR-0002). "/" stays public as a health check.
func newRouter(g gate, writes, restAPI bool) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			jsonError(w, "not found", 404)
			return
		}
		jsonResponse(w, map[string]string{"service": "things-cloud-api", "status": "ok", "version": version})
	})
	g.registerPublic(mux)
	mux.Handle("/mcp", g.protect(newMCPHandler(writes), false))

	if !restAPI {
		return mux
	}
	read := func(path string, h http.HandlerFunc) { mux.Handle(path, g.protect(h, false)) }
	write := func(path string, h http.HandlerFunc) { mux.Handle(path, g.protect(h, true)) }
	read("/api/verify", handleVerify)
	read("/api/sync", handleSync)
	read("/api/tasks/inbox", handleInbox)
	read("/api/tasks/today", handleToday)
	read("/api/tasks/anytime", handleAnytime)
	read("/api/tasks/someday", handleSomeday)
	read("/api/tasks/upcoming", handleUpcoming)
	read("/api/projects", handleProjects)
	read("/api/areas", handleAreas)
	read("/api/tags", handleTags)
	if writes {
		write("/api/tasks/create", handleCreateTask)
		write("/api/tasks/complete", handleCompleteTask)
		write("/api/tasks/trash", handleTrashTask)
		write("/api/tasks/edit", handleEditTask)
	}
	return mux
}

// newThingsClient never sets Client.Debug: upstream dumps whole requests,
// including "Authorization: Password <plaintext>", and offers no redaction
// hook (spike report B7, KI-09).
func newThingsClient(endpoint, username, password string) *things.Client {
	return things.New(endpoint, username, password)
}

// connectThings sets the package globals the handlers use (KI-17): the
// client, the mirror under dataDir after an initial sync, and the write
// history. The sandbox e2e harness starts the server through it too.
func connectThings(username, password, dataDir string) (closeMirror func() error, err error) {
	client = newThingsClient(things.APIEndpoint, username, password)

	dbPath := syncDBPath(dataDir)
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o700); err != nil {
		return nil, fmt.Errorf("failed to create data directory: %w", err)
	}
	log.Printf("Sync database: %s", dbPath)

	syncer, err = sync.Open(dbPath, client)
	if err != nil {
		return nil, fmt.Errorf("failed to open sync database: %w", err)
	}
	mirror = newSyncGate(syncer, time.Now)

	if err := runInitialSync(mirror); err != nil {
		syncer.Close()
		return nil, err
	}

	history, err = client.OwnHistory()
	if err != nil {
		syncer.Close()
		return nil, fmt.Errorf("failed to get history: %w", err)
	}
	if err := history.Sync(); err != nil {
		syncer.Close()
		return nil, fmt.Errorf("failed to sync history: %w", redactRequestURL(err))
	}
	log.Println("History ready for writes")
	return syncer.Close, nil
}

func runInitialSync(s changeSyncer) error {
	log.Println("Performing initial sync...")
	changes, err := s.Sync()
	if err != nil {
		return fmt.Errorf("initial sync failed: %w", err)
	}
	log.Printf("Initial sync complete: %d changes", len(changes))
	return nil
}

func serveWithGracefulShutdown(ctx context.Context, srv shutdownServer, timeout time.Duration) error {
	serverStopped := make(chan struct{})
	shutdownErrCh := make(chan error, 1)

	go func() {
		select {
		case <-ctx.Done():
			shutdownCtx, cancel := context.WithTimeout(context.Background(), timeout)
			defer cancel()
			shutdownErrCh <- srv.Shutdown(shutdownCtx)
		case <-serverStopped:
		}
	}()

	err := srv.ListenAndServe()
	close(serverStopped)

	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}

	select {
	case shutdownErr := <-shutdownErrCh:
		if shutdownErr != nil && !errors.Is(shutdownErr, http.ErrServerClosed) {
			log.Printf("graceful shutdown failed: %v", shutdownErr)
		}
	case <-ctx.Done():
		shutdownErr := <-shutdownErrCh
		if shutdownErr != nil && !errors.Is(shutdownErr, http.ErrServerClosed) {
			log.Printf("graceful shutdown failed: %v", shutdownErr)
		}
	default:
	}

	return nil
}

func main() {
	authCfg, err := loadAuthConfig(os.Getenv)
	if err != nil {
		log.Fatal(err)
	}

	username := os.Getenv("THINGS_USERNAME")
	password := os.Getenv("THINGS_PASSWORD")
	if username == "" || password == "" {
		log.Fatal("THINGS_USERNAME and THINGS_PASSWORD must be set")
	}

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	writes, err := writesEnabled(os.Getenv("ENABLE_WRITES"))
	if err != nil {
		log.Fatal(err)
	}
	if writes {
		log.Println("Writes enabled (ENABLE_WRITES=true)")
	} else {
		log.Println("Read-only: write tools and endpoints are off; set ENABLE_WRITES=true to enable them")
	}
	restAPI, err := parseSwitch("ENABLE_REST_API", os.Getenv("ENABLE_REST_API"))
	if err != nil {
		log.Fatal(err)
	}

	tz := os.Getenv("THINGS_TIMEZONE")
	if ownerZone, err = ownerTimezone(tz); err != nil {
		log.Fatal(err)
	}
	if tz == "" {
		log.Println("THINGS_TIMEZONE is not set: \"today\" is the UTC day; set the owner's IANA zone, e.g. Europe/Moscow (ADR-0003)")
	}
	log.Printf("Timezone %s, today is %s", ownerZone, ownerToday())

	closeMirror, err := connectThings(username, password, os.Getenv("DATA_DIR"))
	if err != nil {
		log.Fatal(err)
	}
	defer closeMirror()

	shutdownCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var g gate
	if authCfg.LocalOnly {
		log.Println("Auth: WARNING: no authentication (INSECURE_NO_AUTH_LOCAL_ONLY=true); listening on 127.0.0.1 only")
		g = localGate{}
	} else {
		og, err := newOAuthGate(authCfg.OAuth, writes, newAuthHTTPClient(), time.Now)
		if err != nil {
			log.Fatal(err)
		}
		log.Printf("Auth: OAuth access tokens for %s from %s", authCfg.OAuth.Resource, authCfg.OAuth.Issuer)
		og.warmUp(shutdownCtx)
		g = og
	}

	server := &http.Server{Addr: listenAddr(authCfg, port), Handler: newRouter(g, writes, restAPI)}

	log.Printf("Starting server %s on %s", version, server.Addr)
	if err := serveWithGracefulShutdown(shutdownCtx, server, shutdownTimeout); err != nil {
		log.Fatal(err)
	}
}
