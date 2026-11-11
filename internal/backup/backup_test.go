package backup

import (
	"archive/zip"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/incurl/pocketghost/internal/config"
	"github.com/incurl/pocketghost/internal/logging"
)

func testConfig(t *testing.T) *config.Config {
	t.Helper()
	home := t.TempDir()
	t.Setenv("PH_HOME", home)
	t.Setenv("DATA_ROOT", filepath.Join(home, "data"))
	t.Setenv("NODE_ENV", "development")
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("config: %v", err)
	}
	return cfg
}

func TestCreateListRestore(t *testing.T) {
	cfg := testConfig(t)
	mgr := New(cfg, logging.Discard(), nil)

	instDir := cfg.InstanceDataPath("abc123")
	if err := os.MkdirAll(filepath.Join(instDir, "pb_data"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(instDir, "logs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(instDir, "pb_data", "data.db"), []byte("sqlite-bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(instDir, "logs", "exec.log"), []byte("log"), 0o644); err != nil {
		t.Fatal(err)
	}

	name, err := mgr.Create(context.Background(), "abc123")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if name == "" {
		t.Fatal("empty backup name")
	}

	// Logs are intentionally excluded from backups.
	zr, err := zip.OpenReader(filepath.Join(cfg.BackupPath("abc123"), name))
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	var sawDB, sawLogs bool
	for _, f := range zr.File {
		if f.Name == "pb_data/data.db" {
			sawDB = true
		}
		if filepath.HasPrefix(f.Name, "logs/") {
			sawLogs = true
		}
	}
	if !sawDB {
		t.Fatal("backup is missing pb_data/data.db")
	}
	if sawLogs {
		t.Fatal("backup should not contain logs")
	}

	names, err := mgr.List("abc123")
	if err != nil || len(names) != 1 {
		t.Fatalf("list: %v %v", names, err)
	}

	// Corrupt the live data and restore.
	if err := os.WriteFile(filepath.Join(instDir, "pb_data", "data.db"), []byte("corrupt"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := mgr.Restore(context.Background(), "abc123", name); err != nil {
		t.Fatalf("restore: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(instDir, "pb_data", "data.db"))
	if err != nil || string(got) != "sqlite-bytes" {
		t.Fatalf("restored data = %q err=%v", got, err)
	}
}

func TestRetention(t *testing.T) {
	cfg := testConfig(t)
	cfg.BackupRetainCount = 2
	mgr := New(cfg, logging.Discard(), nil)

	instDir := cfg.InstanceDataPath("ret1")
	if err := os.MkdirAll(filepath.Join(instDir, "pb_data"), 0o755); err != nil {
		t.Fatal(err)
	}

	// Seed old backups with distinct names.
	dir := cfg.BackupPath("ret1")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"@auto_pb_backup_ph_1.zip", "@auto_pb_backup_ph_2.zip"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	if _, err := mgr.Create(context.Background(), "ret1"); err != nil {
		t.Fatal(err)
	}
	names, _ := mgr.List("ret1")
	if len(names) != 2 {
		t.Fatalf("retention kept %d backups: %v", len(names), names)
	}
}

func TestInvalidNames(t *testing.T) {
	cfg := testConfig(t)
	mgr := New(cfg, logging.Discard(), nil)
	for _, name := range []string{"../../etc/passwd", "x.txt", "sub/dir.zip"} {
		if _, err := mgr.Path("i", name); err == nil {
			t.Errorf("expected rejection of %q", name)
		}
	}
}
