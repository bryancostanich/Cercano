//go:build unix

package launch

import (
	"os"
	"syscall"

	"golang.org/x/sys/unix"
)

// parentIsGone is a strict post-death observation: when the intermediate
// parent dies, the kernel reparents this process, so getppid no longer
// matches the explicitly supplied expected parent pid. Passing the pid
// explicitly (rather than reading getppid at startup) also covers a child
// that starts only after the parent already died.
func parentIsGone(expectedParentPID int) bool {
	return os.Getppid() != expectedParentPID
}

// sessionIndependenceLine reports whether the child really runs as its own
// session and process group (the setsid contract) or was accidentally left
// attached to the parent's group.
func sessionIndependenceLine() string {
	pid := os.Getpid()
	sid, err := unix.Getsid(pid)
	if err == nil && sid == pid && syscall.Getpgrp() == pid {
		return "session-independent"
	}
	return "session-attached"
}

// grandchildGone reports whether a fixture-launched pid is provably exited:
// a reaped or never-reaped-but-dead pid no longer accepts the zero signal
// (zombies still accept it until init reaps them, which the callers poll
// through).
func grandchildGone(pid int) bool {
	return syscall.Kill(pid, 0) != nil
}

// terminateGrandchildPID stops a fixture-owned process this test did NOT
// parent (its parent, the intermediate parent, is already dead, so init
// reaps it after death). Only pids the fixture itself launched are ever
// signalled, and only after they were not observed to exit on their own;
// this mirrors the accepted stale-probe pattern of the existing procx
// fixtures.
func terminateGrandchildPID(pid int) {
	if pid <= 0 || grandchildGone(pid) {
		return // gone already
	}
	_ = syscall.Kill(pid, syscall.SIGKILL)
}
