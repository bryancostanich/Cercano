//go:build !unix

package worker

import (
	"os"
	"os/exec"
)

// setProcessGroup is a no-op here: POSIX process groups don't exist, and the
// worker has no children of its own to keep together.
func setProcessGroup(cmd *exec.Cmd) {}

// killGroupOrProcess: no process group to target, so kill the worker itself.
func killGroupOrProcess(proc *os.Process) {
	if proc == nil {
		return
	}
	_ = proc.Kill()
}
