package app

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/incurl/pocketghost/internal/config"
	"github.com/incurl/pocketghost/internal/store"
)

// fakePocketBase is a minimal stand-in for a PocketBase binary. It honours the
// `serve --http <addr>` contract so the whole lifecycle (spawn, health probe,
// proxy, hibernation) can be exercised without downloading a release.
const fakePocketBase = `package main

import (
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
)

func main() {
	args := os.Args[1:]
	addr := "127.0.0.1:8090"
	for i, a := range args {
		if a == "--http" && i+1 < len(args) {
			addr = args[i+1]
		}
		if strings.HasPrefix(a, "--http=") {
			addr = strings.TrimPrefix(a, "--http=")
		}
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/api/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte("{\"code\":200,\"message\":\"API is healthy.\"}"))
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte("{\"instance\":\"" + os.Getenv("PH_INSTANCE_SUBDOMAIN") + "\",\"path\":\"" + r.URL.Path + "\"}"))
	})

	srv := &http.Server{Addr: addr, Handler: mux}
	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatal(err)
		}
	}()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig
	_ = srv.Close()
	os.Exit(0)
}
`

func buildFakePocketBase(t *testing.T, dir string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("integration test uses unix signals")
	}
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain not available")
	}
	srcDir := filepath.Join(dir, "fakepb")
	if err := os.MkdirAll(srcDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "main.go"), []byte(fakePocketBase), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "go.mod"), []byte("module fakepb\n\ngo 1.21\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(dir, "pocketbase-fake")
	cmd := exec.Command("go", "build", "-o", bin, ".")
	cmd.Dir = srcDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("build fake pocketbase: %v\n%s", err, out)
	}
	return bin
}

func TestEndToEndProxyAndHibernation(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test")
	}
	dir := t.TempDir()
	bin := buildFakePocketBase(t, dir)

	home := filepath.Join(dir, "home")
	t.Setenv("PH_HOME", home)
	t.Setenv("DATA_ROOT", filepath.Join(home, "data"))
	t.Setenv("NODE_ENV", "development")
	t.Setenv("APEX_DOMAIN", "example.test")
	t.Setenv("PH_SECRET", "integration")
	t.Setenv("DAEMON_PB_IDLE_TTL", "1200")
	t.Setenv("PH_INSTANCE_BINARY", bin)
	t.Setenv("PH_INSTANCE_PORT_MIN", "43000")
	t.Setenv("PH_INSTANCE_PORT_MAX", "43100")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("config: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	a, err := New(ctx, cfg)
	if err != nil {
		t.Fatalf("app: %v", err)
	}
	defer a.Close()

	u := &store.User{
		Email:                "owner@example.test",
		Verified:             true,
		Subscription:         store.SubscriptionPremium,
		SubscriptionQuantity: 2,
		TokenKey:             strings.Repeat("t", 50),
		PasswordHash:         "0123456789abcdef0123456789abcdef",
	}
	if err := a.Repo.CreateUser(ctx, u); err != nil {
		t.Fatal(err)
	}
	inst := &store.Instance{Subdomain: "demo", UID: u.ID, Status: store.StatusIdle, Version: "0.23.0", Power: true}
	if err := a.Repo.CreateInstance(ctx, inst); err != nil {
		t.Fatal(err)
	}

	front := httptest.NewServer(a.DaemonHandler())
	defer front.Close()

	get := func(host, path string) (int, string) {
		req, _ := http.NewRequest(http.MethodGet, front.URL+path, nil)
		req.Host = host
		resp, err := front.Client().Do(req)
		if err != nil {
			t.Fatalf("request: %v", err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(body)
	}

	code, body := get("demo.example.test", "/api/collections/posts/records")
	if code != http.StatusOK {
		t.Fatalf("proxy status = %d body=%s", code, body)
	}
	if !strings.Contains(body, `"instance":"demo"`) {
		t.Fatalf("unexpected proxy body: %s", body)
	}

	proc, ok := a.Supervisor.Get(inst.ID)
	if !ok {
		t.Fatal("expected a running tenant process")
	}
	if proc.Port() < 43000 || proc.Port() > 43100 {
		t.Fatalf("port %d outside the configured pool", proc.Port())
	}
	row, err := a.Repo.GetProcess(ctx, inst.ID)
	if err != nil || row.Status != "running" || row.PID != proc.PID() {
		t.Fatalf("registry row = %+v err=%v", row, err)
	}

	// Health probes are answered by the daemon, never proxied to a tenant.
	if code, _ := get("demo.example.test", "/_api/daemon/health"); code != http.StatusOK {
		t.Fatalf("daemon health = %d", code)
	}

	// Hibernation: after the idle TTL the process must be gone.
	deadline := time.Now().Add(25 * time.Second)
	for time.Now().Before(deadline) {
		if _, ok := a.Supervisor.Get(inst.ID); !ok {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if _, ok := a.Supervisor.Get(inst.ID); ok {
		t.Fatal("instance did not hibernate after its idle TTL")
	}

	row, err = a.Repo.GetProcess(ctx, inst.ID)
	if err != nil {
		t.Fatalf("registry row after hibernate: %v", err)
	}
	if row.Status != "stopped" {
		t.Fatalf("status after hibernate = %q", row.Status)
	}
	if row.PID != 0 {
		t.Fatalf("pid after hibernate = %d", row.PID)
	}

	fmt.Fprintln(os.Stderr, "integration: proxy, registry and hibernation verified")
}

// TestCrashRestart verifies a killed tenant process is relaunched with a new PID
// by the supervisor's restart policy (the Docker HEALTHCHECK/restart replacement).
func TestCrashRestart(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test")
	}
	dir := t.TempDir()
	bin := buildFakePocketBase(t, dir)

	home := filepath.Join(dir, "home")
	t.Setenv("PH_HOME", home)
	t.Setenv("DATA_ROOT", filepath.Join(home, "data"))
	t.Setenv("NODE_ENV", "development")
	t.Setenv("APEX_DOMAIN", "example.test")
	t.Setenv("PH_INSTANCE_BINARY", bin)
	t.Setenv("PH_INSTANCE_PORT_MIN", "43200")
	t.Setenv("PH_INSTANCE_PORT_MAX", "43300")

	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	a, err := New(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()

	u := &store.User{
		Email:                "crash@example.test",
		Verified:             true,
		Subscription:         store.SubscriptionPremium,
		SubscriptionQuantity: 1,
		TokenKey:             strings.Repeat("t", 50),
		PasswordHash:         "0123456789abcdef0123456789abcdef",
	}
	if err := a.Repo.CreateUser(ctx, u); err != nil {
		t.Fatal(err)
	}
	inst := &store.Instance{Subdomain: "crash", UID: u.ID, Status: store.StatusIdle, Version: "0.23.0", Power: true}
	if err := a.Repo.CreateInstance(ctx, inst); err != nil {
		t.Fatal(err)
	}

	rt, err := a.Instances.EnsureRunning(ctx, inst)
	if err != nil {
		t.Fatalf("ensure running: %v", err)
	}
	firstPID := rt.PID

	proc, ok := a.Supervisor.Get(inst.ID)
	if !ok {
		t.Fatal("no process after startup")
	}
	if err := proc.Kill(); err != nil {
		t.Fatalf("kill: %v", err)
	}

	deadline := time.Now().Add(25 * time.Second)
	for time.Now().Before(deadline) {
		if p, ok := a.Supervisor.Get(inst.ID); ok && p.PID() != firstPID {
			return // restarted with a new pid
		}
		time.Sleep(250 * time.Millisecond)
	}
	t.Fatal("instance was not restarted after a crash")
}
