//go:build !unix

package llamaserver

import "os/exec"

// configureCancelKillsGroup is a no-op where POSIX process groups aren't
// available. Windows has no process-group kill primitive here; a cancelled
// ctx still kills the direct winget child via exec's default Cancel — only
// the "kill the whole tree including backgrounded grandchildren" behavior
// (which winget doesn't need) is unavailable.
func configureCancelKillsGroup(cmd *exec.Cmd) {}
