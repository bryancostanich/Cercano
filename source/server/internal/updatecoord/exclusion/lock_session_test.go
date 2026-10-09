package exclusion

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// GuardSession capability contract tests: ClaimPublication is the only use
// of a session, and it validates EVERYTHING at use time against the
// handle's live state — a zero-value session proves nothing, an expired
// session (and every copy of it) proves nothing, a session claimed for
// another directory proves nothing, a session on a handle whose Close is
// pending proves nothing, and two concurrent uses of one live session can
// never race each other.

// TestGuardUpdateSessionRefusesUnprovenHandles proves GuardUpdateSession
// keeps every GuardUpdate refusal (it is the same guarded use, only the
// callback shape differs) and never mints a session on refusal.
func TestGuardUpdateSessionRefusesUnprovenHandles(t *testing.T) {
	t.Run("nil receiver", func(t *testing.T) {
		var h *Handle
		ran := false
		err := h.GuardUpdateSession("/must/not/run", func(GuardSession) error { ran = true; return nil })
		if !errors.Is(err, ErrNilHandle) {
			t.Fatalf("err = %v; want ErrNilHandle", err)
		}
		if ran {
			t.Fatal("callback ran on a nil handle")
		}
	})
	t.Run("nil callback", func(t *testing.T) {
		root := t.TempDir()
		h := acquire(t, root, Update)
		if err := h.GuardUpdateSession(root, nil); !errors.Is(err, ErrNilCallback) {
			t.Fatalf("err = %v; want ErrNilCallback", err)
		}
		// The refusal leaves the handle fully usable.
		if err := h.GuardUpdateSession(root, func(GuardSession) error { return nil }); err != nil {
			t.Fatalf("GuardUpdateSession after refusal err = %v; want nil", err)
		}
	})
	t.Run("shared launch handle", func(t *testing.T) {
		root := t.TempDir()
		h := acquire(t, root, Launch)
		ran := false
		err := h.GuardUpdateSession(root, func(GuardSession) error { ran = true; return nil })
		if !errors.Is(err, ErrSharedHandle) {
			t.Fatalf("err = %v; want ErrSharedHandle", err)
		}
		if ran {
			t.Fatal("callback ran under a shared lease")
		}
	})
	t.Run("foreign directory", func(t *testing.T) {
		here := t.TempDir()
		there := t.TempDir()
		h := acquire(t, there, Update)
		ran := false
		err := h.GuardUpdateSession(here, func(GuardSession) error { ran = true; return nil })
		if !errors.Is(err, ErrForeignDirectory) {
			t.Fatalf("err = %v; want ErrForeignDirectory", err)
		}
		if ran {
			t.Fatal("callback ran for a foreign directory")
		}
	})
	t.Run("closed handle", func(t *testing.T) {
		root := t.TempDir()
		h := acquire(t, root, Update)
		if err := h.Close(); err != nil {
			t.Fatal(err)
		}
		ran := false
		err := h.GuardUpdateSession(root, func(GuardSession) error { ran = true; return nil })
		if !errors.Is(err, ErrClosedHandle) {
			t.Fatalf("err = %v; want ErrClosedHandle", err)
		}
		if ran {
			t.Fatal("callback ran on a closed handle")
		}
	})
}

// TestClaimPublicationZeroValueSessionRefuses proves a zero-value
// GuardSession (and one on a nil handle) proves nothing: the claim is
// refused and never touches anything.
func TestClaimPublicationZeroValueSessionRefuses(t *testing.T) {
	var zero GuardSession
	if _, err := zero.ClaimPublication("/must/not/run"); !errors.Is(err, ErrNilHandle) {
		t.Fatalf("zero-value session err = %v; want ErrNilHandle", err)
	}
	var nilHandle GuardSession
	if _, err := nilHandle.ClaimPublication("/must/not/run"); !errors.Is(err, ErrNilHandle) {
		t.Fatalf("nil-handle session err = %v; want ErrNilHandle", err)
	}
}

