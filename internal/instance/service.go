// Package instance is the Go port of InstanceService: it owns the lifecycle of
// tenant PocketBase processes, hibernation, health checks and status sync.
package instance

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/incurl/pocketghost/internal/config"
	"github.com/incurl/pocketghost/internal/logging"
	"github.com/incurl/pocketghost/internal/pbbin"
	"github.com/incurl/pocketghost/internal/procs"
	"github.com/incurl/pocketghost/internal/proxy"
	"github.com/incurl/pocketghost/internal/store"
)

// UserError marks an error whose message is safe to show to an end user.
type UserError struct {
	Msg    string
	Status int
}

func (e *UserError) Error() string { return e.Msg }

// UserErr builds a user-facing error.
func UserErr(msg string) error { return &UserError{Msg: msg, Status: 503} }

// IsUserError reports whether err is user-facing.
func IsUserError(err error) bool {
	var ue *UserError
	return errors.As(err, &ue)
}

// StatusCode returns the HTTP status an error should map to.
func StatusCode(err error) int {
	var ue *UserError
	if errors.As(err, &ue) {
		if ue.Status != 0 {
			return ue.Status
		}
		return 503
	}
	return 0
}

// ErrInstanceNotFound is returned when a host has no instance.
var ErrInstanceNotFound = fmt.Errorf("%w: instance not found", proxy.ErrNoTarget)

// Runtime is the live handle for one warmed instance.
type Runtime struct {
	InstanceID  string
	InternalURL string
	Port        int
	PID         int

	lastRequest  atomic.Int64 // unix nanos
	openRequests atomic.Int64
	lowering     atomic.Bool
	lowered      chan struct{}
	stopOnce     sync.Once
}

// NewRuntime builds a runtime handle.
func NewRuntime(id, url string, port, pid int) *Runtime {
	r := &Runtime{InstanceID: id, InternalURL: url, Port: port, PID: pid, lowered: make(chan struct{})}
	r.lastRequest.Store(time.Now().UnixNano())
	return r
}

// BeginRequest counts an open request and returns a release function.
func (r *Runtime) BeginRequest() func() {
	r.lastRequest.Store(time.Now().UnixNano())
	r.openRequests.Add(1)
	var once sync.Once
	return func() {
		once.Do(func() {
			r.openRequests.Add(-1)
			r.lastRequest.Store(time.Now().UnixNano())
		})
	}
}

// OpenRequests returns the number of in-flight requests.
func (r *Runtime) OpenRequests() int64 { return r.openRequests.Load() }

// IsLowering reports whether shutdown has begun.
func (r *Runtime) IsLowering() bool { return r.lowering.Load() }

// Lowered is closed once the process has been asked to stop.
func (r *Runtime) Lowered() <-chan struct{} { return r.lowered }

func (r *Runtime) markLowered() {
	r.stopOnce.Do(func() {
		r.lowering.Store(true)
		close(r.lowered)
	})
}

// Service owns all tenant runtimes.
type Service struct {
	cfg  *config.Config
	repo *store.Repo
	sup  *procs.Supervisor
	bins *pbbin.Service
	logs *logging.LogStore
	log  *logging.Logger

	mu      sync.Mutex
	entries map[string]*entry

	deployLocks sync.Map // instanceID -> *sync.Mutex

	ready atomic.Bool
}

type entry struct {
	mu     sync.Mutex
	rt     *Runtime
	err    error
	gen    int
	warmup chan struct{}
}

// New creates the instance service.
func New(cfg *config.Config, repo *store.Repo, sup *procs.Supervisor, bins *pbbin.Service,
	logs *logging.LogStore, logger *logging.Logger) *Service {
	s := &Service{
		cfg:     cfg,
		repo:    repo,
		sup:     sup,
		bins:    bins,
		logs:    logs,
		log:     logger.Create("InstanceService"),
		entries: map[string]*entry{},
	}
	return s
}

// SetReady flips the data-plane readiness flag.
func (s *Service) SetReady(v bool) { s.ready.Store(v) }

// Ready reports data-plane readiness.
func (s *Service) Ready() bool { return s.ready.Load() }

// Repo exposes the metadata repository.
func (s *Service) Repo() *store.Repo { return s.repo }

// Supervisor exposes the process supervisor.
func (s *Service) Supervisor() *procs.Supervisor { return s.sup }

func (s *Service) entryFor(id string) *entry {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.entries[id]
	if !ok {
		e = &entry{}
		s.entries[id] = e
	}
	return e
}

