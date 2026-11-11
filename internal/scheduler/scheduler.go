// Package scheduler replaces the Node `cron` package (and the pm2 cron entries)
// with robfig/cron. Jobs run in-process, so a single binary needs no separate
// supervisor for periodic maintenance.
package scheduler

import (
	"context"
	"time"

	"github.com/robfig/cron/v3"

	"github.com/incurl/pocketghost/internal/backup"
	"github.com/incurl/pocketghost/internal/config"
	"github.com/incurl/pocketghost/internal/instance"
	"github.com/incurl/pocketghost/internal/logging"
	"github.com/incurl/pocketghost/internal/store"
)

// Scheduler owns the cron engine.
type Scheduler struct {
	cfg     *config.Config
	repo    *store.Repo
	svc     *instance.Service
	backups *backup.Manager
	log     *logging.Logger
	cron    *cron.Cron
}

// New creates a scheduler.
func New(cfg *config.Config, repo *store.Repo, svc *instance.Service, backups *backup.Manager, logger *logging.Logger) *Scheduler {
	return &Scheduler{
		cfg:     cfg,
		repo:    repo,
		svc:     svc,
		backups: backups,
		log:     logger.Create("scheduler"),
		cron:    cron.New(cron.WithChain(cron.SkipIfStillRunning(cron.DiscardLogger))),
	}
}

// Start registers the maintenance jobs and starts the engine.
func (s *Scheduler) Start() error {
	specs := []struct {
		name string
		spec string
		fn   func(ctx context.Context)
	}{
		{"instance-backups", orDefault(s.cfg.BackupCron, "0 3 * * *"), s.BackupAll},
		{"power-off-reconcile", "* * * * *", s.ReconcilePoweredOff},
		{"retention-sweep", "30 4 * * *", s.RetentionSweep},
	}

	for _, job := range specs {
		j := job
		if _, err := s.cron.AddFunc(j.spec, func() {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
			defer cancel()
			s.log.Debugf("running job %s", j.name)
			j.fn(ctx)
		}); err != nil {
			return err
		}
	}
	s.cron.Start()
	s.log.Infof("scheduler started with %d jobs", len(specs))
	return nil
}

// Stop halts the scheduler, waiting for running jobs.
func (s *Scheduler) Stop() {
	ctx := s.cron.Stop()
	select {
	case <-ctx.Done():
	case <-time.After(10 * time.Second):
	}
}

// BackupAll snapshots every instance with autoVacuum enabled.
func (s *Scheduler) BackupAll(ctx context.Context) {
	instances, err := s.repo.ListInstances(ctx, "")
	if err != nil {
		s.log.Warnf("backup sweep: %v", err)
		return
	}
	for _, inst := range instances {
		if !inst.AutoVacuum {
			continue
		}
		if inst.Suspension != "" {
			continue
		}
		if _, err := s.backups.Create(ctx, inst.ID); err != nil {
			s.log.Warnf("backup %s failed: %v", inst.ID, err)
		}
	}
}

// ReconcilePoweredOff stops any process whose instance was powered off.
func (s *Scheduler) ReconcilePoweredOff(ctx context.Context) {
	if s.svc == nil {
		return
	}
	s.svc.ReconcilePoweredOff(ctx)
}

// RetentionSweep removes backups beyond the retention policy for instances that
// were not touched by the nightly job (for example deleted or archived ones).
func (s *Scheduler) RetentionSweep(ctx context.Context) {
	instances, err := s.repo.ListInstances(ctx, "")
	if err != nil {
		return
	}
	live := map[string]bool{}
	for _, inst := range instances {
		live[inst.ID] = true
	}

	entries, err := readDirNames(s.cfg.BackupRoot)
	if err != nil {
		return
	}
	for _, id := range entries {
		if live[id] {
			continue
		}
		s.log.Infof("removing backups for deleted instance %s", id)
		_ = removeAll(s.cfg.BackupPath(id))
	}
}

func orDefault(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return v
}
