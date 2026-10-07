package exclusion

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"
)

func acquire(t *testing.T, root string, mode Mode) *Handle {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	h, e := Acquire(ctx, root, mode)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = h.Close() })
	return h
}
func blocked(t *testing.T, root string, mode Mode) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel()
	h, e := Acquire(ctx, root, mode)
	if h != nil {
		h.Close()
	}
	if !errors.Is(e, context.DeadlineExceeded) {
		t.Fatalf("expected bounded contention, got %v", e)
	}
}
func TestSharedAndExclusiveModes(t *testing.T) {
	root := t.TempDir()
	a := acquire(t, root, Launch)
	b := acquire(t, root, Launch)
	blocked(t, root, Update)
	a.Close()
	blocked(t, root, Update)
	b.Close()
	x := acquire(t, root, Update)
	blocked(t, root, Update)
	blocked(t, root, Launch)
	x.Close()
	acquire(t, root, Launch)
}
func TestStablePathCloseAndIndependentInstallations(t *testing.T) {
	root := t.TempDir()
	h := acquire(t, root, Update)
	acquire(t, t.TempDir(), Update)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if e := h.Close(); e != nil {
				t.Error(e)
			}
		}()
	}
	wg.Wait()
	if _, e := os.Stat(filepath.Join(root, "update.lock")); e != nil {
		t.Fatal("lock pathname removed", e)
	}
	acquire(t, root, Update)
}
func TestCancelledAcquireDoesNotCreateFile(t *testing.T) {
	root := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, e := Acquire(ctx, root, Update); !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
	if _, e := os.Stat(filepath.Join(root, "update.lock")); !os.IsNotExist(e) {
		t.Fatal("cancelled request created lock")
	}
}
func TestUnsafePathsAndModes(t *testing.T) {
	if _, e := Acquire(context.Background(), "relative", Update); e == nil {
		t.Fatal("relative root accepted")
	}
	root := t.TempDir()
	if _, e := Acquire(context.Background(), root, 0); e == nil {
		t.Fatal("unknown mode accepted")
	}
	if e := os.Mkdir(filepath.Join(root, "update.lock"), 0700); e != nil {
		t.Fatal(e)
	}
	if _, e := Acquire(context.Background(), root, Update); !errors.Is(e, ErrUnsafePath) {
		t.Fatal(e)
	}
}
func TestSymlinkRefused(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(t.TempDir(), "untouched")
	os.WriteFile(target, []byte("untouched"), 0600)
	if e := os.Symlink(target, filepath.Join(root, "update.lock")); e != nil {
		if runtime.GOOS == "windows" {
			t.Skip("symlink capability unavailable")
		}
		t.Fatal(e)
	}
	if _, e := Acquire(context.Background(), root, Update); !errors.Is(e, ErrUnsafePath) {
		t.Fatal(e)
	}
	b, e := os.ReadFile(target)
	if e != nil || string(b) != "untouched" {
		t.Fatal("target modified")
	}
}
func TestLockWorker(t *testing.T) {
	if os.Getenv("CERCANO_LOCK_FIXTURE") != "yes" {
		t.Skip("subprocess helper")
	}
	root := os.Getenv("CERCANO_LOCK_FIXTURE_ROOT")
	mode := Update
	if os.Getenv("CERCANO_LOCK_FIXTURE_MODE") == "launch" {
		mode = Launch
	}
	h := acquire(t, root, mode)
	if e := os.WriteFile(filepath.Join(root, "ready"), []byte("ready"), 0600); e != nil {
		t.Fatal(e)
	}
	for {
		if _, e := os.Stat(filepath.Join(root, "release")); e == nil {
			if e = h.Close(); e != nil {
				t.Fatal(e)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
}
func TestCrossProcessReleaseAndDeath(t *testing.T) {
	for _, mode := range []string{"update", "launch"} {
		for _, kill := range []bool{false, true} {
			name := mode + "/release"
			if kill {
				name = mode + "/death"
			}
			t.Run(name, func(t *testing.T) {
				root := t.TempDir()
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestLockWorker$", "-test.timeout=8s")
				cmd.Env = append(os.Environ(), "CERCANO_LOCK_FIXTURE=yes", "CERCANO_LOCK_FIXTURE_ROOT="+root, "CERCANO_LOCK_FIXTURE_MODE="+mode)
				if e := cmd.Start(); e != nil {
					t.Fatal(e)
				}
				done := make(chan error, 1)
				go func() { done <- cmd.Wait() }()
				reaped := false
				defer func() {
					_ = cmd.Process.Kill()
					if !reaped {
						<-done
					}
				}()
				for {
					if _, e := os.Stat(filepath.Join(root, "ready")); e == nil {
						break
					}
					select {
					case e := <-done:
						reaped = true
						t.Fatalf("worker exited before ready: %v", e)
					case <-ctx.Done():
						t.Fatal(ctx.Err())
					case <-time.After(10 * time.Millisecond):
					}
				}
				blocked(t, root, Update)
				if mode == "update" {
					blocked(t, root, Launch)
				} else {
					h := acquire(t, root, Launch)
					h.Close()
				}
				if kill {
					if e := cmd.Process.Kill(); e != nil {
						t.Fatal(e)
					}
				} else {
					if e := os.WriteFile(filepath.Join(root, "release"), nil, 0600); e != nil {
						t.Fatal(e)
					}
				}
				e := <-done
				reaped = true
				if !kill && e != nil {
					t.Fatal(e)
				}
				acquire(t, root, Update)
			})
		}
	}
}
