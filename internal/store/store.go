// Package store is the pure-Go replacement for the `better-sqlite3` metadata
// database. It keeps PocketBase-compatible record ids, timestamps and JSON
// encodings so records can be exported/imported from a real PocketBase
// mothership database without loss.
package store

import (
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite" // CGO-free SQLite driver
)

// Open opens (and migrates) the metadata database at path.
func Open(path string) (*sql.DB, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("store: create dir: %w", err)
	}

	q := url.Values{}
	q.Set("_pragma", "busy_timeout(5000)")
	q.Set("_pragma", "journal_mode(WAL)")
	q.Set("_pragma", "foreign_keys(ON)")
	q.Set("_pragma", "synchronous(NORMAL)")

	dsn := "file:" + path + "?" + q.Encode()
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("store: open %s: %w", path, err)
	}

	// SQLite handles concurrency best with a single writer; WAL keeps readers
	// unblocked regardless.
	db.SetMaxOpenConns(8)
	db.SetMaxIdleConns(4)
	db.SetConnMaxLifetime(time.Hour)

	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("store: ping %s: %w", path, err)
	}
	if err := Migrate(db); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}
