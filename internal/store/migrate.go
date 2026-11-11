package store

import (
	"database/sql"
	"fmt"
	"strings"
)

// Migrate applies the schema idempotently and records the migration name.
func Migrate(db *sql.DB) error {
	stmts := []string{schemaUsers, schemaRest, schemaIndexes}
	for _, s := range stmts {
		for _, stmt := range splitStatements(s) {
			if _, err := db.Exec(stmt); err != nil {
				return fmt.Errorf("store: migrate: %w\nstatement: %s", err, stmt)
			}
		}
	}
	_, err := db.Exec(`insert or ignore into schema_migrations(name, applied_at) values(?, datetime('now'))`, "001_initial")
	if err != nil {
		return fmt.Errorf("store: record migration: %w", err)
	}
	return nil
}

// schemaIndexes holds extra indexes added after the initial schema, so future
// versions can append here without rewriting the table definitions.
const schemaIndexes = `
create index if not exists idx_instances_edge on instances(edge);
create index if not exists idx_deploys_instance on deploys(instance);
create index if not exists idx_api_tokens_token on api_tokens(token);
create index if not exists idx_ssh_keys_fingerprint on ssh_keys(fingerprint);
`

// splitStatements splits a multi-statement SQL blob on semicolons at line ends.
// The schema contains no triggers or string literals with semicolons, so this is
// safe and keeps the constants readable.
func splitStatements(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ";") {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, p)
		}
	}
	return out
}
