//go:build windows

package launch

// TEST-ONLY native Windows launch diagnostics for the CI refusals observed
// in run 37664479558 (ordinary Launch tests failing with AccessDenied under
// CREATE_BREAKAWAY_FROM_JOB; the detach chain's intermediate parent failing
// so the fixture hit the pidfile timeout). Nothing here is production code
// and nothing changes production launch behavior, any job policy, any CI or
// parent job limit, or any workflow: these diagnostics only OBSERVE and
// REPORT, and the normal production launch tests stay red until the refusal
// reason is resolved.
//
// The question being answered: is the production flag set
// (CREATE_NEW_PROCESS_GROUP | DETACHED_PROCESS | CREATE_BREAKAWAY_FROM_JOB)
// refused because
//
//   (a) a job in the process's chain forbids breakaway (JOB POLICY —
//       ERROR_ACCESS_DENIED is the documented refusal when a job in the
//       chain lacks JOB_OBJECT_LIMIT_BREAKAWAY_OK), or
//   (b) the flag combination itself is rejected by CreateProcess (FLAGS —
//       ERROR_INVALID_PARAMETER), independent of any job?
//
// What is reported and how it must be interpreted:
//
//   - currentProcessJobReport reports the calling helper's own job context:
//     IsProcessInJob (NULL job handle = membership in ANY job) plus the
//     limit flags of the job the calling process belongs to
//     (QueryInformationJobObject(NULL, JobObjectExtendedLimitInformation)).
//     INTERPRETATION LIMITS: the NULL job handle exposes only the immediate
//     job of the calling process; nested job chains are NOT enumerable
//     through this API, and a restrictive OUTER job's flags are not shown
//     even though an outer job is exactly what can forbid the breakaway.
//     A missing JOB_OBJECT_LIMIT_BREAKAWAY_OK in the immediate job is
//     strong evidence for job policy, but its presence alone does not
//     prove the breakaway will succeed, and a chained outer job can refuse
//     it either way. The only decisive evidence for (a) vs (b) is the
//     classified CreateProcess outcome of the flag variants below, on a
//     real Windows host.
//
//   - the diagnostic test probes the SAME trusted test binary with three
//     EXACT creation-flag variants (no breakaway / the production breakaway
//     set / breakaway without the new process group) and classifies every
//     CreateProcess outcome. It ALWAYS reports and asserts nothing except
//     genuinely unexpected API outcomes; access-denied and
//     invalid-parameter refusals are the two candidate answers being
//     diagnosed, so they are reported, never counted as failures here.
//
//   - launchFailureReport annotates every refused Launch in the test
//     fixtures (persistStartError, launchEchoOnce) with the same
//     classification and job context, so the currently-red production
//     tests fail fast with self-describing errors in native CI output.

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// The three EXACT flag variants under diagnosis. These are TEST-ONLY
// probes: the production launch flags in launch_windows.go are not
// changed, consulted or special-cased.
const (
	// diagFlagVariantNoBreakaway: the production set minus
	// CREATE_BREAKAWAY_FROM_JOB. Refusal here means the problem is NOT
	// breakaway-related at all.
	diagFlagVariantNoBreakaway = detachedCreateNewProcessGroup | detachedDetachedProcess
	// diagFlagVariantProductionBreakaway: the exact production set.
	diagFlagVariantProductionBreakaway = detachedCreateNewProcessGroup | detachedDetachedProcess | detachedBreakawayFromJob
	// diagFlagVariantBreakawayNoNewGroup: breakaway without
	// CREATE_NEW_PROCESS_GROUP, isolating the group flag.
	diagFlagVariantBreakawayNoNewGroup = detachedDetachedProcess | detachedBreakawayFromJob
)

// IsProcessInJob is provided by neither x/sys/windows nor the standard
// syscall package, so this TEST-ONLY probe resolves it from kernel32
// explicitly. The signature matches the OS API: a NULL job handle tests
// membership in ANY job.
var (
	modkernel32        = windows.NewLazySystemDLL("kernel32.dll")
	procIsProcessInJob = modkernel32.NewProc("IsProcessInJob")
)

func isProcessInJob(proc windows.Handle) (bool, error) {
	var inJob int32
	r, _, callErr := procIsProcessInJob.Call(uintptr(proc), 0, uintptr(unsafe.Pointer(&inJob)))
	if r == 0 {
		return false, callErr
	}
	return inJob != 0, nil
}

