//go:build darwin || linux

package llamaserver

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"
)

func setProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

var (
	terminatePollInterval = 100 * time.Millisecond
	terminateGrace        = 2 * time.Second
	killGrace             = 5 * time.Second
)

type terminationResult struct {
	PID        int
	Wait       time.Duration
	Escalated  bool
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
	return terminateProcessGroup(proc.Pid, func() error { return proc.Kill() })
}

// processAlive reports whether a process with the given PID exists. EPERM
// means it exists but belongs to someone else — alive for our purposes.
func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || err == syscall.EPERM
}

// processCommand returns the full command line of the process, or "" when
// it can't be read (process gone, ps unavailable).
func processCommand(pid int) string {
	out, err := exec.Command("ps", "-o", "command=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// terminateGroup asks the process group to exit and escalates to SIGKILL
// if it hasn't after a short grace period. It only returns once the
// recorded PID is gone, or a bounded timeout expires.
func terminateGroup(pid int) (terminationResult, error) {
	return terminateProcessGroup(pid, nil)
}

func terminateProcessGroup(pid int, individualKill func() error) (terminationResult, error) {
	res := terminationResult{PID: pid}
	started := time.Now()
	defer func() { res.Wait = time.Since(started) }()

	if pid <= 0 || !processAlive(pid) {
		res.AlreadyGone = true
		return res, nil
	}

	if err := signalProcessGroupOrPID(pid, syscall.SIGTERM); err != nil && err != syscall.ESRCH {
		// Best effort: if the process-group signal failed for reasons other
		// than "already gone", try the os.Process kill fallback for owned
		// children. It sends SIGKILL to the individual process, not the
		// group, but it is better than returning after signal delivery failed.
		res.Escalated = true
		if individualKill != nil {
			_ = individualKill()
		} else {
			_ = syscall.Kill(pid, syscall.SIGKILL)
		}
	} else if waitForProcessGone(pid, terminateGrace) {
		res.Wait = time.Since(started)
		return res, nil
	}

	if processAlive(pid) {
		res.Escalated = true
		if err := signalProcessGroupOrPID(pid, syscall.SIGKILL); err != nil && err != syscall.ESRCH {
			if individualKill != nil {
				_ = individualKill()
			} else {
				_ = syscall.Kill(pid, syscall.SIGKILL)
			}
		}
	}
	if waitForProcessGone(pid, killGrace) {
		res.Wait = time.Since(started)
		return res, nil
	}
	res.Wait = time.Since(started)
	return res, fmt.Errorf("process %d still alive after SIGTERM + SIGKILL (%s)", pid, res.Wait.Round(time.Millisecond))
}

func signalProcessGroupOrPID(pid int, sig syscall.Signal) error {
	// Spawned llama-servers are placed in their own process group with
	// PGID == PID. Signal the group first so subprocesses die too.
	if err := syscall.Kill(-pid, sig); err != nil {
		if err == syscall.ESRCH {
			return syscall.Kill(pid, sig)
		}
		return err
	}
	return nil
}

func waitForProcessGone(pid int, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for {
		if !processAlive(pid) {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(terminatePollInterval)
	}
}

// testTerminationTimeouts lets tests shorten the real-world grace periods
// without changing production constants.
func testTerminationTimeouts(term, kill, poll time.Duration) func() {
	oldTerm, oldKill, oldPoll := terminateGrace, killGrace, terminatePollInterval
	terminateGrace, killGrace, terminatePollInterval = term, kill, poll
	return func() {
		terminateGrace, killGrace, terminatePollInterval = oldTerm, oldKill, oldPoll
	}
}
