// Package state implements the approved SQLite-backed updater state slice.
//
// Scope. This package is the FIRST persistence slice for the approved
// updater-state design (efforts/cross-platform-updates/persistence-gate.md):
// a dedicated, per-user, per-installation SQLite database using the project's
// existing modernc.org/sqlite dependency. It is NOT the conversation database
// and never opens it. It adds no dependency.
//
// Explicitness. No function in this package discovers or opens the user's
// live state by default. StatePath is a PURE function over caller-supplied
// platform values (home, LocalAppData, XDG_STATE_HOME) and an opaque
// installation identifier; Open requires an explicit state root supplied by
// the caller and never derives one from the executable, the executable's
// version, or the current user's environment. Tests run against temporary
// directories only.
//
// Locations (per the approved design; no database may live inside a keg,
// extracted archive, or version directory):
//
//	Windows: <LocalAppData>\Cercano\updater\<installID>\state.db
//	Linux:   ${XDG_STATE_HOME:-$HOME/.local/state}/cercano/updater/<installID>/state.db
//	macOS:   ~/Library/Application Support/Cercano/updater/<installID>/state.db
//
// Installation identity. The installation identifier is a stable, opaque
// component supplied by the trusted resolver (the installation
// classification layer). This package never derives an identity from a path,
// an executable version, or any heuristic; it only validates that the
// supplied identifier is a SAFE directory component.
//
// Dedication. A database is accepted only when it is recognizably this
// package's own schema: the SQLite application_id, user_version, the exact
// table set, and the strict state_meta rows (schema_version and install_id)
// must all match, and they are validated BEFORE any write or journal pragma
// touches an existing database. Future, foreign, corrupt, or nonempty-unknown
// databases are refused without modification. There is no migration and no
// destructive reset: an unsupported database is an error, never a
// re-initialization.
//
// Primitives, not orchestration. This slice deliberately provides a NARROW
// store primitive: monotonic operation IDs, transactional revisions, and
// compare-and-swap saves/loads of the canonical operation.Record and the
// strict policy.DelegationRecord. It does NOT reimplement the operation
// lifecycle (Start/Apply) from the operation package: an adapter that
// restores a Record into the pure operation.Store and replays events through
// its Apply methods, with the CAS revision preventing cross-process stale
// overwrites, is the NEXT slice. Runtime attachment (process coordination,
// admission, activation recovery) is likewise later work.
//
// Failure discipline. Corrupt or malformed rows are rejected, never treated
// as empty or silently re-created. Nothing logs raw user reasons or paths;
// errors carry machine-readable sentinels.
package state

import (
	"fmt"
	"path"
	"strings"
	"unicode/utf8"
)

// Supported platform names for StatePath.
const (
	platformWindows = "windows"
	platformLinux   = "linux"
	platformDarwin  = "darwin"
)

// maxInstallIDBytes bounds the opaque installation identifier so a malformed
// identifier can never produce absurd paths.
const maxInstallIDBytes = 128

