package instance

import (
	"context"
	"sync"
	"time"

	"github.com/incurl/pocketghost/internal/config"
	"github.com/incurl/pocketghost/internal/store"
)

// idleTTL resolves the instance's hibernation timeout.
func idleTTL(inst *store.Instance, cfg *config.Config) time.Duration {
	if inst.IdleTTL > 0 {
		return time.Duration(inst.IdleTTL) * time.Millisecond
	}
	if cfg.DaemonPBIdleTTL > 0 {
		return cfg.DaemonPBIdleTTL
	}
	return time.Hour
}

// setStatus writes the instance status back to the metadata database. A daemon
// that cannot reach the control plane keeps serving traffic; the status catches
// up on the next successful write.
func (s *Service) setStatus(ctx context.Context, id string, status store.InstanceStatus) {
	if err := s.repo.PatchInstanceFields(ctx, id, map[string]any{"status": string(status)}); err != nil {
		s.log.Debugf("could not persist status %s for %s: %v", status, id, err)
	}
}

// hibernateWhenIdle stops a process once it has had no open requests for ttl.
// The check runs every second, matching the legacy interval.
func (s *Service) hibernateWhenIdle(id string, rt *Runtime, ttl time.Duration) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-rt.Lowered():
			return
		case <-ticker.C:
			if rt.OpenRequests() > 0 {
				continue
			}
			last := time.Unix(0, rt.lastRequest.Load())
			if time.Since(last) < ttl {
				continue
			}
			s.log.Debugf("%s idle for %s, hibernating", id, ttl)
			s.logs.Writer(id, "exec").Info("Instance has been idle. Hibernating to conserve resources.")
			s.shutdownRuntime(id, rt)
			return
		}
	}
}

// shutdownRuntime stops the process behind a runtime and clears its entry.
func (s *Service) shutdownRuntime(id string, rt *Runtime) {
	rt.markLowered()
	ctx, cancel := context.WithTimeout(context.Background(), s.cfg.StopTimeout+15*time.Second)
	defer cancel()
	if err := s.sup.Stop(ctx, id); err != nil {
		s.log.Warnf("stop %s: %v", id, err)
	}
	s.setStatus(ctx, id, store.StatusIdle)

	s.mu.Lock()
	if e, ok := s.entries[id]; ok && e.rt == rt {
		e.rt = nil
		e.err = nil
	}
	s.mu.Unlock()
}

// Stop powers down an instance (used by the API and by power-off reconciliation).
func (s *Service) Stop(ctx context.Context, id string) error {
	s.mu.Lock()
	e := s.entries[id]
	var rt *Runtime
	if e != nil {
		rt = e.rt
	}
	s.mu.Unlock()

	if rt != nil {
		s.shutdownRuntime(id, rt)
		return nil
	}
	if err := s.sup.Stop(ctx, id); err != nil {
		return err
	}
	s.setStatus(ctx, id, store.StatusIdle)
	return nil
}

// Restart stops and then warms an instance again.
func (s *Service) Restart(ctx context.Context, inst *store.Instance) (*Runtime, error) {
	if err := s.Stop(ctx, inst.ID); err != nil {
		return nil, err
	}
	return s.EnsureRunning(ctx, inst)
}

// deployMutex returns the per-instance deploy mutex.
func (s *Service) deployMutex(id string) *sync.Mutex {
	m, _ := s.deployLocks.LoadOrStore(id, &sync.Mutex{})
	return m.(*sync.Mutex)
}

// WithInstanceStopped stops the instance, runs fn, then releases the deploy lock.
// This is the Go equivalent of the legacy `withInstanceStopped` helper used by
// the deploy API to swap files while PocketBase is down.
func (s *Service) WithInstanceStopped(ctx context.Context, id string, fn func() error) (wasRunning bool, err error) {
	mu := s.deployMutex(id)
	mu.Lock()
	defer mu.Unlock()

	s.mu.Lock()
	e := s.entries[id]
	wasRunning = e != nil && e.rt != nil && !e.rt.IsLowering()
	s.mu.Unlock()

	if wasRunning {
		if stopErr := s.Stop(ctx, id); stopErr != nil {
			return wasRunning, stopErr
		}
	}
	err = fn()

	if wasRunning {
		inst, getErr := s.repo.GetInstance(ctx, id)
		if getErr == nil {
			if _, warmErr := s.EnsureRunning(ctx, inst); warmErr != nil && err == nil {
				err = warmErr
			}
		}
	}
	return wasRunning, err
}

// ReconcilePoweredOff stops processes whose instances were powered off in the
// control plane (the mirror's `handlePowerOff` path).
func (s *Service) ReconcilePoweredOff(ctx context.Context) {
	instances, err := s.repo.ListInstances(ctx, "")
	if err != nil {
		s.log.Warnf("power-off reconciliation failed: %v", err)
		return
	}
	for _, inst := range instances {
		if inst.Power {
			continue
		}
		if s.sup.IsRunning(inst.ID) {
			s.log.Infof("instance %s powered off, stopping process", inst.ID)
			if err := s.Stop(ctx, inst.ID); err != nil {
				s.log.Warnf("stop powered-off %s: %v", inst.ID, err)
			}
		}
	}
}
