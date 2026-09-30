//go:build !unix

package procx

import (
	"os"
	"os/exec"
)

// setProcessGroup is a no-op: POSIX process groups don't exist here.
func setProcessGroup(cmd *exec.Cmd) {}

// terminateGroup has no group to target, so it kills the child itself.
func terminateGroup(proc *os.Process) error {
	if proc == nil {
		return os.ErrProcessDone
	}
	return proc.Kill()
}

// killGroup: no process group to sweep. WaitDelay's own escalation
// (os.Process.Kill on the direct child) is all that's available.
func killGroup(pid int) {}
