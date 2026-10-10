//go:build windows

package selection

import (
	"golang.org/x/sys/windows"
	"os"
)

// Keep the identity handle pinned across unlink; FILE_SHARE_DELETE permits the
// exact operation while preserving object identity until Close.
func openRemovalCandidate(path string) (*os.File, error) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	h, err := windows.CreateFile(p, windows.GENERIC_READ, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(h), path), nil
}
