package procs

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/incurl/pocketghost/internal/logging"
)

// ErrStopped is returned when an operation targets a process that has exited.
var ErrStopped = errors.New("procs: process stopped")

// Limits are the OS-level resource caps applied to a tenant process. They are
// the native replacement for Docker's container memory/cpu/pids limits.
type Limits struct {
	MemoryMB   int64
	CPUPercent int
	Nofile     uint64
	DiskMB     int64
}

// Options configures one PocketBase process.
type Options struct {
	InstanceID  string
	Subdomain   string
	Version     string
	BinPath     string
	WorkDir     string
	Port        int
	Args        []string
	Env         map[string]string
	Logs        *logging.LogStore
	Logger      *logging.Logger
	StopTimeout time.Duration
	Limits      Limits
	// LogTarget names the instance log file (legacy default: "exec").
	LogTarget string
}

// Exit describes a process exit.
type Exit struct {
	Code   int
	Err    error
	At     time.Time
	Signal string
}

// Process is a running native PocketBase instance.
type Process struct {
	opts    Options
	cmd     *exec.Cmd
	pid     int
	started time.Time

	writer *logging.LogWriter

	exitOnce sync.Once
	exitCh   chan Exit
	exited   atomic.Bool
	stopOnce sync.Once
}

// PID returns the OS process id.
func (p *Process) PID() int { return p.pid }

// Port returns the allocated loopback port.
func (p *Process) Port() int { return p.opts.Port }

// InstanceID returns the owning instance.
func (p *Process) InstanceID() string { return p.opts.InstanceID }

// InternalURL is the loopback base URL the reverse proxy targets.
func (p *Process) InternalURL() string {
	return "http://127.0.0.1:" + strconv.Itoa(p.opts.Port)
}

// StartedAt returns the spawn time.
func (p *Process) StartedAt() time.Time { return p.started }

// Running reports whether the process is still alive.
func (p *Process) Running() bool { return !p.exited.Load() }

// Exited exposes the exit notification channel (single receive).
func (p *Process) Exited() <-chan Exit { return p.exitCh }

func (p *Process) pump(r interface{ Read([]byte) (int, error) }, stream string) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		line := sc.Text()
		if p.writer != nil {
			if stream == "stderr" {
				p.writer.Error(line)
			} else {
				p.writer.Info(line)
			}
		}
		if p.opts.Logger.Enabled(logging.LevelDebug) {
			p.opts.Logger.Debugf("[%s %s] %s", p.opts.InstanceID, stream, line)
		}
	}
}

func (p *Process) wait() {
	err := p.cmd.Wait()
	p.exited.Store(true)
	removePIDFile(p.opts.WorkDir)

	exit := Exit{At: time.Now(), Err: err}
	if p.cmd.ProcessState != nil {
		exit.Code = p.cmd.ProcessState.ExitCode()
		if ws, ok := p.cmd.ProcessState.Sys().(interface{ Signaled() bool }); ok && ws.Signaled() {
			exit.Signal = "signaled"
		}
	}
	if err != nil && exit.Code == 0 {
		exit.Code = 1
	}
	p.exitOnce.Do(func() { p.exitCh <- exit })
}

// Spawn starts a PocketBase process.
func Spawn(ctx context.Context, opts Options) (*Process, error) {
	if opts.BinPath == "" {
		return nil, fmt.Errorf("procs: empty binary path for %s", opts.InstanceID)
	}
	if st, err := os.Stat(opts.BinPath); err != nil {
		return nil, fmt.Errorf("procs: binary %s: %w", opts.BinPath, err)
	} else if st.IsDir() {
		return nil, fmt.Errorf("procs: binary %s is a directory", opts.BinPath)
	}
	if err := os.MkdirAll(opts.WorkDir, 0o755); err != nil {
		return nil, fmt.Errorf("procs: workdir: %w", err)
	}

	if opts.StopTimeout <= 0 {
		opts.StopTimeout = 5 * time.Second
	}
	if opts.LogTarget == "" {
		opts.LogTarget = "exec"
	}
	if opts.Logger == nil {
		opts.Logger = logging.Discard()
	}

	args := opts.Args
	if len(args) == 0 {
		args = BuildServeArgs(opts.Port, false, false)
	}

	cmd := exec.Command(opts.BinPath, args...)
	cmd.Dir = opts.WorkDir
	cmd.Env = buildEnv(opts)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("procs: stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("procs: stderr pipe: %w", err)
	}

	applySysProcAttr(cmd)

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("procs: start %s: %w", opts.BinPath, err)
	}

	p := &Process{
		opts:    opts,
		cmd:     cmd,
		pid:     cmd.Process.Pid,
		started: time.Now(),
		exitCh:  make(chan Exit, 1),
	}
	if opts.Logs != nil {
		p.writer = opts.Logs.Writer(opts.InstanceID, opts.LogTarget)
	}

	applyRuntimeLimits(p.pid, opts.Limits, opts.Logger)
	writePIDFile(opts.WorkDir, p.pid)

	go p.pump(stdout, "stdout")
	go p.pump(stderr, "stderr")
	go p.wait()

	opts.Logger.Infof("started %s pid=%d port=%d version=%s", opts.InstanceID, p.pid, opts.Port, opts.Version)
	return p, nil
}

