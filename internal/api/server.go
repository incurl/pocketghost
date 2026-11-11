package api

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
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
	Mailer    Mailer
}

// Server is the control-plane HTTP server.
type Server struct {
	cfg       *config.Config
	repo      *store.Repo
	svc       *instance.Service
	bins      *pbbin.Service
	logs      *logging.LogStore
	stats     *metrics.Stats
	log       *logging.Logger
	jwtSecret []byte
	dash      fs.FS
	backups   Backups
	mailer    Mailer
	mux       *http.ServeMux
	started   time.Time
}

// New builds the server.
func New(opts Options) (*Server, error) {
	if opts.Logger == nil {
		opts.Logger = logging.Discard()
	}
	if opts.Stats == nil {
		opts.Stats = metrics.New()
	}

	secret, err := resolveJWTSecret(opts.Config, opts.Repo)
	if err != nil {
		return nil, err
	}

	s := &Server{
		cfg:       opts.Config,
		repo:      opts.Repo,
		svc:       opts.Instances,
		bins:      opts.Binaries,
		logs:      opts.Logs,
		stats:     opts.Stats,
		log:       opts.Logger.Create("api"),
		jwtSecret: secret,
		dash:      opts.Dashboard,
		backups:   opts.Backups,
		mailer:    opts.Mailer,
		mux:       http.NewServeMux(),
		started:   time.Now(),
	}
	s.routes()
	return s, nil
}

// Handler returns the middleware-wrapped handler.
func (s *Server) Handler() http.Handler {
	return s.recoverer(s.accessLog(s.mux))
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

// requireOperator guards the daemon admin API with the shared PH_SECRET, or a
// superuser token when PH_SECRET is unset (development).
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
		if !s.isSuperuser(r) {
			writeError(w, http.StatusUnauthorized, "Set PH_SECRET or authenticate as a superuser")
			return
		}
		next(w, r)
	}
}

func resolveJWTSecret(cfg *config.Config, repo *store.Repo) ([]byte, error) {
	if cfg.Secret != "" {
		sum := sha256.Sum256([]byte("pocketghost-jwt:" + cfg.Secret))
		return sum[:], nil
	}
	if v, ok, err := repo.KVGet("jwt_secret"); err == nil && ok && v != "" {
		return []byte(v), nil
	}
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return nil, err
	}
	secret := hex.EncodeToString(buf)
	if err := repo.KVSet("jwt_secret", secret); err != nil {
		return nil, err
	}
	return []byte(secret), nil
}

func (s *Server) routes() {
	m := s.mux

	// Health (daemon + firewall probe paths).
	m.HandleFunc("GET /_api/daemon/health", s.handleHealth)
	m.HandleFunc("GET /api/firewall/health", s.handleHealth)
	m.HandleFunc("GET /api/health", s.handleHealth)

	// Auth + user collection (PocketBase compatible).
	m.HandleFunc("POST /api/collections/users/auth-with-password", s.handleUserAuth)
	m.HandleFunc("POST /api/collections/users/auth-refresh", s.handleUserAuthRefresh)
	m.HandleFunc("POST /api/collections/users/records", s.handleUserCreate)
	m.HandleFunc("GET /api/collections/users/records/{id}", s.handleUserView)
	m.HandleFunc("PATCH /api/collections/users/records/{id}", s.handleUserUpdate)
	m.HandleFunc("POST /api/collections/users/request-verification", s.handleRequestVerification)
	m.HandleFunc("POST /api/collections/users/confirm-verification", s.handleConfirmVerification)
	m.HandleFunc("POST /api/collections/users/request-password-reset", s.handleRequestPasswordReset)
	m.HandleFunc("POST /api/collections/users/confirm-password-reset", s.handleConfirmPasswordReset)
	m.HandleFunc("POST /api/collections/users/request-email-change", s.handleRequestEmailChange)
	m.HandleFunc("POST /api/collections/users/confirm-email-change", s.handleConfirmEmailChange)

	// Superuser auth.
	m.HandleFunc("POST /api/collections/_superusers/auth-with-password", s.handleSuperuserAuth)

	// Instance collection (PocketBase-compatible read path).
	m.HandleFunc("GET /api/collections/instances/records", s.handleInstanceList)
	m.HandleFunc("GET /api/collections/instances/records/{id}", s.handleInstanceView)

	// Instance management REST API (dashboard + CLI).
	m.HandleFunc("POST /api/instance", s.handleInstanceCreate)
	m.HandleFunc("PUT /api/instance/{id}", s.handleInstanceUpdate)
	m.HandleFunc("DELETE /api/instance/{id}", s.handleInstanceDelete)

	// Misc mothership-compatible routes.
	m.HandleFunc("GET /api/versions", s.handleVersions)
	m.HandleFunc("GET /api/userToken/{id}", s.requireSuperuser(s.handleUserToken))
	m.HandleFunc("GET /stats.json", s.handleStatsJSON)
	m.HandleFunc("GET /api/signup", s.handleSignupCheck)
	m.HandleFunc("POST /api/signup", s.handleSignupConfirm)

	// Daemon admin API (operator + CLI).
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

	// Dashboard SPA (catch-all).
	m.HandleFunc("/", s.handleDashboard)
}
