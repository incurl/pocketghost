package store

import (
	"context"
	"database/sql"
	"errors"
)

// ListSSHKeys returns the SSH keys registered to a user.
func (r *Repo) ListSSHKeys(ctx context.Context, uid string) ([]*SSHKey, error) {
	rows, err := r.DB.QueryContext(ctx,
		`select id, created, updated, uid, name, key, fingerprint from ssh_keys where uid = ? order by created`, uid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*SSHKey
	for rows.Next() {
		var k SSHKey
		if err := rows.Scan(&k.ID, &k.Created, &k.Updated, &k.UID, &k.Name, &k.Key, &k.Fingerprint); err != nil {
			return nil, err
		}
		out = append(out, &k)
	}
	return out, rows.Err()
}

// CreateSSHKey registers a new SSH key for a user.
func (r *Repo) CreateSSHKey(ctx context.Context, k *SSHKey) error {
	if k.ID == "" {
		k.ID = NewID()
	}
	now := NowString()
	if k.Created == "" {
		k.Created = now
	}
	k.Updated = now
	_, err := r.DB.ExecContext(ctx, `
insert into ssh_keys (id, created, updated, uid, name, key, fingerprint) values (?,?,?,?,?,?,?)`,
		k.ID, k.Created, k.Updated, k.UID, k.Name, k.Key, k.Fingerprint)
	return err
}

// DeleteSSHKey removes a key owned by uid.
func (r *Repo) DeleteSSHKey(ctx context.Context, uid, id string) error {
	res, err := r.DB.ExecContext(ctx, `delete from ssh_keys where id = ? and uid = ?`, id, uid)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// FindSSHKeyByFingerprint looks up a key by SSH fingerprint.
func (r *Repo) FindSSHKeyByFingerprint(ctx context.Context, fingerprint string) (*SSHKey, error) {
	var k SSHKey
	err := r.DB.QueryRowContext(ctx,
		`select id, created, updated, uid, name, key, fingerprint from ssh_keys where fingerprint = ?`,
		fingerprint).Scan(&k.ID, &k.Created, &k.Updated, &k.UID, &k.Name, &k.Key, &k.Fingerprint)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &k, nil
}
