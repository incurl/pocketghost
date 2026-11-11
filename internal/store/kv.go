package store

import (
	"context"
	"database/sql"
	"errors"
)

// KVGet reads a key/value pair from the internal settings table.
func (r *Repo) KVGet(key string) (string, bool, error) {
	return r.KVGetContext(context.Background(), key)
}

// KVGetContext is KVGet with an explicit context.
func (r *Repo) KVGetContext(ctx context.Context, key string) (string, bool, error) {
	var v string
	err := r.DB.QueryRowContext(ctx, `select value from kv where key = ?`, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return v, true, nil
}

// KVSet writes a key/value pair.
func (r *Repo) KVSet(key, value string) error {
	return r.KVSetContext(context.Background(), key, value)
}

// KVSetContext is KVSet with an explicit context.
func (r *Repo) KVSetContext(ctx context.Context, key, value string) error {
	_, err := r.DB.ExecContext(ctx, `
insert into kv (key, value, updated) values (?,?,?)
on conflict(key) do update set value = excluded.value, updated = excluded.updated`,
		key, value, NowString())
	return err
}

// Audit appends an audit log entry (the legacy `mkAudit` helper).
func (r *Repo) Audit(ctx context.Context, uid, action, detail string) error {
	_, err := r.DB.ExecContext(ctx, `
insert into audit_logs (id, created, updated, uid, action, detail) values (?,?,?,?,?,?)`,
		NewID(), NowString(), NowString(), uid, action, detail)
	return err
}
