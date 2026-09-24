//go:build darwin && cgo

package brewrestart

/*
#include <errno.h>
#include <libproc.h>
#include <stdint.h>
static int cercano_list_user(uint32_t uid, int32_t *out, int bytes, int *error) {
 errno=0;
 int n=proc_listpids(PROC_UID_ONLY,uid,out,bytes);
 *error=(n<=0) ? errno : 0;
 return n;
}
*/
import "C"

import (
	"os"
	"syscall"
	"unsafe"
)

// ListCandidates returns same-user PIDs, not verified agent identities.
// Every candidate must still pass Inspect, ownership and socket checks.
func ListCandidates() ([]int, error) {
	return collectPIDs(func(buf []int32) (int, error) {
		var code C.int
		n := C.cercano_list_user(C.uint32_t(os.Getuid()), (*C.int32_t)(unsafe.Pointer(&buf[0])), C.int(len(buf)*4), &code)
		if code != 0 {
			return 0, syscall.Errno(code)
		}
		return int(n), nil
	})
}
