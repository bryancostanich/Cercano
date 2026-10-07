//go:build windows

package launch

import "syscall"

// Deliberate, explicit Windows creation flags.
//
// CREATE_NEW_PROCESS_GROUP (0x00000200): the child becomes the root of its
// own console process group, so Ctrl-C / Ctrl-Break events generated for
// the initiating parent's group are not delivered to the child.
//
// DETACHED_PROCESS (0x00000008): the child inherits NO console at all
// instead of sharing the parent's, so a console close or handoff cannot
// terminate it and a console-subsystem utility neither blocks on the
// parent's console nor pops a window.
//
// Deliberately NOT set: CREATE_BREAKAWAY_FROM_JOB. Escape from a
// restrictive job object is neither claimed nor attempted. If the
// initiating parent runs inside a job that kills children on job close,
// the child stays inside that job and can be terminated with it; surviving
// such a job remains an explicit bootstrap review gate, not a property of
// this primitive.
const (
	detachedCreateNewProcessGroup = 0x00000200
	detachedDetachedProcess       = 0x00000008
)

// detachedSysProcAttr returns the Windows launch flags described above.
// The flags are constant by design: no caller-supplied flags, so no
// metadata-driven process configuration can sneak in here.
func detachedSysProcAttr() (*syscall.SysProcAttr, error) {
	return &syscall.SysProcAttr{
		CreationFlags: detachedCreateNewProcessGroup | detachedDetachedProcess,
	}, nil
}
