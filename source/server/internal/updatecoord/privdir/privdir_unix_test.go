//go:build unix

package privdir

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// TestEnsureCreatesPrivateDirectory covers create-then-verify and
// re-verify on Unix.
func TestEnsureCreatesPrivateDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "private")
	created, err := Ensure(dir)
	if err != nil || !created {
		t.Fatalf("Ensure(create) = created=%v err=%v; want created=true err=nil", created, err)
	}
	info, err := os.Lstat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o700 {
		t.Fatalf("mode = %o; want 0700", info.Mode().Perm())
	}
	created, err = Ensure(dir)
	if err != nil || created {
		t.Fatalf("Ensure(existing) = created=%v err=%v; want created=false err=nil", created, err)
	}
}

// TestEnsureRefusesPermissiveExistingUnchanged proves a permissive
// existing directory is refused and left unchanged (never chmodded).
func TestEnsureRefusesPermissiveExistingUnchanged(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "permissive")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	created, err := Ensure(dir)
	if created || !errors.Is(err, ErrUnsafeACL) {
		t.Fatalf("Ensure(permissive) = created=%v err=%v; want ErrUnsafeACL", created, err)
	}
	info, err := os.Lstat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o755 {
		t.Fatalf("mode changed to %o; permissive target must be left unchanged", info.Mode().Perm())
	}
}

// TestEnsureClassifiesSiblingSymlinkAndForeignOwner covers sibling file,
// symlink, and foreign-uid classification without creating any other
// account (foreign uid is checked by running as an unprivileged uid only
// when available; otherwise classification is skipped).
func TestEnsureClassifiesSiblingSymlinkAndForeignOwner(t *testing.T) {
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
		t.Skipf("cannot create symlink fixture: %v", err)
	}
	if _, err := Ensure(link); !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("Ensure(symlink) err = %v; want ErrUnsafePath", err)
	}

	if os.Getuid() != 0 {
		// Foreign-owner classification: a directory owned by an
		// unprivileged "nobody"-style uid is refused. It is created via
		// syscall chown which may be unavailable in sandboxes, so the
		// check is best-effort and skipped, not faked.
		if err := os.Mkdir(filepath.Join(root, "foreign"), 0o700); err != nil {
			t.Fatal(err)
		}
		foreign := filepath.Join(root, "foreign")
		if err := os.Chown(foreign, 65534, -1); err != nil {
			t.Skipf("cannot create foreign-uid fixture: %v", err)
		}
		if _, err := Ensure(foreign); !errors.Is(err, ErrForeignOwner) {
			t.Fatalf("Ensure(foreign-owned) err = %v; want ErrForeignOwner", err)
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
