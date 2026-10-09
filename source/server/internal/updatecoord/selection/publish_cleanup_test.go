package selection

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Staging-cleanup and pre-commit-recheck tests. These use ONLY the
// operation's own staging fixtures (found by the staging prefix inside
// the test-owned private directory), the REAL exclusion lock, and
// package-private failure points. The cooperative contract is explicit:
// a same-user writer inside the exclusion is assumed to be this
// protocol, and anything that breaks a binding mid-window is refused,
// never silently published or silently deleted.

var errTestStagingOpen = errors.New("test: failure injected while staging handle is open")

// withStagingOpenHook installs the early staging failure point (runs
// while the owned handle is still open).
func withStagingOpenHook(t *testing.T, fn func(f *os.File) error) {
	t.Helper()
	prev := testHookStagingOpen
	testHookStagingOpen = fn
	t.Cleanup(func() { testHookStagingOpen = prev })
}

// stagingPath returns the single staging tempfile this operation currently
// owns (the file Publish itself just created — an owned fixture).
func stagingPath(t *testing.T, dir string) string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var found []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), stagingPrefix) {
			found = append(found, filepath.Join(dir, e.Name()))
		}
	}
	if len(found) != 1 {
		t.Fatalf("staging fixtures = %v; want exactly one owned staging file", found)
	}
	return found[0]
}

func destAbsent(t *testing.T, dir string) {
	t.Helper()
	if _, err := os.Lstat(filepath.Join(dir, FileName)); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("destination exists in %s; want absent", dir)
	}
}

// TestPublishEarlyStagingFailureClosesHandleBeforeCleanup proves the
// close-on-every-path contract: a failure injected while the owned
// staging handle is still open must still close the handle (required
// before the delete, especially on Windows) and clean the owned staging
// file, with no ErrStagingRetained swallowed or invented.
func TestPublishEarlyStagingFailureClosesHandleBeforeCleanup(t *testing.T) {
	dir := newPrivateDir(t)
	lock := acquireUpdateLock(t, dir)
	var handle *os.File
	withStagingOpenHook(t, func(f *os.File) error {
		handle = f
		return errTestStagingOpen
	})
	_, err := Publish(context.Background(), dir, lock, Expected{Absent: true}, testSelection(1, "1.0.1"))
	if !errors.Is(err, errTestStagingOpen) {
		t.Fatalf("Publish err = %v; want injected staging-open failure", err)
	}
	if errors.Is(err, ErrStagingRetained) {
		t.Fatalf("Publish err = %v; cleanup succeeded, so retention must not be reported", err)
	}
	if _, serr := handle.Stat(); !errors.Is(serr, os.ErrClosed) {
		t.Fatalf("staging handle state after early failure = %v; want closed (ErrClosed)", serr)
	}
	assertNoStagingLeftovers(t, dir)
	destAbsent(t, dir)
}

// TestPublishCanceledPreCommitHookMustNotCommit proves cancellation is
// re-checked immediately before the commit: a pre-commit hook that ends
// the context must yield ErrCanceled with NOTHING committed, the
// destination untouched, and staging cleaned.
func TestPublishCanceledPreCommitHookMustNotCommit(t *testing.T) {
	dir := newPrivateDir(t)
	lock := acquireUpdateLock(t, dir)
	ctx, cancel := context.WithCancel(context.Background())
	withHooks(t, func() error {
		cancel()
		return nil
	}, nil)
	_, err := Publish(ctx, dir, lock, Expected{Absent: true}, testSelection(1, "1.0.1"))
	if !errors.Is(err, ErrCanceled) {
		t.Fatalf("Publish err = %v; want ErrCanceled (hook canceled the context)", err)
	}
	destAbsent(t, dir)
	assertNoStagingLeftovers(t, dir)
}

