//go:build darwin || linux

package imagecheck

import (
	"golang.org/x/sys/unix"
	"os"
)

// Nonblocking and no-follow also protect against replacement with a FIFO/link
// between the entry check and open. The opened handle is checked again by caller.
func openMember(root *os.Root, name string) (*os.File, error) {
	return root.OpenFile(name, os.O_RDONLY|unix.O_NONBLOCK|unix.O_NOFOLLOW, 0)
}
