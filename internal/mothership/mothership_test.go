package mothership

import (
	"context"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pocketbase/pocketbase/core"

	"github.com/incurl/pocketghost/internal/logging"
)

// TestAssetsMaterialize verifies the original hooks land on disk unchanged.
func TestAssetsMaterialize(t *testing.T) {
	root := t.TempDir()
	if _, err := Materialize(root); err != nil {
		t.Fatalf("materialize: %v", err)
	}

	for _, rel := range []string{
		"pb_hooks/mothership.pb.js",
		"pb_hooks/mothership.js",
		"pb_admin_ext/live/main.js",
	} {
		if _, err := os.Stat(filepath.Join(root, rel)); err != nil {
			t.Fatalf("missing %s: %v", rel, err)
		}
	}

	migrations, err := os.ReadDir(filepath.Join(root, "pb_migrations"))
	if err != nil {
		t.Fatal(err)
	}
	if len(migrations) != 21 {
		t.Fatalf("expected 21 migrations, got %d", len(migrations))
	}

	// The hooks must be byte-identical to the embedded originals.
	embedded, err := Assets.ReadFile("assets/pb_hooks/mothership.pb.js")
	if err != nil {
		t.Fatal(err)
	}
	onDisk, err := os.ReadFile(filepath.Join(root, "pb_hooks/mothership.pb.js"))
	if err != nil {
		t.Fatal(err)
	}
	if string(embedded) != string(onDisk) {
		t.Fatal("materialized hooks differ from the embedded originals")
	}

	// Materializing twice must be idempotent.
	if _, err := Materialize(root); err != nil {
		t.Fatalf("second materialize: %v", err)
	}
}

// TestHooksAreNodeFree guards the core assumption of this migration: the
// original mothership hooks must not require Node built-ins, because they run
// inside PocketBase's pure-Go JSVM.
func TestHooksAreNodeFree(t *testing.T) {
	for _, name := range []string{"mothership.pb.js", "mothership.js"} {
		data, err := Assets.ReadFile("assets/pb_hooks/" + name)
		if err != nil {
			t.Fatal(err)
		}
		body := string(data)
		for _, banned := range []string{
			"require('fs')", `require("fs")`,
			"require('crypto')", `require("crypto")`,
			"require('path')", `require("path")`,
			"require('os')", `require("os")`,
			"require('node:",
		} {
			if strings.Contains(body, banned) {
				t.Errorf("%s requires a Node built-in (%q)", name, banned)
			}
		}
	}
}

// TestMigrationsAreNodeFree is the same guard for the migration files.
func TestMigrationsAreNodeFree(t *testing.T) {
	entries, err := Assets.ReadDir("assets/pb_migrations")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		data, err := Assets.ReadFile("assets/pb_migrations/" + e.Name())
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(data), "require(") {
			t.Errorf("%s contains a require() call", e.Name())
		}
	}
}

// TestStoreRoundTrip exercises the edge's read/write path against the real
// PocketBase app (with the original hooks loaded).
func TestStoreRoundTrip(t *testing.T) {
	if testing.Short() {
		t.Skip("boots a full PocketBase app")
	}
	root := t.TempDir()
	if _, err := Materialize(root); err != nil {
		t.Fatal(err)
	}
	app := New(Config{
		DataDir: filepath.Join(root, "pb_data"),
		Root:    root,
		Addr:    "127.0.0.1:0",
		IsDev:   true,
		Logger:  logging.Discard(),
	})

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	if err := app.Bootstrap(ctx); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}

	store := NewStore(app.Core())

	user, err := store.CreateUser(ctx, &User{
		Email:                "owner@example.test",
		Verified:             true,
		Subscription:         "premium",
		SubscriptionQuantity: 2,
		TokenKey:             strings.Repeat("t", 50),
		PasswordHash:         "0123456789abcdef0123456789abcdef",
	})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	if len(user.ID) != IDLength {
		t.Fatalf("user id = %q, want %d chars", user.ID, IDLength)
	}

	inst, err := store.CreateInstance(ctx, &Instance{
		Subdomain:  "demo",
		UID:        user.ID,
		Status:     "idle",
		Version:    "0.23.0",
		Power:      true,
		SyncAdmin:  true,
		AutoVacuum: true,
	})
	if err != nil {
		t.Fatalf("create instance: %v", err)
	}
	if inst.ID == "" {
		t.Fatal("instance id was not assigned")
	}

	// The instance must be reachable by hostname (the proxy's lookup path).
	got, err := store.GetInstanceByHost(ctx, "demo.pockethost.io", "pockethost.io")
	if err != nil {
		t.Fatalf("lookup by host: %v", err)
	}
	if got.UID != user.ID || got.Subdomain != "demo" {
		t.Fatalf("looked up %+v", got)
	}

	// The status sync path (edge -> control plane) must work.
	if err := store.UpdateInstanceFields(ctx, inst.ID, map[string]any{"status": "running"}); err != nil {
		t.Fatalf("update status: %v", err)
	}
	got, _ = store.GetInstance(ctx, inst.ID)
	if got.Status != "running" {
		t.Fatalf("status = %q, want running", got.Status)
	}

	// The native process registry must round-trip.
	if err := store.UpsertProcess(ctx, &Process{
		InstanceID: inst.ID,
		PID:        4242,
		Port:       31000,
		Status:     "running",
		Version:    "0.23.0",
	}); err != nil {
		t.Fatalf("upsert process: %v", err)
	}
	proc, err := store.GetProcess(ctx, inst.ID)
	if err != nil {
		t.Fatalf("get process: %v", err)
	}
	if proc.PID != 4242 || proc.Port != 31000 {
		t.Fatalf("process = %+v", proc)
	}
	ports, err := store.PortsInUse(ctx)
	if err != nil || ports[31000] != inst.ID {
		t.Fatalf("ports in use = %#v err=%v", ports, err)
	}
}

