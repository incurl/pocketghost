package scheduler

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/incurl/pocketghost/internal/backup"
	"github.com/incurl/pocketghost/internal/config"
	"github.com/incurl/pocketghost/internal/logging"
	"github.com/incurl/pocketghost/internal/mothership"
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

	msRoot := filepath.Join(home, "mothership")
	if _, err := mothership.Materialize(msRoot); err != nil {
		t.Fatalf("materialize: %v", err)
	}
	ms := mothership.New(mothership.Config{
		DataDir: filepath.Join(msRoot, "pb_data"),
		Root:    msRoot,
		Addr:    "127.0.0.1:0",
		IsDev:   true,
		Logger:  logging.Discard(),
	})
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	if err := ms.Bootstrap(ctx); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}

	return cfg, store.NewRepo(mothership.NewStore(ms.Core()))
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
