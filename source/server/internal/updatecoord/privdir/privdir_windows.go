//go:build windows

package privdir

import (
	"errors"
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

// tokenOwnerInfo is the fixed-layout TOKEN_OWNER structure returned by
// GetTokenInformation(TokenOwner): the SID the token assigns as the
// DEFAULT OWNER of objects it creates. x/sys/windows does not export this
// structure, so the single documented field is declared here.
// https://learn.microsoft.com/en-us/windows/win32/api/winnt/ns-winnt-token_owner
type tokenOwnerInfo struct {
	Owner *windows.SID
}

// identity is the REAL current process identity captured from the process
// token. No environment variable or account name is ever consulted; if
// any part cannot be captured, callers are refused (fail closed).
type identity struct {
	// user is the TokenUser SID: the account principal of this process.
	user *windows.SID
	// owner is the TokenOwner SID: the default owner this token assigns
	// to objects it creates. For a normal (UAC-filtered or elevated) user
	// token this is the user's own SID; for tokens whose default owner is
	// the built-in Administrators group (for example the built-in
	// Administrator account) it is that group SID. Both cases are real
	// token values and both are accepted owners.
	owner *windows.SID
	// elevated is the TokenElevation state. It is DIAGNOSTIC CONTEXT
	// ONLY: classification never branches on it. UAC elevation of the
	// same user does not change the user SID, so elevated and
	// non-elevated contexts of one user classify identically. No claim is
	// made about a different user's elevation, which was not tested.
	elevated bool
}

// currentIdentity captures the real process identity from the process
// token. It never falls back to names or environment values.
func currentIdentity() (identity, error) {
	var token windows.Token
	if err := windows.OpenProcessToken(windows.CurrentProcess(), windows.TOKEN_QUERY, &token); err != nil {
		return identity{}, err
	}
	defer token.Close()
	tu, err := token.GetTokenUser()
	if err != nil {
		return identity{}, err
	}
	user := tu.User.Sid
	if user == nil || !user.IsValid() || user.String() == "" {
		return identity{}, errors.New("token user SID is not usable")
	}
	owner, err := tokenDefaultOwner(token)
	if err != nil {
		return identity{}, err
	}
	return identity{user: user, owner: owner, elevated: token.IsElevated()}, nil
}

// tokenDefaultOwner reads TokenOwner from the token and copies the SID out
// of the query buffer.
func tokenDefaultOwner(token windows.Token) (*windows.SID, error) {
	var need uint32
	if err := windows.GetTokenInformation(token, windows.TokenOwner, nil, 0, &need); err != nil {
		if !errors.Is(err, windows.ERROR_INSUFFICIENT_BUFFER) || need == 0 {
			return nil, err
		}
	}
	buf := make([]byte, need)
	var returned uint32
	if err := windows.GetTokenInformation(token, windows.TokenOwner, &buf[0], uint32(len(buf)), &returned); err != nil {
		return nil, err
	}
	info := (*tokenOwnerInfo)(unsafe.Pointer(&buf[0]))
	if info.Owner == nil || !info.Owner.IsValid() {
		return nil, errors.New("token default owner SID is not usable")
	}
	return info.Owner.Copy()
}

// accessPolicy is the per-call approved principal set: the real token
// identities plus the OS-trusted principals local SYSTEM and built-in
// Administrators. It is the ONLY trustee and owner set this package ever
// accepts on Windows.
type accessPolicy struct {
	id     identity
	system *windows.SID // local SYSTEM  (S-1-5-18)
	admins *windows.SID // built-in Administrators (S-1-5-32-544)
}

func newAccessPolicy(id identity) (accessPolicy, error) {
	system, err := windows.CreateWellKnownSid(windows.WinLocalSystemSid)
	if err != nil {
		return accessPolicy{}, err
	}
	admins, err := windows.CreateWellKnownSid(windows.WinBuiltinAdministratorsSid)
	if err != nil {
		return accessPolicy{}, err
	}
	return accessPolicy{id: id, system: system, admins: admins}, nil
}

// userWriteMask is the set of access bits that prove an ACE grants the
// current user write capability on the directory itself. Absence of every
// one of them means write access could not be proven and the directory is
// refused (fail closed).
const userWriteMask = windows.GENERIC_ALL | windows.GENERIC_WRITE |
	windows.DELETE | windows.WRITE_DAC | windows.WRITE_OWNER |
	windows.FILE_WRITE_DATA | windows.FILE_WRITE_ATTRIBUTES |
	windows.FILE_WRITE_EA | windows.FILE_APPEND_DATA

// ownerTrusted reports whether owner is a principal this package accepts:
// the token's user SID or the token's default owner SID (see identity.owner
// for the documented user-vs-administrator distinction). It branches only
// on SIDs captured from the real token, never on the elevation flag.
func (p accessPolicy) ownerTrusted(owner *windows.SID) bool {
	if owner == nil || !owner.IsValid() {
		return false
	}
	if owner.Equals(p.id.user) {
		return true
	}
	return p.id.owner != nil && owner.Equals(p.id.owner)
}

// trusteeTrusted reports whether an ACE trustee may hold any access under
// the approved policy. Unknown or unrecognized SIDs — Everyone, Users,
// Authenticated Users, domain principals, arbitrary groups — are
// untrusted: an ACE naming them is refused, not guessed at.
func (p accessPolicy) trusteeTrusted(sid *windows.SID) bool {
	if sid == nil || !sid.IsValid() {
		return false
	}
	if sid.Equals(p.id.user) {
		return true
	}
	if p.id.owner != nil && sid.Equals(p.id.owner) {
		return true
	}
	return sid.Equals(p.system) || sid.Equals(p.admins)
}

// verifyDACL conservatively verifies the security descriptor of an
// existing directory. Unknown or ambiguous input is REFUSED, never
// repaired: this package never rewrites a DACL it did not create.
func (p accessPolicy) verifyDACL(sd *windows.SECURITY_DESCRIPTOR) error {
	control, _, err := sd.Control()
	if err != nil {
		return fmt.Errorf("%w: security descriptor control unreadable: %v", ErrUnsafeACL, err)
	}
	if control&windows.SE_DACL_PRESENT == 0 {
		// A NULL DACL grants EVERYONE full access: maximally unsafe.
		return fmt.Errorf("%w: no DACL present (world-accessible policy)", ErrUnsafeACL)
	}
	if control&windows.SE_DACL_PROTECTED == 0 {
		// Unprotected: inherited ACEs from the parent are in force, and
		// inherited read for untrusted principals cannot be ruled out.
		// Refuse; the directory is left unchanged.
		return fmt.Errorf("%w: DACL is not protected, inherited policy in force", ErrUnsafeACL)
	}
	dacl, _, err := sd.DACL()
	if err != nil || dacl == nil {
		return fmt.Errorf("%w: DACL not present or empty", ErrUnsafeACL)
	}
	userWrite := false
	for i := uint32(0); i < uint32(dacl.AceCount); i++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, i, &ace); err != nil {
			return fmt.Errorf("%w: ACE unreadable: %v", ErrUnsafeACL, err)
		}
		if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE {
			// Deny, object-, callback- (conditional), audit- or future
			// typed ACEs have semantics this policy does not prove:
			// refuse, do not guess.
			return fmt.Errorf("%w: unsupported ACE type %d", ErrUnsafeACL, ace.Header.AceType)
		}
		if ace.Header.AceFlags&windows.INHERITED_ACE != 0 {
			return fmt.Errorf("%w: inherited ACE present", ErrUnsafeACL)
		}
		if ace.Header.AceFlags&^(windows.OBJECT_INHERIT_ACE|windows.CONTAINER_INHERIT_ACE|windows.INHERIT_ONLY_ACE) != 0 {
			return fmt.Errorf("%w: unsupported ACE flags %#x", ErrUnsafeACL, ace.Header.AceFlags)
		}
		trustee := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
		if !p.trusteeTrusted(trustee) {
			return fmt.Errorf("%w: ACE names an untrusted or unrecognized principal", ErrUnsafeACL)
		}
		if ace.Header.AceFlags&windows.INHERIT_ONLY_ACE == 0 &&
			ace.Mask&userWriteMask != 0 && trustee.Equals(p.id.user) {
			userWrite = true
		}
	}
	if !userWrite {
		// Fail closed: an existing directory the current user cannot
		// provably write is not a usable private updater state directory.
		return fmt.Errorf("%w: no ACE grants the current user write access", ErrUnsafeACL)
	}
	return nil
}

