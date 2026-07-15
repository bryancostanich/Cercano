//go:build !unix

package llamaserver

import "os/exec"

// configureCancelKillsGroup is a no-op where POSIX process groups aren't
// available. Managed install is unsupported on those platforms (installCommand
// returns ErrUnsupported before a subprocess is ever started), so the
// cancel-kill path is unreachable there.
func configureCancelKillsGroup(cmd *exec.Cmd) {}
