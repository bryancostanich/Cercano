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
//
// Binding note: the exclusion lock file lives INSIDE the publication
// directory, and the guarded-use API proves the handle was acquired for
// exactly that directory — so an unsafe publication directory can never
// be reached through a foreign-bound handle (that is a binding refusal,
// covered in publish_guard_test.go). Every test below therefore binds
// the handle while the directory is still safe, and only THEN makes the
// directory unsafe, which is also the realistic mid-flight corruption
// the verify-only classification must catch.

// TestPublishRefusesPermissiveDirectoryUnchanged proves an unsafe
// permission root is refused through the verify-only privdir
// classification and nothing is rewritten or published (the unsafe mode
// is left exactly as found).
func TestPublishRefusesPermissiveDirectoryUnchanged(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "permissive")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	// Bind first, while the directory is still safe to lock in.
	lock := acquireUpdateLock(t, dir)
	if err := os.Chmod(dir, 0o777); err != nil {
		t.Fatal(err)
	}
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
	assertNoStagingLeftovers(t, dir)
}

// TestPublishRefusesSymlinkedDirectoryUnchanged proves a symlinked
// publication root is refused (never followed, never rewritten) even
// when the handle is correctly bound to the path: the real directory is
// moved away and a symlink is left in its place before the publish. The
// guarded use re-proves the acquired directory identity before the
// callback runs, so this mid-flight replacement surfaces as the guard's
// ErrReplacedLock classified as ErrInvalidRequest; the symlink is left
// exactly as planted and the moved target is untouched.
func TestPublishRefusesSymlinkedDirectoryUnchanged(t *testing.T) {
	root := t.TempDir()
	real := filepath.Join(root, "real")
	if _, err := privdir.Ensure(real); err != nil {
		t.Fatal(err)
	}
	// Bind while the path is still the real private directory.
	lock := acquireUpdateLock(t, real)
	moved := filepath.Join(root, "moved")
	if err := os.Rename(real, moved); err != nil {
		t.Skipf("cannot move the locked fixture directory: %v", err)
	}
	if err := os.Symlink(moved, real); err != nil {
		t.Skipf("cannot create symlink fixture: %v", err)
	}
	_, err := Publish(context.Background(), real, lock, Expected{Absent: true}, testSelection(1, "1.0.1"))
	if !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("Publish(symlink dir) err = %v; want ErrInvalidRequest refusal", err)
	}
	if !errors.Is(err, exclusion.ErrReplacedLock) {
		t.Fatalf("Publish(symlink dir) err = %v; want the guard's ErrReplacedLock refusal", err)
	}
	if info, lerr := os.Lstat(real); lerr != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("symlink root was followed or rewritten: (%v, %v)", info, lerr)
	}
	got, rerr := filepath.EvalSymlinks(real)
	if rerr != nil {
		t.Fatalf("symlink target became unreadable: %v", rerr)
	}
	want, werr := filepath.EvalSymlinks(moved)
	if werr != nil || got != want {
		t.Fatalf("symlink target changed: resolved %q; want %q (was %q) left in place", got, want, moved)
	}
}

// TestPublishRefusesDirectoryReplacedByRegularFile proves a publication
// path that has become a regular file is refused with nothing rewritten:
// the handle stays correctly bound (it was acquired when the directory
// existed), and the guarded use re-proves the acquired directory
// identity before the callback runs, so the refusal is the guard's
// ErrReplacedLock classified as ErrInvalidRequest — never a provision
// attempt and never a write to the impostor entry.
func TestPublishRefusesDirectoryReplacedByRegularFile(t *testing.T) {
	dir := newPrivateDir(t)
	lock := acquireUpdateLock(t, dir)
	replaced := filepath.Join(t.TempDir(), "moved-private")
	if err := os.Rename(dir, replaced); err != nil {
		t.Skipf("cannot move the locked fixture directory: %v", err)
	}
	if err := os.WriteFile(dir, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Publish(context.Background(), dir, lock, Expected{Absent: true}, testSelection(1, "1.0.1"))
	if !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("Publish(regular file at dir path) err = %v; want ErrInvalidRequest refusal", err)
	}
	if !errors.Is(err, exclusion.ErrReplacedLock) {
		t.Fatalf("Publish(regular file at dir path) err = %v; want the guard's ErrReplacedLock refusal", err)
	}
	if got, rerr := os.ReadFile(dir); rerr != nil || string(got) != "not a directory" {
		t.Fatalf("entry at the directory path was rewritten or removed: (%q, %v)", got, rerr)
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
