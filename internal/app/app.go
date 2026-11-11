// Package app wires the control plane together.
//
// The control plane is the original PocketHost mothership application, running
// unmodified inside the pure-Go PocketBase SDK (internal/mothership). This
// package adds the edge: native process supervision, the reverse proxy, the
// rate limiter, the file services and the scheduler.
package app

import (
	"context"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/incurl/pocketghost/internal/api"
	"github.com/incurl/pocketghost/internal/backup"
	"github.com/incurl/pocketghost/internal/config"
	"github.com/incurl/pocketghost/internal/dashboard"
	"github.com/incurl/pocketghost/internal/instance"
	"github.com/incurl/pocketghost/internal/logging"
	"github.com/incurl/pocketghost/internal/metrics"
	"github.com/incurl/pocketghost/internal/mothership"
	"github.com/incurl/pocketghost/internal/pbbin"
	"github.com/incurl/pocketghost/internal/procs"
	"github.com/incurl/pocketghost/internal/proxy"
	"github.com/incurl/pocketghost/internal/ratelimit"
	"github.com/incurl/pocketghost/internal/scheduler"
	"github.com/incurl/pocketghost/internal/store"
	"github.com/incurl/pocketghost/internal/tlscert"
)

// App holds every long-lived component.
type App struct {
	Config     *config.Config
	Logger     *logging.Logger
	Mothership *mothership.App
	Repo       *store.Repo
	Logs       *logging.LogStore
	Ports      *procs.PortAllocator
	Supervisor *procs.Supervisor
	Binaries   *pbbin.Service
	Instances  *instance.Service
	Backups    *backup.Manager
	Scheduler  *scheduler.Scheduler
	API        *api.Server
	Proxy      *proxy.Handler
	Stats      *metrics.Stats
	TLS        *tlscert.Manager

	// dashboardHandler serves the embedded Svelte build.
	dashboardHandler http.Handler
}

// New builds the application graph.
func New(ctx context.Context, cfg *config.Config) (*App, error) {
	logger := logging.New(logging.Options{
		Level:     levelFor(cfg),
		Namespace: "pockethost",
	})
	if cfg.Debug {
		for _, kv := range cfg.LogSummary() {
			logger.Infof("%s: %s", kv[0], kv[1])
		}
	}

	// The mothership application (the original PocketHost control plane) runs
	// inside the PocketBase SDK and owns the database.
	msRoot := filepath.Join(cfg.Home, "mothership")
	if _, err := mothership.Materialize(msRoot); err != nil {
		return nil, err
	}
	ms := mothership.New(mothership.Config{
		DataDir:        filepath.Join(msRoot, "pb_data"),
		Root:           msRoot,
		Addr:           fmt.Sprintf("127.0.0.1:%d", cfg.MothershipPort),
		IsDev:          cfg.IsDev,
		AllowedOrigins: []string{"*"},
		Logger:         logger,
	})
	if err := ms.Bootstrap(ctx); err != nil {
		return nil, err
	}

	repo := store.NewRepo(mothership.NewStore(ms.Core()))

	logs := logging.NewLogStore(cfg.InstancesRoot(), 0, logger.Create("instance-logs"))

	ports, err := procs.NewPortAllocator(cfg.PortPoolMin, cfg.PortPoolMax)
	if err != nil {
		return nil, err
	}
	if used, err := repo.PortsInUse(ctx); err == nil {
		ports.Seed(used)
	}

	sup := procs.NewSupervisor(ports, logs, repo, logger.Create("supervisor"),
		cfg.MaxConcurrentLaunches, cfg.StopTimeout, procs.DefaultRestartPolicy())

	bins := pbbin.New(cfg.PocketBaseRoot, logger.Create("pocketbase"))
	bins.DownloadTo = cfg.PocketBaseDownloadURL

	svc := instance.New(cfg, repo, sup, bins, logs, logger)
	if err := sup.Reconcile(ctx); err != nil {
		logger.Warnf("runtime reconcile: %v", err)
	}

	backups := backup.New(cfg, logger, svc)
	sched := scheduler.New(cfg, repo, svc, backups, logger)

	stats := metrics.New()
	edgeProxy := proxy.New(proxy.Options{
		Resolver: svc,
		Logger:   logger,
		Stats:    stats,
	})

	var dashFS fs.FS
	if d := dashboard.FS(); d != nil {
		dashFS = d
	}

	apiSrv, err := api.New(api.Options{
		Config:    cfg,
		Repo:      repo,
		Instances: svc,
		Binaries:  bins,
		Logs:      logs,
		Stats:     stats,
		Logger:    logger,
		Dashboard: dashFS,
		Backups:   backups,
	})
	if err != nil {
		return nil, err
	}

	a := &App{
		Config:     cfg,
		Logger:     logger,
		Mothership: ms,
		Repo:       repo,
		Logs:       logs,
		Ports:      ports,
		Supervisor: sup,
		Binaries:   bins,
		Instances:  svc,
		Backups:    backups,
		Scheduler:  sched,
		API:        apiSrv,
		Proxy:      edgeProxy,
		Stats:      stats,
	}

	svc.SetReady(true)

	if tlsMgr, err := tlscert.New(cfg, logger); err == nil {
		a.TLS = tlsMgr
	} else {
		logger.Warnf("TLS unavailable: %v", err)
	}

	return a, nil
}

// Close releases resources.
func (a *App) Close() error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_ = a.Supervisor.StopAll(ctx)
	_ = a.Logs.Close()
	return nil
}

// MigrateLegacy imports records from a legacy PocketBase mothership database.
//
// The control plane database is now a PocketBase database, so the legacy
// TypeScript deployment's data.db is imported as-is: PocketBase's own schema
// is identical, and the import copies the tables the legacy build created.
func (a *App) MigrateLegacy(ctx context.Context, legacyPath string) (*store.ImportStats, error) {
	stats, err := mothership.ImportLegacy(ctx, a.Mothership.Core(), legacyPath)
	if err != nil {
		return nil, err
	}
	a.Logger.Infof("legacy import complete: %+v", *stats)
	return stats, nil
}

// RateLimiter builds the edge rate limiter for firewall mode.
func (a *App) RateLimiter() *ratelimit.Middleware {
	res := &limiterResolver{repo: a.Repo, cfg: a.Config}
	return ratelimit.NewMiddleware(ratelimit.NewLimiter(), res, a.Logger, a.Config.DisableFirewallRateLimit)
}

func levelFor(cfg *config.Config) logging.Level {
	if cfg.Trace {
		return logging.LevelTrace
	}
	if cfg.Debug {
		return logging.LevelDebug
	}
	return logging.LevelInfo
}

var _ = os.Getenv
