package store

import (
	"context"

	"github.com/incurl/pocketghost/internal/mothership"
)

// UpsertProcess writes the runtime state of a tenant process.
func (r *Repo) UpsertProcess(ctx context.Context, p *Process) error {
	return r.store.UpsertProcess(ctx, &mothership.Process{
		InstanceID: p.InstanceID,
		PID:        p.PID,
		Port:       p.Port,
		Version:    p.Version,
		BinPath:    p.BinPath,
		WorkDir:    p.WorkDir,
		Status:     p.Status,
		StartedAt:  p.StartedAt,
		StoppedAt:  p.StoppedAt,
		ExitCode:   p.ExitCode,
		Restarts:   p.Restarts,
		LastError:  p.LastError,
	})
}

// GetProcess loads the runtime row of an instance.
func (r *Repo) GetProcess(ctx context.Context, instanceID string) (*Process, error) {
	p, err := r.store.GetProcess(ctx, instanceID)
	if err != nil {
		return nil, err
	}
	return toProcess(p), nil
}

// ListProcesses returns every runtime row.
func (r *Repo) ListProcesses(ctx context.Context) ([]*Process, error) {
	list, err := r.store.ListProcesses(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]*Process, 0, len(list))
	for _, p := range list {
		out = append(out, toProcess(p))
	}
	return out, nil
}

func toProcess(p *mothership.Process) *Process {
	if p == nil {
		return nil
	}
	return &Process{
		InstanceID: p.InstanceID,
		PID:        p.PID,
		Port:       p.Port,
		Version:    p.Version,
		BinPath:    p.BinPath,
		WorkDir:    p.WorkDir,
		Status:     p.Status,
		StartedAt:  p.StartedAt,
		StoppedAt:  p.StoppedAt,
		ExitCode:   p.ExitCode,
		Restarts:   p.Restarts,
		LastError:  p.LastError,
	}
}

// MarkProcessStopped records a clean or crashed exit.
func (r *Repo) MarkProcessStopped(ctx context.Context, instanceID string, exitCode int, lastErr string) error {
	return r.store.MarkProcessStopped(ctx, instanceID, exitCode, lastErr)
}

// PortsInUse returns the ports currently recorded as allocated.
func (r *Repo) PortsInUse(ctx context.Context) (map[int]string, error) {
	return r.store.PortsInUse(ctx)
}
