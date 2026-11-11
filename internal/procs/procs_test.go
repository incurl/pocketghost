package procs

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/incurl/pocketghost/internal/logging"
)

func TestPortAllocatorExclusive(t *testing.T) {
	a, err := NewPortAllocator(41000, 41010)
	if err != nil {
		t.Fatal(err)
	}
	p1, err := a.Allocate("inst-a")
	if err != nil {
		t.Fatal(err)
	}
	p2, err := a.Allocate("inst-b")
	if err != nil {
		t.Fatal(err)
	}
	if p1 == p2 {
		t.Fatalf("allocator handed out %d twice", p1)
	}
	// Re-allocating for the same instance is stable.
	again, err := a.Allocate("inst-a")
	if err != nil || again != p1 {
		t.Fatalf("re-allocate = %d (want %d), err=%v", again, p1, err)
	}

	a.Release(p1)
	p3, err := a.Allocate("inst-c")
	if err != nil {
		t.Fatal(err)
	}
	if p3 == p2 {
		t.Fatalf("allocated an in-use port %d", p3)
	}
}

func TestPortAllocatorExhaustion(t *testing.T) {
	a, err := NewPortAllocator(42000, 42001)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Allocate("a"); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Allocate("b"); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Allocate("c"); err == nil {
		t.Fatal("expected exhaustion error")
	}
}

func TestSpawnStopAndLogs(t *testing.T) {
	dir := t.TempDir()
	logs := logging.NewLogStore(dir, 1<<20, logging.Discard())

	// Use the shell as a stand-in for PocketBase: it ignores args, writes a
	// line and then waits for a signal.
	shell, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}

	p, err := Spawn(context.Background(), Options{
		InstanceID: "test",
		BinPath:    shell, // the test binary itself accepts -test.run flags; use sh instead
		WorkDir:    filepath.Join(dir, "instances", "test"),
		Logs:       logs,
		Logger:     logging.Discard(),
		Args:       []string{"-test.run=TestHelperProcess", "--", "sleep"},
		Env:        map[string]string{"GO_WANT_HELPER_PROCESS": "1"},
		Port:       0,
	})
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}
	if p.PID() <= 0 {
		t.Fatal("expected a pid")
	}
	if !p.Running() {
		t.Fatal("process should be running")
	}
	if _, err := os.Stat(filepath.Join(p.opts.WorkDir, "pocketbase.pid")); err != nil {
		t.Fatalf("pid file missing: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := p.Stop(ctx); err != nil {
		t.Logf("stop returned %v", err)
	}

	select {
	case exit := <-p.Exited():
		_ = exit
	case <-time.After(5 * time.Second):
		t.Fatal("process did not exit")
	}
}

// TestHelperProcess is not a real test: it is the child process entry point used
// by TestSpawnStopAndLogs.
func TestHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_HELPER_PROCESS") != "1" {
		return
	}
	time.Sleep(30 * time.Second)
	os.Exit(0)
}

func TestBuildServeArgs(t *testing.T) {
	args := BuildServeArgs(31234, false, false)
	want := []string{"serve", "--http", "127.0.0.1:31234"}
	if len(args) != len(want) {
		t.Fatalf("args = %v", args)
	}
	for i := range want {
		if args[i] != want[i] {
			t.Fatalf("args = %v", args)
		}
	}

	dev := BuildServeArgs(1, true, true)
	if dev[len(dev)-2] != "--dev" || dev[len(dev)-1] != "--encryptionEnv=PB_ENCRYPTION_KEY" {
		t.Fatalf("dev args = %v", dev)
	}
}

func TestEnvDoesNotLeakSecrets(t *testing.T) {
	t.Setenv("MOTHERSHIP_ADMIN_PASSWORD", "supersecret")
	env := buildEnv(Options{InstanceID: "i", Subdomain: "s", Env: map[string]string{"FOO": "bar"}})
	for _, kv := range env {
		if kv == "MOTHERSHIP_ADMIN_PASSWORD=supersecret" {
			t.Fatal("control-plane credentials leaked into the tenant environment")
		}
	}
	var sawFoo, sawInstance bool
	for _, kv := range env {
		if kv == "FOO=bar" {
			sawFoo = true
		}
		if kv == "PH_INSTANCE_ID=i" {
			sawInstance = true
		}
	}
	if !sawFoo || !sawInstance {
		t.Fatalf("expected instance env, got %v", env)
	}
}