// TestPublishReplacedStagingRefusedAndPreserved proves the pinned-identity
// recheck: a pre-commit hook that swaps the staging file for a different
// inode holding IDENTICAL bytes must not be published (no inode-ABA
// confusion), and the unprovable replacement is retained, reported with
// ErrStagingRetained, and its bytes preserved.
func TestPublishReplacedStagingRefusedAndPreserved(t *testing.T) {
	gen1 := testSelection(1, "1.0.1")
	next := testSelection(2, "1.0.2")
	canonical := canonicalBytes(t, next)
	t.Run("refused, destination unchanged, replacement preserved", func(t *testing.T) {
		dir := newPrivateDir(t)
		lock := acquireUpdateLock(t, dir)
		d1 := writeFixture(t, dir, gen1)
		withHooks(t, func() error {
			p := stagingPath(t, dir)
			// A different inode with byte-identical content: only the
			// pinned identity (not the content) can catch the swap.
			swap := filepath.Join(dir, "hook-owned-swap")
			if err := os.WriteFile(swap, canonical, 0o600); err != nil {
				return err
			}
			return os.Rename(swap, p)
		}, nil)
		_, err := Publish(context.Background(), dir, lock, Expected{Selection: gen1, Digest: d1}, next)
		if !errors.Is(err, ErrStagingRetained) {
			t.Fatalf("Publish err = %v; want ErrStagingRetained (a replaced staging file must not be published)", err)
		}
		if got := readDest(t, dir); !bytes.Equal(got, canonicalBytes(t, gen1)) {
			t.Fatalf("destination changed: %q; want gen1 unchanged", got)
		}
		p := stagingPath(t, dir) // exactly the retained replacement remains
		got, rerr := os.ReadFile(p)
		if rerr != nil || !bytes.Equal(got, canonical) {
			t.Fatalf("retained replacement = (%q, %v); want preserved byte-identical content", got, rerr)
		}
	})
	t.Run("hook failure with replaced staging joins the retained error", func(t *testing.T) {
		dir := newPrivateDir(t)
		lock := acquireUpdateLock(t, dir)
		d1 := writeFixture(t, dir, gen1)
		replacement := []byte("hook replacement payload")
		withHooks(t, func() error {
			p := stagingPath(t, dir)
			swap := filepath.Join(dir, "hook-owned-swap")
			if err := os.WriteFile(swap, replacement, 0o600); err != nil {
				return err
			}
			if err := os.Rename(swap, p); err != nil {
				return err
			}
			return errTestPreCommit
		}, nil)
		_, err := Publish(context.Background(), dir, lock, Expected{Selection: gen1, Digest: d1}, next)
		if !errors.Is(err, errTestPreCommit) {
			t.Fatalf("Publish err = %v; want injected pre-commit failure", err)
		}
		if !errors.Is(err, ErrStagingRetained) {
			t.Fatalf("Publish err = %v; want the retention failure joined, never swallowed", err)
		}
		if got := readDest(t, dir); !bytes.Equal(got, canonicalBytes(t, gen1)) {
			t.Fatalf("destination changed: %q; want gen1 unchanged", got)
		}
		p := stagingPath(t, dir)
		got, rerr := os.ReadFile(p)
		if rerr != nil || !bytes.Equal(got, replacement) {
			t.Fatalf("retained replacement = (%q, %v); want preserved replacement bytes", got, rerr)
		}
	})
}

// TestPublishRewrittenStagingRefusedNotCommitted proves the content
// binding: the same inode rewritten in place with SAME-LENGTH different
// bytes passes the identity check but must still be refused — unknown
// bytes are never published.
func TestPublishRewrittenStagingRefusedNotCommitted(t *testing.T) {
	dir := newPrivateDir(t)
	lock := acquireUpdateLock(t, dir)
	gen1 := testSelection(1, "1.0.1")
	d1 := writeFixture(t, dir, gen1)
	next := testSelection(2, "1.0.2")
	withHooks(t, func() error {
		p := stagingPath(t, dir)
		got, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		rewritten := append([]byte(nil), got...)
		rewritten[0] = ' ' // same length, different bytes: only the content binding catches this
		if len(rewritten) != len(got) || bytes.Equal(rewritten, got) {
			t.Fatal("rewrite fixture must be same-length and different")
		}
		return os.WriteFile(p, rewritten, 0o600)
	}, nil)
	_, err := Publish(context.Background(), dir, lock, Expected{Selection: gen1, Digest: d1}, next)
	if !errors.Is(err, ErrStagingRetained) {
		t.Fatalf("Publish err = %v; want ErrStagingRetained (rewritten staging bytes must not be published)", err)
	}
	if got := readDest(t, dir); !bytes.Equal(got, canonicalBytes(t, gen1)) {
		t.Fatalf("destination changed: %q; want gen1 unchanged", got)
	}
	// The rewritten file IS provably this operation's (identity holds), so
	// cleanup removes it.
	assertNoStagingLeftovers(t, dir)
}

