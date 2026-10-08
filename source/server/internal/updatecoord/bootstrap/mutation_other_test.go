//go:build !windows

package bootstrap

// mutationPreventedByOS is always false on this platform family: POSIX
// allows rebinding or renaming away a pathname whose file is open, so the
// rename-based adversarial mutations are expected to succeed. Any rename
// error is a genuine test failure, never an accepted "prevention".
func mutationPreventedByOS(err error) bool { return false }
