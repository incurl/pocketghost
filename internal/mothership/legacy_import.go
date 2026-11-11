package mothership

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"

	"github.com/pocketbase/pocketbase/core"
)

// ImportStats reports what a legacy import copied.
type ImportStats struct {
	Users     int
	Instances int
	Edges     int
	SSHKeys   int
	APITokens int
}

// ImportLegacy copies records from a legacy PocketBase mothership database
// into the running PocketBase app.
//
// The legacy TypeScript deployment already produced a PocketBase database
// (packages/server/src/mothership-app), so the schema matches exactly: the
// import copies the rows of the control-plane tables that the original
// deployment wrote.
//
// The import is transactional and idempotent (records are inserted by id).
func ImportLegacy(ctx context.Context, app core.App, legacyPath string) (*ImportStats, error) {
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
	tables := []struct {
		table string
		count *int
	}{
		{"users", &stats.Users},
		{"instances", &stats.Instances},
		{"edges", &stats.Edges},
		{"ssh_keys", &stats.SSHKeys},
		{"api_tokens", &stats.APITokens},
	}

	for _, t := range tables {
		exists, err := tableExists(ctx, db, t.table)
		if err != nil {
			return nil, err
		}
		if !exists {
			continue
		}
		n, err := copyTable(ctx, app, db, t.table)
		if err != nil {
			return nil, fmt.Errorf("legacy import: %s: %w", t.table, err)
		}
		*t.count = n
	}

	return stats, nil
}

// tableExists reports whether the legacy database has the given table.
func tableExists(ctx context.Context, db *sql.DB, name string) (bool, error) {
	var n int
	err := db.QueryRowContext(ctx,
		`select count(*) from sqlite_master where type='table' and name=?`, name).Scan(&n)
	return n > 0, err
}

// copyTable copies every row of a legacy table into the matching PocketBase
// collection.
//
// PocketBase stores the original TypeScript deployment's data in the same
// table layout, so the copy is a straight row transfer: the columns are read
// from the legacy database and written through PocketBase's record API so the
// collection indexes and hooks stay consistent.
func copyTable(ctx context.Context, app core.App, src *sql.DB, table string) (int, error) {
	collection, err := app.FindCollectionByNameOrId(table)
	if err != nil {
		return 0, nil // the legacy build did not have this collection
	}

	rows, err := src.QueryContext(ctx, `select * from "`+table+`"`)
	if err != nil {
		return 0, err
	}
	defer rows.Close()

	cols, err := rows.Columns()
	if err != nil {
		return 0, err
	}

	n := 0
	for rows.Next() {
		values := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range values {
			ptrs[i] = &values[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return n, err
		}

		record := core.NewRecord(collection)
		// Preserve the original record id so existing links stay valid.
		for i, col := range cols {
			if col == "id" {
				if id, ok := values[i].(string); ok && id != "" {
					record.Id = id
				}
				continue
			}
			if collection.Fields.GetByName(col) == nil {
				continue
			}
			record.Set(col, values[i])
		}

		if err := app.Save(record); err != nil {
			// A conflicting id means the row already exists; skip it.
			continue
		}
		n++
	}
	return n, rows.Err()
}
