package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"cercano/source/server/internal/loopcompact"
)

// cloudFallbackTimeout must outlive a slow cloud call but stay well inside the
// shutdown drain, or a fallback in flight blocks a clean exit. The constant
// itself now lives in internal/loopcompact (the ONE shared summarizer
// construction), so this asserts against the shared policy value.
func TestCloudFallbackTimeoutFitsDrainGrace(t *testing.T) {
	if loopcompact.CloudFallbackTimeout >= drainGrace {
		t.Fatalf("cloudFallbackTimeout %v must be < drainGrace %v", loopcompact.CloudFallbackTimeout, drainGrace)
	}
	if loopcompact.CloudFallbackTimeout < 30*time.Second {
		t.Fatalf("cloudFallbackTimeout %v is too tight to be worth detaching for", loopcompact.CloudFallbackTimeout)
	}
}

// detachedFallbackCtx mirrors the context construction at the cloud fallback
// site: escape the parent's DEADLINE, honor the parent's CANCELLATION.
func detachedFallbackCtx(parent context.Context, d time.Duration) (context.Context, func()) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(parent), d)
	stop := context.AfterFunc(parent, func() {
		if !errors.Is(context.Cause(parent), context.DeadlineExceeded) {
			cancel()
		}
	})
	return ctx, func() { stop(); cancel() }
}

// The bug this fixes: the fallback inherited an already-spent pass deadline and
// died before the request left the process.
func TestFallbackCtxSurvivesParentDeadline(t *testing.T) {
	parent, cancelParent := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancelParent()

	ctx, done := detachedFallbackCtx(parent, time.Minute)
	defer done()

	<-parent.Done() // parent deadline expires
	// Give AfterFunc a chance to run and (incorrectly) cancel.
	time.Sleep(50 * time.Millisecond)

	if err := ctx.Err(); err != nil {
		t.Fatalf("fallback ctx died with parent deadline: %v (the exact bug being fixed)", err)
	}
}

// But a real cancellation (shutdown) must still tear the fallback down, or the
// call outlives the process it belongs to.
func TestFallbackCtxHonorsParentCancel(t *testing.T) {
	parent, cancelParent := context.WithCancel(context.Background())
	ctx, done := detachedFallbackCtx(parent, time.Minute)
	defer done()

	cancelParent()

	select {
	case <-ctx.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("fallback ctx ignored parent cancellation; a shutdown would not drain")
	}
}

// The fallback still enforces a ceiling of its own.
func TestFallbackCtxEnforcesOwnTimeout(t *testing.T) {
	ctx, done := detachedFallbackCtx(context.Background(), 20*time.Millisecond)
	defer done()

	select {
	case <-ctx.Done():
		if !errors.Is(ctx.Err(), context.DeadlineExceeded) {
			t.Fatalf("want DeadlineExceeded, got %v", ctx.Err())
		}
	case <-time.After(2 * time.Second):
		t.Fatal("fallback ctx never expired on its own deadline")
	}
}
