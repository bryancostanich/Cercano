//go:build darwin && cgo

package brewrestart

/*
#include <errno.h>
#include <libproc.h>
#include <string.h>

// Read the identity twice around the executable-path query. A disappearing
// process, credential change or PID reuse must never produce a mixed snapshot.
static int cercano_identity(int pid, struct proc_bsdinfo *info, char *path) {
 struct proc_bsdinfo before;
 memset(&before, 0, sizeof(before));
 memset(info, 0, sizeof(*info));
 errno = 0;
 if (proc_pidinfo(pid, PROC_PIDTBSDINFO, 0, &before, sizeof(before)) != sizeof(before))
  return errno ? errno : ESRCH;
 if (proc_pidpath(pid, path, PROC_PIDPATHINFO_MAXSIZE) <= 0)
  return errno ? errno : ESRCH;
 if (proc_pidinfo(pid, PROC_PIDTBSDINFO, 0, info, sizeof(*info)) != sizeof(*info))
  return errno ? errno : ESRCH;
 if (before.pbi_start_tvsec != info->pbi_start_tvsec ||
     before.pbi_start_tvusec != info->pbi_start_tvusec ||
     before.pbi_uid != info->pbi_uid) return EAGAIN;
 return 0;
}
*/
import "C"

import (
	"fmt"
	"syscall"
)

// Inspect reads a single process's identity using libproc. It neither connects
// to an agent nor sends signals. Callers must revalidate before acting and
// separately verify which process owns the target socket.
func Inspect(pid int) (Identity, error) {
	if pid <= 0 || int64(pid) > 2147483647 {
		return Identity{}, fmt.Errorf("invalid PID %d", pid)
	}
	var info C.struct_proc_bsdinfo
	var path [C.PROC_PIDPATHINFO_MAXSIZE]C.char
	if code := C.cercano_identity(C.int(pid), &info, &path[0]); code != 0 {
		return Identity{}, fmt.Errorf("inspect PID %d: %w", pid, syscall.Errno(code))
	}
	return Identity{
		PID: pid, UID: uint32(info.pbi_uid), Executable: C.GoString(&path[0]),
		StartSeconds: uint64(info.pbi_start_tvsec), StartMicroseconds: uint64(info.pbi_start_tvusec),
	}, nil
}
