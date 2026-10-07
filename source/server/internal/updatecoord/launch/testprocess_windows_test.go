//go:build windows

package launch

// job-parent helper for the Windows job-object fixtures. It creates a
// job it OWNS, assigns itself to it, then attempts the launch through
// the primitive:
//
//   - deny variant: the job forbids breakaway (JOB_OBJECT_LIMIT_
//     KILL_ON_JOB_CLOSE without JOB_OBJECT_LIMIT_BREAKAWAY_OK), so the
//     launch must be refused with the OS error surfaced and no child
//     started (CreateProcess is atomic: refusal means none). The
//     kill-on-close job is also the containment safety net for an
//     unexpectedly accepted child.
//   - allow variant: the job permits breakaway and kills on close, so
//     the launched child escapes the job and must outlive this parent's
//     exit — which closes the last job handle and terminates the job.
//
// No inherited, CI or global job is created, modified or bypassed: this
// job belongs to this process and dies with it.

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	jobVariantDeny  = "deny"
	jobVariantAllow = "allow"
)

func jobParentMain() {
	switch *jobVariant {
	case jobVariantDeny, jobVariantAllow:
	default:
		fmt.Fprintln(os.Stderr, "job-parent: unknown variant:", *jobVariant)
		os.Exit(3)
	}
	exe, err := helperExecutable()
	if err != nil {
		fmt.Fprintln(os.Stderr, "job-parent: own path:", err)
		os.Exit(3)
	}

	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		writeJobResult("job-create-failed: " + err.Error())
		os.Exit(3)
	}
	var info windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if *jobVariant == jobVariantAllow {
		info.BasicLimitInformation.LimitFlags |= windows.JOB_OBJECT_LIMIT_BREAKAWAY_OK
	}
	if _, err := windows.SetInformationJobObject(job,
		windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
		writeJobResult("job-set-failed: " + err.Error())
		os.Exit(3)
	}
	self, err := windows.GetCurrentProcess()
	if err != nil {
		writeJobResult("self-handle-failed: " + err.Error())
		os.Exit(3)
	}
	if err := windows.AssignProcessToJobObject(job, self); err != nil {
		writeJobResult("job-assign-failed: " + err.Error())
		os.Exit(3)
	}

	proc, err := Launch(Options{
		Executable: exe,
		Argv: []string{
			"-launch-testprocess-role=" + helperRoleFinalChild,
			"-launch-testprocess-completion=" + *completionMarkerPath,
			"-launch-testprocess-parent-done=" + *parentDoneMarkerPath,
			"-launch-testprocess-parent-pid=" + strconv.Itoa(os.Getpid()),
		},
		StdoutPath: *childLogPath,
		StderrPath: *childLogPath,
	})
	if err != nil {
		// No child exists, including here in the allow variant, where a
		// restrictive outer job chain (outside this fixture's control)
		// forbids the breakaway. The classification is recorded so the
		// fixture can distinguish the intended denial from anything
		// else instead of guessing.
		writeJobResult("launch-failed: " + classifyJobRefusal(err))
		os.Exit(0)
	}
	if *jobVariant == jobVariantDeny {
		// Must not happen: the restrictive job must have refused the
		// breakaway. The child stayed inside the fixture job and dies
		// with it when this process exits; record the bug loudly.
		writeJobResult("accepted-unexpected: pid=" + strconv.Itoa(proc.Pid()))
		os.Exit(3)
	}
	if err := os.WriteFile(*parentDoneMarkerPath, []byte("gone"), 0o600); err != nil {
		writeJobResult("marker-failed: " + err.Error())
		os.Exit(3)
	}
	if err := os.WriteFile(*childPidFilePath, []byte(strconv.Itoa(proc.Pid())), 0o600); err != nil {
		writeJobResult("pidfile-failed: " + err.Error())
		os.Exit(3)
	}
	writeJobResult("launched: pid=" + strconv.Itoa(proc.Pid()))
	os.Exit(0)
}

// classifyJobRefusal names the OS refusal explicitly.
func classifyJobRefusal(err error) string {
	switch {
	case errors.Is(err, windows.ERROR_ACCESS_DENIED):
		return "breakaway-denied(access-denied): " + err.Error()
	case errors.Is(err, windows.ERROR_INVALID_PARAMETER):
		return "creation-flags-or-params-invalid(invalid-parameter): " + err.Error()
	default:
		return "other: " + err.Error()
	}
}

func writeJobResult(text string) {
	_ = os.WriteFile(*jobResultPath, []byte(text+"\n"), 0o600)
}
