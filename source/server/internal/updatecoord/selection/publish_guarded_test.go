package selection

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"cercano/source/server/internal/updatecoord/exclusion"
)

// PublishGuarded capability contract tests: the ONLY accepted authority is
// a GuardSession capability minted by exclusion.GuardUpdateSession for
// EXACTLY the publication directory and still live at use time. A
// zero-value, expired, copied-after-expiry, or foreign-bound session —
// or a second concurrent publication on the same live session — is
// refused with nothing observed, staged, or committed.

// TestPublishGuardedPublishesUnderMintedSession proves the happy path:
// inside the minting callback the capability publishes exactly like
// Publish (same result, same canonical bytes), and the same session is
// refused once the callback has returned.
func TestPublishGuardedPublishesUnderMintedSession(t *testing.T) {
	dir := newPrivateDir(t)
	lock := acquireUpdateLock(t, dir)
	next := testSelection(1, "1.0.1")
	var res Result
	var token exclusion.GuardSession
	if err := lock.GuardUpdateSession(dir, func(g exclusion.GuardSession) error {
		token = g
		var err error
		res, err = PublishGuarded(context.Background(), g, dir, Expected{Absent: true}, next)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if res.State != CommitConfirmed {
		t.Fatalf("state = %q; want CommitConfirmed", res.State)
	}
	if got := readDest(t, dir); string(got) != string(canonicalBytes(t, next)) {
		t.Fatalf("destination = %q; want the published canonical bytes", got)
	}
	// The token expired with its callback: a later use is refused.
	if _, err := PublishGuarded(context.Background(), token, dir, Expected{Absent: true}, testSelection(2, "1.0.2")); err == nil {
		t.Fatal("PublishGuarded with an expired session succeeded")
	}
}

// TestPublishGuardedRefusesUnprovenSessions proves every refused
// capability is a typed refusal (ErrInvalidRequest wrapping the exclusion
// sentinel) with NOTHING observed, staged, or committed.
func TestPublishGuardedRefusesUnprovenSessions(t *testing.T) {
	next := testSelection(1, "1.0.1")

	t.Run("zero-value session", func(t *testing.T) {
		dir := newPrivateDir(t)
		var zero exclusion.GuardSession
		_, err := PublishGuarded(context.Background(), zero, dir, Expected{Absent: true}, next)
		if !errors.Is(err, ErrInvalidRequest) || !errors.Is(err, exclusion.ErrNilHandle) {
			t.Fatalf("err = %v; want ErrInvalidRequest wrapping ErrNilHandle", err)
		}
		assertNothingWritten(t, dir)
	})
	t.Run("expired session and copies", func(t *testing.T) {
		dir := newPrivateDir(t)
		lock := acquireUpdateLock(t, dir)
		var live, copyOfLive exclusion.GuardSession
		if err := lock.GuardUpdateSession(dir, func(g exclusion.GuardSession) error {
			live, copyOfLive = g, g
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		for name, tok := range map[string]exclusion.GuardSession{"expired": live, "expired copy": copyOfLive} {
			_, err := PublishGuarded(context.Background(), tok, dir, Expected{Absent: true}, next)
			if !errors.Is(err, ErrInvalidRequest) || !errors.Is(err, exclusion.ErrExpiredSession) {
				t.Fatalf("%s err = %v; want ErrInvalidRequest wrapping ErrExpiredSession", name, err)
			}
		}
		assertNothingWritten(t, dir)
	})
	t.Run("foreign directory", func(t *testing.T) {
		dir := newPrivateDir(t)
		other := newPrivateDir(t)
		lock := acquireUpdateLock(t, dir)
		if err := lock.GuardUpdateSession(dir, func(g exclusion.GuardSession) error {
			// The capability is bound to dir; publishing for other is a
			// wrong installation association and must refuse.
			_, err := PublishGuarded(context.Background(), g, other, Expected{Absent: true}, next)
			if !errors.Is(err, ErrInvalidRequest) || !errors.Is(err, exclusion.ErrForeignDirectory) {
				t.Fatalf("err = %v; want ErrInvalidRequest wrapping ErrForeignDirectory", err)
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		assertNothingWritten(t, dir)
		assertNothingWritten(t, other)
	})
	t.Run("session on a closed handle's directory", func(t *testing.T) {
		dir := newPrivateDir(t)
		lock := acquireUpdateLock(t, dir)
		if err := lock.Close(); err != nil {
			t.Fatal(err)
		}
		if err := lock.GuardUpdateSession(dir, func(g exclusion.GuardSession) error {
			_, err := PublishGuarded(context.Background(), g, dir, Expected{Absent: true}, next)
			if !errors.Is(err, ErrInvalidRequest) || !errors.Is(err, exclusion.ErrClosedHandle) {
				t.Fatalf("err = %v; want ErrInvalidRequest wrapping ErrClosedHandle", err)
			}
			return nil
		}); err == nil {
			t.Fatal("GuardUpdateSession succeeded on a closed handle")
		}
		assertNothingWritten(t, dir)
	})
}

// TestPublishGuardedConcurrentSessionUseCannotRace proves concurrent
// publications on ONE live session (freely copied) can never race: each
// publication either wins the session's single claim or is refused with
// ErrSessionBusy, and the before-commit hook proves at most one
// publication is ever mid-flight. Whatever committed is a complete,
// valid selection — never interleaved or torn output.
func TestPublishGuardedConcurrentSessionUseCannotRace(t *testing.T) {
	dir := newPrivateDir(t)
	lock := acquireUpdateLock(t, dir)
	const workers = 6
	var inside, peak atomic.Int64
	withHooks(t, func() error {
		c := inside.Add(1)
		for {
			p := peak.Load()
			if c <= p || peak.CompareAndSwap(p, c) {
				break
			}
		}
		time.Sleep(2 * time.Millisecond) // widen the race window
		inside.Add(-1)
		return nil
	}, nil)
	if err := lock.GuardUpdateSession(dir, func(g exclusion.GuardSession) error {
		start := make(chan struct{})
		errs := make(chan error, workers)
		var wg sync.WaitGroup
		for i := 0; i < workers; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				tok := g // a copy: copies of the capability must not widen the race
				next := testSelection(1, "1.0.1")
				_, err := PublishGuarded(context.Background(), tok, dir, Expected{Absent: true}, next)
				errs <- err
			}()
		}
		close(start)
		wg.Wait()
		close(errs)
		refused, succeeded := 0, 0
		for err := range errs {
			switch {
			case err == nil:
				succeeded++
			case errors.Is(err, ErrInvalidRequest) && errors.Is(err, exclusion.ErrSessionBusy):
				refused++
			default:
				t.Fatalf("unexpected concurrent PublishGuarded err = %v", err)
			}
		}
		if succeeded != 1 {
			t.Fatalf("succeeded = %d, refused = %d; want exactly one publication to win the claim", succeeded, refused)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if got := peak.Load(); got != 1 {
		t.Fatalf("peak concurrent publications %d, want 1", got)
	}
	// Whatever won, the destination is exactly that publication's
	// canonical bytes: a complete selection, never torn output.
	if got := readDest(t, dir); string(got) != string(canonicalBytes(t, testSelection(1, "1.0.1"))) {
		t.Fatalf("destination = %q; want the canonical published bytes", got)
	}
	assertNoStagingLeftovers(t, dir)
}

// assertNothingWritten proves a refused publication left the directory
// with no destination and no staging leftovers.
func assertNothingWritten(t *testing.T, dir string) {
	t.Helper()
	if _, lerr := os.Lstat(filepath.Join(dir, FileName)); !errors.Is(lerr, os.ErrNotExist) {
		t.Fatalf("destination exists in %s; a refused capability must commit nothing (%v)", dir, lerr)
	}
	assertNoStagingLeftovers(t, dir)
}
