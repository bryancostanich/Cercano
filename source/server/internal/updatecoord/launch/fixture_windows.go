//go:build windows

package launch

import (
	"fmt"
	"os"
	"runtime"

	"golang.org/x/sys/windows"
)

// readFixtureFile is a test helper that reads fixture protocol files with
// Windows-specific sharing permissions. On Windows, it uses CreateFile with
// FILE_SHARE_READ|WRITE|DELETE to allow concurrent access, avoiding the
// AccessDenied error that os.Rename encounters when there are concurrent
// readers. On Unix, it uses the standard os.ReadFile.
func readFixtureFile(path string) ([]byte, error) {
	if runtime.GOOS == "windows" {
		// Windows: CreateFile with sharing flags to allow concurrent readers
		handle, err := windows.CreateFile(
			windows.StringToUTF16Ptr(path),
			windows.GENERIC_READ,
			windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
			nil,
			windows.OPEN_EXISTING,
			windows.FILE_ATTRIBUTE_NORMAL,
			0,
		)
		if err != nil {
			return nil, fmt.Errorf("CreateFile: %w", err)
		}
		defer windows.CloseHandle(handle) //nolint:errcheck // read-only handle
		
		var fileInfo windows.ByHandleFileInformation
		if err := windows.GetFileInformationByHandle(handle, &fileInfo); err != nil {
			return nil, fmt.Errorf("GetFileInformationByHandle: %w", err)
		}
		
		// Check if it's a regular file
		if fileInfo.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY != 0 {
			return nil, fmt.Errorf("path is a directory")
		}
		
		// Read the file content
		fileSize := uint64(fileInfo.FileSizeHigh)<<32 | uint64(fileInfo.FileSizeLow)
		if fileSize > 1<<30 { // 1GB limit
			return nil, fmt.Errorf("file too large: %d bytes", fileSize)
		}
		
		buffer := make([]byte, fileSize)
		var bytesRead uint32
		if err := windows.ReadFile(handle, &buffer[0], uint32(fileSize), &bytesRead, nil); err != nil {
			return nil, fmt.Errorf("ReadFile: %w", err)
		}
		
		return buffer[:bytesRead], nil
	}
	
	// Unix: use standard os.ReadFile
	return os.ReadFile(path)
}