//go:build windows

package launch

// TEST-ONLY Windows process-liveness and fixture-job primitives. No
// production launch behavior, flag or policy is changed here.
//
// CI run 37667337045 established the native facts this harness is built
// on:
//
//   - the CI test process is in a job (IsInJob=true) whose limit flags are
//     0x0 — no JOB_OBJECT_LIMIT_BREAKAWAY_OK, no SILENT_BREAKAWAY_OK;
//   - a CreateProcess WITHOUT breakaway (0x208) succeeds from that
//     context;
//   - EVERY breakaway flag variant is refused with ERROR_ACCESS_DENIED
//     from that context (a job policy refusal — the production flags are
//     valid, the policy denies them);
//   - a launch from a helper process inside its OWN permissive
//     (allow-breakaway, kill-on-close) job SUCCEEDS, with the child
//     completing and its log present.
//
// The same run exposed the two harness bugs fixed here:
//
//   - liveness was guessed with os.FindProcess != nil, which on Windows
//     ALWAYS succeeds (a Go Process object can outlive the process it
//     names), so a completed child looked "still present". Liveness is
//     now the exact OS answer on a REAL handle: OpenProcess(SYNCHRONIZE)
//     + WaitForSingleObject(handle, 0).
//   - the final child's parent-death proof was the parent-done marker
//     plus a 500 ms grace sleep — a heuristic that can observe a living
//     parent as gone. The child now opens a SYNCHRONIZE handle to the
//     parent and waits on it; every error fails loudly, death is never
//     assumed.
//
// Two bound handshakes make handle binding deterministic (no races, no
// sleeps):
//
//   - parent-bound: the child records that it holds the parent's handle;
//     the intermediate/job parent waits for that record before exiting,
//     so the child's OpenProcess can never race the parent's teardown.
//   - child-release: after its completion marker the child stays alive
//     until the fixture releases it, so the fixture binds a VERIFIED
//     handle to the child while it is provably alive, then observes the
//     exit through that handle. Cleanup kills act on the same handle
//     (TerminateProcess), never on a possibly recycled pid: no
//     post-exit pid signalling exists.

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// waitForParentGone is the authoritative parent-death proof for the final
// child: a real SYNCHRONIZE handle to the expected parent pid, waited on
// until the OS signals the handle (exactly when the process exits). No
// marker heuristic, no grace period. Every error is returned and fails
// the child loudly — death is never assumed.

// The WAIT_* wait-event values (WAIT_OBJECT_0 = 0, WAIT_TIMEOUT =
// 0x102): these are the WaitForSingleObject EVENT answers, deliberately
// written as untyped constants because x/sys/windows also exports a
// syscall.Errno named WAIT_TIMEOUT (the Win32 error code 258 — same
// numeric value, wrong type for a wait event).
const (
	waitEventObject0 = 0x00000000
	waitEventTimeout = 0x00000102
)

func waitForParentGone(expectedParentPID int) error {
	if expectedParentPID <= 0 {
		return fmt.Errorf("no parent pid supplied")
	}
	h, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(expectedParentPID))
	if err != nil {
		return fmt.Errorf("opening parent %d: %w", expectedParentPID, err)
	}
	defer windows.CloseHandle(h) //nolint:errcheck // observation handle only
	if err := publishFixtureFile(*parentBoundMarkerPath, []byte("bound")); err != nil {
		return fmt.Errorf("writing parent-bound marker %s: %w", *parentBoundMarkerPath, err)
	}
	ev, err := windows.WaitForSingleObject(h, windows.INFINITE)
	if err != nil {
		return fmt.Errorf("waiting on parent %d: %w", expectedParentPID, err)
	}
	if ev != waitEventObject0 {
		return fmt.Errorf("waiting on parent %d: unexpected wait event %d", expectedParentPID, ev)
	}
	return nil
}

// sessionIndependenceLine is the Windows analog of the Unix
// session-independence proof. CREATE_NEW_PROCESS_GROUP plus
// DETACHED_PROCESS mean the launched child shares NO console with the
// initiating parent, so no console event (Ctrl-C / console close)
// generated for the parent's console can ever reach it. The native probe
// available to a fixture child is the console itself: a process attached
// to any console answers GetConsoleCP, while the detached child — which
// neither inherits nor creates one — fails with ERROR_INVALID_HANDLE.
// That answer is reported exactly; a console-served child is reported as
// attached. (Job independence is proven separately and decisively by the
// breakaway launch itself; this line is only the session analog.)
func sessionIndependenceLine() string {
	if _, err := windows.GetConsoleCP(); err == nil {
		return "console-attached"
	}
	return "session-independent"
}

// holdForFixtureRelease keeps a COMPLETED child alive until the fixture
// releases it (the fixture half of the bound handshake): the fixture
// binds and verifies its handle to this pid only while it is provably
// this process, so the pid can never be recycled under the fixture's
// observation or cleanup. Bounded, so a fixture that never releases
// cannot wedge the child forever.
func holdForFixtureRelease() error {
	if *childReleasePath == "" {
		return fmt.Errorf("no release path supplied")
	}
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if data, err := readFixtureFile(*childReleasePath); err == nil && len(data) > 0 {
			return nil
		}
		time.Sleep(20 * time.Millisecond)
	}
	return fmt.Errorf("fixture never released the completed child within 30s")
}

