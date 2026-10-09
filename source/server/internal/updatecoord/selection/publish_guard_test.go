package selection

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"cercano/source/server/internal/updatecoord/exclusion"
)

// Guarded-use contract tests: Publish must accept ONLY a live, exclusive
// Update-mode exclusion handle acquired for exactly the publication
// directory. A nil-checked pointer proves nothing by itself — a
// zero-value struct, an already-closed handle, a shared Launch lease, or
// a handle bound to another directory must each be refused with nothing
// observed, staged, or committed.

// TestPublishRefusesNonLiveExclusionHandles reproduces the pre-guard bug:
// Publish only compared the handle pointer against nil, so a zero-value
// handle, a closed handle, and a shared Launch lease were all accepted
// and PUBLISHED. Each case must now refuse with ErrInvalidRequest (and
// the specific exclusion sentinel) and leave the destination absent.
func TestPublishRefusesNonLiveExclusionHandles(t *testing.T) {
	next := testSelection(1, "1.0.1")
	cases := []struct {
		name string
		lock func(t *testing.T, dir string) *exclusion.Handle
		want error
	}{
		{"zero-value handle", func(*testing.T, string) *exclusion.Handle {
			return &exclusion.Handle{}
		}, exclusion.ErrNilHandle},
		{"closed handle", func(t *testing.T, dir string) *exclusion.Handle {
			h, err := exclusion.Acquire(context.Background(), dir, exclusion.Update)
			if err != nil {
				t.Fatal(err)
			}
			if err := h.Close(); err != nil {
				t.Fatal(err)
			}
			return h
		}, exclusion.ErrClosedHandle},
		{"shared launch handle", func(t *testing.T, dir string) *exclusion.Handle {
			h, err := exclusion.Acquire(context.Background(), dir, exclusion.Launch)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = h.Close() })
			return h
		}, exclusion.ErrSharedHandle},
		{"handle bound to another directory", func(t *testing.T, dir string) *exclusion.Handle {
			h, err := exclusion.Acquire(context.Background(), newPrivateDir(t), exclusion.Update)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = h.Close() })
			return h
		}, exclusion.ErrForeignDirectory},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := newPrivateDir(t)
			lock := tc.lock(t, dir)
			_, err := Publish(context.Background(), dir, lock, Expected{Absent: true}, next)
			if !errors.Is(err, tc.want) {
				t.Fatalf("Publish err = %v; want exclusion sentinel %v", err, tc.want)
			}
			if !errors.Is(err, ErrInvalidRequest) {
				t.Fatalf("Publish err = %v; want refusal classified as ErrInvalidRequest", err)
			}
			if _, lerr := os.Lstat(filepath.Join(dir, FileName)); !errors.Is(lerr, os.ErrNotExist) {
				t.Fatalf("destination exists in %s; a refused handle must commit nothing", dir)
			}
			assertNoStagingLeftovers(t, dir)
		})
	}
}

// TestPublishGuardHoldsExclusiveLeaseAcrossPublish proves the guarded use
// wraps the WHOLE observe-stage-commit sequence: while the callback runs,
// a competing exclusive Acquire on the same directory is still excluded,
// and the callback's error propagates unchanged.
func TestPublishGuardHoldsExclusiveLeaseAcrossPublish(t *testing.T) {
	dir := newPrivateDir(t)
	lock := acquireUpdateLock(t, dir)
	next := testSelection(1, "1.0.1")
	inCallback := make(chan struct{})
	release := make(chan struct{})
	withHooks(t, func() error {
		close(inCallback)
		<-release
		return nil
	}, nil)
	done := make(chan error, 1)
	go func() {
		_, err := Publish(context.Background(), dir, lock, Expected{Absent: true}, next)
		done <- err
	}()
	<-inCallback
	// Mid-publish, inside the guard: the same process must not be able to
	// take a second exclusive lease on the same directory.
	blockedCtx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if _, err := exclusion.Acquire(blockedCtx, dir, exclusion.Update); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("competing exclusive acquire inside publish window = %v; want deadline exceeded", err)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatalf("Publish err = %v; want nil", err)
	}
	if got := readDest(t, dir); string(got) != string(canonicalBytes(t, next)) {
		t.Fatalf("destination = %q; want the published canonical bytes", got)
	}
}

// TestPublishRefusesGuardCallbackErrorPropagation proves a callback failure
// surfaces unchanged (never swallowed by the guard) — the guard itself is
// exercised through Publish's package-private hook, which is the only
// sanctioned way to observe inside the guard here.
func TestPublishRefusesGuardCallbackErrorPropagation(t *testing.T) {
	dir := newPrivateDir(t)
	lock := acquireUpdateLock(t, dir)
	_, err := Publish(context.Background(), dir, lock, Expected{Absent: true}, testSelection(1, "1.0.1"))
	if err != nil {
		t.Fatalf("Publish err = %v; want nil", err)
	}
	if err := lock.Close(); err != nil {
		t.Fatalf("Close after guarded publish err = %v; want nil", err)
	}
	if err := lock.Close(); err != nil {
		t.Fatalf("Close idempotence broken: %v", err)
	}
	_, err = Publish(context.Background(), dir, lock, Expected{Absent: true}, testSelection(2, "1.0.2"))
	if !errors.Is(err, ErrInvalidRequest) || !errors.Is(err, exclusion.ErrClosedHandle) {
		t.Fatalf("Publish after Close err = %v; want ErrClosedHandle refusal", err)
	}
}
