package store

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
)

// ImportStats reports what a legacy import copied.
type ImportStats struct {
	Users     int
	Instances int
	Edges     int
	SSHKeys   int
	APITokens int
	Versions  int
}

// ImportLegacy copies records from a PocketBase mothership database (produced by
// the TypeScript deployment) into the Go metadata database.
//
// PocketBase stores each collection in a table with the collection name, so the
// copy is a per-table, per-column intersection. Missing columns fall back to the
// schema defaults, which makes the importer work across PocketBase 0.17..0.4x.
// The import is idempotent: records are inserted or replaced by primary key.
func ImportLegacy(ctx context.Context, repo *Repo, legacyPath string) (*ImportStats, error) {
	if _, err := os.Stat(legacyPath); err != nil {
		return nil, fmt.Errorf("legacy import: %w", err)
	}

	q := url.Values{}
	q.Set("mode", "ro")
	q.Set("_pragma", "busy_timeout(5000)")
	db, err := sql.Open("sqlite", "file:"+legacyPath+"?"+q.Encode())
	if err != nil {
		return nil, fmt.Errorf("legacy import: open: %w", err)
	}
	defer db.Close()

	if err := db.PingContext(ctx); err != nil {
		return nil, fmt.Errorf("legacy import: ping: %w", err)
	}

	stats := &ImportStats{}
	tx, err := repo.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	tables := []struct {
		name  string
		dest  string
		count *int
	}{
		{"users", "users", &stats.Users},
		{"instances", "instances", &stats.Instances},
		{"edges", "edges", &stats.Edges},
		{"ssh_keys", "ssh_keys", &stats.SSHKeys},
		{"api_tokens", "api_tokens", &stats.APITokens},
		{"versions", "versions", &stats.Versions},
	}

	for _, t := range tables {
		exists, err := tableExists(ctx, db, t.name)
		if err != nil {
			return nil, err
		}
		if !exists {
			continue
		}
		n, err := copyTable(ctx, tx, db, t.name, t.dest)
		if err != nil {
			return nil, fmt.Errorf("legacy import: %s: %w", t.name, err)
		}
		*t.count = n
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return stats, nil
}

func tableExists(ctx context.Context, db *sql.DB, name string) (bool, error) {
	var n int
	err := db.QueryRowContext(ctx,
		`select count(*) from sqlite_master where type='table' and name=?`, name).Scan(&n)
	return n > 0, err
}

func tableColumns(ctx context.Context, q interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}, table string) ([]string, error) {
	rows, err := q.QueryContext(ctx, `pragma table_info(`+table+`)`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var cols []string
	for rows.Next() {
		var (
			cid     int
			name    string
			ctype   string
			notnull int
			dflt    sql.NullString
			pk      int
		)
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			return nil, err
		}
		cols = append(cols, name)
	}
	return cols, rows.Err()
}

func copyTable(ctx context.Context, tx *sql.Tx, src *sql.DB, srcTable, destTable string) (int, error) {
	srcCols, err := tableColumns(ctx, src, srcTable)
	if err != nil {
		return 0, err
	}
	destCols, err := tableColumns(ctx, tx, destTable)
	if err != nil {
		return 0, err
	}

	destSet := make(map[string]bool, len(destCols))
	for _, c := range destCols {
		destSet[c] = true
	}

	// PocketBase renamed `passwordHash` to `password` in 0.23 and `_admins` to
	// `_superusers`; normalise the column name rather than dropping the value.
	alias := func(c string) string {
		if c == "passwordHash" {
			return "password"
		}
		return c
	}

	var shared []string
	for _, c := range srcCols {
		a := alias(c)
		if destSet[a] {
			shared = append(shared, c)
		}
	}
	if len(shared) == 0 {
		return 0, nil
	}

	srcQuoted := make([]string, len(shared))
	destQuoted := make([]string, len(shared))
	placeholders := make([]string, len(shared))
	for i, c := range shared {
		srcQuoted[i] = `"` + c + `"`
		destQuoted[i] = `"` + alias(c) + `"`
		placeholders[i] = "?"
	}

	selectSQL := fmt.Sprintf(`select %s from "%s"`, joinComma(srcQuoted), srcTable)
	insertSQL := fmt.Sprintf(`insert or replace into "%s" (%s) values (%s)`,
		destTable, joinComma(destQuoted), joinComma(placeholders))

	rows, err := src.QueryContext(ctx, selectSQL)
	if err != nil {
		return 0, err
	}
	defer rows.Close()

	stmt, err := tx.PrepareContext(ctx, insertSQL)
	if err != nil {
		return 0, err
	}
	defer stmt.Close()

	n := 0
	for rows.Next() {
		vals := make([]any, len(shared))
		ptrs := make([]any, len(shared))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return n, err
		}
		if _, err := stmt.ExecContext(ctx, vals...); err != nil {
			return n, err
		}
		n++
	}
	return n, rows.Err()
}

func joinComma(parts []string) string {
	out := ""
	for i, p := range parts {
		if i > 0 {
			out += ", "
		}
		out += p
	}
	return out
}
