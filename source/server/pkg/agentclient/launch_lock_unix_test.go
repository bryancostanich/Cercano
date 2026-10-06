//go:build unix

package agentclient

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestUpgradeLaunchLockExcludesClients(t *testing.T) {
	dir := t.TempDir()
	// The old client helper and exported coordinator API share the actual
	// same acquisition function and lock filename.
	f, err := acquireLaunchLock(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { releaseAutoLaunchLock(f) }()
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	release, err := AcquireAutoLaunchLock(ctx, dir)
	if release != nil {
		release()
		t.Fatal("second holder acquired lock")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected deadline, got %v", err)
	}
	releaseAutoLaunchLock(f)
	f = nil
	release, err = AcquireAutoLaunchLock(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	release()
}

func TestUpgradeLaunchLockRejectsRelativeDirectory(t *testing.T) {
	if release, err := AcquireAutoLaunchLock(context.Background(), "relative"); err == nil {
		release()
		t.Fatal("relative directory accepted")
	}
}
