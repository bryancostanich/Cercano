//go:build darwin || linux

package mistralrs

import (
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

func killProcess(proc *os.Process) error {
	if proc == nil {
		return nil
	}
	if err := syscall.Kill(-proc.Pid, syscall.SIGTERM); err != nil {
		return proc.Kill()
	}
	return nil
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
// if it hasn't after a short grace period. mistral.rs exits promptly on
// SIGTERM; the escalation exists for a wedged one.
func terminateGroup(pid int) {
	_ = syscall.Kill(-pid, syscall.SIGTERM)
	for i := 0; i < 20; i++ {
		if !processAlive(pid) {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	_ = syscall.Kill(-pid, syscall.SIGKILL)
}
