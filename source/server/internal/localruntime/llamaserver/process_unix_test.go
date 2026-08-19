//go:build darwin || linux

package llamaserver

import (
	"os/exec"
	"testing"
	"time"
)

func waitOwnedProcess(cmd *exec.Cmd) <-chan error {
	done := make(chan error, 1)
	go func() {
		_, err := cmd.Process.Wait()
		done <- err
	}()
	return done
}

func startProcessGroup(t *testing.T, name string, args ...string) *exec.Cmd {
	t.Helper()
	cmd := exec.Command(name, args...)
	setProcessGroup(cmd)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	})
	return cmd
}

func TestKillProcessWithResult_WaitsForSIGTERMDeath(t *testing.T) {
	defer testTerminationTimeouts(500*time.Millisecond, time.Second, 10*time.Millisecond)()
	cmd := startProcessGroup(t, "/bin/sleep", "60")
	wait := waitOwnedProcess(cmd) // emulate Provider.watch owning Wait

	res, err := killProcessWithResult(cmd.Process)
	if err != nil {
		t.Fatalf("killProcessWithResult: %v", err)
	}
	select {
	case <-wait:
		// reaped
	case <-time.After(time.Second):
		t.Fatal("process was not reaped after killProcessWithResult returned")
	}
	if res.PID != cmd.Process.Pid {
		t.Errorf("PID = %d, want %d", res.PID, cmd.Process.Pid)
	}
	if res.Escalated {
		t.Error("sleep should exit on SIGTERM; did not expect SIGKILL escalation")
	}
	if res.Wait <= 0 || res.Wait > time.Second {
		t.Errorf("Wait = %s, want a bounded positive duration", res.Wait)
	}
}

func TestKillProcessWithResult_EscalatesAndConfirmsAfterSIGKILL(t *testing.T) {
	defer testTerminationTimeouts(50*time.Millisecond, time.Second, 10*time.Millisecond)()
	// A single Perl process ignores SIGTERM and sleeps. SIGKILL to the
	// process group must still kill it and then confirm the recorded PID
	// disappeared. Avoid a shell loop here: the loop's child can receive
	// the group SIGTERM and make the shell exit before escalation.
	cmd := startProcessGroup(t, "/usr/bin/perl", "-e", "$SIG{TERM}=sub{}; while (1) { sleep 1 }")
	wait := waitOwnedProcess(cmd)
	// Give the interpreter time to install the signal handler. Without
	// this, the test races and SIGTERM can land before Perl has decided
	// to ignore it.
	time.Sleep(50 * time.Millisecond)

	res, err := killProcessWithResult(cmd.Process)
	if err != nil {
		t.Fatalf("killProcessWithResult: %v", err)
	}
	select {
	case <-wait:
		// reaped
	case <-time.After(time.Second):
		t.Fatal("process was not reaped after SIGKILL escalation")
	}
	if !res.Escalated {
		t.Error("expected SIGKILL escalation for SIGTERM-ignoring process")
	}
	if processAlive(cmd.Process.Pid) {
		t.Fatal("killProcessWithResult returned before the PID disappeared")
	}
}

func TestTerminateGroup_AlreadyGoneIsSuccess(t *testing.T) {
	defer testTerminationTimeouts(50*time.Millisecond, 50*time.Millisecond, 5*time.Millisecond)()
	cmd := exec.Command("/usr/bin/true")
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	res, err := terminateGroup(cmd.Process.Pid)
	if err != nil {
		t.Fatalf("terminateGroup on an already-gone pid: %v", err)
	}
	if !res.AlreadyGone {
		t.Errorf("AlreadyGone = false, want true")
	}
}
