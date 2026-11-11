//go:build windows

package procs

import (
	"os/exec"
	"runtime"
	"syscall"

	"github.com/incurl/pocketghost/internal/logging"
)

// applySysProcAttr gives the tenant process its own console process group on
// Windows so a CTRL_BREAK can be delivered to it alone.
func applySysProcAttr(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP}
}

func signalGraceful(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	return cmd.Process.Signal(syscall.SIGTERM)
}

func signalForce(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	return cmd.Process.Kill()
}

func applyRuntimeLimits(pid int, l Limits, logger *logging.Logger) {
	if logger != nil && (l.MemoryMB > 0 || l.CPUPercent > 0) {
		logger.Debugf("OS resource limits are not enforced on Windows for pid %d", pid)
	}
}

// PidAlive reports whether a PID exists.
func PidAlive(pid int) bool { return pid > 0 }

func goRuntimeEnv(l Limits, env map[string]string) {
	if l.MemoryMB > 0 {
		env["GOMEMLIMIT"] = itoa(int64(l.MemoryMB)) + "MiB"
	}
	if l.CPUPercent > 0 {
		cpus := runtime.NumCPU() * l.CPUPercent / 100
		if cpus < 1 {
			cpus = 1
		}
		env["GOMAXPROCS"] = itoa(int64(cpus))
	}
}
