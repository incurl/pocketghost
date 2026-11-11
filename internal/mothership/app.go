package mothership

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"

	"github.com/pocketbase/pocketbase"
	"github.com/pocketbase/pocketbase/apis"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/plugins/jsvm"
	"github.com/pocketbase/pocketbase/plugins/migratecmd"

	"github.com/incurl/pocketghost/internal/logging"
)

// Config configures the mothership app.
type Config struct {
	// DataDir is the PocketBase data directory (contains data.db).
	DataDir string
	// Root is the directory that holds pb_hooks/pb_migrations/pb_admin_ext.
	Root string
	// HooksDir overrides the directory the JS hooks are loaded from.
	// It defaults to <Root>/pb_hooks.
	HooksDir string
	// Addr is the HTTP listen address for the control plane.
	Addr string
	// IsDev enables PocketBase's dev mode (live migrations, verbose logging).
	IsDev bool
	// AllowedOrigins is the CORS allow-list for the control-plane API.
	AllowedOrigins []string
	// Logger receives operational messages.
	Logger *logging.Logger
	// OnServe lets the edge register extra routes on the PocketBase router.
	OnServe func(app core.App)
}

// App is a running mothership control plane.
type App struct {
	pb     *pocketbase.PocketBase
	core   core.App
	cfg    Config
	logger *logging.Logger
}

// New creates (but does not start) the mothership app.
func New(cfg Config) *App {
	if cfg.Logger == nil {
		cfg.Logger = logging.Discard()
	}
	if cfg.Addr == "" {
		cfg.Addr = "127.0.0.1:8090"
	}
	if len(cfg.AllowedOrigins) == 0 {
		cfg.AllowedOrigins = []string{"*"}
	}

	app := newApp(cfg)
	return &App{pb: app, core: app, cfg: cfg, logger: cfg.Logger.Create("mothership")}
}

// hooksDir returns the directory the JS hooks are loaded from.
func (c Config) hooksDir() string {
	if c.HooksDir != "" {
		return c.HooksDir
	}
	return filepath.Join(c.Root, "pb_hooks")
}

// newApp builds a PocketBase instance configured for the mothership.
func newApp(cfg Config) *pocketbase.PocketBase {
	app := pocketbase.NewWithConfig(pocketbase.Config{
		HideStartBanner:  true,
		DefaultDev:       cfg.IsDev,
		DefaultDataDir:   cfg.DataDir,
		DataMaxOpenConns: 10,
		DataMaxIdleConns: 5,
	})

	jsvm.MustRegister(app, jsvm.Config{
		HooksDir:      cfg.hooksDir(),
		MigrationsDir: filepath.Join(cfg.Root, "pb_migrations"),
		// The assets are embedded, so there is nothing on disk to watch.
		HooksWatch:    false,
		HooksPoolSize: 8,
	})

	migratecmd.MustRegister(app, app.RootCmd, migratecmd.Config{
		Dir:          filepath.Join(cfg.Root, "pb_migrations"),
		Automigrate:  true,
		TemplateLang: migratecmd.TemplateLangJS,
	})

	// The native process registry becomes a PocketBase collection so it lives
	// in the same database as the rest of the control-plane data.
	registerGoMigrations()

	return app
}

// Core exposes the PocketBase app for reads and writes.
func (a *App) Core() core.App { return a.core }

// PB exposes the underlying PocketBase instance.
func (a *App) PB() *pocketbase.PocketBase { return a.pb }

// DataDir returns the PocketBase data directory.
func (a *App) DataDir() string { return a.core.DataDir() }

// Bootstrap initializes the app and applies every migration.
//
// The order matches PocketBase's own serve command:
//
//	app.Bootstrap()                        // connects the DB, runs system migrations
//	apis.Serve(app, apis.ServeConfig{...}) // runs the JS migrations, then serves
//
// The JS migrations create the mothership collections (users, instances,
// edges, ...), which the original hooks then query from their own bootstrap
// handlers — so the JS migrations must be applied before the hooks run.
func (a *App) Bootstrap(ctx context.Context) error {
	// The original mothership hooks register an onBootstrap handler that runs
	// raw SQL against the `instances` table (see refreshPublicStats in
	// pb_hooks/mothership.js). PocketBase fires OnBootstrap handlers in reverse
	// registration order, so the JS hook handler would run *before* the core
	// bootstrap that applies the migrations creating `instances`.
	//
	// To keep the original hooks byte-for-byte unmodified we therefore apply
	// the migrations in a first pass that has no hooks loaded, and only then
	// bootstrap the real app (with hooks).
	if err := a.applyMigrations(); err != nil {
		return err
	}
	if err := a.core.Bootstrap(); err != nil {
		return fmt.Errorf("mothership: bootstrap: %w", err)
	}
	a.logger.Infof("mothership ready (data dir %s)", a.core.DataDir())
	return nil
}

