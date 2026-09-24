//go:build darwin && cgo

package brewrestart

/*
#include <errno.h>
#include <libproc.h>
#include <stdlib.h>
#include <string.h>
#include <arpa/inet.h>

// Examine one known process, not the system-wide process list. Return an
// error on an unstable/incomplete descriptor snapshot rather than guessing.
static int cercano_listener(int pid, const char *address, int port, int *found) {
 errno=0;
 int n=proc_pidinfo(pid,PROC_PIDLISTFDS,0,NULL,0);
 if(n<=0) return errno ? errno : ESRCH;
 if(n>16*1024*1024) return EOVERFLOW;
 int capacity=n+32*sizeof(struct proc_fdinfo);
 struct proc_fdinfo *fds=calloc(1,capacity);
 if(!fds) return ENOMEM;
 errno=0;
 n=proc_pidinfo(pid,PROC_PIDLISTFDS,0,fds,capacity);
 if(n<=0 || n>=capacity || n%sizeof(*fds)) {int e=errno ? errno : EAGAIN;free(fds);return e;}
 *found=0;
 for(int i=0;i<n/sizeof(*fds);i++) {
  if(fds[i].proc_fdtype!=PROX_FDTYPE_SOCKET) continue;
  struct socket_fdinfo s; memset(&s,0,sizeof(s)); errno=0;
  if(proc_pidfdinfo(pid,fds[i].proc_fd,PROC_PIDFDSOCKETINFO,&s,sizeof(s))!=sizeof(s)) {
   int e=errno ? errno : EAGAIN;free(fds);return e;
  }
  if(s.psi.soi_kind!=SOCKINFO_TCP || s.psi.soi_proto.pri_tcp.tcpsi_state!=TSI_S_LISTEN) continue;
  struct in_sockinfo *in=&s.psi.soi_proto.pri_tcp.tcpsi_ini;
  if(ntohs((uint16_t)in->insi_lport)!=port) continue;
  char text[INET6_ADDRSTRLEN]; const char *converted=NULL;
  if(s.psi.soi_family==AF_INET) converted=inet_ntop(AF_INET,&in->insi_laddr.ina_46.i46a_addr4,text,sizeof(text));
  if(s.psi.soi_family==AF_INET6) converted=inet_ntop(AF_INET6,&in->insi_laddr.ina_6,text,sizeof(text));
  if(converted && !strcmp(address,text)) *found=1;
 }
 free(fds);return 0;
}
*/
import "C"

import (
	"fmt"
	"net/netip"
	"os"
	"syscall"
	"unsafe"
)

// HoldsListener verifies a known same-user process owns an exact loopback TCP
// listener. Wildcard listeners are deliberately not accepted. This is a
// per-candidate check, not discovery or a proof of unique socket ownership.
func HoldsListener(expected Identity, endpoint netip.AddrPort) (bool, error) {
	if !endpoint.IsValid() || !endpoint.Addr().IsLoopback() || endpoint.Port() == 0 || endpoint.Addr().Zone() != "" {
		return false, fmt.Errorf("restart requires an explicit loopback TCP endpoint")
	}
	before, err := Inspect(expected.PID)
	if err != nil {
		return false, err
	}
	if !before.SameProcess(expected) || before.UID != uint32(os.Getuid()) {
		return false, fmt.Errorf("listener process identity changed or belongs to another user")
	}
	address := C.CString(endpoint.Addr().String())
	defer C.free(unsafe.Pointer(address))
	var found C.int
	if code := C.cercano_listener(C.int(expected.PID), address, C.int(endpoint.Port()), &found); code != 0 {
		return false, fmt.Errorf("inspect process listener: %w", syscall.Errno(code))
	}
	after, err := Inspect(expected.PID)
	if err != nil {
		return false, err
	}
	if !before.SameProcess(after) {
		return false, fmt.Errorf("process changed during socket inspection")
	}
	return found != 0, nil
}