// creationDescriptor builds the protected DACL applied to NEW directories
// this package creates — and only those. "D:P" refuses inheritance so no
// untrusted principal gains inherited read or write; the three ACEs grant
// full control (with object/container inheritance) to the REAL current
// user SID and the OS-trusted local SYSTEM and built-in Administrators.
func creationDescriptor(id identity) (*windows.SECURITY_DESCRIPTOR, error) {
	user := id.user.String()
	if user == "" {
		return nil, errors.New("token user SID could not be rendered")
	}
	return windows.SecurityDescriptorFromString(
		"D:P" +
			"(A;OICI;FA;;;" + user + ")" +
			"(A;OICI;FA;;;SY)" +
			"(A;OICI;FA;;;BA)")
}

// ensure is the Windows half of Ensure.
func ensure(dir string) (created bool, err error) {
	id, err := currentIdentity()
	if err != nil {
		return false, fmt.Errorf("%w: %v", ErrUnverifiedIdentity, err)
	}
	policy, err := newAccessPolicy(id)
	if err != nil {
		return false, fmt.Errorf("%w: %v", ErrUnverifiedIdentity, err)
	}
	sd, err := creationDescriptor(id)
	if err != nil {
		return false, fmt.Errorf("privdir: build creation DACL: %w", err)
	}
	sa := &windows.SecurityAttributes{
		Length:             uint32(unsafe.Sizeof(windows.SecurityAttributes{})),
		SecurityDescriptor: sd,
	}
	p16, err := windows.UTF16PtrFromString(dir)
	if err != nil {
		return false, fmt.Errorf("%w: %v", ErrInvalidPath, err)
	}
	err = windows.CreateDirectory(p16, sa)
	switch {
	case err == nil:
		// Readback: prove through a handle that the created directory
		// actually carries the protected policy.
		if verr := verifyExisting(dir, policy); verr != nil {
			return false, fmt.Errorf("privdir: newly created directory failed verification: %w", verr)
		}
		return true, nil
	case errors.Is(err, windows.ERROR_ALREADY_EXISTS):
		if verr := verifyExisting(dir, policy); verr != nil {
			return false, verr
		}
		return false, nil
	case errors.Is(err, windows.ERROR_PATH_NOT_FOUND):
		return false, fmt.Errorf("%w: parent directory missing: %v", ErrInvalidPath, err)
	default:
		return false, fmt.Errorf("privdir: create directory: %w", err)
	}
}

