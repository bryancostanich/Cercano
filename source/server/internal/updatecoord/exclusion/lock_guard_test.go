package exclusion

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// TestGuardUpdateRefusesUnprovenHandles proves every guarded-use refusal
// path runs NO callback and leaves the handle untouched: a nil receiver,
// a zero-value handle, a closed handle, a shared Launch lease that is
// still live, and a handle bound to a different directory (including the
// empty string). The shared-lease and foreign-directory cases also prove
// mode and directory were RECORDED AT ACQUIRE time, not inferred from
// the receiver later.
func TestGuardUpdateRefusesUnprovenHandles(t *testing.T) {
	t.Run("nil receiver", func(t *testing.T) {
		var h *Handle
		ran := false
		err := h.GuardUpdate("/must/not/run", func() error { ran = true; return nil })
		if !errors.Is(err, ErrNilHandle) {
			t.Fatalf("GuardUpdate(nil) err = %v; want ErrNilHandle", err)
		}
		if ran {
			t.Fatal("callback ran on a nil handle")
		}
	})
	t.Run("zero-value handle", func(t *testing.T) {
		var h Handle
		ran := false
		err := h.GuardUpdate("/must/not/run", func() error { ran = true; return nil })
		if !errors.Is(err, ErrNilHandle) {
			t.Fatalf("GuardUpdate(zero) err = %v; want ErrNilHandle", err)
		}
		if ran {
			t.Fatal("callback ran on a zero-value handle")
		}
	})
	t.Run("closed handle", func(t *testing.T) {
		root := t.TempDir()
		h := acquire(t, root, Update)
		if err := h.Close(); err != nil {
			t.Fatal(err)
		}
		ran := false
		err := h.GuardUpdate(root, func() error { ran = true; return nil })
		if !errors.Is(err, ErrClosedHandle) {
			t.Fatalf("GuardUpdate(closed) err = %v; want ErrClosedHandle", err)
		}
		if ran {
			t.Fatal("callback ran on a closed handle")
		}
	})
	t.Run("shared launch handle refused while still live", func(t *testing.T) {
		root := t.TempDir()
		h := acquire(t, root, Launch)
		ran := false
		err := h.GuardUpdate(root, func() error { ran = true; return nil })
		if !errors.Is(err, ErrSharedHandle) {
			t.Fatalf("GuardUpdate(launch) err = %v; want ErrSharedHandle", err)
		}
		if ran {
			t.Fatal("callback ran under a shared lease")
		}
		// The lease itself is untouched by the refusal: Close still works.
		if err := h.Close(); err != nil {
			t.Fatalf("Close after refused guard: %v", err)
		}
	})
	t.Run("foreign directory", func(t *testing.T) {
		here := t.TempDir()
		there := t.TempDir()
		h := acquire(t, there, Update)
		ran := false
		err := h.GuardUpdate(here, func() error { ran = true; return nil })
		if !errors.Is(err, ErrForeignDirectory) {
			t.Fatalf("GuardUpdate(here, bound elsewhere) err = %v; want ErrForeignDirectory", err)
		}
		if ran {
			t.Fatal("callback ran on a foreign-bound handle")
		}
		// The same handle still guards its own recorded directory: the
		// binding check is exact-path, not mode-guessing.
		if err := h.GuardUpdate(there, func() error { return nil }); err != nil {
			t.Fatalf("GuardUpdate(bound directory) err = %v; want nil", err)
		}
		// The empty string is never a directory association.
		if err := h.GuardUpdate("", func() error { ran = true; return nil }); !errors.Is(err, ErrForeignDirectory) {
			t.Fatalf("GuardUpdate(\"\") err = %v; want ErrForeignDirectory", err)
		}
		if ran {
			t.Fatal("callback ran for an empty directory association")
		}
	})
}

