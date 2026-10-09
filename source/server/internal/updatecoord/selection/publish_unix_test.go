//go:build unix

package selection

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"cercano/source/server/internal/updatecoord/activation"
	"cercano/source/server/internal/updatecoord/exclusion"
	"cercano/source/server/internal/updatecoord/privdir"
)

// Unix-only fixture cases: permissive directory modes and symlinks are
// only constructible (and only refused) on Unix here; the same refusals
// are compile-checked on Windows and exercised by native Windows CI when
// it exists.

// acquireLockElsewhere returns a REAL held update-exclusion handle on a
// separate test-owned private directory, for cases where the publication
// directory itself is unsafe (and therefore cannot host the lock file).
func acquireLockElsewhere(t *testing.T) *exclusion.Handle {
	t.Helper()
	return acquireUpdateLock(t, newPrivateDir(t))
}

// TestPublishRefusesPermissiveDirectoryUnchanged proves an unsafe
// permission root is refused through the privdir guard and nothing is
// rewritten or published (the unsafe mode is left exactly as found).
func TestPublishRefusesPermissiveDirectoryUnchanged(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "permissive")
	if err := os.Mkdir(dir, 0o777); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o777); err != nil {
		t.Fatal(err)
	}
	lock := acquireLockElsewhere(t)
	gen1 := testSelection(1, "1.0.1")
	fixture := canonicalBytes(t, gen1)
	if err := os.WriteFile(filepath.Join(dir, FileName), fixture, 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Publish(context.Background(), dir, lock, Expected{Selection: gen1, Digest: digestOf(fixture)}, testSelection(2, "1.0.2"))
	if !errors.Is(err, ErrUnsafeDirectory) || !errors.Is(err, privdir.ErrUnsafeACL) {
		t.Fatalf("Publish(permissive dir) err = %v; want ErrUnsafeDirectory wrapping privdir.ErrUnsafeACL", err)
	}
	info, err := os.Lstat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o777 {
		t.Fatalf("unsafe directory mode changed to %o; refused roots must be left unchanged", info.Mode().Perm())
	}
	if got, rerr := os.ReadFile(filepath.Join(dir, FileName)); rerr != nil || string(got) != string(fixture) {
		t.Fatalf("destination changed or unreadable: (%q, %v)", got, rerr)
	}
}

// TestPublishRefusesSymlinkedDirectoryUnchanged proves a symlinked
// publication root is refused (never followed, never rewritten).
func TestPublishRefusesSymlinkedDirectoryUnchanged(t *testing.T) {
	root := t.TempDir()
	real := filepath.Join(root, "real")
	if _, err := privdir.Ensure(real); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("cannot create symlink fixture: %v", err)
	}
	lock := acquireLockElsewhere(t)
	_, err := Publish(context.Background(), link, lock, Expected{Absent: true}, testSelection(1, "1.0.1"))
	if !errors.Is(err, ErrUnsafeDirectory) || !errors.Is(err, privdir.ErrUnsafePath) {
		t.Fatalf("Publish(symlink dir) err = %v; want ErrUnsafeDirectory wrapping privdir.ErrUnsafePath", err)
	}
	if info, lerr := os.Lstat(link); lerr != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("symlink root was followed or rewritten: (%v, %v)", info, lerr)
	}
}

// TestPublishRefusesSymlinkAndNonRegularDestination proves a non-regular
// destination (symlink, directory) is refused unchanged, with staging
// cleaned and the symlink target untouched.
func TestPublishRefusesSymlinkAndNonRegularDestination(t *testing.T) {
	t.Run("symlink destination", func(t *testing.T) {
		dir := newPrivateDir(t)
		lock := acquireUpdateLock(t, dir)
		target := filepath.Join(t.TempDir(), "target")
		if err := os.WriteFile(target, []byte("target contents"), 0o600); err != nil {
			t.Fatal(err)
		}
		dest := filepath.Join(dir, FileName)
		if err := os.Symlink(target, dest); err != nil {
			t.Skipf("cannot create symlink fixture: %v", err)
		}
		_, err := Publish(context.Background(), dir, lock, Expected{Absent: true}, testSelection(1, "1.0.1"))
		if !errors.Is(err, ErrDestinationUnsafe) || !errors.Is(err, activation.ErrSelectionUnreadable) {
			t.Fatalf("Publish(symlink dest) err = %v; want ErrDestinationUnsafe wrapping ErrSelectionUnreadable", err)
		}
		info, lerr := os.Lstat(dest)
		if lerr != nil || info.Mode()&os.ModeSymlink == 0 {
			t.Fatalf("destination no longer a symlink: (%v, %v)", info, lerr)
		}
		if got, rerr := os.ReadFile(target); rerr != nil || string(got) != "target contents" {
			t.Fatalf("symlink target changed: (%q, %v)", got, rerr)
		}
		assertNoStagingLeftovers(t, dir)
	})
	t.Run("directory destination", func(t *testing.T) {
		dir := newPrivateDir(t)
		lock := acquireUpdateLock(t, dir)
		dest := filepath.Join(dir, FileName)
		if err := os.Mkdir(dest, 0o700); err != nil {
			t.Fatal(err)
		}
		_, err := Publish(context.Background(), dir, lock, Expected{Absent: true}, testSelection(1, "1.0.1"))
		if !errors.Is(err, ErrDestinationUnsafe) {
			t.Fatalf("Publish(directory dest) err = %v; want ErrDestinationUnsafe", err)
		}
		if info, lerr := os.Lstat(dest); lerr != nil || !info.IsDir() {
			t.Fatalf("destination changed: (%v, %v)", info, lerr)
		}
		assertNoStagingLeftovers(t, dir)
	})
}
