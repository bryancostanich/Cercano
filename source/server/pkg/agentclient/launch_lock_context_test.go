//go:build unix

package agentclient

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestLaunchLockHonorsCancellation(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("TMPDIR", t.TempDir())
	first, err := acquireAutoLaunchLock()
	if err != nil {
		t.Fatal(err)
	}
	defer releaseAutoLaunchLock(first)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if l, err := acquireAutoLaunchLockContext(ctx); !errors.Is(err, context.Canceled) {
		releaseAutoLaunchLock(l)
		t.Fatalf("cancelled launch lock=%v", err)
	}
	ctx, cancel = context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	start := time.Now()
	if l, err := acquireAutoLaunchLockContext(ctx); !errors.Is(err, context.DeadlineExceeded) {
		releaseAutoLaunchLock(l)
		t.Fatalf("deadline lock=%v", err)
	}
	if time.Since(start) > time.Second {
		t.Fatal("launch lock cancellation was not bounded")
	}
}
