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

func authMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		apiKey := os.Getenv("API_KEY")
		if apiKey == "" {
			next(w, r)
			return
		}
		auth := r.Header.Get("Authorization")
		if auth != "Bearer "+apiKey {
			jsonError(w, "unauthorized", 401)
			return
		}
		next(w, r)
	}
}

func authHandlerMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		apiKey := os.Getenv("API_KEY")
		if apiKey == "" {
			next.ServeHTTP(w, r)
			return
		}
		if r.Header.Get("Authorization") != "Bearer "+apiKey {
			jsonError(w, "unauthorized", 401)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func debugAuthMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if os.Getenv("DEBUG") != "true" {
			jsonError(w, "not found", 404)
			return
		}

		apiKey := os.Getenv("API_KEY")
		if apiKey == "" {
			jsonError(w, "debug endpoints require API_KEY", 503)
			return
		}

		if r.Header.Get("Authorization") != "Bearer "+apiKey {
			jsonError(w, "unauthorized", 401)
			return
		}

		next(w, r)
	}
}

// newThingsClient never sets Client.Debug: upstream dumps whole requests,
// including "Authorization: Password <plaintext>", and offers no redaction
// hook (spike report B7, KI-09). DEBUG only gates the debug endpoints.
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
	username := os.Getenv("THINGS_USERNAME")
	password := os.Getenv("THINGS_PASSWORD")
	if username == "" || password == "" {
		log.Fatal("THINGS_USERNAME and THINGS_PASSWORD must be set")
	}

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	closeMirror, err := connectThings(username, password, os.Getenv("DATA_DIR"))
	if err != nil {
		log.Fatal(err)
	}
	defer closeMirror()

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			jsonError(w, "not found", 404)
			return
		}
		jsonResponse(w, map[string]string{"service": "things-cloud-api", "status": "ok"})
	})

	http.HandleFunc("/api/verify", authMiddleware(handleVerify))
	http.HandleFunc("/api/sync", authMiddleware(handleSync))
	http.HandleFunc("/api/tasks/inbox", authMiddleware(handleInbox))
	http.HandleFunc("/api/tasks/today", authMiddleware(handleToday))
	http.HandleFunc("/api/tasks/anytime", authMiddleware(handleAnytime))
	http.HandleFunc("/api/tasks/someday", authMiddleware(handleSomeday))
	http.HandleFunc("/api/tasks/upcoming", authMiddleware(handleUpcoming))
	http.HandleFunc("/api/projects", authMiddleware(handleProjects))
	http.HandleFunc("/api/areas", authMiddleware(handleAreas))
	http.HandleFunc("/api/tags", authMiddleware(handleTags))

	// Write endpoints
	http.HandleFunc("/api/tasks/create", authMiddleware(handleCreateTask))
	http.HandleFunc("/api/tasks/complete", authMiddleware(handleCompleteTask))
	http.HandleFunc("/api/tasks/trash", authMiddleware(handleTrashTask))
	http.HandleFunc("/api/tasks/edit", authMiddleware(handleEditTask))

	// Debug endpoint — dump raw write history items
	http.HandleFunc("/api/debug/history", debugAuthMiddleware(func(w http.ResponseWriter, r *http.Request) {
		items, _, err := client.HistoryWithID(history.ID).Items(things.ItemsOptions{})
		if err != nil {
			jsonError(w, err.Error(), 500)
			return
		}
		type debugItem struct {
			UUID string `json:"uuid"`
			things.Item
		}
		out := make([]debugItem, len(items))
		for i, item := range items {
			out[i] = debugItem{UUID: item.UUID, Item: item}
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(out)
	}))

	// Debug endpoint — list all history keys (uses SDK method with auth)
	http.HandleFunc("/api/debug/histories", debugAuthMiddleware(func(w http.ResponseWriter, r *http.Request) {
		histories, err := client.Histories()
		if err != nil {
			jsonError(w, fmt.Sprintf("list histories failed: %v", err), 500)
			return
		}
		keys := make([]string, len(histories))
		for i, h := range histories {
			keys[i] = h.ID
		}
		jsonResponse(w, map[string]any{
			"own_history": history.ID,
			"all_keys":    keys,
			"total":       len(keys),
		})
	}))

	// Debug endpoint — delete the current history key (uses SDK method with auth)
	http.HandleFunc("/api/debug/delete-history", debugAuthMiddleware(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			jsonError(w, "POST required", 405)
			return
		}
		keyToDelete := history.ID
		var body struct {
			Key string `json:"key"`
		}
		ok, err := decodeOptionalJSONBody(w, r, &body)
		if err != nil {
			if isRequestBodyTooLarge(err) {
				jsonError(w, errRequestBodyTooLarge.Error(), http.StatusRequestEntityTooLarge)
				return
			}
		} else if ok && body.Key != "" {
			keyToDelete = body.Key
		}
		h := client.HistoryWithID(keyToDelete)
		if err := h.Delete(); err != nil {
			jsonError(w, fmt.Sprintf("delete failed: %v", err), 500)
			return
		}
		log.Printf("[DELETE-HISTORY] key=%s deleted successfully", keyToDelete)
		jsonResponse(w, map[string]any{
			"deleted": keyToDelete,
			"status":  "accepted",
		})
	}))

	// Debug endpoint — delete account and recreate it
	http.HandleFunc("/api/debug/nuke-account", debugAuthMiddleware(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			jsonError(w, "POST required", 405)
			return
		}
		email := client.EMail
		password := os.Getenv("THINGS_PASSWORD")

		log.Printf("[NUKE] Deleting account...")
		if err := client.Accounts.Delete(); err != nil {
			jsonError(w, fmt.Sprintf("delete failed: %v", err), 500)
			return
		}
		log.Printf("[NUKE] Account deleted. Recreating...")

		newClient, err := client.Accounts.SignUp(email, password)
		if err != nil {
			jsonError(w, fmt.Sprintf("signup failed: %v", err), 500)
			return
		}
		log.Printf("[NUKE] Signup complete. Account needs email confirmation.")

		_ = newClient
		jsonResponse(w, map[string]any{
			"status":  "account deleted and re-created",
			"email":   email,
			"message": "Check email for confirmation code, then POST to /api/debug/confirm-account",
		})
	}))

	// Debug endpoint — confirm account with email code
	http.HandleFunc("/api/debug/confirm-account", debugAuthMiddleware(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			jsonError(w, "POST required", 405)
			return
		}
		var body struct {
			Code string `json:"code"`
		}
		if err := decodeJSONBody(w, r, &body); err != nil {
			if isRequestBodyTooLarge(err) {
				jsonError(w, errRequestBodyTooLarge.Error(), http.StatusRequestEntityTooLarge)
				return
			}
			jsonError(w, "JSON body with 'code' required", 400)
			return
		}
		if body.Code == "" {
			jsonError(w, "JSON body with 'code' required", 400)
			return
		}
		if err := client.Accounts.Confirm(body.Code); err != nil {
			jsonError(w, fmt.Sprintf("confirm failed: %v", err), 500)
			return
		}
		if err := client.Accounts.AcceptSLA(); err != nil {
			log.Printf("[CONFIRM] SLA accept failed (non-fatal): %v", err)
		}
		log.Printf("[CONFIRM] Account confirmed and SLA accepted")
		jsonResponse(w, map[string]any{
			"status": "confirmed",
		})
	}))

	// MCP endpoint (no bearer auth — claude.ai connectors use OAuth which we don't implement)
	http.Handle("/mcp", newMCPHandler())

	shutdownCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	server := &http.Server{Addr: ":" + port}

	log.Printf("Starting server on :%s", port)
	if err := serveWithGracefulShutdown(shutdownCtx, server, shutdownTimeout); err != nil {
		log.Fatal(err)
	}
}
