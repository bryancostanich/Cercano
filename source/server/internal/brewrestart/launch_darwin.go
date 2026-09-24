//go:build darwin && cgo

package brewrestart

/*
#include <errno.h>
#include <libproc.h>
#include <sys/sysctl.h>
#include <string.h>
#include <stdlib.h>

static int cercano_args(int pid, char **out, size_t *size) {
 int maxargs=0; size_t n=sizeof(maxargs);
 int limit[2]={CTL_KERN,KERN_ARGMAX};
 if(sysctl(limit,2,&maxargs,&n,NULL,0)!=0) return errno;
 if(maxargs<=0 || maxargs>16*1024*1024) return EOVERFLOW;
 char *buf=calloc(1,maxargs);
 if(!buf) return ENOMEM;
 n=maxargs; int mib[3]={CTL_KERN,KERN_PROCARGS2,pid};
 if(sysctl(mib,3,buf,&n,NULL,0)!=0) {int e=errno; free(buf); return e;}
 *out=buf; *size=n; return 0;
}
static int cercano_cwd(int pid, char *out) {
 struct proc_vnodepathinfo info;
 memset(&info,0,sizeof(info)); errno=0;
 if(proc_pidinfo(pid,PROC_PIDVNODEPATHINFO,0,&info,sizeof(info))!=sizeof(info)) return errno ? errno : ESRCH;
 if(!info.pvi_cdir.vip_path[0]) return ENOENT;
 memcpy(out,info.pvi_cdir.vip_path,sizeof(info.pvi_cdir.vip_path));
 return 0;
}
*/
import "C"

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"unsafe"
)

// CaptureLaunchState binds the captured state to a verified process identity.
// It does not stop the process. The coordinator must revalidate identity and
// socket ownership immediately before requesting shutdown.
func CaptureLaunchState(expected Identity) (LaunchState, error) {
	before, err := Inspect(expected.PID)
	if err != nil {
		return LaunchState{}, err
	}
	if !before.SameProcess(expected) || before.UID != uint32(os.Getuid()) {
		return LaunchState{}, fmt.Errorf("process identity changed or is not owned by this user")
	}
	var data *C.char
	var size C.size_t
	if code := C.cercano_args(C.int(expected.PID), &data, &size); code != 0 {
		return LaunchState{}, fmt.Errorf("read launch state: %w", syscall.Errno(code))
	}
	defer C.free(unsafe.Pointer(data))
	raw := C.GoBytes(unsafe.Pointer(data), C.int(size))
	args, env, err := parseProcArgs(raw)
	clear(raw)
	if err != nil {
		return LaunchState{}, err
	}
	var cwd [C.MAXPATHLEN]C.char
	if code := C.cercano_cwd(C.int(expected.PID), &cwd[0]); code != 0 {
		return LaunchState{}, fmt.Errorf("read working directory: %w", syscall.Errno(code))
	}
	dir := C.GoString(&cwd[0])
	if !filepath.IsAbs(dir) {
		return LaunchState{}, fmt.Errorf("working directory is not absolute")
	}
	after, err := Inspect(expected.PID)
	if err != nil {
		return LaunchState{}, err
	}
	if !before.SameProcess(after) {
		return LaunchState{}, fmt.Errorf("process identity changed during launch-state capture")
	}
	return LaunchState{Identity: after, Args: args, Env: env, Directory: dir}, nil
}
