//go:build !darwin && !linux

package llamaserver

import (
	"os"
	"os/exec"
	"time"
)

func setProcessGroup(cmd *exec.Cmd) {}

type terminationResult struct {
	PID         int
	Wait        time.Duration
	Escalated   bool
	AlreadyGone bool
}

func killProcess(proc *os.Process) error {
	_, err := killProcessWithResult(proc)
	return err
}

func killProcessWithResult(proc *os.Process) (terminationResult, error) {
	if proc == nil {
		return terminationResult{}, nil
	}
	started := time.Now()
	err := proc.Kill()
	return terminationResult{PID: proc.Pid, Wait: time.Since(started), Escalated: true}, err
}

// Orphan sweeping is unix-only: without a reliable liveness + command-line
// probe, reaping by recorded PID risks killing an unrelated process, so
// these stubs make SweepOrphans a no-op.
func processAlive(int) bool { return false }

func processCommand(int) string { return "" }

func terminateGroup(int) (terminationResult, error) { return terminationResult{}, nil }
