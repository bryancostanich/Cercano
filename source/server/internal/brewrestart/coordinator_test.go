package brewrestart

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"reflect"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestCoordinateRestartOrderingAndFailures(t *testing.T) {
	for _, fail := range []string{"", "capture", "preflight", "lock", "changed", "shutdown", "wait-exit", "start", "ready", "absent"} {
		t.Run(fail, func(t *testing.T) {
			id := Identity{PID: 1, UID: 501, Executable: "/opt/homebrew/Cellar/cercano/1/bin/cercano", StartSeconds: 1}
			source := &fakeProcesses{pids: []int{1}, ids: map[int]Identity{1: id}, listeners: map[int]bool{1: true}}
			if fail == "absent" {
				source.pids = nil
			}
			var calls []string
			step := func(name string) error {
				calls = append(calls, name)
				if name == fail {
					return errors.New("injected failure")
				}
				return nil
			}
			ops := restartOps{
				source:    source,
				capture:   func(Identity) (LaunchState, error) { return LaunchState{Identity: id}, step("capture") },
				preflight: func(string, LaunchState) error { return step("preflight") },
				lock: func(context.Context, LaunchState) (func(), error) {
					if err := step("lock"); err != nil {
						return nil, err
					}
					if fail == "changed" {
						q := id
						q.StartSeconds++
						source.ids[1] = q
					}
					return func() { calls = append(calls, "release") }, nil
				},
				shutdown: func(context.Context, Identity, netip.AddrPort) error { return step("shutdown") },
				waitExit: func(context.Context, Identity) error { return step("wait-exit") },
				start:    func(string, LaunchState) (Identity, error) { return Identity{PID: 2}, step("start") },
				ready:    func(context.Context, Identity, netip.AddrPort) error { return step("ready") },
			}
			restarted, err := coordinateRestart(context.Background(), ops, "/opt/homebrew/Cellar/cercano/2/bin/cercano", 501, netip.MustParseAddrPort("127.0.0.1:12345"))
			wantErr := fail != "" && fail != "absent"
			if (err != nil) != wantErr || restarted != (fail == "") {
				t.Fatalf("restarted=%v err=%v", restarted, err)
			}
			all := []string{"capture", "preflight", "lock", "shutdown", "wait-exit", "start", "ready"}
			var want []string
			if fail != "absent" {
				for _, name := range all {
					if fail == "changed" && name == "shutdown" {
						break
					}
					want = append(want, name)
					if fail == name {
						break
					}
				}
				if fail != "capture" && fail != "preflight" && fail != "lock" {
					want = append(want, "release")
				}
			}
			if !reflect.DeepEqual(calls, want) {
				t.Fatalf("calls %v want %v", calls, want)
			}
		})
	}
}

// An ambiguous safe-stop ending (deadline/transport) is resolved by a fresh
// bounded local inspection of the verified identity — never by guessing busy
// or left-alive from the client's own deadline, and never with a forced stop.
func TestCoordinateRestartUncertainStopResolution(t *testing.T) {
	id := Identity{PID: 1, UID: 501, Executable: "/opt/homebrew/Cellar/cercano/1/bin/cercano", StartSeconds: 1}
	for _, tt := range []struct {
		name          string
		expireDuring  bool                 // restart deadline expires while the stop wait blocks
		during        func(*fakeProcesses) // identity change after discovery, during the stop
		wantRestarted bool
		wantErrIn     string
	}{
		{name: "agent verifiably gone continues existing restart", during: func(f *fakeProcesses) {
			f.inspectErrors[1] = syscall.ESRCH
		}, wantRestarted: true},
		{name: "exact same agent alive is unconfirmed, never forced", wantErrIn: "same agent was present at the last inspection"},
		{name: "reused or foreign PID identity refuses restart", during: func(f *fakeProcesses) {
			reused := id
			reused.StartSeconds++
			f.ids[1] = reused
		}, wantErrIn: "could not be confirmed"},
		{name: "unreadable identity refuses restart", during: func(f *fakeProcesses) {
			f.inspectErrors[1] = syscall.EPERM
		}, wantErrIn: "could not be confirmed"},
		{name: "expired post-install deadline prohibits recovery", expireDuring: true,
			wantErrIn: "could not be confirmed"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			source := &fakeProcesses{pids: []int{1}, ids: map[int]Identity{1: id}, listeners: map[int]bool{1: true},
				inspectErrors: map[int]error{}, listenErrors: map[int]error{}}
			var calls []string
			starts := 0
			ops := restartOps{
				source:    source,
				capture:   func(Identity) (LaunchState, error) { return LaunchState{Identity: id}, nil },
				preflight: func(string, LaunchState) error { return nil },
				lock:      func(context.Context, LaunchState) (func(), error) { return func() {}, nil },
				shutdown: func(ctx context.Context, _ Identity, _ netip.AddrPort) error {
					if tt.during != nil {
						tt.during(source)
					}
					if tt.expireDuring {
						<-ctx.Done() // the bounded stop wait ends at the caller's deadline
					}
					return fmt.Errorf("%w: ambiguous ending", ErrSafeStopUncertain)
				},
				waitExit: func(context.Context, Identity) error { calls = append(calls, "wait-exit"); return nil },
				start: func(string, LaunchState) (Identity, error) {
					starts++
					calls = append(calls, "start")
					return Identity{PID: 2}, nil
				},
				ready: func(context.Context, Identity, netip.AddrPort) error { calls = append(calls, "ready"); return nil },
			}
			budget := 5 * time.Second
			if tt.expireDuring {
				budget = 100 * time.Millisecond
			}
			ctx, cancel := context.WithTimeout(context.Background(), budget)
			defer cancel()
			restarted, err := coordinateRestart(ctx, ops, "/opt/homebrew/Cellar/cercano/2/bin/cercano", 501, netip.MustParseAddrPort("127.0.0.1:12345"))
			if (err != nil) != (tt.wantErrIn != "") || restarted != tt.wantRestarted {
				t.Fatalf("restarted=%v err=%v", restarted, err)
			}
			if err != nil {
				if !isSafeStopUncertain(err) {
					t.Fatalf("err=%v, want typed uncertain outcome", err)
				}
				if !strings.Contains(err.Error(), tt.wantErrIn) {
					t.Fatalf("err=%v, want it to report %q", err, tt.wantErrIn)
				}
				if strings.Contains(err.Error(), "busy") {
					t.Fatalf("err=%v must not claim busy from the client deadline alone", err)
				}
			}
			if tt.wantRestarted {
				if !reflect.DeepEqual(calls, []string{"wait-exit", "start", "ready"}) {
					t.Fatalf("calls=%v, want the existing restart sequence after a positive exit", calls)
				}
				return
			}
			if starts != 0 || len(calls) != 0 {
				t.Fatalf("starts=%d calls=%v, want no forced stop, no exit wait and no replacement", starts, calls)
			}
		})
	}
}
