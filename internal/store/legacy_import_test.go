package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
)

// TestImportLegacy builds a miniature PocketBase-shaped database and verifies the
// importer copies records (including the 0.22 -> 0.23 passwordHash rename).
func TestImportLegacy(t *testing.T) {
	ctx := context.Background()
	repo := testRepo(t)

	legacyPath := filepath.Join(t.TempDir(), "legacy.db")
	db, err := sql.Open("sqlite", "file:"+legacyPath)
	if err != nil {
		t.Fatalf("open legacy: %v", err)
	}
	defer db.Close()

	stmts := []string{
		`create table users (id text primary key, created text, updated text, email text,
			verified integer, subscription text, subscription_quantity integer, tokenKey text, passwordHash text)`,
		`create table instances (id text primary key, created text, updated text, subdomain text,
			uid text, status text, version text, power integer, dev integer)`,
		`create table edges (id text primary key, created text, updated text, edge_id text, region text)`,
		`insert into users values ('u1','2024-01-01 00:00:00.000Z','2024-01-01 00:00:00.000Z','a@b.c',1,'premium',3,'tk','bcrypthash')`,
		`insert into instances values ('i1','2024-01-01 00:00:00.000Z','2024-01-01 00:00:00.000Z','legacy','u1','idle','0.22.0',1,1)`,
		`insert into edges values ('e1','2024-01-01 00:00:00.000Z','2024-01-01 00:00:00.000Z','sfo-2','sfo')`,
	}
	for _, stmt := range stmts {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("legacy setup: %v\n%s", err, stmt)
		}
	}

	stats, err := ImportLegacy(ctx, repo, legacyPath)
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if stats.Users != 1 || stats.Instances != 1 || stats.Edges != 1 {
		t.Fatalf("unexpected stats: %+v", *stats)
	}

	u, err := repo.GetUserByEmail(ctx, "a@b.c")
	if err != nil {
		t.Fatalf("imported user: %v", err)
	}
	if u.PasswordHash != "bcrypthash" {
		t.Fatalf("passwordHash not migrated: %q", u.PasswordHash)
	}
	if u.SubscriptionQuantity != 3 {
		t.Fatalf("subscription_quantity = %d", u.SubscriptionQuantity)
	}

	inst, err := repo.GetInstanceBySubdomain(ctx, "legacy")
	if err != nil {
		t.Fatalf("imported instance: %v", err)
	}
	if inst.UID != "u1" || inst.Version != "0.22.0" || !inst.Power {
		t.Fatalf("instance mismatch: %+v", inst)
	}

	// Importing twice must be idempotent.
	if _, err := ImportLegacy(ctx, repo, legacyPath); err != nil {
		t.Fatalf("second import: %v", err)
	}
	if n, _ := repo.CountInstancesByUID(ctx, "u1"); n != 1 {
		t.Fatalf("expected 1 instance after re-import, got %d", n)
	}
}
