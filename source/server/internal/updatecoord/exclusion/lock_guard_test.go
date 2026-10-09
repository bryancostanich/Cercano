package exclusion

import (
	"errors"
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
