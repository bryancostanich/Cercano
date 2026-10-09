package activation

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Reader errors are machine-readable sentinels; none embeds a raw user
// path. Absent, unreadable, and malformed are three DISTINCT results a
// caller must treat differently (see Observed).
var (
	// ErrSelectionAbsent: no selection exists at the explicit path. This is
	// a first-class state (for example, a first install before its first
	// switch), never an error to hide.
	ErrSelectionAbsent = errors.New("activation: selection file absent")
	// ErrSelectionUnreadable: the entry exists but cannot be safely read —
	// not a regular file, a symlink, a race replaced the file mid-read, or
	// an I/O failure. The observation is untrustworthy; automatic changes
	// must stop.
	ErrSelectionUnreadable = errors.New("activation: selection file unreadable")
	// ErrSelectionMalformed: the bytes were read within bounds but are not a
	// valid selection payload. Treated exactly like unreadable for
	// reconciliation: refuse automatic changes.
	ErrSelectionMalformed = errors.New("activation: selection file malformed")
)

// ReadSelection reads and parses the selection descriptor at the explicit,
// caller-trusted path.
//
// The path is used EXACTLY as given — this package has no default or live
// location and never derives one — and must be a cleaned, NUL-free,
// absolute path on the host platform. The read is bounded by
// maxSelectionJSONBytes; an entry that is absent, or that is not a regular
// non-link file with a stable identity observed before AND after the read,
// is refused. No path component or file is ever written, created, or
// removed; the file's permissions are NOT checked (this package claims no
// filesystem-access protection beyond regularity and identity).
//
// Distinct results: (zero, ErrSelectionAbsent) when nothing exists at the
// path; (zero, ErrSelectionUnreadable) for anything that exists but cannot
// be proven to be a stable regular file; (zero, ErrSelectionMalformed) for
// present-but-invalid content; (Selection, nil) only for a fully validated
// descriptor.
func ReadSelection(path string) (Selection, error) {
	var zero Selection
	if strings.ContainsRune(path, 0) || !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return zero, fmt.Errorf("%w: path is not an explicit cleaned absolute path", ErrSelectionUnreadable)
	}
	before, err := os.Lstat(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return zero, ErrSelectionAbsent
		}
		return zero, fmt.Errorf("%w: %w", ErrSelectionUnreadable, err)
	}
	if !before.Mode().IsRegular() {
		// Covers symlinks, directories, devices, sockets: the entry is
		// present but is not a plain file this reader can trust.
		return zero, fmt.Errorf("%w: entry is not a regular non-link file", ErrSelectionUnreadable)
	}
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return zero, ErrSelectionAbsent
		}
		return zero, fmt.Errorf("%w: %w", ErrSelectionUnreadable, err)
	}
	defer f.Close() //nolint:errcheck // read-only; the caller sees the parse result
	opened, err := f.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(before, opened) {
		return zero, fmt.Errorf("%w: file identity changed between stat and open", ErrSelectionUnreadable)
	}
	data, err := io.ReadAll(io.LimitReader(f, maxSelectionJSONBytes+1))
	if err != nil {
		return zero, fmt.Errorf("%w: %w", ErrSelectionUnreadable, err)
	}
	after, err := os.Lstat(path)
	if err != nil || !after.Mode().IsRegular() || !os.SameFile(before, after) {
		return zero, fmt.Errorf("%w: file identity changed during read", ErrSelectionUnreadable)
	}
	if len(data) > maxSelectionJSONBytes {
		return zero, fmt.Errorf("%w: selection exceeds %d bytes", ErrSelectionMalformed, maxSelectionJSONBytes)
	}
	sel, err := ParseSelection(data)
	if err != nil {
		return zero, fmt.Errorf("%w: %w", ErrSelectionMalformed, err)
	}
	return sel, nil
}
