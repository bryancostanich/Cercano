//go:build !windows

package launch

import (
	"fmt"
	"os"
)

// launchFailureReport: this platform has no native job-object diagnostics,
// so the TEST-ONLY failure-reporting hook (persistStartError,
// launchEchoOnce) degrades to the plain error. The Windows implementation
// with the job-context report lives in launch_diag_windows_test.go.
func launchFailureReport(err error) string {
	return "unclassified-platform-error: " + err.Error()
}

// probeChildMain is classified unsupported here: the probe child exists
// only for the Windows CreateProcess flag diagnostics, and pretending a
// probe role on other platforms would claim diagnostics they do not have.
func probeChildMain() {
	fmt.Fprintln(os.Stderr, "probe-child: unsupported on this platform")
	os.Exit(3)
}
