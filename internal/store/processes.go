package store

import (
	"context"
	"database/sql"
	"errors"
)

// UpsertProcess writes the durable runtime state for an instance.
func (r *Repo) UpsertProcess(ctx context.Context, p *Process) error {
	_, err := r.DB.ExecContext(ctx, `
insert into processes (instance_id, pid, port, version, bin_path, workdir, status,
 started_at, stopped_at, exit_code, restarts, last_error)
values (?,?,?,?,?,?,?,?,?,?,?,?)
on conflict(instance_id) do update set
  pid = excluded.pid,
  port = excluded.port,
  version = excluded.version,
  bin_path = excluded.bin_path,
  workdir = excluded.workdir,
  status = excluded.status,
  started_at = excluded.started_at,
  stopped_at = excluded.stopped_at,
  exit_code = excluded.exit_code,
  restarts = excluded.restarts,
  last_error = excluded.last_error`,
		p.InstanceID, p.PID, p.Port, p.Version, p.BinPath, p.WorkDir, p.Status,
		p.StartedAt, p.StoppedAt, p.ExitCode, p.Restarts, p.LastError,
	)
	return err
}

// GetProcess loads one runtime row.
func (r *Repo) GetProcess(ctx context.Context, instanceID string) (*Process, error) {
	var p Process
	err := r.DB.QueryRowContext(ctx, `
select instance_id, pid, port, version, bin_path, workdir, status,
       started_at, stopped_at, exit_code, restarts, last_error
from processes where instance_id = ?`, instanceID).Scan(
		&p.InstanceID, &p.PID, &p.Port, &p.Version, &p.BinPath, &p.WorkDir, &p.Status,
		&p.StartedAt, &p.StoppedAt, &p.ExitCode, &p.Restarts, &p.LastError,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &p, nil
}

// ListProcesses returns every runtime row (used by `pocketghost status` and by
// crash recovery at boot).
func (r *Repo) ListProcesses(ctx context.Context) ([]*Process, error) {
	rows, err := r.DB.QueryContext(ctx, `
select instance_id, pid, port, version, bin_path, workdir, status,
       started_at, stopped_at, exit_code, restarts, last_error
from processes`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Process
	for rows.Next() {
		var p Process
		if err := rows.Scan(
			&p.InstanceID, &p.PID, &p.Port, &p.Version, &p.BinPath, &p.WorkDir, &p.Status,
			&p.StartedAt, &p.StoppedAt, &p.ExitCode, &p.Restarts, &p.LastError,
		); err != nil {
			return nil, err
		}
		out = append(out, &p)
	}
	return out, rows.Err()
}

// MarkProcessStopped records a clean or crashed exit.
func (r *Repo) MarkProcessStopped(ctx context.Context, instanceID string, exitCode int, lastErr string) error {
	_, err := r.DB.ExecContext(ctx, `
update processes set status='stopped', pid=0, stopped_at=?, exit_code=?, last_error=?
where instance_id = ?`, NowString(), exitCode, lastErr, instanceID)
	return err
}

// DeleteProcess removes the runtime row.
func (r *Repo) DeleteProcess(ctx context.Context, instanceID string) error {
	_, err := r.DB.ExecContext(ctx, `delete from processes where instance_id = ?`, instanceID)
	return err
}

// PortsInUse returns the ports currently recorded as allocated.
func (r *Repo) PortsInUse(ctx context.Context) (map[int]string, error) {
	rows, err := r.DB.QueryContext(ctx, `select port, instance_id from processes where port > 0`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int]string{}
	for rows.Next() {
		var (
			port int
			id   string
		)
		if err := rows.Scan(&port, &id); err != nil {
			return nil, err
		}
		out[port] = id
	}
	return out, rows.Err()
}
