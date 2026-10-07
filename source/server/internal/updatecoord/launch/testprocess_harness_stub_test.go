//go:build !windows

package launch

import (
	"fmt"
	"os"
	"testing"
)

// launchEchoOncePositive: the owned permissive job launch harness is a
// Windows-only concept; on every other platform launchEchoOnce uses its
// direct launch path, so there is no harness dispatch here.
func launchEchoOncePositive(t *testing.T, stdoutPath, stderrPath string, extraBytes int) (int, bool) {
	return 0, false
}

// ownedEchoMain is classified unsupported here: the owned permissive job
// launch harness exists only for the Windows job-object context, and
// pretending one would claim job semantics this platform does not have.
func ownedEchoMain() {
	fmt.Fprintln(os.Stderr, "owned-echo: unsupported on this platform")
	os.Exit(3)
}
