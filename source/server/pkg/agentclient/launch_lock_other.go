//go:build !unix

package agentclient

import "os"

func acquireAutoLaunchLock() (*os.File, error) {
	return nil, nil
}

func releaseAutoLaunchLock(*os.File) {}