// EnsureRunning warms an instance and returns its runtime handle.
func (s *Service) EnsureRunning(ctx context.Context, inst *store.Instance) (*Runtime, error) {
	e := s.entryFor(inst.ID)

	e.mu.Lock()
	if e.rt != nil && !e.rt.IsLowering() && s.sup.IsRunning(inst.ID) {
		rt := e.rt
		e.mu.Unlock()
		return rt, nil
	}
	if e.warmup != nil {
		ch := e.warmup
		e.mu.Unlock()
		select {
		case <-ch:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		e.mu.Lock()
		rt, err := e.rt, e.err
		e.mu.Unlock()
		if rt != nil && err == nil {
			return rt, nil
		}
		return nil, err
	}
	e.warmup = make(chan struct{})
	e.gen++
	gen := e.gen
	e.mu.Unlock()

	rt, err := s.start(ctx, inst)

	e.mu.Lock()
	if e.gen == gen {
		e.rt, e.err = rt, err
		if e.warmup != nil {
			close(e.warmup)
		}
		e.warmup = nil
	}
	e.mu.Unlock()
	if err != nil {
		return nil, err
	}
	return rt, nil
}

func (s *Service) start(ctx context.Context, inst *store.Instance) (*Runtime, error) {
	log := s.log.Create(inst.Subdomain).Breadcrumb(inst.ID, inst.Version)

	// Plan / power / verification gates, in the same order as the original.
	owner, err := s.repo.GetUser(ctx, inst.UID)
	if err != nil {
		return nil, fmt.Errorf("owner lookup for %s: %w", inst.ID, err)
	}
	if owner.SubscriptionQuantity == 0 {
		return nil, &UserError{
			Msg:    fmt.Sprintf(`Instances will not run until you <a href=%s>upgrade</a>`, s.cfg.AppURL+"/access"),
			Status: 402,
		}
	}
	if !inst.Power {
		return nil, UserErr("This instance is powered off. See " + s.cfg.AppURL + "/docs/power for more information.")
	}
	if !owner.Verified {
		return nil, UserErr("Log in at " + s.cfg.AppURL + " to verify your account.")
	}
	if strings.TrimSpace(inst.Suspension) != "" || strings.TrimSpace(owner.Suspension) != "" {
		return nil, UserErr("This instance is suspended.")
	}

	version := orDefault(inst.Version, s.cfg.AllowedPocketBaseRange)
	binPath := s.cfg.InstanceBinaryOverride
	if binPath == "" {
		resolved, err := s.bins.Resolve(version)
		if err != nil {
			return nil, UserErr(fmt.Sprintf("No PocketBase version satisfying %s", inst.Version))
		}
		version = resolved
		binPath, err = s.bins.EnsureBinary(ctx, version)
		if err != nil {
			return nil, fmt.Errorf("binary for %s: %w", inst.ID, err)
		}
	}

	workDir := s.cfg.InstanceDataPath(inst.ID)
	if err := logging.EnsureInstanceDirStructure(workDir, log); err != nil {
		return nil, err
	}

	env := s.instanceEnv(inst, owner, log)

	// In a dev checkout the instance app migrations/hooks were bind-mounted into
	// the container; as native processes they are copied into the workdir.
	s.seedInstanceApp(workDir, log)

	proc, err := s.sup.EnsureRunning(ctx, procs.StartSpec{
		Instance:   inst,
		BinPath:    binPath,
		Version:    version,
		Env:        env,
		Dev:        inst.Dev,
		Encryption: env["PB_ENCRYPTION_KEY"] != "",
		WorkDir:    workDir,
		Limits: procs.Limits{
			MemoryMB:   s.cfg.InstanceMemoryMB,
			CPUPercent: s.cfg.InstanceCPUPercent,
			Nofile:     s.cfg.InstanceNofile,
			DiskMB:     s.cfg.InstanceDiskMB,
		},
	})
	if err != nil {
		return nil, err
	}

	if err := proc.HealthCheck(ctx, 45*time.Second); err != nil {
		_ = s.sup.Stop(context.Background(), inst.ID)
		return nil, fmt.Errorf("instance %s did not become healthy: %w", inst.ID, err)
	}

	rt := NewRuntime(inst.ID, proc.InternalURL(), proc.Port(), proc.PID())
	s.setStatus(ctx, inst.ID, store.StatusRunning)
	go s.hibernateWhenIdle(inst.ID, rt, idleTTL(inst, s.cfg))
	log.Infof("%s is running on port %d (pid %d)", proc.InternalURL(), proc.Port(), proc.PID())
	return rt, nil
}

// Resolve implements proxy.Resolver: it maps a request host to a running
// instance so the reverse proxy can forward traffic to it.
func (s *Service) Resolve(ctx context.Context, host string) (string, error) {
	if !s.ready.Load() {
		return "", proxy.ErrNotReady
	}
	inst, err := s.repo.GetInstanceByHost(ctx, host, s.cfg.ApexDomain)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return "", ErrInstanceNotFound
		}
		return "", err
	}
	rt, err := s.EnsureRunning(ctx, inst)
	if err != nil {
		return "", err
	}
	return rt.InternalURL, nil
}