// verifyExisting classifies an existing entry through a HANDLE anchored at
// the path: CreateFile with FILE_FLAG_OPEN_REPARSE_POINT opens a reparse
// point itself instead of traversing it, and every subsequent query
// (attributes, security info) acts on that handle, not on a path that
// could be substituted between checks. It never chmods, rewrites an unsafe
// DACL, or takes ownership.
func verifyExisting(dir string, policy accessPolicy) error {
	handle, err := openDirectoryHandle(dir)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(handle)

	var fi windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(handle, &fi); err != nil {
		return fmt.Errorf("privdir: inspect handle attributes: %w", err)
	}
	if fi.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		return fmt.Errorf("%w: %q is a reparse point", ErrUnsafePath, dir)
	}
	if fi.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY == 0 {
		return fmt.Errorf("%w: %q is not a directory", ErrNotDirectory, dir)
	}
	sd, err := windows.GetSecurityInfo(handle, windows.SE_FILE_OBJECT,
		windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return fmt.Errorf("%w: cannot read security descriptor: %v", ErrUnverifiedIdentity, err)
	}
	owner, _, err := sd.Owner()
	if err != nil || owner == nil {
		return fmt.Errorf("%w: cannot read owner: %v", ErrUnverifiedIdentity, err)
	}
	if !policy.ownerTrusted(owner) {
		return fmt.Errorf("%w: owner is not the current token identity", ErrForeignOwner)
	}
	return policy.verifyDACL(sd)
}

// openDirectoryHandle opens a handle for READ_CONTROL and attributes.
func openDirectoryHandle(dir string) (windows.Handle, error) {
	p16, err := windows.UTF16PtrFromString(dir)
	if err != nil {
		return 0, fmt.Errorf("%w: %v", ErrInvalidPath, err)
	}
	handle, err := windows.CreateFile(p16,
		windows.READ_CONTROL|windows.FILE_READ_ATTRIBUTES,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, windows.OPEN_EXISTING,
		windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT,
		0)
	if err != nil {
		return 0, fmt.Errorf("privdir: open %q for verification: %w", dir, err)
	}
	return handle, nil
}
