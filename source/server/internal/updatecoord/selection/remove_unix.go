//go:build darwin || linux

package selection

import (
	"golang.org/x/sys/unix"
	"os"
)

func openRemovalCandidate(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
}
