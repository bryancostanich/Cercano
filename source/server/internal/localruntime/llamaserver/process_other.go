//go:build !darwin && !linux

package llamaserver

import (
	"os"
	"os/exec"
)

func setProcessGroup(cmd *exec.Cmd) {}

func killProcess(proc *os.Process) error {
	if proc == nil {
		return nil
	}
	return proc.Kill()
}

// Orphan sweeping is unix-only: without a reliable liveness + command-line
// probe, reaping by recorded PID risks killing an unrelated process, so
// these stubs make SweepOrphans a no-op.
func processAlive(int) bool { return false }

func processCommand(int) string { return "" }

func terminateGroup(int) {}
