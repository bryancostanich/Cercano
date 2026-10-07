//go:build unix

package launch

import "syscall"

// detachedSysProcAttr starts the child in its own session (setsid): it
// becomes session leader with no controlling terminal and leaves the
// parent's process group. The parent's exit, its terminal signals and any
// process-group control targeted at the parent therefore cannot reach or
// terminate the child. This mirrors the existing agentclient detach
// pattern (pkg/agentclient/detach_unix.go).
func detachedSysProcAttr() (*syscall.SysProcAttr, error) {
	return &syscall.SysProcAttr{Setsid: true}, nil
}
