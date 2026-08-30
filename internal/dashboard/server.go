package dashboard

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/steveyegge/beads/internal/storage"
)

//go:embed static
var staticFiles embed.FS

// Config holds options for the dashboard server.
type Config struct {
	Host         string
	Port         int
	ReadOnly     bool
	PollInterval time.Duration
	NoOpen       bool
	// StaticDir, when non-empty, serves the SPA straight off disk from this
	// path instead of the go:embed'd static/ tree, so editing app.js/style.css
	// takes effect on browser refresh with no Go rebuild. Dev-only.
	StaticDir string
}

// DefaultConfig returns production defaults.
func DefaultConfig() Config {
	return Config{
		Host:         "127.0.0.1",
		Port:         7700,
		ReadOnly:     false,
		PollInterval: 250 * time.Millisecond,
	}
}

// Server is the dashboard HTTP server.
type Server struct {
	cfg      Config
	store    storage.Storage
	hub      *Hub
	watcher  *Watcher
	handlers *Handlers
	http     *http.Server
}

// New creates a Server.
func New(store storage.Storage, cfg Config) *Server {
	hub := NewHub()
	return &Server{
		cfg:      cfg,
		store:    store,
		hub:      hub,
		watcher:  NewWatcher(store, hub, cfg.PollInterval),
		handlers: NewHandlers(store, cfg.ReadOnly),
	}
}

// ListenAddr resolves the actual listen address, auto-incrementing port if busy.
func (s *Server) ListenAddr() string {
	port := s.cfg.Port
	host := s.cfg.Host
	for {
		addr := fmt.Sprintf("%s:%d", host, port)
		ln, err := net.Listen("tcp", addr)
		if err == nil {
			_ = ln.Close()
			s.cfg.Port = port
			return addr
		}
		port++
		if port > s.cfg.Port+20 {
			// Give up after 20 attempts.
			s.cfg.Port = port - 1
			return fmt.Sprintf("%s:%d", host, port-1)
		}
	}
}

// URL returns the base URL for the dashboard.
func (s *Server) URL() string {
	return fmt.Sprintf("http://%s:%d", s.cfg.Host, s.cfg.Port)
}

// Run starts the HTTP server and watcher, blocking until ctx is cancelled.
func (s *Server) Run(ctx context.Context) error {
	mux := http.NewServeMux()

	// REST API
	mux.HandleFunc("/api/v1/issues", s.routeIssues)
	mux.HandleFunc("/api/v1/issues/", s.routeIssueByID)
	mux.HandleFunc("/api/v1/graph", s.handlers.GetGraph)
	mux.HandleFunc("/api/v1/stats", s.handlers.GetStats)
	mux.HandleFunc("/api/v1/events", s.handlers.GetEvents)
	mux.Handle("/api/v1/stream", s.hub)

	// Static SPA. In dev mode (--static-dir) files are read live off disk so
	// frontend edits show up on refresh; otherwise strip the "static/" prefix
	// off the go:embed'd tree baked into the binary.
	var sub fs.FS
	if s.cfg.StaticDir != "" {
		sub = os.DirFS(s.cfg.StaticDir)
	} else {
		var err error
		sub, err = fs.Sub(staticFiles, "static")
		if err != nil {
			return fmt.Errorf("dashboard: embedded FS error: %w", err)
		}
	}
	fileServer := http.FileServer(http.FS(sub))
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		// Serve index.html for any non-API, non-asset path (SPA routing).
		path := strings.TrimPrefix(r.URL.Path, "/")
		if path == "" {
			path = "."
		}
		if _, err := fs.Stat(sub, path); err != nil {
			r2 := r.Clone(r.Context())
			r2.URL.Path = "/"
			fileServer.ServeHTTP(w, r2)
			return
		}
		fileServer.ServeHTTP(w, r)
	})

	addr := s.ListenAddr()
	s.http = &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}

	// Start watcher in background.
	watchCtx, cancelWatch := context.WithCancel(ctx)
	go s.watcher.Run(watchCtx)

	// Auto-open browser.
	if !s.cfg.NoOpen {
		go func() {
			time.Sleep(200 * time.Millisecond)
			_ = OpenURL(s.URL())
		}()
	}

	errCh := make(chan error, 1)
	go func() {
		if err := s.http.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
	}()

	select {
	case <-ctx.Done():
		cancelWatch()
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return s.http.Shutdown(shutCtx)
	case err := <-errCh:
		cancelWatch()
		return err
	}
}

// routeIssues dispatches /api/v1/issues (no trailing ID).
func (s *Server) routeIssues(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/api/v1/issues/ready" {
		s.handlers.ReadyIssues(w, r)
		return
	}
	if r.Method == http.MethodGet {
		s.handlers.ListIssues(w, r)
		return
	}
	http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
}

// routeIssueByID dispatches /api/v1/issues/{id}[/action].
func (s *Server) routeIssueByID(w http.ResponseWriter, r *http.Request) {
	// Strip prefix: /api/v1/issues/
	path := strings.TrimPrefix(r.URL.Path, "/api/v1/issues/")
	parts := strings.SplitN(path, "/", 2)
	id := parts[0]
	if id == "" {
		s.routeIssues(w, r)
		return
	}
	if id == "ready" {
		s.handlers.ReadyIssues(w, r)
		return
	}

	action := ""
	if len(parts) == 2 {
		action = parts[1]
	}

	switch {
	case r.Method == http.MethodGet && action == "":
		s.handlers.GetIssue(w, r, id)
	case r.Method == http.MethodGet && action == "deps":
		// Simplified: redirect to graph for now.
		s.handlers.GetGraph(w, r)
	case r.Method == http.MethodPatch && action == "":
		s.handlers.PatchIssue(w, r, id)
	case r.Method == http.MethodPost && action == "claim":
		s.handlers.ClaimIssue(w, r, id)
	case r.Method == http.MethodPost && action == "close":
		s.handlers.CloseIssue(w, r, id)
	default:
		http.Error(w, "not found", http.StatusNotFound)
	}
}