// currentProcessJobReport reports the calling helper's own native job
// context: whether it is in any job at all, and the limit flags of its
// immediate job. See the file comment for the interpretation limits — in
// particular that a nested outer job's flags are invisible here.
func currentProcessJobReport() string {
	self, err := windows.GetCurrentProcess()
	if err != nil {
		return "current-process-handle-failed: " + err.Error()
	}
	inJob, err := isProcessInJob(self)
	if err != nil {
		return "is-process-in-job-failed: " + err.Error()
	}
	if !inJob {
		return "in-job=false (CREATE_BREAKAWAY_FROM_JOB is documented as a no-op when the parent is in no job)"
	}
	var info windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
	var returned uint32
	if err := windows.QueryInformationJobObject(0,
		windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info)), &returned); err != nil {
		return "in-job=true but QueryInformationJobObject(NULL, JobObjectExtendedLimitInformation) failed: " + err.Error()
	}
	flags := info.BasicLimitInformation.LimitFlags
	return fmt.Sprintf("in-job=true immediate-job-limit-flags=0x%08X breakaway-ok=%v silent-breakaway-ok=%v kill-on-close=%v die-on-unhandled-exception=%v",
		flags,
		flags&windows.JOB_OBJECT_LIMIT_BREAKAWAY_OK != 0,
		flags&windows.JOB_OBJECT_LIMIT_SILENT_BREAKAWAY_OK != 0,
		flags&windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE != 0,
		flags&windows.JOB_OBJECT_LIMIT_DIE_ON_UNHANDLED_EXCEPTION != 0)
}

// classifyCreateProcessRefusal names the OS refusal code explicitly. Both
// access-denied and invalid-parameter are CLASSIFIED outcomes, never
// failures of the diagnostics: they are the two candidate answers to the
// flags-vs-job-policy question. The remaining codes are unexpected for a
// verified absolute trusted test binary launched with documented flags.
func classifyCreateProcessRefusal(err error) (code, interpretation string, unexpected bool) {
	switch {
	case errors.Is(err, windows.ERROR_ACCESS_DENIED):
		return "access-denied",
			"consistent with job policy refusing the breakaway: a job in the chain lacks JOB_OBJECT_LIMIT_BREAKAWAY_OK (or the launch is otherwise denied by policy)",
			false
	case errors.Is(err, windows.ERROR_INVALID_PARAMETER):
		return "invalid-parameter",
			"consistent with a bad creation-flag combination rejected by CreateProcess itself, independent of job policy",
			false
	case errors.Is(err, windows.ERROR_FILE_NOT_FOUND):
		return "file-not-found", "unexpected for a verified absolute test binary", true
	case errors.Is(err, windows.ERROR_PATH_NOT_FOUND):
		return "path-not-found", "unexpected for a verified absolute test binary", true
	case errors.Is(err, windows.ERROR_ELEVATION_REQUIRED):
		return "elevation-required", "unexpected: the trusted test binary requires elevation", true
	default:
		return "unclassified", "unexpected: " + err.Error(), true
	}
}

// launchFailureReport is the TEST-ONLY report for a refused Launch: the
// classified CreateProcess outcome plus the calling helper's own job
// context, so native CI failure output carries the diagnosis with it.
func launchFailureReport(err error) string {
	code, interpretation, _ := classifyCreateProcessRefusal(err)
	return fmt.Sprintf("code=%s (%s) job-context=[%s] error=%v",
		code, interpretation, currentProcessJobReport(), err)
}

// probeMarkerContent is the one known temp marker the probe child writes.
const probeMarkerContent = "probe-child-ran"

// probeChildMain is the diagnostic probe child: it writes one known temp
// marker and exits. It inherits no console (DETACHED_PROCESS variants),
// its stdout/stderr are the null device, and it touches nothing else.
func probeChildMain() {
	if *probeMarkerPath == "" {
		fmt.Fprintln(os.Stderr, "probe-child: no marker path supplied")
		os.Exit(3)
	}
	if err := os.WriteFile(*probeMarkerPath, []byte(probeMarkerContent), 0o600); err != nil {
		fmt.Fprintln(os.Stderr, "probe-child: marker:", err)
		os.Exit(3)
	}
	os.Exit(0)
}

