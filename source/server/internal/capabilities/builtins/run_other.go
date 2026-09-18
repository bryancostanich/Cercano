//go:build !unix

package builtins

import (
	"os"
	"os/exec"
)

// setRunProcessGroup is a no-op: POSIX process groups don't exist here.
func setRunProcessGroup(cmd *exec.Cmd) {}

// terminateRunGroup has no group to target, so it kills the child itself.
func terminateRunGroup(proc *os.Process) error {
	if proc == nil {
		return os.ErrProcessDone
	}
	return proc.Kill()
}

// killRunGroup: no process group to sweep. WaitDelay's own escalation
// (os.Process.Kill on the direct child) is all that's available.
func killRunGroup(pid int) {}
