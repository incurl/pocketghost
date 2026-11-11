package scheduler

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/incurl/pocketghost/internal/backup"
	"github.com/incurl/pocketghost/internal/config"
	"github.com/incurl/pocketghost/internal/logging"
	"github.com/incurl/pocketghost/internal/store"
)

func testDeps(t *testing.T) (*config.Config, *store.Repo) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("PH_HOME", home)
	t.Setenv("DATA_ROOT", filepath.Join(home, "data"))
	t.Setenv("NODE_ENV", "development")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("config: %v", err)
	}
	db, err := store.Open(filepath.Join(home, "meta.db"))
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return cfg, store.NewRepo(db)
}

func TestStartStop(t *testing.T) {
	cfg, repo := testDeps(t)
	mgr := backup.New(cfg, logging.Discard(), nil)
	s := New(cfg, repo, nil, mgr, logging.Discard())

	if err := s.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	done := make(chan struct{})
	go func() { s.Stop(); close(done) }()
	select {
	case <-done:
	case <-time.After(15 * time.Second):
		t.Fatal("scheduler did not stop")
	}
}

func TestBackupSweepWithNoInstances(t *testing.T) {
	cfg, repo := testDeps(t)
	mgr := backup.New(cfg, logging.Discard(), nil)
	s := New(cfg, repo, nil, mgr, logging.Discard())
	// Must be a no-op, not a panic.
	s.BackupAll(context.Background())
	s.RetentionSweep(context.Background())
}