// TestMothershipBootsWithOriginalHooks is the decisive test: the SDK app must
// apply the original migrations and serve the original routes.
func TestMothershipBootsWithOriginalHooks(t *testing.T) {
	if testing.Short() {
		t.Skip("boots a full PocketBase app")
	}
	root := t.TempDir()
	if _, err := Materialize(root); err != nil {
		t.Fatal(err)
	}
	dataDir := filepath.Join(root, "pb_data")
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		t.Fatal(err)
	}

	app := New(Config{
		DataDir: dataDir,
		Root:    root,
		Addr:    "127.0.0.1:0",
		IsDev:   true,
		Logger:  logging.Discard(),
	})

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	if err := app.Bootstrap(ctx); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}

	pb := app.Core()

	// The original migrations must have created the mothership collections.
	for _, name := range []string{"users", "instances", "edges", "regions"} {
		if _, err := pb.FindCollectionByNameOrId(name); err != nil {
			t.Fatalf("collection %q missing after migrations: %v", name, err)
		}
	}

	// All 21 original JS migrations must have been applied. They are
	// registered once per app instance, and the boot sequence creates a
	// migration pass plus the real app, so expect the count to be a multiple.
	applied := map[string]bool{}
	for _, m := range core.AppMigrations.Items() {
		if strings.HasSuffix(m.File, ".js") {
			applied[m.File] = true
		}
	}
	if len(applied) != 21 {
		t.Fatalf("expected 21 distinct JS migrations, got %d", len(applied))
	}

	// The instance collection must carry the fields the dashboard reads.
	inst, err := pb.FindCollectionByNameOrId("instances")
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"subdomain", "uid", "status", "version", "power", "cname", "idleTtl", "edge", "syncAdmin", "autoVacuum"} {
		if inst.Fields.GetByName(field) == nil {
			t.Errorf("instances collection is missing the %q field", field)
		}
	}

	// The users collection must carry the auth and billing fields.
	users, err := pb.FindCollectionByNameOrId("users")
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"email", "verified", "subscription", "subscription_quantity", "subscription_status", "trusted_ips", "volume_storage_used", "object_storage_used"} {
		if users.Fields.GetByName(field) == nil {
			t.Errorf("users collection is missing the %q field", field)
		}
	}

	// The native process registry must exist as a collection.
	procs, err := pb.FindCollectionByNameOrId("processes")
	if err != nil {
		t.Fatalf("processes collection missing: %v", err)
	}
	for _, field := range []string{"instance", "pid", "port", "status", "version"} {
		if procs.Fields.GetByName(field) == nil {
			t.Errorf("processes collection is missing the %q field", field)
		}
	}

	// The original JS hooks must have registered their routes. PocketBase
	// assembles its router during OnServe, so serve on a real listener and
	// probe the routes over HTTP.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	serveCtx, serveCancel := context.WithCancel(context.Background())
	defer serveCancel()

	served := make(chan error, 1)
	go func() { served <- app.ServeOn(serveCtx, ln) }()

	client := &http.Client{Timeout: 5 * time.Second}
	base := "http://" + ln.Addr().String()

	// Wait for the server to accept connections.
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := client.Get(base + "/api/health")
		if err == nil {
			_ = resp.Body.Close()
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	for _, probe := range []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/api/versions"},
		{http.MethodGet, "/stats.json"},
		{http.MethodGet, "/api/health"},
		// The original hooks register POST /api/instance (create),
		// PUT /api/instance/{id} and DELETE /api/instance/{id}.
		{http.MethodPost, "/api/instance"},
		{http.MethodPost, "/api/signup"},
	} {
		req, _ := http.NewRequest(probe.method, base+probe.path, nil)
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("%s %s: %v", probe.method, probe.path, err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode == http.StatusNotFound {
			t.Errorf("route %s %s was not registered by the original hooks", probe.method, probe.path)
		}
		t.Logf("%s %s -> %d", probe.method, probe.path, resp.StatusCode)
	}

	// The realtime endpoint (used by the dashboard for live instance updates)
	// must exist so the Svelte client can subscribe.
	req, _ := http.NewRequest(http.MethodPost, base+"/api/realtime", strings.NewReader(""))
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("realtime: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		t.Error("realtime endpoint is missing; the dashboard live updates would break")
	}
	t.Logf("POST /api/realtime -> %d", resp.StatusCode)
}
