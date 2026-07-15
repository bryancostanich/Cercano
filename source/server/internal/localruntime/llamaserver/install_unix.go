//go:build unix

package llamaserver

import (
	"os/exec"
	"syscall"
)

// configureCancelKillsGroup makes a cancelled context tear down the whole
// process group rather than just the direct child.
//
// exec.CommandContext's default cancel signals only cmd.Process. A shell like
// `sh -c "step-that-backgrounds-a-helper & …"` (real package managers do this)
// leaves that grandchild alive, and because it inherited the stdout/stderr
// pipe's write end, Install's line drain never reaches EOF — Wait blocks and a
// "Cancel" click hangs. Putting the child in its own process group (Setpgid)
// and signalling the negated PGID reaps the entire tree, so the pipes close and
// Install returns ctx.Err() promptly.
func configureCancelKillsGroup(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		// Negative pid targets the process group led by the child (its PGID
		// equals its PID because of Setpgid above). ESRCH means it already
		// exited — not an error worth surfacing.
		if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err != nil && err != syscall.ESRCH {
			return err
		}
		return nil
	}
}
