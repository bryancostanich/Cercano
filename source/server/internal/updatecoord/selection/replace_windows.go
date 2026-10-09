//go:build windows

package selection

import (
	"errors"
	"fmt"

	"golang.org/x/sys/windows"
)

// Windows commit implementation, compile-verified only. NATIVE WINDOWS
// RUNTIME BEHAVIOR (atomicity and flush semantics on real NTFS/FAT
// volumes) IS PENDING NATIVE CI VERIFICATION — this file is written
// against documented MoveFileEx semantics and is NOT claimed verified
// until the Windows job runs it.
//
// Documented flush limits:
//   - Replacements use MoveFileEx with MOVEFILE_REPLACE_EXISTING|
//     MOVEFILE_WRITE_THROUGH. MOVEFILE_WRITE_THROUGH guarantees the
//     function does not return before a copy+delete-style move is
//     flushed; a same-volume rename is a metadata operation whose
//     on-disk durability rests on the NTFS journal, and FAT-family
//     volumes have no journal.
//   - syncSelectionDirectory additionally flushes the directory handle
//     with FlushFileBuffers. FlushFileBuffers on a directory is
//     unsupported on FAT-family volumes; a flush failure yields
//     COMMITTED_BUT_DURABILITY_UNCERTAIN, never a rollback.
//   - MoveFileEx replacing a destination that another process holds
//     open without FILE_SHARE_DELETE fails with a sharing violation —
//     a pre-commit failure with nothing changed. Go's own os.Open
//     opens with FILE_SHARE_DELETE, so cooperating Go readers do not
//     block the replace; other readers are outside this package's
//     control.

func moveFileSelection(from, to string, flags uint32) error {
	from16, err := windows.UTF16PtrFromString(from)
	if err != nil {
		return fmt.Errorf("selection: encode source path: %w", err)
	}
	to16, err := windows.UTF16PtrFromString(to)
	if err != nil {
		return fmt.Errorf("selection: encode destination path: %w", err)
	}
	if err := windows.MoveFileEx(from16, to16, flags); err != nil {
		return err
	}
	return nil
}

// commitReplaceSelection replaces the existing destination in one
// MoveFileEx with REPLACE_EXISTING (so an unexpected entry at the name is
// replaced, matching the Unix rename semantics; cooperating consistency
// comes from the re-read check, not from this call).
func commitReplaceSelection(tmp, dest string) error {
	if err := moveFileSelection(tmp, dest, windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH); err != nil {
		return fmt.Errorf("selection: replace selection: %w", err)
	}
	return nil
}

// commitCreateSelection first-publishes the staged file ONLY IF the
// destination name does not exist: MoveFileEx WITHOUT
// MOVEFILE_REPLACE_EXISTING fails with ERROR_ALREADY_EXISTS instead of
// clobbering an unexpected file, so a first creation never replaces an
// entry that appeared after the re-read.
func commitCreateSelection(tmp, dest string) error {
	err := moveFileSelection(tmp, dest, windows.MOVEFILE_WRITE_THROUGH)
	if err != nil {
		if errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
			return fmt.Errorf("%w: destination appeared unexpectedly before creation", ErrConflict)
		}
		return fmt.Errorf("selection: create selection: %w", err)
	}
	return nil
}

// syncSelectionDirectory flushes the publication directory handle so the
// name change is handed to the filesystem before the caller is told the
// selection is committed; see the file comment for the volume limits.
func syncSelectionDirectory(dir string) error {
	p16, err := windows.UTF16PtrFromString(dir)
	if err != nil {
		return fmt.Errorf("selection: encode directory path: %w", err)
	}
	h, err := windows.CreateFile(p16, windows.GENERIC_WRITE,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		return fmt.Errorf("selection: open directory for flush: %w", err)
	}
	defer windows.CloseHandle(h) //nolint:errcheck // flush already reported; the result stands
	if err := windows.FlushFileBuffers(h); err != nil {
		return fmt.Errorf("selection: flush directory: %w", err)
	}
	return nil
}