// probeCreateProcessFlagVariant starts the SAME trusted test binary with
// one EXACT creation-flag variant and reports the classified outcome. The
// probe child's output is discarded (nil stdio in os/exec is the null
// device) except for its known temp marker; the wait is bounded and the
// child — a fixture-owned child only — is reaped on every path, hard-
// killed only when the bound wait expires. No global, job or parent limit
// is read or changed: the probe only calls CreateProcess. os/exec passes
// SysProcAttr.CreationFlags through to CreateProcess verbatim, so the
// variants are exact.
func probeCreateProcessFlagVariant(t *testing.T, label string, flags uint32) (outcome string) {
	t.Helper()
	dir := t.TempDir()
	marker := filepath.Join(dir, "probe-marker")
	exe, err := helperExecutable()
	if err != nil {
		t.Fatalf("test binary path: %v", err)
	}
	cmd := exec.Command(exe,
		"-launch-testprocess-role="+helperRoleProbeChild,
		"-launch-testprocess-probe-marker="+marker,
	)
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: flags}
	if err := cmd.Start(); err != nil {
		code, interpretation, unexpected := classifyCreateProcessRefusal(err)
		diagEmit(t, "DIAGNOSTIC %s (flags 0x%08X): CreateProcess REFUSED code=%s: %s; error=%v",
			label, flags, code, interpretation, err)
		if unexpected {
			t.Errorf("DIAGNOSTIC %s: CreateProcess refused with an unexpected code %q for a verified trusted test binary: %v", label, code, err)
		}
		return "refused:" + code
	}
	pid := cmd.Process.Pid
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case <-time.After(10 * time.Second):
		// Bound wait expired: terminate and reap THIS fixture-owned child
		// only, then report the anomaly.
		_ = cmd.Process.Kill()
		<-done
		t.Errorf("DIAGNOSTIC %s: probe child %d did not exit within 10s (unexpected API behavior)", label, pid)
		return "bound-wait-exceeded"
	case waitErr := <-done:
		if waitErr != nil {
			t.Errorf("DIAGNOSTIC %s: probe child %d exited with an unexpected error: %v", label, pid, waitErr)
			return "child-exit-error"
		}
		data, readErr := os.ReadFile(marker)
		if readErr != nil || string(data) != probeMarkerContent {
			t.Errorf("DIAGNOSTIC %s: probe child %d exited 0 but its known temp marker is missing (read error %v)", label, pid, readErr)
			return "marker-missing"
		}
		diagEmit(t, "DIAGNOSTIC %s (flags 0x%08X): CreateProcess SUCCEEDED and the probe child ran to completion (pid %d)",
			label, flags, pid)
		return "succeeded"
	}
}

// diagEmit reports one diagnostic line both through t.Log and directly to
// the test binary's stderr. Non-verbose `go test` — the native CI
// invocation — hides t.Log output of passing tests and hides direct
// writes while the whole package passes; whenever ANY test in the package
// fails (exactly the state being diagnosed: the production launch tests
// stay red until the refusal reason is resolved), the direct writes are
// echoed into the CI log. Emission alone never changes any test outcome.
func diagEmit(t *testing.T, format string, args ...any) {
	t.Helper()
	t.Logf(format, args...)
	fmt.Fprintf(os.Stderr, format+"\n", args...)
}

// TestOneShotLaunch_DiagnoseCreateProcessFlagVariantsVsJobContext is the
// TEST-ONLY diagnostic for the Windows CI launch refusals. It always
// reports the current job context and the outcome of each of the three
// EXACT flag variants, and asserts nothing except genuinely unexpected
// API outcomes (see classifyCreateProcessRefusal, the bound-wait and
// marker checks in probeCreateProcessFlagVariant). It deliberately does
// NOT prove or disprove job independence, does not skip any other test,
// and does not change any production expectation.
//
// Reading the report:
//
//   - variant-1 succeeds and variant-2 is refused access-denied → job
//     policy: the flags are not bad; a job in the chain forbids breakaway.
//   - variant-2 or variant-3 is refused invalid-parameter → the flag
//     combination is rejected by CreateProcess itself: investigate the
//     flags.
//   - variant-1 is also refused → the refusal is not breakaway-related
//     at all; something else in the environment blocks the launch.
//
// PROOF LIMITS: this test runs only on Windows, so its evidence comes
// exclusively from the native Windows CI matrix (or any real Windows
// host); no local non-Windows run can contribute. If CI cannot run it,
// independence and the flags-vs-policy question need external Windows
// proof, not guesswork.
func TestOneShotLaunch_DiagnoseCreateProcessFlagVariantsVsJobContext(t *testing.T) {
	diagEmit(t, "DIAGNOSTIC job context of this test process: %s", currentProcessJobReport())
	probeCreateProcessFlagVariant(t, "variant-1-no-breakaway", diagFlagVariantNoBreakaway)
	probeCreateProcessFlagVariant(t, "variant-2-production-breakaway", diagFlagVariantProductionBreakaway)
	probeCreateProcessFlagVariant(t, "variant-3-breakaway-without-new-group", diagFlagVariantBreakawayNoNewGroup)
	diagEmit(t, "DIAGNOSTIC complete: read the three variant outcomes above against the job context; this test asserts only genuinely unexpected API behavior")
}
