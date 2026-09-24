//go:build darwin && cgo

package brewrestart

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// Only the child launched by this test is inspected or killed. Copying the
// test executable gives us a harmless real process in a fake keg;
// no Cercano agent, user config or installation is touched.
func TestInspectIgnoresArgvAndRepointedSymlink(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	oldExe := filepath.Join(root, "Cellar", "cercano", "1", "bin", "cercano")
	newExe := filepath.Join(root, "Cellar", "cercano", "2", "bin", "cercano")
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(self)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{oldExe, newExe} {
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, contents, 0700); err != nil {
			t.Fatal(err)
		}
	}
	link := filepath.Join(root, "cercano")
	if err := os.Symlink(oldExe, link); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, link, "-test.run=^TestIdentityChild$")
	cmd.Env = append(os.Environ(), "CERCANO_IDENTITY_CHILD=1")
	output, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd.Args[0] = newExe // Spoof the command text: kernel identity must win.
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	scanner := bufio.NewScanner(output)
	if !scanner.Scan() || scanner.Text() != "ready" {
		t.Fatal("fixture failed to become ready")
	}
	before, err := Inspect(cmd.Process.Pid)
	if err != nil {
		t.Fatal(err)
	}
	if before.Executable != oldExe || before.UID != uint32(os.Getuid()) || before.StartSeconds == 0 {
		t.Fatalf("unexpected kernel identity: %+v", before)
	}
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(newExe, link); err != nil {
		t.Fatal(err)
	}
	after, err := Inspect(cmd.Process.Pid)
	if err != nil {
		t.Fatal(err)
	}
	if !before.SameProcess(after) {
		t.Fatalf("symlink switch changed process identity: %+v -> %+v", before, after)
	}
	if err := CheckOwned(after, newExe, uint32(os.Getuid())); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = cmd.Wait()
	if _, err := Inspect(cmd.Process.Pid); err == nil {
		t.Fatal("exited process still inspectable")
	}
}

func TestInspectRejectsInvalidPID(t *testing.T) {
	for _, pid := range []int{0, -1} {
		if _, err := Inspect(pid); err == nil {
			t.Fatalf("accepted PID %d", pid)
		}
	}
}

func TestIdentityChild(t *testing.T) {
	if os.Getenv("CERCANO_IDENTITY_CHILD") != "1" {
		t.Skip("subprocess only")
	}
	fmt.Println("ready")
	time.Sleep(time.Minute)
}
