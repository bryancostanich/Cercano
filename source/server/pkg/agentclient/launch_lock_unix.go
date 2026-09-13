//go:build unix

package agentclient

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

func acquireAutoLaunchLock() (*os.File, error) {
	path := filepath.Join(os.TempDir(), "cercano-agent-launch.lock")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, fmt.Errorf("open launch lock %s: %w", path, err)
	}
	for {
		if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
			if err == syscall.EINTR {
				continue
			}
			_ = f.Close()
			return nil, fmt.Errorf("lock %s: %w", path, err)
		}
		break
	}
	return f, nil
}

func releaseAutoLaunchLock(f *os.File) {
	if f == nil {
		return
	}
	_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	_ = f.Close()
}
