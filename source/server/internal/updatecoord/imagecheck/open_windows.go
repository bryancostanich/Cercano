//go:build windows

package imagecheck

import "os"

// Root confines pathname resolution; the caller refuses reparse/symlink entries
// and checks the opened identity against the observed regular file.
func openMember(root *os.Root, name string) (*os.File, error) {
	return root.OpenFile(name, os.O_RDONLY, 0)
}