// awaitChildParentBound is the parent half of the handshake: do not exit
// until the child has provably opened a real handle to this process, so
// the child's OpenProcess can never race this process's teardown and the
// test fixture's handle close.
func awaitChildParentBound() error {
	if *parentBoundMarkerPath == "" {
		return fmt.Errorf("no parent-bound path supplied")
	}
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if data, err := readFixtureFile(*parentBoundMarkerPath); err == nil && len(data) > 0 {
			return nil
		}
		time.Sleep(20 * time.Millisecond)
	}
	return fmt.Errorf("child never recorded its parent handle within 15s")
}

// enterOwnedPermissiveFixtureJob creates a job this helper process OWNS
// (JOB_OBJECT_LIMIT_BREAKAWAY_OK | JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE)
// and assigns itself to it. CI run 37667337045 proved the OS permits the
// production breakaway launch from exactly this context even though the
// inherited CI job (limit flags 0x0) denies it. Nothing inherited,
// CI-owned or global is read or modified: this job dies with this
// process, and a child that failed to break away would die with it too
// (the kill-on-close job is the containment safety net).
func enterOwnedPermissiveFixtureJob() error {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return fmt.Errorf("CreateJobObject: %w", err)
	}
	var info windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
	info.BasicLimitInformation.LimitFlags =
		windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE | windows.JOB_OBJECT_LIMIT_BREAKAWAY_OK
	if _, err := windows.SetInformationJobObject(job,
		windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
		_ = windows.CloseHandle(job)
		return fmt.Errorf("SetInformationJobObject: %w", err)
	}
	self, err := windows.GetCurrentProcess()
	if err != nil {
		_ = windows.CloseHandle(job)
		return fmt.Errorf("GetCurrentProcess: %w", err)
	}
	if err := windows.AssignProcessToJobObject(job, self); err != nil {
		_ = windows.CloseHandle(job)
		return fmt.Errorf("AssignProcessToJobObject: %w", err)
	}
	// The job handle is deliberately NOT closed: it is the process's
	// membership anchor, and closing the last handle to a
	// KILL_ON_JOB_CLOSE job would terminate this process. The handle —
	// and with it the job — dies with the process.
	return nil
}

// fixtureWatch is the test-bound observation of one fixture child, held
// through a REAL process handle so every later answer (exited? kill?)
// refers to exactly this process even after the pid is recycled.
type fixtureWatch struct {
	handle windows.Handle
	pid    int
}

// watchFixtureChild binds the observation handle as early as possible
// after the pid is known and verifies the pid currently maps to the
// fixture's own test-binary image before anything is observed or
// signalled. An open failure or an image mismatch is an error — never a
// silently assumed death, and never a licence to touch the pid.
func watchFixtureChild(pid int) (*fixtureWatch, error) {
	if pid <= 0 {
		return nil, fmt.Errorf("invalid fixture pid %d", pid)
	}
	h, err := windows.OpenProcess(
		windows.PROCESS_QUERY_LIMITED_INFORMATION|windows.SYNCHRONIZE|windows.PROCESS_TERMINATE,
		false, uint32(pid))
	if err != nil {
		return nil, fmt.Errorf("opening fixture child %d: %w", pid, err)
	}
	if err := verifyOwnedImage(h); err != nil {
		_ = windows.CloseHandle(h)
		return nil, fmt.Errorf("fixture child %d: %w", pid, err)
	}
	return &fixtureWatch{handle: h, pid: pid}, nil
}

// verifyOwnedImage proves the handle refers to the fixture's own test
// binary, so a recycled pid can never be observed or signalled by
// mistake.
func verifyOwnedImage(h windows.Handle) error {
	buf := make([]uint16, 1024)
	size := uint32(len(buf))
	if err := windows.QueryFullProcessImageName(h, 0, &buf[0], &size); err != nil {
		return fmt.Errorf("querying image name: %w", err)
	}
	image := windows.UTF16ToString(buf[:size])
	want, err := helperExecutable()
	if err != nil {
		return fmt.Errorf("own executable path: %w", err)
	}
	if !sameImagePath(image, want) {
		return fmt.Errorf("pid maps to %q, not the fixture's test binary %q — refusing to observe or signal a possibly recycled pid", image, want)
	}
	return nil
}

