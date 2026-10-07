//go:build unix

package launch

// Unix-specific output and executable validation. These fixtures never
// let the primitive hang: a FIFO is always propped open by an owned
// read-write keeper, and the fixed open path opens FIFOs non-blocking
// anyway.

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// A FIFO at the log path must be refused: opening it for writing would
// block until a reader appears, and a child writing into an unattended
// FIFO dies on SIGPIPE. The owned RDWR keeper keeps this test from ever
// hanging on the pre-fix open; the fixed path rejects the FIFO by type
// before opening.
func TestOneShotLaunch_RejectsFIFOOutputPath(t *testing.T) {
	dir := t.TempDir()
	fifo := filepath.Join(dir, "out.fifo")
	if err := unix.Mkfifo(fifo, 0o600); err != nil {
		t.Fatalf("mkfifo: %v", err)
	}
	keeper, err := unix.Open(fifo, unix.O_RDWR, 0)
	if err != nil {
		t.Fatalf("keeper: %v", err)
	}
	defer unix.Close(keeper) //nolint:errcheck // fixture-owned keeper

	exe, err := helperExecutable()
	if err != nil {
		t.Fatalf("test binary path: %v", err)
	}
	proc, err := Launch(Options{
		Executable: exe,
		Argv:       []string{"-launch-testprocess-role=" + helperRoleEchoOnce},
		StdoutPath: fifo,
	})
	if !errors.Is(err, ErrInvalidOutputPath) {
		t.Fatalf("want ErrInvalidOutputPath, got err=%v proc=%v", err, proc)
	}
	if proc != nil {
		t.Fatalf("process returned alongside error: %v", proc)
	}
}

// A newly created log file must be 0600: it is owned by the initiating
// app's user, and the child's diagnostics are not for group/other eyes.
func TestOneShotLaunch_NewLogFilesAreOwnerOnly(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "out.log")
	errLog := filepath.Join(dir, "err.log")
	launchEchoOnce(t, out, errLog, 0)
	for _, p := range []string{out, errLog} {
		info, err := os.Stat(p)
		if err != nil {
			t.Fatalf("stat %s: %v", p, err)
		}
		perm := info.Mode().Perm()
		if perm&0o077 != 0 {
			t.Errorf("%s mode = %o, want group/other bits clear (0600)", p, perm)
		}
		if perm&0o600 != 0o600 {
			t.Errorf("%s mode = %o, want owner rw", p, perm)
		}
	}
}

// A non-regular executable must be refused as ErrInvalidExecutable:
// "not a directory" alone let a device or FIFO reach fork/exec.
func TestOneShotLaunch_RejectsNonRegularExecutable(t *testing.T) {
	dir := t.TempDir()
	fifo := filepath.Join(dir, "exe.fifo")
	if err := unix.Mkfifo(fifo, 0o600); err != nil {
		t.Fatalf("mkfifo: %v", err)
	}
	for _, path := range []string{"/dev/null", fifo} {
		proc, err := Launch(Options{Executable: path})
		if !errors.Is(err, ErrInvalidExecutable) {
			t.Errorf("executable %q: want ErrInvalidExecutable, got err=%v proc=%v", path, err, proc)
		}
		if proc != nil {
			t.Errorf("executable %q: process returned alongside error", path)
		}
	}
}

// Reaping contract: an exited short child must not linger as a zombie
// while the initiating parent (this test) is still alive. The test never
// waits on the child itself: a zombie still answers signal 0, and only a
// full reap frees the pid, so the transition to ESRCH is native proof
// that the launcher's reaper acted; wait4(WNOHANG) then agrees that
// nothing waitable remains. The safety net never signals a confirmed
// reaped pid, so a recycled pid can never be hit.
func TestOneShotLaunch_ReapsShortChildWhileParentAlive(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "out.log")
	exe, err := helperExecutable()
	if err != nil {
		t.Fatalf("test binary path: %v", err)
	}
	proc, err := Launch(Options{
		Executable: exe,
		Argv:       []string{"-launch-testprocess-role=" + helperRoleEchoOnce},
		StdoutPath: out,
	})
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	pid := proc.Pid()
	confirmedGone := false
	t.Cleanup(func() {
		// Never signal after reaping is confirmed: the pid may have
		// been recycled by then.
		if !confirmedGone {
			terminateGrandchildPID(pid)
		}
	})

	// The log line proves the child ran and exited; this test does NOT
	// wait on it.
	waitForFile(t, out, 10*time.Second)

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if err := syscall.Kill(pid, 0); err != nil {
			confirmedGone = true
			// Native wait agreement: nothing waitable remains.
			var status syscall.WaitStatus
			if _, err := syscall.Wait4(pid, &status, syscall.WNOHANG, nil); err != syscall.ECHILD {
				t.Errorf("wait4(%d) = %v, want ECHILD (no unreaped child remains)", pid, err)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("child %d still answers signal 0 10s after exiting: unreaped zombie while parent alive", pid)
}
