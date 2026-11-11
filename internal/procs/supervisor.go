package procs

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/incurl/pocketghost/internal/logging"
	"github.com/incurl/pocketghost/internal/store"
)

// StartSpec describes how to launch one tenant instance.
type StartSpec struct {
	Instance   *store.Instance
	BinPath    string
	Version    string
	Env        map[string]string
	Dev        bool
	Encryption bool
	WorkDir    string
	Limits     Limits
}

// RestartPolicy mirrors the retry/cooldown semantics the container supervisor had.
type RestartPolicy struct {
	MaxRestarts int
	Backoff     time.Duration
	MaxBackoff  time.Duration
	Cooldown    time.Duration
}

// DefaultRestartPolicy is used when no policy is configured.
func DefaultRestartPolicy() RestartPolicy {
	return RestartPolicy{MaxRestarts: 5, Backoff: 500 * time.Millisecond, MaxBackoff: 30 * time.Second, Cooldown: 10 * time.Minute}
}

// Supervisor owns every tenant process on this node. It is the Go replacement
// for `dockerode` container bookkeeping.
type Supervisor struct {
	ports  *PortAllocator
	logs   *logging.LogStore
	repo   *store.Repo
	logger *logging.Logger
	policy RestartPolicy
	stopTO time.Duration

	mu       sync.Mutex
	procs    map[string]*Process
	specs    map[string]StartSpec
	desired  map[string]bool
	stopping map[string]bool
	restarts map[string]int
	lastExit map[string]Exit

	launchSem chan struct{}
}

// NewSupervisor builds a supervisor. maxConcurrent bounds simultaneous launches
// (the replacement for the `bottleneck` limiter around Docker spawns).
func NewSupervisor(ports *PortAllocator, logs *logging.LogStore, repo *store.Repo,
	logger *logging.Logger, maxConcurrent int, stopTimeout time.Duration, policy RestartPolicy) *Supervisor {
	if maxConcurrent <= 0 {
		maxConcurrent = 5
	}
	if stopTimeout <= 0 {
		stopTimeout = 5 * time.Second
	}
	return &Supervisor{
		ports:     ports,
		logs:      logs,
		repo:      repo,
		logger:    logger,
		policy:    policy,
		stopTO:    stopTimeout,
		procs:     map[string]*Process{},
		specs:     map[string]StartSpec{},
		desired:   map[string]bool{},
		stopping:  map[string]bool{},
		restarts:  map[string]int{},
		lastExit:  map[string]Exit{},
		launchSem: make(chan struct{}, maxConcurrent),
	}
}

// Get returns the live process for an instance, if any.
func (s *Supervisor) Get(instanceID string) (*Process, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.procs[instanceID]
	if !ok || !p.Running() {
		return nil, false
	}
	return p, true
}

// IsRunning reports whether the instance has a live process.
func (s *Supervisor) IsRunning(instanceID string) bool {
	_, ok := s.Get(instanceID)
	return ok
}

