package store

import (
	"context"
	"database/sql"
	"errors"
)

const edgeColumns = `id, created, updated, edge_id, region, status, internal_url, last_seen, version`

func scanEdge(sc interface{ Scan(...any) error }) (*Edge, error) {
	var e Edge
	if err := sc.Scan(&e.ID, &e.Created, &e.Updated, &e.EdgeID, &e.Region, &e.Status,
		&e.InternalURL, &e.LastSeen, &e.Version); err != nil {
		return nil, err
	}
	return &e, nil
}

// UpsertEdge inserts or updates an edge by its stable `edge_id`.
func (r *Repo) UpsertEdge(ctx context.Context, e *Edge) error {
	if e.ID == "" {
		e.ID = NewID()
	}
	now := NowString()
	if e.Created == "" {
		e.Created = now
	}
	e.Updated = now
	_, err := r.DB.ExecContext(ctx, `
insert into edges (`+edgeColumns+`) values (?,?,?,?,?,?,?,?,?)
on conflict(edge_id) do update set
  updated = excluded.updated,
  region = excluded.region,
  status = excluded.status,
  internal_url = excluded.internal_url,
  last_seen = excluded.last_seen,
  version = excluded.version`,
		e.ID, e.Created, e.Updated, e.EdgeID, e.Region, e.Status, e.InternalURL, e.LastSeen, e.Version)
	return err
}

// GetEdgeByEdgeID loads an edge by its fleet-wide identifier.
func (r *Repo) GetEdgeByEdgeID(ctx context.Context, edgeID string) (*Edge, error) {
	e, err := scanEdge(r.DB.QueryRowContext(ctx, `select `+edgeColumns+` from edges where edge_id = ?`, edgeID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return e, err
}

// GetEdge loads an edge by record id.
func (r *Repo) GetEdge(ctx context.Context, id string) (*Edge, error) {
	e, err := scanEdge(r.DB.QueryRowContext(ctx, `select `+edgeColumns+` from edges where id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return e, err
}

// ListEdges returns all known edges.
func (r *Repo) ListEdges(ctx context.Context) ([]*Edge, error) {
	rows, err := r.DB.QueryContext(ctx, `select `+edgeColumns+` from edges order by region, edge_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Edge
	for rows.Next() {
		e, err := scanEdge(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// CountInstancesOnEdge counts instances assigned to an edge (for placement).
func (r *Repo) CountInstancesOnEdge(ctx context.Context, edgeRecordID string) (int, error) {
	var n int
	err := r.DB.QueryRowContext(ctx, `select count(*) from instances where edge = ?`, edgeRecordID).Scan(&n)
	return n, err
}

// ListVersions returns the known PocketBase versions, newest first.
func (r *Repo) ListVersions(ctx context.Context) ([]*Version, error) {
	rows, err := r.DB.QueryContext(ctx,
		`select id, created, updated, version, notes, platform, sha256 from versions order by created desc`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Version
	for rows.Next() {
		var v Version
		if err := rows.Scan(&v.ID, &v.Created, &v.Updated, &v.Version, &v.Notes, &v.Platform, &v.SHA256); err != nil {
			return nil, err
		}
		out = append(out, &v)
	}
	return out, rows.Err()
}

// UpsertVersion records a downloadable PocketBase version.
func (r *Repo) UpsertVersion(ctx context.Context, v *Version) error {
	if v.ID == "" {
		v.ID = NewID()
	}
	now := NowString()
	if v.Created == "" {
		v.Created = now
	}
	v.Updated = now
	_, err := r.DB.ExecContext(ctx, `
insert into versions (id, created, updated, version, notes, platform, sha256)
values (?,?,?,?,?,?,?)
on conflict(version) do update set updated=excluded.updated, notes=excluded.notes,
  platform=excluded.platform, sha256=excluded.sha256`,
		v.ID, v.Created, v.Updated, v.Version, v.Notes, v.Platform, v.SHA256)
	return err
}