// ValidateInstallID reports whether id is a safe, opaque installation
// identifier usable as a single directory component on all three supported
// platforms. It must be nonempty, valid UTF-8, at most maxInstallIDBytes
// bytes, free of NUL and control characters, free of path separators and
// drive-colons ('/', '\\', ':'), a lowercase ASCII letter/digit/dash/underscore component,
// neither "." nor "..", and not a reserved DOS device name (CON, PRN, AUX,
// NUL, COM1-9, LPT1-9, case-insensitive) so it can never be confused with a
// device or a traversal component on Windows.
func ValidateInstallID(id string) error {
	if id == "" {
		return fmt.Errorf("%w: empty", ErrInvalidInstallID)
	}
	if len(id) > maxInstallIDBytes {
		return fmt.Errorf("%w: longer than %d bytes", ErrInvalidInstallID, maxInstallIDBytes)
	}
	if !utf8.ValidString(id) {
		return fmt.Errorf("%w: invalid UTF-8", ErrInvalidInstallID)
	}
	if id == "." || id == ".." {
		return fmt.Errorf("%w: %q is a relative component", ErrInvalidInstallID, id)
	}
	// Opaque IDs use one canonical ASCII spelling on case-insensitive systems.
	for _, r := range id {
		if !((r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_') {
			return fmt.Errorf("%w: noncanonical component", ErrInvalidInstallID)
		}
	}

	if reservedDOSName(id) {
		return fmt.Errorf("%w: %q is a reserved device name", ErrInvalidInstallID, id)
	}
	return nil
}

// reservedDOSName reports whether the component collides (case-insensitively,
// with the classic extension-stripping rule) with a reserved DOS device
// name. Windows still interprets these as devices even with extensions.
func reservedDOSName(id string) bool {
	base := id
	if i := strings.IndexByte(base, '.'); i >= 0 {
		base = base[:i]
	}
	if base == "" {
		return false
	}
	base = strings.ToUpper(base)
	switch base {
	case "CON", "PRN", "AUX", "NUL":
		return true
	}
	if len(base) == 4 {
		switch base[:3] {
		case "COM", "LPT":
			if base[3] >= '1' && base[3] <= '9' {
				return true
			}
		}
	}
	return false
}

// StateRoot returns the per-user state BASE directory for the target
// platform — the directory that will contain the Cercano updater chain:
//
//	windows: localappdata (required; must be a Windows absolute path)
//	linux:   xdg (must be absolute) when nonempty, else home + "/.local/state"
//	darwin:  home + "/Library/Application Support"
//
// It is pure: it performs no filesystem access and no environment lookups.
// All inputs are caller-supplied probe results. Paths are validated against
// the TARGET platform's syntax, not the host's, so a darwin build can plan a
// windows location and vice versa.
func StateRoot(platform, home, localappdata, xdg string) (string, error) {
	switch platform {
	case platformWindows:
		if err := validateWindowsAbs(localappdata); err != nil {
			return "", err
		}
		return trimWindows(localappdata), nil
	case platformLinux:
		if xdg != "" {
			if err := validateUnixAbs(xdg); err != nil {
				return "", err
			}
			return strings.TrimSuffix(xdg, "/"), nil
		}
		if err := validateUnixAbs(home); err != nil {
			return "", err
		}
		return strings.TrimSuffix(home, "/") + "/.local/state", nil
	case platformDarwin:
		if err := validateUnixAbs(home); err != nil {
			return "", err
		}
		return strings.TrimSuffix(home, "/") + "/Library/Application Support", nil
	default:
		return "", fmt.Errorf("%w: %q", ErrUnsupportedPlatform, platform)
	}
}

// StatePath returns the full state database path for an installation on the
// target platform:
//
//	<root>/Cercano/updater/<installID>/state.db    (windows, darwin)
//	<root>/cercano/updater/<installID>/state.db    (linux)
//
// where <root> is StateRoot(platform, home, localappdata, xdg). The install
// identifier is validated as a safe opaque component (ValidateInstallID);
// it is never derived here from an executable path or version. Pure
// function: no filesystem access, no environment reads, no default live
// locations.
func StatePath(platform, home, localappdata, xdg, installID string) (string, error) {
	if err := ValidateInstallID(installID); err != nil {
		return "", err
	}
	root, err := StateRoot(platform, home, localappdata, xdg)
	if err != nil {
		return "", err
	}
	const stateFile = "state.db"
	switch platform {
	case platformWindows:
		return root + `\Cercano\updater\` + installID + `\` + stateFile, nil
	case platformLinux:
		return root + "/cercano/updater/" + installID + "/" + stateFile, nil
	case platformDarwin:
		return root + "/Cercano/updater/" + installID + "/" + stateFile, nil
	default:
		return "", fmt.Errorf("%w: %q", ErrUnsupportedPlatform, platform)
	}
}

// validateUnixAbs enforces target-platform absolute syntax for unix paths:
// rooted at '/' and not a bare root (which could never hold a per-user
// chain).
func validateUnixAbs(p string) error {
	if !utf8.ValidString(p) || strings.ContainsRune(p, 0) || !strings.HasPrefix(p, "/") || p == "/" || path.Clean(p) != p {
		return fmt.Errorf("%w: not a canonical per-user absolute path", ErrInvalidStateRoot)
	}
	return nil
}

// validateWindowsAbs enforces target-platform Windows absolute syntax:
// drive-absolute (X:\ or X:/) or UNC (\\server\share). Anything else —
// rooted-but-driveless, relative, or empty — is refused.
func validateWindowsAbs(p string) error {
	if !utf8.ValidString(p) || strings.ContainsRune(p, 0) {
		return ErrInvalidStateRoot
	}
	p = strings.ReplaceAll(p, `\`, "/")
	if strings.HasPrefix(p, "//?/") || strings.HasPrefix(p, "//./") {
		return ErrInvalidStateRoot
	}
	if len(p) > 3 && isDriveLetter(p[0]) && p[1] == ':' && p[2] == '/' && path.Clean(p[2:]) == p[2:] && !strings.Contains(p[2:], ":") {
		return nil
	}
	if strings.HasPrefix(p, "//") {
		tail := strings.TrimPrefix(p, "//")
		parts := strings.Split(tail, "/")
		if len(parts) >= 2 && parts[0] != "" && parts[1] != "" && path.Clean(tail) == tail && !strings.Contains(tail, ":") {
			return nil
		}
	}
	return fmt.Errorf("%w: not a canonical Windows absolute path", ErrInvalidStateRoot)
}

// trimWindows strips trailing separators from a validated Windows root so
// joining never produces doubled separators.
func trimWindows(p string) string {
	return strings.TrimRight(p, `\`)
}

// isDriveLetter reports whether c is an ASCII drive letter.
func isDriveLetter(c byte) bool {
	return (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z')
}