// sameImagePath compares two executable paths the way Windows treats
// them: case-insensitive, with either separator equivalent.
func sameImagePath(a, b string) bool {
	norm := func(p string) string {
		return strings.ReplaceAll(filepath.Clean(p), `\`, "/")
	}
	return strings.EqualFold(norm(a), norm(b))
}

// Exited is the exact OS answer on the bound handle: WAIT_OBJECT_0 means
// the process has exited, WAIT_TIMEOUT means it is still running. Any
// other result is an error — never an assumed death.
func (w *fixtureWatch) Exited() (bool, error) {
	ev, err := windows.WaitForSingleObject(w.handle, 0)
	if err != nil {
		return false, fmt.Errorf("waiting on fixture child %d: %w", w.pid, err)
	}
	switch ev {
	case waitEventObject0:
		return true, nil
	case waitEventTimeout:
		return false, nil
	default:
		return false, fmt.Errorf("waiting on fixture child %d: unexpected event %d", w.pid, ev)
	}
}

// TerminateIfRunning is the cleanup safety net: it acts only through the
// bound handle (TerminateProcess cannot target a recycled pid), and only
// while the handle is not yet signaled — the caller additionally never
// calls it after a confirmed exit, so no post-exit signal exists.
func (w *fixtureWatch) TerminateIfRunning() {
	if ev, err := windows.WaitForSingleObject(w.handle, 0); err == nil && ev == waitEventTimeout {
		_ = windows.TerminateProcess(w.handle, 1)
	}
}

// Close releases the observation handle.
func (w *fixtureWatch) Close() {
	_ = windows.CloseHandle(w.handle)
}

// TestOneShotLaunch_BoundExitObjectExistsAndSignals is the small probe
// that confirms, with the OS's own answers, the exact primitive every
// handle-based liveness check in this harness rests on:
//
//   - a real SYNCHRONIZE handle can be bound to a fixture-owned child
//     (image-verified, so never a recycled pid);
//   - while the child provably runs (it holds for release after writing
//     its marker), the bound handle reads NOT signaled;
//   - after the child exits, the process object STILL EXISTS and reads
//     SIGNALLED — repeatedly, and for a blocking wait — even though
//     nobody reaped or re-opened anything in between.
//
// This is precisely what the broken os.FindProcess check of CI run
// 37667337045 could not see (FindProcess "succeeds" for any pid, so a
// completed child looked alive; the signaled state of a verified handle
// is the real answer). The probe child is a plain fixture-owned child
// with NO breakaway flags, so it launches even from the restrictive
// default CI job (CI run 37667337045 proved the no-breakaway variant
// launches fine); nothing here bypasses any job policy.
func TestOneShotLaunch_BoundExitObjectExistsAndSignals(t *testing.T) {
	dir := t.TempDir()
	completion := filepath.Join(dir, "completion")
	release := filepath.Join(dir, "release")
	exe, err := helperExecutable()
	if err != nil {
		t.Fatalf("test binary path: %v", err)
	}
	cmd := exec.Command(exe,
		"-launch-testprocess-role="+helperRoleHoldChild,
		"-launch-testprocess-completion="+completion,
		"-launch-testprocess-child-release="+release,
	)
	reaped := false
	t.Cleanup(func() {
		// Bound, fixture-owned cleanup only: kill and reap THIS child
		// if the probe failed before reaping it. Never a bare-pid
		// signal after a confirmed exit.
		if !reaped {
			_ = cmd.Process.Kill()
			_ = cmd.Wait() //nolint:errcheck // cleanup path only
		}
	})
	if err := cmd.Start(); err != nil {
		t.Fatalf("starting hold child: %v", err)
	}
	watch, err := watchFixtureChild(cmd.Process.Pid)
	if err != nil {
		t.Fatalf("binding hold child %d: %v", cmd.Process.Pid, err)
	}
	defer watch.Close() //nolint:errcheck // test-scoped observation handle

	// The child is provably alive (marker written, still holding for the
	// release), so the bound handle MUST read not-signaled here — the
	// deterministic negative side of the liveness contract.
	waitForFile(t, completion, 10*time.Second)
	gone, werr := watch.Exited()
	if werr != nil {
		t.Fatalf("probing the bound handle of the held child: %v", werr)
	}
	if gone {
		t.Fatalf("bound handle signalled while the hold child is provably alive and unreleased")
	}

	// Release: the bound handle must become signaled. The release is
	// published atomically (see publishFixtureFile) so the child can
	// never observe a created-but-empty or partial file.
	if err := publishFixtureFile(release, []byte("go")); err != nil {
		t.Fatalf("writing the release marker: %v", err)
	}
	signaled := false
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		gone, werr := watch.Exited()
		if werr != nil {
			t.Fatalf("probing the bound handle after release: %v", werr)
		}
		if gone {
			signaled = true
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !signaled {
		t.Fatalf("bound handle never signalled within 10s of releasing the hold child")
	}

	// The exit object EXISTS and stays SIGNALLED after the exit, with no
	// reaping in between: both a zero-timeout poll and a blocking wait
	// must answer WAIT_OBJECT_0, repeatedly. If this ever fails, every
	// exit observation in this harness loses its foundation.
	for i := 0; i < 3; i++ {
		ev, err := windows.WaitForSingleObject(watch.handle, windows.INFINITE)
		if err != nil {
			t.Fatalf("post-exit wait %d on the bound handle: %v", i+1, err)
		}
		if ev != waitEventObject0 {
			t.Fatalf("post-exit wait %d event = %d, want WAIT_OBJECT_0 (signalled exit object)", i+1, ev)
		}
	}

	// Reap this fixture-owned child and disarm the cleanup net.
	if err := cmd.Wait(); err != nil {
		t.Errorf("hold child exited with an unexpected error: %v", err)
	}
	reaped = true
}
