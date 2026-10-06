package brewrestart

import (
	"errors"
	"net/netip"
	"syscall"
	"testing"
)

type fakeProcesses struct {
	pids          []int
	ids           map[int]Identity
	listeners     map[int]bool
	inspectErrors map[int]error
	listenErrors  map[int]error
	listError     error
	inspected     []int
	probed        []int
}

func (f *fakeProcesses) ListCandidates() ([]int, error) { return f.pids, f.listError }
func (f *fakeProcesses) Inspect(pid int) (Identity, error) {
	f.inspected = append(f.inspected, pid)
	return f.ids[pid], f.inspectErrors[pid]
}
func (f *fakeProcesses) HoldsListener(id Identity, _ netip.AddrPort) (bool, error) {
	f.probed = append(f.probed, id.PID)
	return f.listeners[id.PID], f.listenErrors[id.PID]
}

func TestDiscover(t *testing.T) {
	const newExe = "/opt/homebrew/Cellar/cercano/2/bin/cercano"
	endpoint := netip.MustParseAddrPort("127.0.0.1:12345")
	id := func(pid int) Identity {
		return Identity{PID: pid, UID: 501, Executable: "/opt/homebrew/Cellar/cercano/1/bin/cercano", StartSeconds: 10}
	}
	for _, tt := range []struct {
		name    string
		setup   func(*fakeProcesses)
		want    int
		wantErr bool
	}{
		{"absent", func(f *fakeProcesses) { f.pids = nil }, 0, false},
		{"owner", func(f *fakeProcesses) {}, 1, false},
		{"duplicate enumeration", func(f *fakeProcesses) { f.pids = []int{1, 1} }, 1, false},
		{"not listening", func(f *fakeProcesses) { f.listeners[1] = false }, 0, false},
		{"other user", func(f *fakeProcesses) { p := f.ids[1]; p.UID++; f.ids[1] = p }, 0, false},
		{"development binary", func(f *fakeProcesses) { p := f.ids[1]; p.Executable = "/tmp/dev/cercano"; f.ids[1] = p }, 0, false},
		{"ambiguous", func(f *fakeProcesses) { f.pids = []int{1, 2}; f.ids[2] = id(2); f.listeners[2] = true }, 0, true},
		{"disappeared", func(f *fakeProcesses) { f.inspectErrors[1] = syscall.ESRCH }, 0, false},
		{"disappeared during socket probe", func(f *fakeProcesses) { f.listenErrors[1] = syscall.ESRCH }, 0, false},
		{"unreadable identity", func(f *fakeProcesses) { f.inspectErrors[1] = syscall.EPERM }, 0, true},
		{"unstable socket snapshot", func(f *fakeProcesses) { f.listenErrors[1] = syscall.EAGAIN }, 0, true},
		{"list error", func(f *fakeProcesses) { f.listError = errors.New("denied") }, 0, true},
		{"mismatched PID", func(f *fakeProcesses) { f.ids[1] = id(2) }, 0, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			f := &fakeProcesses{pids: []int{1}, ids: map[int]Identity{1: id(1)}, listeners: map[int]bool{1: true}, inspectErrors: map[int]error{}, listenErrors: map[int]error{}}
			tt.setup(f)
			got, err := discover(f, newExe, 501, endpoint)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err %v", err)
			}
			pid := 0
			if got != nil {
				pid = got.PID
			}
			if pid != tt.want {
				t.Fatalf("got PID %d want %d", pid, tt.want)
			}
			if (tt.name == "development binary" || tt.name == "other user") && len(f.probed) != 0 {
				t.Fatal("probed a foreign process's sockets")
			}
		})
	}
	f := &fakeProcesses{}
	if _, err := discover(f, "/tmp/cercano", 501, endpoint); err == nil {
		t.Fatal("accepted invalid installation")
	}
	if _, err := discover(f, newExe, 501, netip.MustParseAddrPort("192.0.2.1:1")); err == nil {
		t.Fatal("accepted remote endpoint")
	}
	if len(f.inspected) > 0 {
		t.Fatal("invalid input reached process inspection")
	}
}