// TestPublishHookChangedDestinationRefusedUnchanged proves the
// destination state is re-proven immediately before the commit: a hook
// that changes the destination after the initial observation is a
// conflict that leaves the NEW observed value in place, on both the
// replace and the create path.
func TestPublishHookChangedDestinationRefusedUnchanged(t *testing.T) {
	t.Run("replace path", func(t *testing.T) {
		dir := newPrivateDir(t)
		lock := acquireUpdateLock(t, dir)
		gen1 := testSelection(1, "1.0.1")
		d1 := writeFixture(t, dir, gen1)
		racing := canonicalBytes(t, testSelection(7, "9.9.9"))
		withHooks(t, func() error {
			return os.WriteFile(filepath.Join(dir, FileName), racing, 0o600)
		}, nil)
		_, err := Publish(context.Background(), dir, lock, Expected{Selection: gen1, Digest: d1}, testSelection(2, "1.0.2"))
		if !errors.Is(err, ErrConflict) {
			t.Fatalf("Publish err = %v; want ErrConflict (destination changed after the initial read)", err)
		}
		if got := readDest(t, dir); !bytes.Equal(got, racing) {
			t.Fatalf("racing writer's value clobbered: %q; want %q left in place", got, racing)
		}
		assertNoStagingLeftovers(t, dir)
	})
	t.Run("create path", func(t *testing.T) {
		dir := newPrivateDir(t)
		lock := acquireUpdateLock(t, dir)
		racing := []byte("someone else's file")
		withHooks(t, func() error {
			return os.WriteFile(filepath.Join(dir, FileName), racing, 0o600)
		}, nil)
		_, err := Publish(context.Background(), dir, lock, Expected{Absent: true}, testSelection(1, "1.0.1"))
		if !errors.Is(err, ErrConflict) {
			t.Fatalf("Publish err = %v; want ErrConflict (destination appeared before creation)", err)
		}
		if got := readDest(t, dir); !bytes.Equal(got, racing) {
			t.Fatalf("unexpected entry clobbered: %q; want %q", got, racing)
		}
		assertNoStagingLeftovers(t, dir)
	})
}

// TestUpdateStagingAfterCommitProvesBeforeRemoving proves the post-commit
// staging-name cleanup removes exactly the owned file and retains plus
// reports anything unprovable; the commit itself is never at stake.
func TestUpdateStagingAfterCommitProvesBeforeRemoving(t *testing.T) {
	t.Run("provable owned file is removed", func(t *testing.T) {
		dir := newPrivateDir(t)
		f, err := os.CreateTemp(dir, stagingPrefix)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.Write([]byte("staged")); err != nil {
			t.Fatal(err)
		}
		if err := f.Close(); err != nil {
			t.Fatal(err)
		}
		identity, err := os.Stat(f.Name())
		if err != nil {
			t.Fatal(err)
		}
		if err := updateStagingAfterCommit(dir, f.Name(), identity); err != nil {
			t.Fatalf("updateStagingAfterCommit(owned) err = %v; want nil", err)
		}
		if _, lerr := os.Lstat(f.Name()); !errors.Is(lerr, fs.ErrNotExist) {
			t.Fatalf("owned staging name still present: %v", lerr)
		}
	})
	t.Run("unprovable entry is retained and reported", func(t *testing.T) {
		dir := newPrivateDir(t)
		owned, err := os.CreateTemp(dir, stagingPrefix)
		if err != nil {
			t.Fatal(err)
		}
		owned.Close()
		identity, err := os.Stat(owned.Name())
		if err != nil {
			t.Fatal(err)
		}
		// A different inode now sits at the staging name (created first,
		// then renamed over, so the inode cannot be the original's): the
		// pinned identity must refuse to remove it.
		swap := []byte("not this operation's file")
		swapPath := filepath.Join(dir, "helper-owned-swap")
		if err := os.WriteFile(swapPath, swap, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(swapPath, owned.Name()); err != nil {
			t.Fatal(err)
		}
		other, err := os.Stat(owned.Name())
		if err != nil {
			t.Fatal(err)
		}
		if os.SameFile(identity, other) {
			t.Fatal("fixture setup error: swap must be a different inode")
		}
		err = updateStagingAfterCommit(dir, owned.Name(), identity)
		if !errors.Is(err, ErrStagingRetained) {
			t.Fatalf("updateStagingAfterCommit(swap) err = %v; want ErrStagingRetained", err)
		}
		got, rerr := os.ReadFile(owned.Name())
		if rerr != nil || !bytes.Equal(got, swap) {
			t.Fatalf("unprovable entry not preserved: (%q, %v)", got, rerr)
		}
	})
}

// TestStagingCommittedDetectsLinkedDestination proves the create-path
// commit-error guard: once the destination IS this operation's staged
// file, a link that surfaced a late error must not be misreported as
// not-committed, and a foreign destination must not count as committed.
func TestStagingCommittedDetectsLinkedDestination(t *testing.T) {
	dir := newPrivateDir(t)
	f, err := os.CreateTemp(dir, stagingPrefix)
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	staged, err := os.Stat(f.Name())
	if err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(dir, FileName)
	if err := os.Link(f.Name(), dest); err != nil {
		t.Skipf("cannot hardlink fixture: %v", err)
	}
	if !stagingCommitted(dest, staged) {
		t.Fatalf("destination holding the staged inode must prove the commit took effect")
	}
	f2, err := os.CreateTemp(dir, stagingPrefix)
	if err != nil {
		t.Fatal(err)
	}
	f2.Close()
	other, err := os.Stat(f2.Name())
	if err != nil {
		t.Fatal(err)
	}
	if stagingCommitted(dest, other) {
		t.Fatalf("destination held by a different inode must not count as committed")
	}
	if stagingCommitted(dest, nil) {
		t.Fatalf("nil pinned identity must not count as committed")
	}
}
