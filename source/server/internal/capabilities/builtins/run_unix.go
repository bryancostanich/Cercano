//go:build unix

package builtins

import (
	"os"
	"os/exec"
	"syscall"
)

// setRunProcessGroup puts the command in its own process group (PGID == PID)
// so a timeout can signal the whole tree at once. Without this, killing the
// direct child leaves backgrounded grandchildren running — and, because they
// inherit the stdout/stderr pipe write ends, Wait blocks forever draining a
// pipe that never closes. Mirror of internal/worker/spawn_unix.go.
func setRunProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// terminateRunGroup asks the whole process group to exit. Used as cmd.Cancel,
// so a timed-out command gets a chance to clean up before WaitDelay escalates.
// ESRCH (nobody left in the group) is reported as os.ErrProcessDone so os/exec
// treats it as an already-finished process rather than a cancellation failure.
func terminateRunGroup(proc *os.Process) error {
	if proc == nil {
		return os.ErrProcessDone
	}
	err := syscall.Kill(-proc.Pid, syscall.SIGTERM)
	switch err {
	case nil:
		return nil
	case syscall.ESRCH:
		return os.ErrProcessDone
	default:
		// Group signal failed for some other reason; fall back to the
		// individual child so cancellation still does something.
		return proc.Signal(syscall.SIGTERM)
	}
}

// killRunGroup is the final sweep after a timeout: SIGKILL anything still
// alive in the group. os/exec's WaitDelay escalation only kills the direct
// child, so grandchildren that ignored SIGTERM are cleaned up here.
func killRunGroup(pid int) {
	if pid <= 0 {
		return
	}
	_ = syscall.Kill(-pid, syscall.SIGKILL)
}
