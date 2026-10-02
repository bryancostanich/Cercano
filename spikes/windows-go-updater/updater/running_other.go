//go:build !windows

package updater

// anyBinaryRunningHost is the non-Windows branch of the "is a required
// binary currently running from the active version dir" check.
//
// HONESTY NOTE (platform asymmetry, recorded in the README): on macOS/Linux,
// an open or running executable does NOT block rename/replace, so a
// versioned-directory activation never hits a lock on this platform and the
// running-state check is unnecessary for correctness. This branch therefore
// reports "not running". The observable POSIX fact — rename of a running
// binary succeeds — is captured by lockbehavior_test.go. Only Windows
// actually needs the probe (running_windows.go), and its Windows-only
// verification is explicitly left to Windows CI (documented as a gap).
func anyBinaryRunningHost(o Options) (bool, string) {
	return false, ""
}
