package store

import (
	"context"
	"path/filepath"
	"testing"
)

func testRepo(t *testing.T) *Repo {
	t.Helper()
	db, err := Open(filepath.Join(t.TempDir(), "meta.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return NewRepo(db)
}

func TestUserAndInstanceLifecycle(t *testing.T) {
	ctx := context.Background()
	repo := testRepo(t)

	u := &User{Email: "Owner@Example.com", TokenKey: "tk", PasswordHash: "hash", SubscriptionQuantity: 2}
	if err := repo.CreateUser(ctx, u); err != nil {
		t.Fatalf("create user: %v", err)
	}
	if len(u.ID) != IDLength {
		t.Fatalf("expected %d-char id, got %q", IDLength, u.ID)
	}

	got, err := repo.GetUserByEmail(ctx, "owner@example.com")
	if err != nil {
		t.Fatalf("get by email: %v", err)
	}
	if got.ID != u.ID {
		t.Fatalf("email lookup returned %q, want %q", got.ID, u.ID)
	}

	inst := &Instance{
		Subdomain: "demo",
		UID:       u.ID,
		Version:   "0.22.0",
		Power:     true,
		Secrets:   map[string]string{"API_KEY": "v"},
	}
	if err := repo.CreateInstance(ctx, inst); err != nil {
		t.Fatalf("create instance: %v", err)
	}

	byHost, err := repo.GetInstanceByHost(ctx, "demo.pockethost.io", "pockethost.io")
	if err != nil {
		t.Fatalf("get by host: %v", err)
	}
	if byHost.ID != inst.ID {
		t.Fatalf("host lookup returned %q", byHost.ID)
	}

	if err := repo.PatchInstanceFields(ctx, inst.ID, map[string]any{"status": "running"}); err != nil {
		t.Fatalf("patch: %v", err)
	}
	fresh, _ := repo.GetInstance(ctx, inst.ID)
	if fresh.Status != StatusRunning {
		t.Fatalf("status = %q, want running", fresh.Status)
	}
	if fresh.Secrets["API_KEY"] != "v" {
		t.Fatalf("secrets lost: %#v", fresh.Secrets)
	}

	// CNAME takes precedence over the subdomain mapping.
	inst.CNAME = "custom.example.org"
	if err := repo.UpdateInstance(ctx, inst); err != nil {
		t.Fatalf("update: %v", err)
	}
	if cname, err := repo.GetInstanceByHost(ctx, "custom.example.org", "pockethost.io"); err != nil || cname.ID != inst.ID {
		t.Fatalf("cname lookup failed: %v", err)
	}

	if n, err := repo.CountInstancesByUID(ctx, u.ID); err != nil || n != 1 {
		t.Fatalf("count = %d, err = %v", n, err)
	}

	if err := repo.DeleteInstance(ctx, inst.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := repo.GetInstance(ctx, inst.ID); err != ErrNotFound {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestProcessRegistry(t *testing.T) {
	ctx := context.Background()
	repo := testRepo(t)

	u := &User{Email: "p@example.com", TokenKey: "tk", PasswordHash: "x"}
	if err := repo.CreateUser(ctx, u); err != nil {
		t.Fatal(err)
	}
	inst := &Instance{Subdomain: "proc", UID: u.ID}
	if err := repo.CreateInstance(ctx, inst); err != nil {
		t.Fatal(err)
	}

	p := &Process{InstanceID: inst.ID, PID: 4242, Port: 31234, Status: "running", Version: "0.22.0"}
	if err := repo.UpsertProcess(ctx, p); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	got, err := repo.GetProcess(ctx, inst.ID)
	if err != nil || got.PID != 4242 || got.Port != 31234 {
		t.Fatalf("get process: %+v err=%v", got, err)
	}

	ports, err := repo.PortsInUse(ctx)
	if err != nil || ports[31234] != inst.ID {
		t.Fatalf("ports in use: %#v err=%v", ports, err)
	}

	if err := repo.MarkProcessStopped(ctx, inst.ID, 1, "boom"); err != nil {
		t.Fatal(err)
	}
	got, _ = repo.GetProcess(ctx, inst.ID)
	if got.Status != "stopped" || got.PID != 0 {
		t.Fatalf("after stop: %+v", got)
	}
}

func TestKVAndAudit(t *testing.T) {
	ctx := context.Background()
	repo := testRepo(t)

	if _, ok, err := repo.KVGet("missing"); err != nil || ok {
		t.Fatalf("expected missing key, ok=%v err=%v", ok, err)
	}
	if err := repo.KVSet("k", "v1"); err != nil {
		t.Fatal(err)
	}
	if err := repo.KVSet("k", "v2"); err != nil {
		t.Fatal(err)
	}
	v, ok, err := repo.KVGet("k")
	if err != nil || !ok || v != "v2" {
		t.Fatalf("kv = %q ok=%v err=%v", v, ok, err)
	}
	if err := repo.Audit(ctx, "u1", "test", "detail"); err != nil {
		t.Fatal(err)
	}
}
