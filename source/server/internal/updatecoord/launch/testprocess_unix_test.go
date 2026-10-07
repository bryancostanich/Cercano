//go:build unix

package launch

import (
	"fmt"
	"os"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

// waitForParentGone is a strict post-death observation: when the
// intermediate parent dies, the kernel reparents this process, so getppid
// no longer matches the explicitly supplied expected parent pid. Passing
// the pid explicitly (rather than reading getppid at startup) also covers
// a child that starts only after the parent already died. The observation
// is the kernel's, so no bound handshake or grace period is needed.
func waitForParentGone(expectedParentPID int) error {
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		if os.Getppid() != expectedParentPID {
			return nil
		}
		time.Sleep(20 * time.Millisecond)
	}
	return fmt.Errorf("intermediate parent %d never exited within 60s", expectedParentPID)
}

// holdForFixtureRelease: the Unix fixture observes the child through the
// kernel (signal 0), so no bound handshake is needed.
func holdForFixtureRelease() error { return nil }

// awaitChildParentBound: the child's parent-death proof needs no parent
// handle on Unix, so there is nothing to await.
func awaitChildParentBound() error { return nil }

// enterOwnedPermissiveFixtureJob: Windows job objects do not exist on
// Unix; the fixture-owned job context is a Windows-only concept.
func enterOwnedPermissiveFixtureJob() error { return nil }

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

// fixtureWatch is the test-bound observation of one fixture child. On Unix
// the pid stays owned by this user and the kernel answers signal 0
// authoritatively; a reaped or dead pid answers ESRCH.
type fixtureWatch struct{ pid int }

// watchFixtureChild binds the observation as early as possible after the
// pid is known. Errors fail the fixture loudly — liveness is never
// guessed.
func watchFixtureChild(pid int) (*fixtureWatch, error) {
	if pid <= 0 {
		return nil, fmt.Errorf("invalid fixture pid %d", pid)
	}
	return &fixtureWatch{pid: pid}, nil
}

// Exited reports the kernel's answer for the watched pid: ESRCH means
// provably gone (reaped, or dead with init having reaped it); no error
// means still present. Any other probe error is returned, never silently
// mapped to "gone".
func (w *fixtureWatch) Exited() (bool, error) {
	switch err := syscall.Kill(w.pid, 0); {
	case err == nil:
		return false, nil
	case err == syscall.ESRCH:
		return true, nil
	default:
		return false, fmt.Errorf("probing fixture pid %d: %w", w.pid, err)
	}
}

// TerminateIfRunning is the cleanup safety net: it acts only while the
// watched pid provably still accepts the zero signal (the caller never
// calls it after a confirmed exit), so a recycled pid can never be hit.
func (w *fixtureWatch) TerminateIfRunning() {
	if err := syscall.Kill(w.pid, 0); err == nil {
		_ = syscall.Kill(w.pid, syscall.SIGKILL)
	}
}

// Close releases the observation rights (none on Unix).
func (w *fixtureWatch) Close() {}
