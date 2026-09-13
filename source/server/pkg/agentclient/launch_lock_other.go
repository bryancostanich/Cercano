//go:build !unix

package agentclient

import (
	"context"
	"os"
)

func acquireAutoLaunchLock() (*os.File, error) {
	return nil, nil
}

func releaseAutoLaunchLock(*os.File) {}

func acquireAutoLaunchLockContext(ctx context.Context) (*os.File, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return acquireAutoLaunchLock()
}
