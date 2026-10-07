//go:build windows

package agentclient

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

const (
	helperEnv      = "CERCANO_LAUNCH_LOCK_HELPER"
	helperDirEnv   = "CERCANO_LAUNCH_LOCK_DIR"
	helperReadyEnv = "CERCANO_LAUNCH_LOCK_READY"
)

func TestWindowsLaunchLockWorker(t *testing.T) {
	if os.Getenv(helperEnv) != "1" {
		t.Skip("owned subprocess fixture")
	}
	runLaunchLockHelper()
}

// runLaunchLockHelper is the child side of the process-death test. It is a
// helper binary built from the test executable itself — no real agent is
// started or terminated. It acquires the lock in the caller-supplied
// directory, signals readiness, and holds the lock until the parent kills it
// (or a safety timeout expires, releasing the lock via defer).
func runLaunchLockHelper() {
	dir := os.Getenv(helperDirEnv)
	ready := os.Getenv(helperReadyEnv)
	release, err := AcquireAutoLaunchLock(context.Background(), dir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "helper acquire:", err)
		os.Exit(1)
	}
	defer release()
	if err := os.WriteFile(ready, []byte("locked"), 0600); err != nil {
		fmt.Fprintln(os.Stderr, "helper ready write:", err)
		os.Exit(1)
	}
	// Hold until killed. Process termination closes all handles, which is
	// exactly what the parent test wants to observe.
	time.Sleep(2 * time.Minute)
}

func TestWindowsLaunchLockCanceledBeforeCreation(t *testing.T) {
	dir := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	release, err := AcquireAutoLaunchLock(ctx, dir)
	if err == nil {
		release()
		t.Fatal("acquired lock with canceled context")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
	if _, serr := os.Stat(filepath.Join(dir, "cercano-agent-launch.lock")); !errors.Is(serr, os.ErrNotExist) {
		t.Fatalf("lock file created despite canceled context: %v", serr)
	}
}

func TestWindowsLaunchLockContentionDeadline(t *testing.T) {
	dir := t.TempDir()
	release, err := AcquireAutoLaunchLock(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	second, err := AcquireAutoLaunchLock(ctx, dir)
	if err == nil {
		second()
		t.Fatal("second holder acquired lock")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected deadline, got %v", err)
	}
}

func TestWindowsLaunchLockReleaseReacquire(t *testing.T) {
	dir := t.TempDir()
	release, err := AcquireAutoLaunchLock(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	release()
	release() // idempotent; must not unlock a stale handle or panic
	release2, err := AcquireAutoLaunchLock(context.Background(), dir)
	if err != nil {
		t.Fatalf("reacquire after release: %v", err)
	}
	release2()
	// Nil safety: private release used by ensureServerLaunched.
	releaseAutoLaunchLock(nil)
}

func TestWindowsLaunchLockConcurrentStartsSerialize(t *testing.T) {
	dir := t.TempDir()
	var mu sync.Mutex
	active, maxActive := 0, 0
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			release, err := AcquireAutoLaunchLock(ctx, dir)
			if err != nil {
				t.Error(err)
				return
			}
			defer release()
			mu.Lock()
			active++
			if active > maxActive {
				maxActive = active
			}
			mu.Unlock()
			time.Sleep(10 * time.Millisecond)
			mu.Lock()
			active--
			mu.Unlock()
		}()
	}
	wg.Wait()
	if maxActive != 1 {
		t.Fatalf("max concurrent holders = %d, want 1", maxActive)
	}
}

func TestWindowsLaunchLockChildProcessDeathReleases(t *testing.T) {
	dir := t.TempDir()
	ready := filepath.Join(dir, "helper-ready.txt")
	workerCtx, stop := context.WithTimeout(context.Background(), 20*time.Second)
	defer stop()
	cmd := exec.CommandContext(workerCtx, os.Args[0], "-test.run=^TestWindowsLaunchLockWorker$", "-test.timeout=15s")
	cmd.Env = append(os.Environ(),
		helperEnv+"=1",
		helperDirEnv+"="+dir,
		helperReadyEnv+"="+ready,
	)
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()
	waitForReady := func() bool {
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			if _, err := os.Stat(ready); err == nil {
				return true
			}
			time.Sleep(10 * time.Millisecond)
		}
		return false
	}
	if !waitForReady() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		t.Fatal("helper never acquired the lock")
	}
	// While the child holds the lock, this process must not acquire it.
	shortCtx, cancelShort := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancelShort()
	if second, err := AcquireAutoLaunchLock(shortCtx, dir); err == nil {
		second()
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		t.Fatal("acquired lock while child process held it")
	} else if !errors.Is(err, context.DeadlineExceeded) {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		t.Fatalf("expected deadline while child held lock, got %v", err)
	}
	// Kill the child; the OS closes its handle and releases the byte-range
	// lock without any unlink of the lock file.
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Wait(); err != nil {
		t.Logf("helper exit: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	release, err := AcquireAutoLaunchLock(ctx, dir)
	if err != nil {
		t.Fatalf("lock not released after child death: %v", err)
	}
	release()
}

// The real context cancels immediately after the first successful Err check,
// modeling cancellation while filesystem preparation is in progress.
type cancelAfterFirstCheck struct {
	context.Context
	cancel context.CancelFunc
	once   sync.Once
}

func (c *cancelAfterFirstCheck) Err() error { err := c.Context.Err(); c.once.Do(c.cancel); return err }
func TestWindowsLaunchLockCancellationDuringPreparation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	wrapped := &cancelAfterFirstCheck{Context: ctx, cancel: cancel}
	release, err := AcquireAutoLaunchLock(wrapped, t.TempDir())
	if release != nil {
		release()
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("returned lock after cancellation: %v", err)
	}
}
