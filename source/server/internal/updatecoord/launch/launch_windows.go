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
// CREATE_BREAKAWAY_FROM_JOB (0x01000000): fail-closed job independence.
// Detachment alone does NOT make the child outlive a parent job that
// kills children on job close; without breakaway the child stays inside
// the parent's job object and dies with it. With this flag the child is
// created outside every job in the parent's chain — but only when the OS
// permits it: every nested job in the chain must set
// JOB_OBJECT_LIMIT_BREAKAWAY_OK, and a restrictive outer job (a CI or
// deployment policy this primitive must not bypass) therefore refuses
// the launch. When the parent is in no job at all, the flag is a no-op.
// If the OS denies the breakaway, CreateProcess fails and Launch returns
// that error: there is NO silent fallback to a child that would die with
// the parent's job, and no attempt to change any job's policy. A parent
// that cannot independently launch must fail closed.
const (
	detachedCreateNewProcessGroup = 0x00000200
	detachedDetachedProcess       = 0x00000008
	detachedBreakawayFromJob      = 0x01000000
)

// detachedSysProcAttr returns the Windows launch flags described above.
// The flags are constant by design: no caller-supplied flags, so no
// metadata-driven process configuration can sneak in here.
func detachedSysProcAttr() (*syscall.SysProcAttr, error) {
	return &syscall.SysProcAttr{
		CreationFlags: detachedCreateNewProcessGroup | detachedDetachedProcess | detachedBreakawayFromJob,
	}, nil
}
