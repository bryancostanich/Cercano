//go:build darwin && cgo

package brewrestart

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestCaptureLaunchAndListener(t *testing.T) {
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe, "-test.run=^TestLaunchChild$", "--", "", "argument with spaces")
	cmd.Dir = dir
	cmd.Env = []string{"HOME=" + dir, "CERCANO_LAUNCH_CHILD=1", "LAUNCH_TEST_SECRET=private=value", "EMPTY="}
	cmd.Args[0] = "spoofed executable name"
	output, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	input, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	scan := bufio.NewScanner(output)
	if !scan.Scan() {
		t.Fatal("child never became ready")
	}
	endpoint, err := netip.ParseAddrPort(scan.Text())
	if err != nil {
		t.Fatal("invalid child endpoint")
	}
	identity, err := Inspect(cmd.Process.Pid)
	if err != nil {
		t.Fatal(err)
	}
	state, err := CaptureLaunchState(identity)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(state.Args, cmd.Args) {
		t.Fatalf("argument boundaries differ (got %d args, want %d)", len(state.Args), len(cmd.Args))
	}
	// macOS can add loader environment entries; verify all supplied entries.
	for _, want := range cmd.Env {
		found := false
		for _, entry := range state.Env {
			if entry == want {
				found = true
			}
		}
		if !found {
			t.Fatal("supplied environment entry missing")
		}
	}
	if state.Directory != dir {
		t.Fatal("working directory was not preserved")
	}
	if ok, err := HoldsListener(identity, endpoint); err != nil || !ok {
		t.Fatalf("expected listener: found=%v err=%v", ok, err)
	}
	stale := identity
	stale.StartMicroseconds++
	if _, err := CaptureLaunchState(stale); err == nil {
		t.Fatal("accepted stale launch identity")
	}
	if _, err := HoldsListener(stale, endpoint); err == nil {
		t.Fatal("accepted stale listener identity")
	}
	if _, err := HoldsListener(identity, netip.MustParseAddrPort("192.0.2.1:12345")); err == nil {
		t.Fatal("accepted remote endpoint")
	}
	if _, err := fmt.Fprintln(input, "close"); err != nil {
		t.Fatal(err)
	}
	if !scan.Scan() || scan.Text() != "closed" {
		t.Fatal("child did not close listener")
	}
	if ok, err := HoldsListener(identity, endpoint); err != nil || ok {
		t.Fatalf("closed listener: found=%v err=%v", ok, err)
	}
	_ = cmd.Process.Kill()
	_ = cmd.Wait()
	if _, err := CaptureLaunchState(identity); err == nil {
		t.Fatal("captured exited process")
	}
}

func TestLaunchChild(t *testing.T) {
	if os.Getenv("CERCANO_LAUNCH_CHILD") != "1" {
		t.Skip("subprocess only")
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	fmt.Println(listener.Addr())
	scanner := bufio.NewScanner(os.Stdin)
	if !scanner.Scan() || scanner.Text() != "close" {
		t.Fatal("missing fixture command")
	}
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	fmt.Println("closed")
	time.Sleep(time.Minute)
}