// Wait blocks until exit or context cancellation.
func (p *Process) Wait(ctx context.Context) (Exit, error) {
	select {
	case e := <-p.exitCh:
		// Re-arm so multiple waiters are not starved.
		p.exitCh <- e
		return e, nil
	case <-ctx.Done():
		return Exit{}, ctx.Err()
	}
}

// Stop performs a graceful shutdown: SIGINT, wait up to StopTimeout, then SIGKILL.
// This replaces `container.stop({signal:'SIGINT', t})` + `container.kill()`.
func (p *Process) Stop(ctx context.Context) error {
	var err error
	p.stopOnce.Do(func() {
		if p.exited.Load() {
			return
		}
		p.opts.Logger.Infof("%s: sending SIGINT (graceful)", p.opts.InstanceID)
		if p.writer != nil {
			p.writer.Info("Stopping instance")
		}
		if e := signalGraceful(p.cmd); e != nil {
			p.opts.Logger.Warnf("%s: graceful signal failed: %v", p.opts.InstanceID, e)
		}

		deadline := time.After(p.opts.StopTimeout)
		select {
		case <-p.exitCh:
			p.exitCh <- Exit{Code: 0}
		case <-deadline:
			p.opts.Logger.Warnf("%s: did not stop within %s, killing", p.opts.InstanceID, p.opts.StopTimeout)
			err = signalForce(p.cmd)
		case <-ctx.Done():
			err = signalForce(p.cmd)
		}
		if err != nil && p.writer != nil {
			p.writer.Error("Failed to stop instance cleanly: " + err.Error())
		}
	})
	return err
}

// Kill force-terminates the process.
func (p *Process) Kill() error {
	if p.exited.Load() {
		return nil
	}
	return signalForce(p.cmd)
}

// HealthCheck probes `GET /api/health` until it succeeds or the deadline passes.
// This replaces the Docker container HEALTHCHECK + inspect polling.
func (p *Process) HealthCheck(ctx context.Context, timeout time.Duration) error {
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	deadline := time.Now().Add(timeout)
	client := &http.Client{Timeout: 2 * time.Second}
	url := p.InternalURL() + "/api/health"

	var lastErr error
	for time.Now().Before(deadline) {
		if !p.Running() {
			return ErrStopped
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		resp, err := client.Get(url)
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
			lastErr = fmt.Errorf("health status %d", resp.StatusCode)
		} else {
			lastErr = err
		}
		time.Sleep(150 * time.Millisecond)
	}
	if lastErr == nil {
		lastErr = errors.New("timeout")
	}
	return fmt.Errorf("procs: %s health check failed: %w", p.opts.InstanceID, lastErr)
}

// BuildServeArgs mirrors the TypeScript `buildServeArgs`: only a fixed allowlist
// of flags is ever added, so a tenant can never widen its own isolation.
func BuildServeArgs(port int, dev bool, encryption bool) []string {
	args := []string{"serve", "--http", "127.0.0.1:" + strconv.Itoa(port)}
	if dev {
		args = append(args, "--dev")
	}
	if encryption {
		args = append(args, "--encryptionEnv=PB_ENCRYPTION_KEY")
	}
	return args
}

func buildEnv(opts Options) []string {
	env := map[string]string{}
	for _, kv := range os.Environ() {
		k, v, ok := strings.Cut(kv, "=")
		if !ok {
			continue
		}
		// Never leak control-plane credentials into tenant processes.
		switch k {
		case "MOTHERSHIP_ADMIN_USERNAME", "MOTHERSHIP_ADMIN_PASSWORD", "PH_SECRET",
			"LS_API_KEY", "LS_WEBHOOK_SECRET", "DISCORD_ALERT_CHANNEL_URL":
			continue
		}
		env[k] = v
	}
	for k, v := range opts.Env {
		env[k] = v
	}
	// Go-runtime level quotas replace container CPU/memory limits.
	goRuntimeEnv(opts.Limits, env)
	env["PH_INSTANCE_ID"] = opts.InstanceID
	env["PH_INSTANCE_SUBDOMAIN"] = opts.Subdomain

	out := make([]string, 0, len(env))
	for k, v := range env {
		out = append(out, k+"="+v)
	}
	return out
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

func writePIDFile(dir string, pid int) {
	_ = os.WriteFile(filepath.Join(dir, "pocketbase.pid"), []byte(strconv.Itoa(pid)), 0o644)
}

func removePIDFile(dir string) {
	_ = os.Remove(filepath.Join(dir, "pocketbase.pid"))
}
