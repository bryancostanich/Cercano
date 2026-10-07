//go:build !windows

package launch

import (
	"fmt"
	"os"
)

// jobParentMain is classified unsupported here: the Windows job-object
// fixtures have no non-Windows implementation, and pretending one would
// claim job semantics this platform does not have.
func jobParentMain() {
	fmt.Fprintln(os.Stderr, "job-parent: unsupported on this platform")
	os.Exit(3)
}
