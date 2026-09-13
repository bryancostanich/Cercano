//go:build darwin || linux || freebsd || openbsd || netbsd || dragonfly

package statelease

import (
	"bufio"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestParticipantsAndResetExcludeEachOther(t *testing.T) {
	root := t.TempDir()
	a, err := Participate(root)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	b, err := Participate(root)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	if l, err := TryReset(root); !errors.Is(err, ErrBusy) {
		if l != nil {
			l.Close()
		}
		t.Fatalf("reset accepted active writers: %v", err)
	}
	a.Close()
	b.Close()
	b.Close()
	reset, err := TryReset(root)
	if err != nil {
		t.Fatal(err)
	}
	defer reset.Close()
	if l, err := Participate(root); !errors.Is(err, ErrBusy) {
		if l != nil {
			l.Close()
		}
		t.Fatalf("participant accepted during reset: %v", err)
	}
	if l, err := TryReset(root); !errors.Is(err, ErrBusy) {
		if l != nil {
			l.Close()
		}
		t.Fatalf("parallel reset accepted: %v", err)
	}
	reset.Close()
	c, err := Participate(root)
	if err != nil {
		t.Fatal(err)
	}
	c.Close()
}

func TestLeaseHelper(t *testing.T) {
	if os.Getenv("CERCANO_LEASE_TEST_HELPER") != "1" {
		return
	}
	root := os.Getenv("CERCANO_LEASE_TEST_ROOT")
	var l *Lease
	var err error
	if os.Getenv("CERCANO_LEASE_TEST_MODE") == "process" {
		err = HoldProcessLifetime()
		runtime.GC()
	} else if os.Getenv("CERCANO_LEASE_TEST_MODE") == "reset" {
		l, err = TryReset(root)
	} else {
		l, err = Participate(root)
	}
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	io.WriteString(os.Stdout, "ready\n")
	io.Copy(io.Discard, os.Stdin)
}
func childLease(t *testing.T, root, mode string) (*exec.Cmd, io.WriteCloser) {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestLeaseHelper$")
	cmd.Env = append(os.Environ(), "CERCANO_LEASE_TEST_HELPER=1", "CERCANO_LEASE_TEST_ROOT="+root, "CERCANO_LEASE_TEST_MODE="+mode)
	if mode == "process" {
		cmd.Env = append(cmd.Env, "HOME="+filepath.Dir(filepath.Dir(root)))
	}
	in, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stderr = os.Stderr
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { in.Close(); cmd.Process.Kill(); cmd.Wait() })
	ready := make(chan string, 1)
	go func() { s, _ := bufio.NewReader(out).ReadString('\n'); ready <- s }()
	select {
	case s := <-ready:
		if s != "ready\n" {
			t.Fatalf("child not ready: %q", s)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("child readiness timed out")
	}
	return cmd, in
}
func TestProcessLifetimeAndCrashRelease(t *testing.T) {
	root := t.TempDir()
	a, ai := childLease(t, root, "participant")
	b, bi := childLease(t, root, "participant")
	if l, err := TryReset(root); !errors.Is(err, ErrBusy) {
		if l != nil {
			l.Close()
		}
		t.Fatalf("reset accepted participants: %v", err)
	}
	ai.Close()
	if err := a.Wait(); err != nil {
		t.Fatal(err)
	}
	if l, err := TryReset(root); !errors.Is(err, ErrBusy) {
		if l != nil {
			l.Close()
		}
		t.Fatalf("reset ignored second participant: %v", err)
	}
	bi.Close()
	if err := b.Wait(); err != nil {
		t.Fatal(err)
	}
	reset, _ := childLease(t, root, "reset")
	if l, err := Participate(root); !errors.Is(err, ErrBusy) {
		if l != nil {
			l.Close()
		}
		t.Fatalf("startup ignored reset: %v", err)
	}
	if err := reset.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	reset.Wait()
	lease, err := TryReset(root)
	if err != nil {
		t.Fatalf("crashed reset retained lock: %v", err)
	}
	lease.Close()
}
func TestLeaseRejectsUnsafeLockTargets(t *testing.T) {
	for _, kind := range []string{"symlink", "directory", "permissions", "hardlink"} {
		t.Run(kind, func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, ".setup-state.lock")
			sentinel := filepath.Join(t.TempDir(), "preserved")
			if err := os.WriteFile(sentinel, []byte("keep"), 0600); err != nil {
				t.Fatal(err)
			}
			var err error
			switch kind {
			case "symlink":
				err = os.Symlink(sentinel, path)
			case "directory":
				err = os.Mkdir(path, 0700)
			case "permissions":
				err = os.WriteFile(path, nil, 0666)
				if err == nil {
					err = os.Chmod(path, 0666)
				}
			case "hardlink":
				err = os.Link(sentinel, path)
			}
			if err != nil {
				t.Fatal(err)
			}
			if lease, err := TryReset(root); err == nil {
				lease.Close()
				t.Fatal("unsafe lock accepted")
			}
			got, err := os.ReadFile(sentinel)
			if err != nil || string(got) != "keep" {
				t.Fatal("preserved file changed")
			}
		})
	}
}

func TestProcessLeaseSurvivesUntilExit(t *testing.T) {
	root := filepath.Join(t.TempDir(), ".cercano", "state")
	child, in := childLease(t, root, "process")
	if l, err := TryReset(root); !errors.Is(err, ErrBusy) {
		if l != nil {
			l.Close()
		}
		t.Fatalf("process lease released early: %v", err)
	}
	in.Close()
	if err := child.Wait(); err != nil {
		t.Fatal(err)
	}
	l, err := TryReset(root)
	if err != nil {
		t.Fatal(err)
	}
	l.Close()
}
