//go:build unix

package agentclient

import "syscall"

// detachSysProcAttr detaches the spawned server from the CLI's tty/process
// group (setsid) so it survives a CLI crash and remains available to other
// clients.
func detachSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setsid: true}
}
