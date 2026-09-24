//go:build darwin && cgo

package brewrestart

import (
	"bufio"
	"context"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// Restrict enumeration to the fixture. All identity and listener inspection
// uses production libproc code; no developer process is inspected or touched.
type fixtureProcesses struct {
	kernelProcesses
	pid int
}

func (p fixtureProcesses) ListCandidates() ([]int, error) { return []int{p.pid}, nil }

func TestDiscoverIsolatedInstalledListener(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	bytes, err := os.ReadFile(self)
	if err != nil {
		t.Fatal(err)
	}
	oldExe := filepath.Join(root, "Cellar", "cercano", "1", "bin", "cercano")
	newExe := filepath.Join(root, "Cellar", "cercano", "2", "bin", "cercano")
	if err := os.MkdirAll(filepath.Dir(oldExe), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(oldExe, bytes, 0700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, oldExe, "-test.run=^TestLaunchChild$")
	cmd.Env = []string{"HOME=" + root, "CERCANO_LAUNCH_CHILD=1"}
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
	endpoint, err := netip.ParseAddrPort(scanner.Text())
	if err != nil {
		t.Fatal(err)
	}
	source := fixtureProcesses{pid: cmd.Process.Pid}
	owner, err := discover(source, newExe, uint32(os.Getuid()), endpoint)
	if err != nil || owner == nil || owner.PID != cmd.Process.Pid {
		t.Fatalf("owner %v err %v", owner, err)
	}
	wrongPrefix := filepath.Join(root, "other", "Cellar", "cercano", "2", "bin", "cercano")
	owner, err = discover(source, wrongPrefix, uint32(os.Getuid()), endpoint)
	if err != nil || owner != nil {
		t.Fatalf("foreign installation selected: %v %v", owner, err)
	}
}
