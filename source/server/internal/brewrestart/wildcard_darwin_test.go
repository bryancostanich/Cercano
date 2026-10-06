//go:build darwin && cgo

package brewrestart

import (
	"bufio"
	"context"
	"net"
	"net/netip"
	"os"
	"os/exec"
	"testing"
	"time"
)

func TestWildcardListenerMatchesLoopback(t *testing.T) {
	for _, network := range []string{"tcp", "tcp4", "tcp6"} {
		t.Run(network, func(t *testing.T) { testWildcardListener(t, network) })
	}
}

func testWildcardListener(t *testing.T, network string) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe, "-test.run=^TestLaunchChild$")
	cmd.Env = []string{"HOME=" + t.TempDir(), "CERCANO_LAUNCH_CHILD=1", "CERCANO_LAUNCH_WILDCARD=" + network}
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	in, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer in.Close()
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	scanner := bufio.NewScanner(out)
	if !scanner.Scan() {
		t.Fatal("child failed to start")
	}
	bound, err := netip.ParseAddrPort(scanner.Text())
	if err != nil {
		t.Fatal(err)
	}
	id, err := Inspect(cmd.Process.Pid)
	if err != nil {
		t.Fatal(err)
	}
	reachable := 0
	for _, addr := range []string{"127.0.0.1", "::1"} {
		endpoint := netip.AddrPortFrom(netip.MustParseAddr(addr), bound.Port())
		// Establish reachability before asking whether the kernel snapshot agrees.
		conn, err := net.DialTimeout("tcp", endpoint.String(), time.Second)
		want := err == nil
		if want {
			reachable++
			_ = conn.Close()
		}
		found, err := HoldsListener(id, endpoint)
		if err != nil || found != want {
			t.Errorf("endpoint %s: reachable=%v found=%v err=%v", endpoint, want, found, err)
		}
	}
	if reachable == 0 {
		t.Fatal("no reachable fixture endpoint")
	}
}