// applyMigrations connects the database and applies the system + JS migrations
// using a throwaway app instance that has no JS hooks registered.
//
// Without the hooks, PocketBase's OnBootstrap trigger has nothing to fire
// before the migrations, so the collections the original hooks expect are
// guaranteed to exist by the time the real app boots.
func (a *App) applyMigrations() error {
	cfg := a.cfg
	// Point the hooks directory at an empty folder so no hook script runs.
	emptyHooks := filepath.Join(a.cfg.Root, ".no_hooks")
	if err := os.MkdirAll(emptyHooks, 0o755); err != nil {
		return fmt.Errorf("mothership: create empty hooks dir: %w", err)
	}
	cfg.HooksDir = emptyHooks

	migrator := newApp(cfg)
	if err := migrator.Bootstrap(); err != nil {
		return fmt.Errorf("mothership: migrate bootstrap: %w", err)
	}
	if err := migrator.RunAllMigrations(); err != nil {
		return fmt.Errorf("mothership: run migrations: %w", err)
	}
	migrator.ResetBootstrapState()
	return nil
}

// Serve starts the HTTP server and blocks until ctx is cancelled or the server
// stops. The PocketBase router serves the original mothership routes, the
// PocketBase record APIs, realtime and the admin UI.
//
// PocketBase's own serve command wires up every API (records, auth, files,
// realtime, batch, logs) plus the JS hooks; we call apis.Serve directly so the
// listen address and CORS origins come from our configuration.
func (a *App) Serve(ctx context.Context) error {
	errCh := make(chan error, 1)

	go func() {
		a.logger.Infof("mothership listening on %s", a.cfg.Addr)
		errCh <- apis.Serve(a.core, apis.ServeConfig{
			HttpAddr:        a.cfg.Addr,
			ShowStartBanner: false,
			AllowedOrigins:  a.cfg.AllowedOrigins,
		})
	}()

	select {
	case <-ctx.Done():
		return nil
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

// ServeOn starts the HTTP server on a caller-provided listener.
//
// This is how the single-binary deployment mounts the mothership on the same
// port as the edge: the listener is shared and PocketBase's router is bound to
// it, while the daemon keeps its own routes on the same mux.
func (a *App) ServeOn(ctx context.Context, ln net.Listener) error {
	errCh := make(chan error, 1)

	// PocketBase builds its router during OnServe; injecting the listener
	// there makes it serve on the address we want.
	a.pb.OnServe().BindFunc(func(e *core.ServeEvent) error {
		e.Listener = ln
		return e.Next()
	})

	go func() {
		errCh <- apis.Serve(a.core, apis.ServeConfig{
			HttpAddr:        ln.Addr().String(),
			ShowStartBanner: false,
			AllowedOrigins:  a.cfg.AllowedOrigins,
		})
	}()

	select {
	case <-ctx.Done():
		return nil
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

// Handler exposes the PocketBase router as an http.Handler so the mothership can
// be mounted inside another server.
//
// The router is only assembled during PocketBase's OnServe event, so the
// handler is resolved lazily on the first request. Call Serve (or ServeOn)
// first so the event has fired.
func (a *App) Handler() http.Handler {
	var (
		once sync.Once
		h    http.Handler
	)
	a.pb.OnServe().BindFunc(func(e *core.ServeEvent) error {
		mux, err := e.Router.BuildMux()
		if err != nil {
			return err
		}
		h = mux
		return e.Next()
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		once.Do(func() {})
		if h != nil {
			h.ServeHTTP(w, r)
			return
		}
		http.Error(w, "mothership not ready", http.StatusServiceUnavailable)
	})
}