// EnsureRunning launches the instance if it is not already live, then waits for
// the process to become healthy.
func (s *Supervisor) EnsureRunning(ctx context.Context, spec StartSpec) (*Process, error) {
	id := spec.Instance.ID

	s.mu.Lock()
	if p, ok := s.procs[id]; ok && p.Running() {
		s.desired[id] = true
		s.mu.Unlock()
		return p, nil
	}
	s.mu.Unlock()

	// Bound concurrent launches, like the Bottleneck limiter did for Docker.
	select {
	case s.launchSem <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	defer func() { <-s.launchSem }()

	s.mu.Lock()
	if p, ok := s.procs[id]; ok && p.Running() {
		s.desired[id] = true
		s.mu.Unlock()
		return p, nil
	}
	s.mu.Unlock()

	port, err := s.ports.Allocate(id)
	if err != nil {
		return nil, err
	}

	workDir := spec.WorkDir
	if workDir == "" {
		return nil, fmt.Errorf("procs: instance %s has no workdir", id)
	}

	env := map[string]string{}
	for k, v := range spec.Env {
		env[k] = v
	}
	encryption := spec.Encryption
	if env["PB_ENCRYPTION_KEY"] != "" {
		encryption = true
	}

	proc, err := Spawn(ctx, Options{
		InstanceID:  id,
		Subdomain:   spec.Instance.Subdomain,
		Version:     spec.Version,
		BinPath:     spec.BinPath,
		WorkDir:     workDir,
		Port:        port,
		Args:        BuildServeArgs(port, spec.Dev, encryption),
		Env:         env,
		Logs:        s.logs,
		Logger:      s.logger.Breadcrumb(spec.Instance.Subdomain, id),
		StopTimeout: s.stopTO,
		Limits:      spec.Limits,
	})
	if err != nil {
		s.ports.Release(port)
		s.persist(ctx, &store.Process{InstanceID: id, Status: "failed", LastError: err.Error(), Version: spec.Version, BinPath: spec.BinPath, WorkDir: workDir})
		return nil, err
	}

	s.mu.Lock()
	s.procs[id] = proc
	s.specs[id] = spec
	s.desired[id] = true
	delete(s.stopping, id)
	s.mu.Unlock()

	s.persist(ctx, &store.Process{
		InstanceID: id, PID: proc.PID(), Port: port, Version: spec.Version,
		BinPath: spec.BinPath, WorkDir: workDir, Status: "running",
		StartedAt: store.FormatTime(proc.StartedAt()),
	})

	go s.watch(proc)
	return proc, nil
}

// watch handles crash detection and bounded auto-restart.
func (s *Supervisor) watch(p *Process) {
	id := p.InstanceID()
	exit, _ := p.Wait(context.Background())

	s.mu.Lock()
	if s.procs[id] == p {
		delete(s.procs, id)
	}
	s.lastExit[id] = exit
	wasStopping := s.stopping[id]
	desired := s.desired[id]
	spec, hasSpec := s.specs[id]
	s.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if wasStopping || !desired {
		s.ports.Release(p.Port())
		s.persist(ctx, &store.Process{
			InstanceID: id, Port: 0, Version: spec.Version, Status: "stopped",
			StoppedAt: store.NowString(), ExitCode: exit.Code,
		})
		s.logger.Infof("%s stopped (code %d)", id, exit.Code)
		return
	}

	s.logger.Warnf("%s exited unexpectedly with code %d (err=%v)", id, exit.Code, exit.Err)

	s.mu.Lock()
	attempts := s.restarts[id]
	s.restarts[id] = attempts + 1
	s.mu.Unlock()

	if !hasSpec || attempts >= s.policy.MaxRestarts {
		s.ports.Release(p.Port())
		s.persist(ctx, &store.Process{
			InstanceID: id, Port: 0, Version: spec.Version, Status: "failed",
			StoppedAt: store.NowString(), ExitCode: exit.Code, Restarts: attempts + 1,
			LastError: fmt.Sprintf("exited with code %d", exit.Code),
		})
		s.logger.Errorf("%s exceeded restart budget (%d), giving up", id, s.policy.MaxRestarts)
		return
	}

	backoff := s.policy.Backoff
	for i := 0; i < attempts; i++ {
		backoff *= 2
		if backoff >= s.policy.MaxBackoff {
			backoff = s.policy.MaxBackoff
			break
		}
	}
	s.logger.Infof("%s restarting in %s (attempt %d/%d)", id, backoff, attempts+1, s.policy.MaxRestarts)

	select {
	case <-time.After(backoff):
	case <-ctx.Done():
		return
	}

	s.mu.Lock()
	stillDesired := s.desired[id]
	s.mu.Unlock()
	if !stillDesired {
		return
	}

	if _, err := s.EnsureRunning(ctx, spec); err != nil {
		s.logger.Errorf("%s restart failed: %v", id, err)
		if s.repo != nil {
			_ = s.repo.MarkProcessStopped(ctx, id, exit.Code, err.Error())
		}
		return
	}
	s.logger.Infof("%s restarted", id)
}

// Stop gracefully stops an instance. It is idempotent and safe to call for
// instances that are not running.
func (s *Supervisor) Stop(ctx context.Context, instanceID string) error {
	s.mu.Lock()
	s.desired[instanceID] = false
	s.stopping[instanceID] = true
	p, ok := s.procs[instanceID]
	delete(s.specs, instanceID)
	s.mu.Unlock()

	if !ok {
		if s.repo != nil {
			_ = s.repo.MarkProcessStopped(ctx, instanceID, 0, "")
		}
		return nil
	}

	err := p.Stop(ctx)
	s.ports.Release(p.Port())

	s.mu.Lock()
	if s.procs[instanceID] == p {
		delete(s.procs, instanceID)
	}
	delete(s.stopping, instanceID)
	delete(s.restarts, instanceID)
	s.mu.Unlock()

	if s.repo != nil {
		_ = s.repo.MarkProcessStopped(ctx, instanceID, 0, "")
	}
	return err
}

// StopAll gracefully stops every managed process (graceful daemon shutdown).
func (s *Supervisor) StopAll(ctx context.Context) error {
	s.mu.Lock()
	ids := make([]string, 0, len(s.procs))
	for id := range s.procs {
		ids = append(ids, id)
	}
	s.mu.Unlock()

	var wg sync.WaitGroup
	errCh := make(chan error, len(ids))
	for _, id := range ids {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			if err := s.Stop(ctx, id); err != nil {
				errCh <- fmt.Errorf("%s: %w", id, err)
			}
		}(id)
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		return err
	}
	return nil
}

// Snapshot returns the runtime state of every managed process.
func (s *Supervisor) Snapshot() []*store.Process {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*store.Process, 0, len(s.procs))
	for id, p := range s.procs {
		out = append(out, &store.Process{
			InstanceID: id,
			PID:        p.PID(),
			Port:       p.Port(),
			Version:    s.specs[id].Version,
			WorkDir:    s.specs[id].WorkDir,
			Status:     "running",
			StartedAt:  store.FormatTime(p.StartedAt()),
			Restarts:   s.restarts[id],
		})
	}
	return out
}

// Reconcile re-adopts or cleans up processes recorded in the database at boot.
//
// A PID that is still alive and still owns its allocated port is re-adopted (it
// survived a control-plane restart). Anything else is marked stopped so the next
// request launches a fresh process.
func (s *Supervisor) Reconcile(ctx context.Context) error {
	if s.repo == nil {
		return nil
	}
	rows, err := s.repo.ListProcesses(ctx)
	if err != nil {
		return err
	}
	ports := map[int]string{}
	for _, row := range rows {
		if row.Status == "running" && row.Port > 0 && PidAlive(row.PID) && portOwnedBy(row.Port, row.PID) {
			s.logger.Infof("re-adopting %s pid=%d port=%d", row.InstanceID, row.PID, row.Port)
			ports[row.Port] = row.InstanceID
			continue
		}
		if row.Status == "running" {
			s.logger.Infof("reaping stale runtime row for %s (pid %d not alive)", row.InstanceID, row.PID)
			_ = s.repo.MarkProcessStopped(ctx, row.InstanceID, 0, "stale after restart")
		}
	}
	s.ports.Seed(ports)
	return nil
}

func (s *Supervisor) persist(ctx context.Context, p *store.Process) {
	if s.repo == nil {
		return
	}
	if err := s.repo.UpsertProcess(ctx, p); err != nil {
		s.logger.Warnf("persist runtime state for %s: %v", p.InstanceID, err)
	}
}
