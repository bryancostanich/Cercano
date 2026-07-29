//go:build unix

package worker

import (
	"os"
	"os/exec"
	"syscall"
)

// setProcessGroup puts the worker in its own process group, keyed by its
// pid, so killGroupOrProcess and the startup orphan-sweep (reap.go) can
// target the whole group at once.
func setProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// killGroupOrProcess kills the process group (so all children die too).
// Mirror of internal/meridian/manager.go:killGroupOrProcess.
func killGroupOrProcess(proc *os.Process) {
	if proc == nil {
		return
	}
	if err := syscall.Kill(-proc.Pid, syscall.SIGKILL); err != nil {
		_ = proc.Kill()
	}
}
