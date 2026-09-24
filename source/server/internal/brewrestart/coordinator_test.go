package brewrestart

import (
	"context"
	"errors"
	"net/netip"
	"reflect"
	"testing"
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