// TestGuardUpdateSerializesClose proves Close cannot release the lease
// while a guarded callback is mid-flight: it blocks until the callback
// returns, a pending Close makes NEW guards fail closed with
// ErrClosedHandle, and once the callback returns the OS lease is
// genuinely released (a fresh exclusive acquire succeeds).
func TestGuardUpdateSerializesClose(t *testing.T) {
	root := t.TempDir()
	h := acquire(t, root, Update)

	inCallback := make(chan struct{})
	release := make(chan struct{})
	guardDone := make(chan error, 1)
	go func() {
		guardDone <- h.GuardUpdate(root, func() error {
			close(inCallback)
			<-release
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
		t.Fatalf("Close returned (%v) while the guarded callback was still running; the lease must stay held", err)
	case <-time.After(100 * time.Millisecond):
	}

	// A Close that is pending mid-callback must be visible to NEW guarded
	// uses: they fail closed, not on the stale pre-Close state.
	deadline := time.Now().Add(3 * time.Second)
	for {
		ran := false
		err := h.GuardUpdate(root, func() error { ran = true; return nil })
		if !ran && errors.Is(err, ErrClosedHandle) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("pending Close never became visible: guard err = %v, ran = %v", err, ran)
		}
		time.Sleep(2 * time.Millisecond)
	}

	close(release)
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

	// The lease is genuinely gone: a fresh exclusive acquire must succeed.
	fresh := acquire(t, root, Update)
	_ = fresh // released by the acquire helper's cleanup
}

// TestGuardUpdateCallbackDoesNotOutliveGuard proves the guard is fully
// released the moment GuardUpdate returns: repeated sequential guards do
// not leak guard state, and a later Close completes without waiting.
func TestGuardUpdateCallbackDoesNotOutliveGuard(t *testing.T) {
	root := t.TempDir()
	h := acquire(t, root, Update)
	for i := 0; i < 16; i++ {
		if err := h.GuardUpdate(root, func() error { return nil }); err != nil {
			t.Fatalf("GuardUpdate #%d err = %v; want nil", i, err)
		}
	}
	closeDone := make(chan error, 1)
	go func() { closeDone <- h.Close() }()
	select {
	case err := <-closeDone:
		if err != nil {
			t.Fatalf("Close err = %v; want nil", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Close blocked after GuardUpdate returned; the guard outlived the callback")
	}
	ran := false
	if err := h.GuardUpdate(root, func() error { ran = true; return nil }); !errors.Is(err, ErrClosedHandle) || ran {
		t.Fatalf("GuardUpdate after Close = (err %v, ran %v); want ErrClosedHandle with no callback", err, ran)
	}
}

// TestGuardUpdateSerializesConcurrentUsesOnOneHandle reproduces the
// overlap bug: two guarded callbacks on the same handle used to run
// concurrently (a simple counter admitted both), so two callers racing
// an expected-state check could both observe the old state and the last
// writer silently won. Now the second callback must not even start
// until the first has finished.
func TestGuardUpdateSerializesConcurrentUsesOnOneHandle(t *testing.T) {
	root := t.TempDir()
	h := acquire(t, root, Update)

	firstEntered := make(chan struct{})
	release := make(chan struct{})
	firstDone := make(chan struct{})
	go func() {
		defer close(firstDone)
		if err := h.GuardUpdate(root, func() error {
			close(firstEntered)
			<-release
			return nil
		}); err != nil {
			t.Error(err)
		}
	}()
	<-firstEntered

	secondRan := make(chan struct{})
	secondDone := make(chan error, 1)
	go func() {
		secondDone <- h.GuardUpdate(root, func() error {
			close(secondRan)
			return nil
		})
	}()
	// While the first callback holds the guard slot, the second must not
	// run: overlapping publications on one lease race each other.
	select {
	case <-secondRan:
		t.Fatal("two guarded callbacks overlapped on one handle; uses must serialize")
	case <-time.After(100 * time.Millisecond):
	}

	close(release)
	<-firstDone
	if err := <-secondDone; err != nil {
		t.Fatalf("second guarded use err = %v; want nil", err)
	}
	select {
	case <-secondRan:
	case <-time.After(3 * time.Second):
		t.Fatal("second guarded use never ran after the first finished")
	}
}

// TestClosePendingRefusesQueuedGuardWaiters proves a Close that arrives
// while a callback runs both blocks on the live callback and refuses the
// waiters queued behind it: the queued guard fails closed with
// ErrClosedHandle (its callback never runs), and neither Close nor the
// waiters deadlock against each other.
func TestClosePendingRefusesQueuedGuardWaiters(t *testing.T) {
	root := t.TempDir()
	h := acquire(t, root, Update)

	inCallback := make(chan struct{})
	release := make(chan struct{})
	guardDone := make(chan error, 1)
	go func() {
		guardDone <- h.GuardUpdate(root, func() error {
			close(inCallback)
			<-release
			return nil
		})
	}()
	<-inCallback

	waiterStarted := make(chan struct{})
	waiterDone := make(chan error, 1)
	go func() {
		close(waiterStarted)
		waiterDone <- h.GuardUpdate(root, func() error { return nil })
	}()
	<-waiterStarted
	time.Sleep(10 * time.Millisecond) // let the waiter reach the queue behind the live guard

	closeStarted := make(chan struct{})
	closeDone := make(chan error, 1)
	go func() {
		close(closeStarted)
		closeDone <- h.Close()
	}()
	<-closeStarted

	close(release)
	if err := <-guardDone; err != nil {
		t.Fatalf("live guarded use err = %v; want nil", err)
	}
	select {
	case err := <-waiterDone:
		if !errors.Is(err, ErrClosedHandle) {
			t.Fatalf("queued waiter err = %v; want ErrClosedHandle", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("queued waiter never returned; a pending Close must refuse waiters, not park them")
	}
	select {
	case err := <-closeDone:
		if err != nil {
			t.Fatalf("Close err = %v; want nil", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Close never completed after the live guard returned")
	}
}

// TestConcurrentCloseWaitersBlockUntilGuardFinishes proves many
// concurrent Closes all block while a guarded callback runs, all return
// once it finishes, and none releases the lease early or reports an
// error from a race with the others.
func TestConcurrentCloseWaitersBlockUntilGuardFinishes(t *testing.T) {
	root := t.TempDir()
	h := acquire(t, root, Update)

	inCallback := make(chan struct{})
	release := make(chan struct{})
	guardDone := make(chan error, 1)
	go func() {
		guardDone <- h.GuardUpdate(root, func() error {
			close(inCallback)
			<-release
			return nil
		})
	}()
	<-inCallback

	const waiters = 4
	closeDone := make(chan error, waiters)
	for i := 0; i < waiters; i++ {
		go func() { closeDone <- h.Close() }()
	}
	select {
	case err := <-closeDone:
		t.Fatalf("a Close returned (%v) while the guarded callback still ran; the lease must stay held", err)
	case <-time.After(100 * time.Millisecond):
	}

	close(release)
	if err := <-guardDone; err != nil {
		t.Fatalf("guarded use err = %v; want nil", err)
	}
	for i := 0; i < waiters; i++ {
		select {
		case err := <-closeDone:
			if err != nil {
				t.Fatalf("Close waiter err = %v; want nil", err)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("a concurrent Close waiter never completed")
		}
	}
}

// TestGuardUpdateRefusesNilCallback proves a nil callback is refused as
// a typed error, not run (and not allowed to panic mid-guard), and the
// refusal leaves the handle fully usable.
func TestGuardUpdateRefusesNilCallback(t *testing.T) {
	root := t.TempDir()
	h := acquire(t, root, Update)
	if err := h.GuardUpdate(root, nil); !errors.Is(err, ErrNilCallback) {
		t.Fatalf("GuardUpdate(nil callback) err = %v; want ErrNilCallback", err)
	}
	if err := h.GuardUpdate(root, func() error { return nil }); err != nil {
		t.Fatalf("GuardUpdate after nil-callback refusal err = %v; want nil (handle must stay usable)", err)
	}
}

// TestZeroValueHandleCloseIsSafeAndIdempotent proves closing a
// zero-value Handle is a safe no-op that never touches an unheld
// descriptor and stays idempotent, and a nil receiver behaves the same.
func TestZeroValueHandleCloseIsSafeAndIdempotent(t *testing.T) {
	var h Handle
	if err := h.Close(); err != nil {
		t.Fatalf("Close(zero-value) err = %v; want nil (nothing was ever held)", err)
	}
	if err := h.Close(); err != nil {
		t.Fatalf("second Close(zero-value) err = %v; want nil (idempotent)", err)
	}
	if err := h.GuardUpdate("/must/not/run", func() error { return nil }); !errors.Is(err, ErrNilHandle) {
		t.Fatalf("GuardUpdate(zero-value) err = %v; want ErrNilHandle", err)
	}
	var p *Handle
	if err := p.Close(); err != nil {
		t.Fatalf("Close(nil) err = %v; want nil", err)
	}
}

// TestGuardUpdateRefusesReplacedLockFileOrDirectory proves the identity
// recheck before every guarded callback: if update.lock or the directory
// was renamed or replaced while the OS lock keeps pinning the old inode,
// the guard refuses and the callback never runs. Close afterwards still
// releases the pinned old-inode lease cleanly.
func TestGuardUpdateRefusesReplacedLockFileOrDirectory(t *testing.T) {
	t.Run("replaced update.lock", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("an open locked file cannot be replaced on Windows")
		}
		root := t.TempDir()
		h := acquire(t, root, Update)
		lockPath := filepath.Join(root, "update.lock")
		if err := os.Remove(lockPath); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(lockPath, []byte("impostor"), 0o600); err != nil {
			t.Fatal(err)
		}
		ran := false
		err := h.GuardUpdate(root, func() error { ran = true; return nil })
		if !errors.Is(err, ErrReplacedLock) {
			t.Fatalf("GuardUpdate(replaced lock file) err = %v; want ErrReplacedLock", err)
		}
		if ran {
			t.Fatal("callback ran on a replaced lock file")
		}
		if err := h.Close(); err != nil {
			t.Fatalf("Close after replaced lock file err = %v; want nil (the pinned inode still releases)", err)
		}
	})
	t.Run("replaced directory", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("a directory holding an open lock file cannot be renamed on Windows")
		}
		base := t.TempDir()
		root := filepath.Join(base, "root")
		if err := os.Mkdir(root, 0o700); err != nil {
			t.Fatal(err)
		}
		h := acquire(t, root, Update)
		if err := os.Rename(root, filepath.Join(base, "moved")); err != nil {
			t.Skipf("cannot move the locked fixture directory: %v", err)
		}
		if err := os.Mkdir(root, 0o700); err != nil {
			t.Fatal(err)
		}
		ran := false
		err := h.GuardUpdate(root, func() error { ran = true; return nil })
		if !errors.Is(err, ErrReplacedLock) {
			t.Fatalf("GuardUpdate(replaced directory) err = %v; want ErrReplacedLock", err)
		}
		if ran {
			t.Fatal("callback ran on a replaced directory")
		}
		if err := h.Close(); err != nil {
			t.Fatalf("Close after replaced directory err = %v; want nil", err)
		}
	})
	t.Run("removed directory", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("a directory holding an open lock file cannot be removed on Windows")
		}
		root := t.TempDir()
		h := acquire(t, root, Update)
		if err := os.RemoveAll(root); err != nil {
			t.Skipf("cannot remove the locked fixture directory: %v", err)
		}
		ran := false
		err := h.GuardUpdate(root, func() error { ran = true; return nil })
		if !errors.Is(err, ErrReplacedLock) {
			t.Fatalf("GuardUpdate(removed directory) err = %v; want ErrReplacedLock", err)
		}
		if !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("GuardUpdate(removed directory) err = %v; want the refusal to wrap fs.ErrNotExist", err)
		}
		if ran {
			t.Fatal("callback ran on a removed directory")
		}
		if err := h.Close(); err != nil {
			t.Fatalf("Close after removed directory err = %v; want nil", err)
		}
	})
}

// TestGuardUpdatePropagatesErrorAndNeverSwallowsPanics proves the
// callback's error is propagated unchanged, and that a panicking callback
// never wedges Close: the guard is released as the panic unwinds while
// the panic itself is re-raised, not swallowed.
func TestGuardUpdatePropagatesErrorAndNeverSwallowsPanics(t *testing.T) {
	root := t.TempDir()
	sentinel := errors.New("guarded work failed")
	h := acquire(t, root, Update)
	if err := h.GuardUpdate(root, func() error { return sentinel }); !errors.Is(err, sentinel) {
		t.Fatalf("GuardUpdate err = %v; want the callback error %v unchanged", err, sentinel)
	}

	// A separate root: h still holds its exclusive lease (released by the
	// acquire helper's cleanup), so the second handle needs its own lock.
	root2 := t.TempDir()
	h2 := acquire(t, root2, Update)
	inCallback := make(chan struct{})
	recovered := make(chan any, 1)
	go func() {
		defer func() { recovered <- recover() }()
		_ = h2.GuardUpdate(root2, func() error {
			close(inCallback)
			panic("guarded use exploded")
		})
	}()
	<-inCallback
	select {
	case v := <-recovered:
		if v != "guarded use exploded" {
			t.Fatalf("panic swallowed or altered: %v", v)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("panic was swallowed: the guard never unwound")
	}
	closeDone := make(chan error, 1)
	go func() { closeDone <- h2.Close() }()
	select {
	case err := <-closeDone:
		if err != nil {
			t.Fatalf("Close after panicking guard err = %v; want nil", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Close blocked after a panicking callback; the guard outlived the panic")
	}
}
