//go:build !unix && !windows

package launch

import (
	"fmt"
	"os"
	"time"
)

// waitForParentGone: this platform has no portable reparent observation
// and no real process handles, so the parent's own done marker plus a
// grace period covering the write-to-exit gap remains the best available
// heuristic proof. (The Windows implementation uses a real SYNCHRONIZE
// handle + wait instead; see launch_liveness_windows_test.go.)
func waitForParentGone(expectedParentPID int) error {
	_ = expectedParentPID
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		data, err := readFixtureFile(*parentDoneMarkerPath)
		if err == nil && len(data) > 0 {
			time.Sleep(500 * time.Millisecond)
			return nil
		}
		time.Sleep(20 * time.Millisecond)
	}
	return fmt.Errorf("parent done marker %s never appeared within 60s", *parentDoneMarkerPath)
}

// holdForFixtureRelease: no bound handshake is used on this platform.
func holdForFixtureRelease() error { return nil }

// awaitChildParentBound: no bound handshake is used on this platform.
func awaitChildParentBound() error { return nil }

// enterOwnedPermissiveFixtureJob: Windows job objects do not exist here.
func enterOwnedPermissiveFixtureJob() error { return nil }

// sessionIndependenceLine: session identity has no portable probe here;
// survival is proven behaviorally by the completion marker.
func sessionIndependenceLine() string { return "session-unchecked" }

// fixtureWatch is the test-bound observation of one fixture child on a
// platform with no authoritative liveness API.
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

// Exited reports whether the pid is provably exited: a pid the platform
// can no longer open cannot be this fixture's running child.
func (w *fixtureWatch) Exited() (bool, error) {
	_, err := os.FindProcess(w.pid)
	return err != nil, nil
}

// TerminateIfRunning is the best-effort cleanup safety net: only pids
// the fixture itself launched are ever passed here, only after they were
// not observed to exit on their own, and the caller never calls it after
// a confirmed exit.
func (w *fixtureWatch) TerminateIfRunning() {
	if proc, err := os.FindProcess(w.pid); err == nil {
		_ = proc.Kill()
	}
}

// Close releases the observation rights (none on this platform).
func (w *fixtureWatch) Close() {}
