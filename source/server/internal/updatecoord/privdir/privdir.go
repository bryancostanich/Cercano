// Package privdir is the per-user private-directory verification and
// creation primitive required by the approved bootstrap/staging work. It
// proves, or creates, ONE directory with the platform's approved
// private-updater-state access policy:
//
// Windows — real token identity plus a protected DACL:
//
//   - Identity is captured from the actual process token (TokenUser,
//     TokenOwner, TokenElevation) via x/sys/windows. Environment names
//     (USERPROFILE, USERNAME, ...) are never consulted; if the token
//     cannot be queried, callers are refused (fail closed).
//   - A NEW directory is created with CreateDirectory and an explicit,
//     protected DACL ("D:P"): one full-control ACE each for the REAL
//     current user SID, local SYSTEM and built-in Administrators
//     (OS-trusted principals), with object/container inheritance so
//     children stay private. Inherited ACEs are deliberately NOT
//     incorporated, so no untrusted principal gains inherited read or
//     write into private updater state.
//   - An EXISTING directory is verified conservatively through a HANDLE
//     anchored at the path (CreateFile with FILE_FLAG_OPEN_REPARSE_POINT,
//     then handle-based GetSecurityInfo), so classification cannot be
//     tricked by a path re-resolution between checks. Reparse points and
//     non-directories are refused.
//   - The existing owner must be the token's user SID, or the token's
//     DEFAULT OWNER SID (TokenOwner). This distinction is deliberate and
//     documented: with a normal UAC-filtered or elevated token the owner
//     assigned at creation is the user's own SID; when the token's default
//     owner is the built-in Administrators group (for example the built-in
//     Administrator account), that group SID is the accepted owner. UAC
//     elevation of the SAME user does not change the user SID, so elevated
//     and non-elevated contexts of one user classify identically; this
//     package has NOT been tested against a DIFFERENT user's elevation and
//     makes no claim about it.
//   - The existing DACL must be protected (SE_DACL_PROTECTED, so inherited
//     policy is not in force) and consist solely of explicit
//     ACCESS_ALLOWED ACEs whose trustees are the trusted set above. Any
//     deny, conditional/callback, object-specific, inherited or
//     unknown-future ACE, any unknown or untrusted trustee (Everyone,
//     Users, domain principals, ...), or a DACL that does not provably
//     grant the current user write access is REFUSED. Refuse, not guess.
//   - This package NEVER chmods, rewrites an unsafe DACL, or takes
//     ownership of an existing directory. Refusals leave the target
//     untouched.
//
// Unix — the same per-user posture with uid/mode guards (consistent with
// internal/updatecoord/state): new directories are created 0o700; an
// existing directory must be owned by the current real uid with mode
// 0o7XX (no group/other access) and user-write access, or it is refused
// unchanged. Symlinks are refused.
//
// Platforms with no approved policy (anything that is neither Windows nor
// a Unix build) fail closed with ErrUnsupportedPlatform; no new platform
// policy is invented here.
//
// The caller supplies the exact path: there are no defaults, no
// environment lookups, no live provisioning, and no production wiring.
// The parent chain above the target is caller-trusted (the same posture
// as the state package); this primitive governs only the leaf directory.
// No credentials, no password prompts, no elevation requests, no
// service installs, and no new network surface are involved.
package privdir

import (
	"errors"
	"fmt"
	"path/filepath"
)

// Refusal sentinels. Callers match with errors.Is. None of them embed
// secrets; all classify a refusal without modifying the target.
var (
	// ErrInvalidPath: the caller-supplied path is not an absolute,
	// already-clean path usable for this primitive.
	ErrInvalidPath = errors.New("privdir: invalid directory path")
	// ErrUnsafePath: the entry at the path is a reparse point (Windows) or
	// a symlink (Unix), or otherwise cannot be treated as a real
	// directory.
	ErrUnsafePath = errors.New("privdir: unsafe directory path")
	// ErrNotDirectory: the entry exists but is not a directory (for
	// example a sibling file).
	ErrNotDirectory = errors.New("privdir: path exists but is not a directory")
	// ErrForeignOwner: the existing directory is owned by a principal
	// other than the real current identity (Windows: token user SID or
	// token default owner SID; Unix: current uid). Ownership is never
	// taken over.
	ErrForeignOwner = errors.New("privdir: directory owner is not the current identity")
	// ErrUnsafeACL: the existing access policy is not the approved
	// private policy — an unprotected DACL with inherited ACEs in force,
	// a deny/conditional/object/unknown ACE, an untrusted trustee, no
	// provable user write access, a null/empty DACL, or (Unix) a
	// permissive or non-writable mode. The target is NEVER chmodded,
	// rewritten or repaired.
	ErrUnsafeACL = errors.New("privdir: existing access policy is not the approved private policy")
	// ErrUnverifiedIdentity: the real current identity (process token /
	// uid) could not be captured or the target's security information
	// could not be read, so nothing can be proven. Fail closed.
	ErrUnverifiedIdentity = errors.New("privdir: current identity or target security could not be verified")
	// ErrUnsupportedPlatform: the build has no approved private-directory
	// policy for this platform and refuses rather than guessing.
	ErrUnsupportedPlatform = errors.New("privdir: platform has no approved private-directory policy")
)

// Ensure creates dir as a new private per-user directory, or verifies an
// existing one against the same policy. It returns whether the directory
// was newly created. dir must be an explicit, absolute, already-clean
// caller path: there are no defaults and no live provision.
//
// A failure never modifies the target: an existing directory with a
// permissive, inherited, unknown or foreign policy is reported and left
// byte-for-byte unchanged (no chmod, no DACL rewrite, no ownership
// change).
func Ensure(dir string) (created bool, err error) {
	if !filepath.IsAbs(dir) || filepath.Clean(dir) != dir {
		return false, fmt.Errorf("%w: %q is not an absolute, clean path", ErrInvalidPath, dir)
	}
	return ensure(dir)
}

// VerifyExisting is the verify-only counterpart of Ensure: it classifies
// an EXISTING directory against the same approved private policy without
// ever creating, chmodding, rewriting or repairing anything. An absent
// directory is refused (its error wraps fs.ErrNotExist) and never
// provisioned; an unsafe existing policy is refused unchanged. Callers
// that must provision use Ensure; callers that must not mutate — a
// publisher re-verifying the directory it is about to write into — use
// VerifyExisting so a single call classifies the directory with no
// exists-then-provision race window at all.
func VerifyExisting(dir string) error {
	if !filepath.IsAbs(dir) || filepath.Clean(dir) != dir {
		return fmt.Errorf("%w: %q is not an absolute, clean path", ErrInvalidPath, dir)
	}
	return verifyExistingDir(dir)
}
