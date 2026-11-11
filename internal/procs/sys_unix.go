//go:build unix

package procs

import (
	"os"
	"os/exec"
	"runtime"
	"syscall"

	"golang.org/x/sys/unix"

	"github.com/incurl/pocketghost/internal/logging"
)

// applySysProcAttr puts every tenant process in its own process group so a stop
// signal reaches PocketBase and any helper it spawned, and so the control plane
// can kill the whole group if graceful shutdown stalls.
func applySysProcAttr(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func signalGraceful(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	// Negative pid targets the process group.
	if err := unix.Kill(-cmd.Process.Pid, unix.SIGINT); err == nil {
		return nil
	}
	return cmd.Process.Signal(os.Interrupt)
}

func signalForce(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	if err := unix.Kill(-cmd.Process.Pid, unix.SIGKILL); err == nil {
		return nil
	}
	return cmd.Process.Kill()
}

// applyRuntimeLimits applies OS-level caps to a freshly started tenant process.
//
// Memory is capped with GOMEMLIMIT (set in the child environment) rather than
// RLIMIT_AS, because PocketBase is a Go program and RLIMIT_AS breaks the Go
// runtime's address-space reservations. RLIMIT_NOFILE is applied here because it
// cannot be expressed through the environment.
func applyRuntimeLimits(pid int, l Limits, logger *logging.Logger) {
	if pid <= 0 {
		return
	}
	if l.Nofile > 0 {
		lim := &unix.Rlimit{Cur: l.Nofile, Max: l.Nofile}
		if err := unix.Prlimit(pid, unix.RLIMIT_NOFILE, lim, nil); err != nil && logger != nil {
			logger.Debugf("prlimit(RLIMIT_NOFILE) on pid %d failed: %v", pid, err)
		}
	}
	if l.CPUPercent > 0 && l.CPUPercent < 100 {
		// Lower scheduling priority instead of throttling with cgroups: a busy
		// tenant then yields to its neighbours without being throttled to a
		// standstill on an otherwise idle box.
		if err := unix.Setpriority(unix.PRIO_PROCESS, pid, 10); err != nil && logger != nil {
			logger.Debugf("setpriority on pid %d failed: %v", pid, err)
		}
	}
}

// PidAlive reports whether a PID exists (EPERM means it exists but is not ours).
func PidAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := unix.Kill(pid, 0)
	return err == nil || err == unix.EPERM
}

// goRuntimeEnv returns the Go-runtime-level limits that replace container quotas.
func goRuntimeEnv(l Limits, env map[string]string) {
	if l.MemoryMB > 0 {
		env["GOMEMLIMIT"] = itoa(int64(l.MemoryMB)) + "MiB"
	}
	if l.CPUPercent > 0 {
		cpus := runtime.NumCPU() * l.CPUPercent / 100
		if cpus < 1 {
			cpus = 1
		}
		if cpus > runtime.NumCPU() {
			cpus = runtime.NumCPU()
		}
		env["GOMAXPROCS"] = itoa(int64(cpus))
	}
}
