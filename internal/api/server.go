// Package api implements the edge-specific HTTP API.
//
// The control-plane REST surface (users, instances, signup, versions, stats,
// realtime) is served by the original mothership hooks running inside the
// PocketBase SDK — see internal/mothership. This package adds the routes that
// belong to the edge: the daemon admin API and the embedded dashboard.
package api

import (
	"context"
	"io/fs"
	"net/http"
	"runtime"
	"strings"
	"time"

	"github.com/incurl/pocketghost/internal/config"
	"github.com/incurl/pocketghost/internal/instance"
	"github.com/incurl/pocketghost/internal/logging"
	"github.com/incurl/pocketghost/internal/metrics"
	"github.com/incurl/pocketghost/internal/pbbin"
	"github.com/incurl/pocketghost/internal/store"
)

// Version is the reported binary version.
var Version = "1.0.0-go"

// Backups abstracts the backup manager so the API does not depend on its
// implementation package.
type Backups interface {
	Create(ctx context.Context, instanceID string) (string, error)
	List(instanceID string) ([]string, error)
	Restore(ctx context.Context, instanceID, name string) error
	Read(instanceID, name string) ([]byte, error)
	Path(instanceID, name string) (string, error)
}

// Options configures the API server.
type Options struct {
	Config    *config.Config
	Repo      *store.Repo
	Instances *instance.Service
	Binaries  *pbbin.Service
	Logs      *logging.LogStore
	Stats     *metrics.Stats
	Logger    *logging.Logger
	Dashboard fs.FS
	Backups   Backups
	// Mothership serves the original control-plane routes. When set it is
	// mounted on the catch-all, so the edge admin routes still win for their
	// own paths.
	Mothership http.Handler
}

// Server is the edge HTTP server.
type Server struct {
	cfg        *config.Config
	repo       *store.Repo
	svc        *instance.Service
	bins       *pbbin.Service
	logs       *logging.LogStore
	stats      *metrics.Stats
	log        *logging.Logger
	dash       fs.FS
	backups    Backups
	mothership http.Handler
	mux        *http.ServeMux
	started    time.Time
}

// New builds the server.
func New(opts Options) (*Server, error) {
	if opts.Logger == nil {
		opts.Logger = logging.Discard()
	}
	if opts.Stats == nil {
		opts.Stats = metrics.New()
	}

	s := &Server{
		cfg:        opts.Config,
		repo:       opts.Repo,
		svc:        opts.Instances,
		bins:       opts.Binaries,
		logs:       opts.Logs,
		stats:      opts.Stats,
		log:        opts.Logger.Create("api"),
		dash:       opts.Dashboard,
		backups:    opts.Backups,
		mothership: opts.Mothership,
		mux:        http.NewServeMux(),
		started:    time.Now(),
	}
	s.routes()
	return s, nil
}

// DashboardHandler returns the handler that serves the embedded dashboard.
func (s *Server) DashboardHandler() http.Handler {
	return http.HandlerFunc(s.handleDashboard)
}

// Handler returns the middleware-wrapped handler.
func (s *Server) Handler() http.Handler {
	return s.recoverer(s.accessLog(s.mux))
}

func (s *Server) routes() {
	m := s.mux

	// The original mothership application owns the control-plane routes.
	if s.mothership != nil {
		m.Handle("/", s.mothership)
	}

	// Health (daemon + firewall probe paths).
	m.HandleFunc("GET /_api/daemon/health", s.handleHealth)
	m.HandleFunc("GET /api/firewall/health", s.handleHealth)

	m.HandleFunc("GET /_api/daemon/processes", s.requireOperator(s.handleProcesses))
	m.HandleFunc("GET /_api/daemon/instances", s.requireOperator(s.handleDaemonInstances))
	m.HandleFunc("POST /_api/daemon/instances/{id}/start", s.requireOperator(s.handleInstanceStart))
	m.HandleFunc("POST /_api/daemon/instances/{id}/stop", s.requireOperator(s.handleInstanceStop))
	m.HandleFunc("POST /_api/daemon/instances/{id}/restart", s.requireOperator(s.handleInstanceRestart))
	m.HandleFunc("GET /_api/daemon/instances/{id}/logs", s.requireOperator(s.handleInstanceLogs))
	m.HandleFunc("GET /_api/daemon/instances/{id}/logs/stream", s.requireOperator(s.handleInstanceLogsStream))
	m.HandleFunc("POST /_api/daemon/instances/{id}/backup", s.requireOperator(s.handleInstanceBackup))
	m.HandleFunc("GET /_api/daemon/instances/{id}/backups", s.requireOperator(s.handleInstanceBackupList))
	m.HandleFunc("GET /_ph/backup/{id}/{zip}", s.handleBackupDownload)
	m.HandleFunc("GET /_api/daemon/version", s.handleVersionInfo)
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	status := "ok"
	code := http.StatusOK
	if s.svc != nil && !s.svc.Ready() {
		status = "starting"
		code = http.StatusServiceUnavailable
	}
	writeJSON(w, code, map[string]any{
		"status":  status,
		"version": Version,
		"uptime":  time.Since(s.started).Round(time.Second).String(),
		"runtime": runtime.Version(),
	})
}

func (s *Server) handleVersionInfo(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"version": Version,
		"cached":  s.bins.Versions(),
		"go":      runtime.Version(),
	})
}

func (s *Server) recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				s.log.Errorf("panic serving %s %s: %v", r.Method, r.URL.Path, rec)
				writeError(w, http.StatusInternalServerError, "Internal server error")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func (s *Server) accessLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(sw, r)
		if !strings.HasPrefix(r.URL.Path, "/assets/") {
			s.log.Debugf("%s %s %d %s", r.Method, r.URL.Path, sw.status, time.Since(start).Round(time.Millisecond))
		}
	})
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

// requireOperator guards the daemon admin API with the shared PH_SECRET.
func (s *Server) requireOperator(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.cfg.Secret != "" {
			got := r.Header.Get("X-PocketHost-Secret")
			if got == "" {
				got = r.URL.Query().Get("secret")
			}
			if got != s.cfg.Secret {
				writeError(w, http.StatusUnauthorized, "unauthorized")
				return
			}
			next(w, r)
			return
		}
		writeError(w, http.StatusUnauthorized, "Set PH_SECRET to use the daemon admin API")
	}
}

// Daemon admin API (operator + CLI).
