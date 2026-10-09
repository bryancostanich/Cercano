//go:build unix

package selection

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
)

// commitReplaceSelection renames the staged tempfile over the existing
// destination. rename(2) is the atomic same-filesystem replacement; the
// destination inode is swapped in one step, and the durability of the
// new name is flushed by syncSelectionDirectory afterwards. rename does
// not follow symlinks in the destination component and replaces whatever
// entry (cooperating or uncooperative) sits at the name — the
// re-read-before-publish check is what keeps cooperating writers
// consistent; no CAS claim is made against uncooperative ones.
func commitReplaceSelection(tmp, dest string) error {
	if err := os.Rename(tmp, dest); err != nil {
		return fmt.Errorf("selection: replace selection: %w", err)
	}
	return nil
}

// commitCreateSelection first-publishes the staged tempfile ONLY IF the
// destination name does not exist: link(2) fails with EEXIST instead of
// clobbering an unexpected file, so a first creation never replaces an
// entry that appeared after the re-read. The staged tempfile keeps its
// original name until the caller removes it by identity.
func commitCreateSelection(tmp, dest string) error {
	if err := os.Link(tmp, dest); err != nil {
		if errors.Is(err, fs.ErrExist) {
			return fmt.Errorf("%w: destination appeared unexpectedly before creation", ErrConflict)
		}
		return fmt.Errorf("selection: create selection: %w", err)
	}
	return nil
}

// syncSelectionDirectory fsyncs the publication directory so the rename
// (and the staging-name removal) reach stable storage before the caller
// is told the selection is committed. This is the measured durability
// boundary: a directory sync proves nothing about power loss, only that
// the entries were handed to the filesystem.
func syncSelectionDirectory(dir string) error {
	f, err := os.Open(dir)
	if err != nil {
		return fmt.Errorf("selection: open directory for sync: %w", err)
	}
	defer f.Close() //nolint:errcheck // sync already reported; the result stands
	if err := f.Sync(); err != nil {
		return fmt.Errorf("selection: sync directory: %w", err)
	}
	return nil
}
