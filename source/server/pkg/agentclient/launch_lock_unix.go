//go:build unix

package agentclient

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

func acquireAutoLaunchLock() (*os.File, error) {
	return acquireLaunchLock(context.Background(), os.TempDir())
}

// AcquireAutoLaunchLock coordinates an upgrade with CLI auto-launch. tempDir
// must come from the running agent's environment, not Homebrew's temporary
// build environment. The returned release function must always be called.
func AcquireAutoLaunchLock(ctx context.Context, tempDir string) (func(), error) {
	f, err := acquireLaunchLock(ctx, tempDir)
	if err != nil {
		return nil, err
	}
	return func() { releaseAutoLaunchLock(f) }, nil
}

func acquireLaunchLock(ctx context.Context, tempDir string) (*os.File, error) {
	if !filepath.IsAbs(tempDir) {
		return nil, fmt.Errorf("launch lock directory must be absolute")
	}
	path := filepath.Join(tempDir, "cercano-agent-launch.lock")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, fmt.Errorf("open launch lock: %w", err)
	}
	for {
		if err := ctx.Err(); err != nil {
			_ = f.Close()
			return nil, err
		}
		err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return f, nil
		}
		if err == syscall.EINTR {
			continue
		}
		if err != syscall.EWOULDBLOCK && err != syscall.EAGAIN {
			_ = f.Close()
			return nil, fmt.Errorf("acquire launch lock: %w", err)
		}
		timer := time.NewTimer(25 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			_ = f.Close()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
}

func releaseAutoLaunchLock(f *os.File) {
	if f == nil {
		return
	}
	_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	_ = f.Close()
}
