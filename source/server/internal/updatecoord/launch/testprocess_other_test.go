//go:build !unix

package launch

import (
	"os"
	"time"
)

// parentIsGone: this platform has no portable reparent observation, so the
// child relies on the marker the intermediate parent writes immediately
// before exiting, plus a grace period covering the write-to-exit gap.
func parentIsGone(expectedParentPID int) bool {
	_ = expectedParentPID
	data, err := os.ReadFile(*parentDoneMarkerPath)
	if err != nil || len(data) == 0 {
		return false
	}
	time.Sleep(500 * time.Millisecond)
	return true
}

// sessionIndependenceLine: session identity has no portable probe here;
// survival is proven behaviorally by the completion marker.
func sessionIndependenceLine() string { return "session-unchecked" }

// grandchildGone reports whether a fixture-launched pid is provably
// exited: an exited Windows process with no remaining handles fails to
// open.
func grandchildGone(pid int) bool {
	_, err := os.FindProcess(pid)
	return err != nil
}

// terminateGrandchildPID is best-effort on this platform: only pids the
// fixture itself launched are ever passed here, only after they were not
// observed to exit on their own, and an already-exited pid either fails
// to open or fails to terminate (both ignored).
func terminateGrandchildPID(pid int) {
	if pid <= 0 {
		return
	}
	if proc, err := os.FindProcess(pid); err == nil {
		_ = proc.Kill()
	}
}
