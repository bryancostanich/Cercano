//go:build windows

package privdir

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

// fileAllAccess is the SDDL "FA" right (FILE_ALL_ACCESS), which x/sys
// does not name. An ACE created from SDDL "FA" carries exactly this mask.
const fileAllAccess = 0x0001F01FF

// mustSID builds an arbitrary well-formed SID (no account is created).
func mustSID(t *testing.T, s string) *windows.SID {
	t.Helper()
	sid, err := windows.StringToSid(s)
	if err != nil {
		t.Fatalf("StringToSid(%q): %v", s, err)
	}
	return sid
}

// TestEnsureCreatesPrivateDirectory covers the normal case: create a new
// private directory in an owned temp directory, then verify it by
// readback — both through the public Ensure API (re-open) and directly
// against the security descriptor.
func TestEnsureCreatesPrivateDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "private")

	created, err := Ensure(dir)
	if err != nil || !created {
		t.Fatalf("Ensure(create) = created=%v err=%v; want created=true err=nil", created, err)
	}

	// Readback through the public API: an already-private directory must
	// verify without modification.
	created, err = Ensure(dir)
	if err != nil || created {
		t.Fatalf("Ensure(existing) = created=%v err=%v; want created=false err=nil", created, err)
	}

	id, err := currentIdentity()
	if err != nil {
		t.Fatalf("currentIdentity: %v", err)
	}
	policy, err := newAccessPolicy(id)
	if err != nil {
		t.Fatalf("newAccessPolicy: %v", err)
	}
	sd := securityInfoFor(t, dir)
	control, _, err := sd.Control()
	if err != nil {
		t.Fatal(err)
	}
	if control&windows.SE_DACL_PROTECTED == 0 {
		t.Fatalf("created DACL is not protected: %#x", control)
	}
	owner, _, err := sd.Owner()
	if err != nil {
		t.Fatal(err)
	}
	if !policy.ownerTrusted(owner) {
		t.Fatalf("created owner %s is not trusted", owner)
	}
	if err := policy.verifyDACL(sd); err != nil {
		t.Fatalf("created DACL failed policy verification: %v", err)
	}
	// The new DACL must grant the real current user full control.
	dacl, _, err := sd.DACL()
	if err != nil || dacl == nil {
		t.Fatalf("DACL: %v %v", dacl, err)
	}
	foundUser := false
	for i := uint32(0); i < uint32(dacl.AceCount); i++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, i, &ace); err != nil {
			t.Fatal(err)
		}
		if (*windows.SID)(unsafe.Pointer(&ace.SidStart)).Equals(id.user) {
			if ace.Mask&fileAllAccess != fileAllAccess && ace.Mask&windows.GENERIC_ALL == 0 {
				t.Fatalf("user ACE mask %#x lacks full access", ace.Mask)
			}
			foundUser = true
		}
	}
	if !foundUser {
		t.Fatal("no ACE names the real current token user")
	}
}

// TestEnsureRefusesPermissiveExistingUnchanged creates a permissive
// directory with the DEFAULT inherited policy (os.Mkdir, which sets no
// DACL — CI runs unprivileged and cannot relax a strict inherited one, but
// an unprotected inherited DACL is already outside the policy), expects
// refusal, and proves the target was left unchanged.
func TestEnsureRefusesPermissiveExistingUnchanged(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "permissive")
	if err := os.Mkdir(dir, 0o777); err != nil { // default/inherited policy, mode bits ignored on Windows
		t.Fatal(err)
	}
	sdBefore := securityInfoFor(t, dir)
	controlBefore, _, err := sdBefore.Control()
	if err != nil {
		t.Fatal(err)
	}
	if controlBefore&windows.SE_DACL_PROTECTED != 0 {
		t.Skipf("unexpected protected DACL on fixture: %#x", controlBefore)
	}

	created, err := Ensure(dir)
	if created {
		t.Fatal("Ensure reported created=true for an existing directory")
	}
	if !errors.Is(err, ErrUnsafeACL) {
		t.Fatalf("Ensure(permissive) err = %v; want ErrUnsafeACL", err)
	}

	// Unchanged: the permissive inherited policy must survive untouched
	// (no DACL rewrite, no ownership change, no chmod).
	sdAfter := securityInfoFor(t, dir)
	controlAfter, _, err := sdAfter.Control()
	if err != nil {
		t.Fatal(err)
	}
	if controlAfter != controlBefore {
		t.Fatalf("control changed: %#x -> %#x", controlBefore, controlAfter)
	}
	ownerBefore, _, _ := sdBefore.Owner()
	ownerAfter, _, _ := sdAfter.Owner()
	if !ownerBefore.Equals(ownerAfter) {
		t.Fatalf("owner changed: %s -> %s", ownerBefore, ownerAfter)
	}
}

// TestEnsureClassifiesSiblingAndReparse covers classification of an
// existing non-directory sibling and of a reparse point (symlink);
// symlink creation may be unavailable on the host, in which case that
// part is skipped rather than faked.
func TestEnsureClassifiesSiblingAndReparse(t *testing.T) {
	root := t.TempDir()

	sibling := filepath.Join(root, "sibling")
	if err := os.WriteFile(sibling, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Ensure(sibling); !errors.Is(err, ErrNotDirectory) {
		t.Fatalf("Ensure(sibling file) err = %v; want ErrNotDirectory", err)
	}

	link := filepath.Join(root, "link")
	target := filepath.Join(root, "target")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("cannot create symlink fixture (host restriction): %v", err)
	}
	if _, err := Ensure(link); !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("Ensure(reparse point) err = %v; want ErrUnsafePath", err)
	}
}

