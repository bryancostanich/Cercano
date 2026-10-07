//go:build windows

package launch

// Owned Windows job-object fixtures. No inherited, CI or global job
// policy is created or modified: each fixture-owned intermediate parent
// (helperRoleJobParent) builds its own job, assigns itself and dies with
// it.
//
//   - restrictive deny: the owned job forbids breakaway and kills on
//     close, so Launch must be refused with the OS error surfaced and no
//     child started. The refusal is classified: only a breakaway denial
//     (access denied) is the intended fail-closed outcome; anything else
//     is reported explicitly rather than counted as success.
//   - permitted breakaway: the owned job permits breakaway and kills on
//     close, so the launched child escapes it and must outlive the
//     parent's own job closure, writing its completion marker afterward.
//     If the host environment forbids the breakaway (a restrictive outer
//     job chain this test must not bypass), the refusal is classified and
//     the test skips: no escape is claimed where none was proven.

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// runJobObjectFixture runs the job-parent helper chain once and checks
// the outcome against the variant's contract.
func runJobObjectFixture(t *testing.T, variant string) {
	t.Helper()

	dir := t.TempDir()
	resultPath := filepath.Join(dir, "job-result")
	pidFile := filepath.Join(dir, "child.pid")
	parentDone := filepath.Join(dir, "parent-done")
	completion := filepath.Join(dir, "completion")
	childLog := filepath.Join(dir, "child.log")

	exe, err := helperExecutable()
	if err != nil {
		t.Fatalf("test binary path: %v", err)
	}
	parent := exec.Command(exe,
		"-launch-testprocess-role="+helperRoleJobParent,
		"-launch-testprocess-job-variant="+variant,
		"-launch-testprocess-job-result="+resultPath,
		"-launch-testprocess-pidfile="+pidFile,
		"-launch-testprocess-child-log="+childLog,
		"-launch-testprocess-parent-done="+parentDone,
		"-launch-testprocess-completion="+completion,
	)
	if err := parent.Start(); err != nil {
		t.Fatalf("starting job-parent: %v", err)
	}
	t.Cleanup(func() {
		_ = parent.Process.Kill() // no-op if it already exited
		_, _ = parent.Process.Wait()
	})

	result := strings.TrimSpace(waitForFile(t, resultPath, 20*time.Second))
	switch {
	case strings.HasPrefix(result, "launch-failed:"):
		assertJobRefusal(t, variant, result)
		if variant == jobVariantDeny {
			// No child may exist after the refusal: neither pidfile
			// nor completion marker may ever appear.
			_ = parent.Wait()
			time.Sleep(2 * time.Second)
			for _, p := range []string{pidFile, completion} {
				if _, err := os.Stat(p); !os.IsNotExist(err) {
					t.Errorf("%s exists after a refused launch; a child was started", filepath.Base(p))
				}
			}
		}
		return
	case strings.HasPrefix(result, "accepted-unexpected:"):
		t.Fatalf("restrictive job ACCEPTED the launch (fail-closed violated): %s", result)
	case strings.HasPrefix(result, "launched:"):
		break
	default:
		t.Fatalf("unclassified job-parent outcome: %q", result)
	}
	if variant == jobVariantDeny {
		t.Fatalf("deny variant launched a child: %s", result)
	}

	// Allow variant: follow the breakaway child past the parent's own
	// job closure.
	rawPid := waitForFile(t, pidFile, 20*time.Second)
	pid, err := strconv.Atoi(strings.TrimSpace(rawPid))
	if err != nil {
		t.Fatalf("unreadable child pid %q: %v", rawPid, err)
	}
	confirmedGone := false
	t.Cleanup(func() {
		if !confirmedGone {
			terminateGrandchildPID(pid)
		}
	})
	// The parent's exit closes the last job handle, terminating the
	// KILL_ON_JOB_CLOSE job. A child that did not truly escape would die
	// here and never write its completion marker.
	if err := parent.Wait(); err != nil {
		t.Fatalf("job-parent: %v", err)
	}

	marker := waitForCompletion(t, completion, childLog, 30*time.Second)
	if strings.TrimSpace(marker) != "complete" {
		t.Fatalf("completion marker = %q", marker)
	}
	logData, err := os.ReadFile(childLog)
	if err != nil {
		t.Fatalf("read child log: %v", err)
	}
	if !strings.Contains(string(logData), "child-alive-after-parent-exit") {
		t.Errorf("child log = %q, want post-job-close child output", logData)
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if grandchildGone(pid) {
			confirmedGone = true
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	if !confirmedGone {
		t.Errorf("breakaway child %d still present after completing", pid)
	}
}

// assertJobRefusal classifies a refused launch: the intended fail-closed
// outcome only in the deny variant, or an honest environment restriction
// (skip, never a claimed escape) in the allow variant.
func assertJobRefusal(t *testing.T, variant, result string) {
	t.Helper()
	detail := strings.TrimSpace(strings.TrimPrefix(result, "launch-failed:"))
	switch {
	case strings.Contains(detail, "breakaway-denied"):
		if variant == jobVariantDeny {
			t.Logf("restrictive job refused the launch as designed: %s", detail)
			return
		}
		t.Skipf("host job chain forbids breakaway; outliving job closure cannot be proven in this environment (refusal: %s)", detail)
	case strings.Contains(detail, "creation-flags-or-params-invalid"):
		t.Errorf("launch refused for flag reasons unrelated to job policy — investigate creation flags: %s", detail)
	default:
		t.Errorf("launch refused with an unclassified error — investigate: %s", detail)
	}
}

// The restrictive deny contract: a job that forbids breakaway must make
// Launch fail closed, with the OS error surfaced and no child started.
func TestOneShotLaunch_RestrictiveJobRefusesLaunch(t *testing.T) {
	runJobObjectFixture(t, jobVariantDeny)
}

// The permitted breakaway contract: when the OS allows the breakaway,
// the child escapes the parent's own kill-on-close job and completes
// after that job has terminated.
func TestOneShotLaunch_PermittedBreakawayChildOutlivesParentJobClose(t *testing.T) {
	runJobObjectFixture(t, jobVariantAllow)
}