// TestClaimPublicationExpiryAndCopies proves the session (and every copy
// taken while it was live) expires the moment its minting callback
// returns: a later claim is refused with ErrExpiredSession and nothing
// from the session survives.
func TestClaimPublicationExpiryAndCopies(t *testing.T) {
	root := t.TempDir()
	h := acquire(t, root, Update)
	var live, copyOfLive GuardSession
	if err := h.GuardUpdateSession(root, func(g GuardSession) error {
		live = g
		copyOfLive = g
		// Inside the minting callback the session IS live: the claim works.
		release, err := g.ClaimPublication(root)
		if err != nil {
			t.Fatalf("live session claim err = %v; want nil", err)
		}
		release()
		release() // a second release is a safe no-op
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := live.ClaimPublication(root); !errors.Is(err, ErrExpiredSession) {
		t.Fatalf("expired session err = %v; want ErrExpiredSession", err)
	}
	if _, err := copyOfLive.ClaimPublication(root); !errors.Is(err, ErrExpiredSession) {
		t.Fatalf("copied expired session err = %v; want ErrExpiredSession (copies expire too)", err)
	}

	// A NEW session (a second guarded use) is live again: reuse works
	// and the OLD token still proves nothing even then.
	if err := h.GuardUpdateSession(root, func(g GuardSession) error {
		release, err := g.ClaimPublication(root)
		if err != nil {
			t.Fatalf("second session claim err = %v; want nil", err)
		}
		release()
		// The first callback's token must not validate against the
		// CURRENT live session.
		if _, err := live.ClaimPublication(root); !errors.Is(err, ErrExpiredSession) {
			t.Fatalf("stale token against a newer live session err = %v; want ErrExpiredSession", err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

// TestClaimPublicationForeignDirectoryRefuses proves a session minted for
// one directory cannot claim publication for another (or for the empty
// string): the binding is exact, and the refusal leaves the session's
// claim slot free for the bound directory.
func TestClaimPublicationForeignDirectoryRefuses(t *testing.T) {
	root := t.TempDir()
	other := t.TempDir()
	h := acquire(t, root, Update)
	if err := h.GuardUpdateSession(root, func(g GuardSession) error {
		if _, err := g.ClaimPublication(other); !errors.Is(err, ErrForeignDirectory) {
			t.Fatalf("foreign directory err = %v; want ErrForeignDirectory", err)
		}
		if _, err := g.ClaimPublication(""); !errors.Is(err, ErrForeignDirectory) {
			t.Fatalf("empty directory err = %v; want ErrForeignDirectory", err)
		}
		// The refused claims did not consume the claim slot: the bound
		// directory still claims fine.
		release, err := g.ClaimPublication(root)
		if err != nil {
			t.Fatalf("bound directory claim err = %v; want nil", err)
		}
		release()
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

// TestClaimPublicationConcurrentUseSerializesOrRefuses proves two
// concurrent uses of one live session can never race each other: while
// one claim is held every other claim of the same session is refused
// with ErrSessionBusy, and under genuine concurrency the number of
// simultaneously-held claims never exceeds one.
func TestClaimPublicationConcurrentUseSerializesOrRefuses(t *testing.T) {
	root := t.TempDir()
	h := acquire(t, root, Update)
	if err := h.GuardUpdateSession(root, func(g GuardSession) error {
		release, err := g.ClaimPublication(root)
		if err != nil {
			t.Fatal(err)
		}
		defer release()
		// A second claim while the first is held: refused, not queued.
		if _, err := g.ClaimPublication(root); !errors.Is(err, ErrSessionBusy) {
			t.Fatalf("concurrent claim err = %v; want ErrSessionBusy", err)
		}
		// After the release the claim is free again — but the deferred
		// release has not run yet, so run the genuine concurrency probe
		// with the claim slot as the only serialization point.
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	// Genuine concurrency: many goroutines claim the same live session
	// (copying it freely). At most one may hold the claim at a time.
	var held, peak atomic.Int64
	if err := h.GuardUpdateSession(root, func(g GuardSession) error {
		const workers = 8
		var wg sync.WaitGroup
		start := make(chan struct{})
		for i := 0; i < workers; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				token := g // a COPY: if copies raced, the probe would catch it
				release, err := token.ClaimPublication(root)
				if err != nil {
					if !errors.Is(err, ErrSessionBusy) {
						t.Errorf("concurrent claim err = %v; want ErrSessionBusy or nil", err)
					}
					return
				}
				c := held.Add(1)
				for {
					p := peak.Load()
					if c <= p || peak.CompareAndSwap(p, c) {
						break
					}
				}
				time.Sleep(2 * time.Millisecond) // widen the race window
				held.Add(-1)
				release()
			}()
		}
		close(start)
		wg.Wait()
		if got := peak.Load(); got != 1 {
			t.Fatalf("peak simultaneously-held claims %d, want 1 (publications must not race)", got)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

// TestClaimPublicationRefusedWhileClosePending proves a session on a
// handle whose Close is pending fails closed: the claim is refused with
// ErrClosedHandle, the pending Close still completes once the guarded
// callback returns, and no deadlock forms between the pending Close and
// the live session.
func TestClaimPublicationRefusedWhileClosePending(t *testing.T) {
	root := t.TempDir()
	h := acquire(t, root, Update)

	inCallback := make(chan struct{})
	releaseCallback := make(chan struct{})
	guardDone := make(chan error, 1)
	go func() {
		guardDone <- h.GuardUpdateSession(root, func(g GuardSession) error {
			close(inCallback)
			<-releaseCallback
			// A Close requested while this callback runs must be visible
			// to the live session's claim: fail closed, not stale-open.
			if _, err := g.ClaimPublication(root); !errors.Is(err, ErrClosedHandle) {
				t.Errorf("claim while Close pending err = %v; want ErrClosedHandle", err)
			}
			return nil
		})
	}()
	<-inCallback

	closeStarted := make(chan struct{})
	closeDone := make(chan error, 1)
	go func() {
		close(closeStarted)
		closeDone <- h.Close()
	}()
	<-closeStarted
	select {
	case err := <-closeDone:
		t.Fatalf("Close returned (%v) while the guarded callback still ran", err)
	case <-time.After(100 * time.Millisecond):
	}

	close(releaseCallback)
	if err := <-guardDone; err != nil {
		t.Fatalf("guarded callback err = %v; want nil", err)
	}
	select {
	case err := <-closeDone:
		if err != nil {
			t.Fatalf("Close err = %v; want nil", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Close never completed after the guarded callback returned")
	}
}