// TestOwnerAndTrusteeClassification exercises the pure classification
// primitives with arbitrary SID values — no other account is created and
// no other user's token is involved. This documents the
// current-user/administrator-default-owner distinction and provides
// same-user-elevated semantic evidence: classification depends only on the
// SIDs captured from the real token, never on the elevation flag. It is
// NOT a claim that another user's UAC context was tested.
func TestOwnerAndTrusteeClassification(t *testing.T) {
	user := mustSID(t, "S-1-5-21-2146772793-4175168523-287240142-1001")
	admins := mustSID(t, "S-1-5-32-544")
	system := mustSID(t, "S-1-5-18")
	users := mustSID(t, "S-1-5-32-545")
	everyone := mustSID(t, "S-1-1-0")
	foreignUser := mustSID(t, "S-1-5-21-2146772793-4175168523-287240142-5002")

	// Normal user token: user SID is also the token default owner.
	normalPolicy := accessPolicy{id: identity{user: user, owner: user, elevated: false}, system: system, admins: admins}
	// Elevated same-user token whose default owner is Administrators:
	// the REAL token values change (owner), the elevation flag is not a
	// policy input. UAC elevation of the SAME user does not change the
	// user SID.
	elevatedPolicy := accessPolicy{id: identity{user: user, owner: admins, elevated: true}, system: system, admins: admins}

	if !normalPolicy.ownerTrusted(user) {
		t.Fatal("own user SID must be a trusted owner")
	}
	if normalPolicy.ownerTrusted(admins) {
		t.Fatal("Administrators must not be a trusted owner when the token default owner is the user SID")
	}
	if !elevatedPolicy.ownerTrusted(admins) {
		t.Fatal("token default owner (Administrators) must be a trusted owner")
	}
	if !elevatedPolicy.ownerTrusted(user) {
		t.Fatal("token user SID must remain a trusted owner in the same-user-elevated context")
	}
	if elevatedPolicy.ownerTrusted(foreignUser) {
		t.Fatal("foreign owner SID must be refused (ErrForeignOwner classification)")
	}

	for _, p := range []accessPolicy{normalPolicy, elevatedPolicy} {
		if !p.trusteeTrusted(user) || !p.trusteeTrusted(system) || !p.trusteeTrusted(admins) {
			t.Fatalf("user/SYSTEM/Administrators must be trusted trustees: %+v", p)
		}
		if p.trusteeTrusted(users) || p.trusteeTrusted(everyone) || p.trusteeTrusted(foreignUser) {
			t.Fatal("Users/Everyone/foreign principals must be refused as trustees")
		}
		if p.trusteeTrusted(nil) {
			t.Fatal("nil trustee must be refused")
		}
	}
}

// TestEnsureRejectsNonCanonicalPath proves the caller-supplied path must
// be absolute and clean; no defaults are applied.
func TestEnsureRejectsNonCanonicalPath(t *testing.T) {
	for _, p := range []string{"relative/dir", t.TempDir() + "/a/../b"} {
		if _, err := Ensure(p); !errors.Is(err, ErrInvalidPath) {
			t.Fatalf("Ensure(%q) err = %v; want ErrInvalidPath", p, err)
		}
	}
}

// TestVerifyExistingClassifiesWithoutMutating proves VerifyExisting is
// verify-only on Windows: it accepts an already-private directory, and
// refuses a path that does not exist WRAPPING fs.ErrNotExist while
// never creating it; the existing classification refusals (unsafe ACL,
// non-directory, reparse point, non-canonical path) are exercised in the
// Ensure fixtures above, which use the same native primitives.
func TestVerifyExistingClassifiesWithoutMutating(t *testing.T) {
	private := filepath.Join(t.TempDir(), "private")
	if _, err := Ensure(private); err != nil {
		t.Fatal(err)
	}
	if err := VerifyExisting(private); err != nil {
		t.Fatalf("VerifyExisting(private) err = %v; want nil", err)
	}

	absent := filepath.Join(t.TempDir(), "absent")
	if err := VerifyExisting(absent); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("VerifyExisting(absent) err = %v; want fs.ErrNotExist", err)
	}
	if _, lerr := os.Lstat(absent); !errors.Is(lerr, fs.ErrNotExist) {
		t.Fatalf("absent path was created by VerifyExisting: %v", lerr)
	}

	if err := VerifyExisting("relative/dir"); !errors.Is(err, ErrInvalidPath) {
		t.Fatalf("VerifyExisting(relative) err = %v; want ErrInvalidPath", err)
	}
}

// securityInfoFor reads OWNER|DACL security information for a path via a
// handle so tests inspect what verification would see.
func securityInfoFor(t *testing.T, dir string) *windows.SECURITY_DESCRIPTOR {
	t.Helper()
	handle, err := openDirectoryHandle(dir)
	if err != nil {
		t.Fatalf("open %q: %v", dir, err)
	}
	defer windows.CloseHandle(handle)
	sd, err := windows.GetSecurityInfo(handle, windows.SE_FILE_OBJECT,
		windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatalf("GetSecurityInfo(%q): %v", dir, err)
	}
	return sd
}
