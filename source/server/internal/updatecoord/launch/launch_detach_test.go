package launch

// The owned testprocess chain proves the primitive's core contract:
//
//	test (fixture) → intermediate parent → final child
//
// The intermediate parent launches the child through Launch and then dies
// (gracefully, or hard-killed by the fixture). The child must survive the
// parent's death, parent-side cancellation and parent stdio closure, then
// write its completion marker. The fixture reaps or terminates every
// process on every path.

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// runDetachedSurvivalFixture runs the chain once and returns the launched
// child's pid and its log path. The intermediate parent either exits on
// its own (hardKill=false) or is hard-killed by the fixture once it
// signals that it launched the child (hardKill=true). Both the
// intermediate parent (a direct child of the test) and the final child
// (the grandchild) are reaped/terminated on every path.
func runDetachedSurvivalFixture(t *testing.T, hardKill bool) (childPID int, childLog string) {
	t.Helper()

	dir := t.TempDir()
	childLog = filepath.Join(dir, "child.log")
	pidFile := filepath.Join(dir, "child.pid")
	parentDone := filepath.Join(dir, "parent-done")
	completion := filepath.Join(dir, "completion")

	exe, err := helperExecutable()
	if err != nil {
		t.Fatalf("test binary path: %v", err)
	}

	// The intermediate parent is a plain fixture-owned process: the test
	// can Wait for it (reap it) and hard-kill it.
	parent := exec.Command(exe,
		"-launch-testprocess-role="+helperRoleIntermediateParent,
		"-launch-testprocess-pidfile="+pidFile,
		"-launch-testprocess-child-log="+childLog,
		"-launch-testprocess-parent-done="+parentDone,
		"-launch-testprocess-completion="+completion,
	)
	if err := parent.Start(); err != nil {
		t.Fatalf("starting intermediate parent: %v", err)
	}
	t.Cleanup(func() {
		_ = parent.Process.Kill() // no-op if it already exited
		_, _ = parent.Process.Wait()
	})

	// Wait for the parent to have launched the child and recorded its pid.
	rawPid := waitForFile(t, pidFile, 20*time.Second)
	pid, err := strconv.Atoi(strings.TrimSpace(rawPid))
	if err != nil {
		t.Fatalf("unreadable child pid %q: %v", rawPid, err)
	}
	// Safety net for the grandchild on every path; the confirmed flag keeps
	// paths where the grandchild was observed to exit from ever signalling
	// again, so a recycled pid can never be terminated by cleanup.
	// t.Cleanup runs on the same goroutine as the test body.
	confirmedGone := false
	t.Cleanup(func() {
		if !confirmedGone {
			terminateGrandchildPID(pid)
		}
	})

	if hardKill {
		// Simulate the initiating parent dying without any cleanup: the
		// child must survive a hard kill exactly as it must survive an
		// ordinary exit.
		_ = parent.Process.Kill()
	}
	if err := parent.Wait(); err != nil && !hardKill {
		t.Fatalf("intermediate parent: %v", err)
	}

	// Assert the completion marker: it only appears after the child
	// observed the parent's death.
	marker := waitForCompletion(t, completion, childLog, 30*time.Second)
	if strings.TrimSpace(marker) != "complete" {
		t.Fatalf("completion marker = %q", marker)
	}

	// The child's output must have reached the caller-owned log file after
	// the parent died; on Unix it also proves the child really holds its
	// own session.
	logData, err := os.ReadFile(childLog)
	if err != nil {
		t.Fatalf("read child log: %v", err)
	}
	if !strings.Contains(string(logData), "child-alive-after-parent-exit") {
		t.Errorf("child log = %q, want post-death child output", logData)
	}
	if strings.Contains(string(logData), "session-attached") {
		t.Errorf("child was left attached to the parent's session/group: %q", logData)
	}

	// Confirm the grandchild exited on its own (init reaped it on Unix);
	// only then is the safety net disarmed.
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if grandchildGone(pid) {
			confirmedGone = true
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	if !confirmedGone {
		t.Errorf("final child %d still present after writing its completion marker", pid)
	}
	return pid, childLog
}

// waitForCompletion polls for the completion marker, dumping the child log
// on timeout for diagnosis.
func waitForCompletion(t *testing.T, path, childLog string, timeout time.Duration) string {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if data, err := os.ReadFile(path); err == nil {
			return string(data)
		}
		time.Sleep(25 * time.Millisecond)
	}
	logData, _ := os.ReadFile(childLog)
	t.Fatalf("timed out waiting for %s; child log: %q", path, logData)
	return ""
}

// The primary contract: the launched child survives the initiating parent's
// ordinary exit, the parent's cancelled context and the parent's closed
// stdio, and completes its work afterward.
func TestOneShotLaunch_ChildSurvivesInitiatingParentExit(t *testing.T) {
	runDetachedSurvivalFixture(t, false)
}

// The same contract must hold when the initiating parent dies without any
// cleanup at all.
func TestOneShotLaunch_ChildSurvivesParentHardKill(t *testing.T) {
	runDetachedSurvivalFixture(t, true)
}
