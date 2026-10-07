//go:build !unix && !windows

package launch

import "syscall"

// detachedSysProcAttr fails closed on platforms without an implemented
// independent-process launch: starting a child that shares the parent's
// process group or console would let parent termination kill it, which is
// exactly the accident this primitive exists to prevent.
func detachedSysProcAttr() (*syscall.SysProcAttr, error) {
	return nil, ErrUnsupportedPlatform
}
