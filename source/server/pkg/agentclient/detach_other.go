//go:build !unix

package agentclient

import "syscall"

// detachSysProcAttr: POSIX session detachment (setsid) has no equivalent
// here; the spawned server process is independent of the CLI's console by
// default on this platform.
func detachSysProcAttr() *syscall.SysProcAttr {
	return nil
}
