package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"cercano/source/server/internal/locus"
	"cercano/source/server/pkg/config"
)

// The DeferralError gate: a size refusal from the local summarizer should only
// reach the cloud when the user's locus actually puts co-processor work there.
func TestCloudIsPrimaryLocus(t *testing.T) {
	for _, tc := range []struct {
		mode string
		want bool
	}{
		// cloud_only has nowhere else to go — deferral must be allowed to
		// spend cloud tokens or compaction cannot make progress at all.
		{string(locus.CloudOnly), true},
		// cloud_primary keeps grunt work local via Coproc(), so an oversized
		// segment defers rather than silently billing the cloud.
		{string(locus.CloudPrimary), false},
		{string(locus.OpenPrimary), false},
		{string(locus.OpenOnly), false},
		// Empty resolves to DefaultMode (cloud_primary) → local coproc.
		{"", false},
		// Garbage must not fail open into cloud spend.
		{"not_a_mode", false},
		// Legacy aliases are normalized at load time, not here; verify the
		// raw legacy string still does not fail open.
		{"local_only", false},
	} {
		if got := cloudIsPrimaryLocus(config.Config{LocusMode: tc.mode}); got != tc.want {
			t.Errorf("cloudIsPrimaryLocus(%q) = %v, want %v", tc.mode, got, tc.want)
		}
	}
}

// cloudFallbackTimeout must outlive a slow cloud call but stay well inside the
// shutdown drain, or a fallback in flight blocks a clean exit.
func TestCloudFallbackTimeoutFitsDrainGrace(t *testing.T) {
	if cloudFallbackTimeout >= drainGrace {
		t.Fatalf("cloudFallbackTimeout %v must be < drainGrace %v", cloudFallbackTimeout, drainGrace)
	}
	if cloudFallbackTimeout < 30*time.Second {
		t.Fatalf("cloudFallbackTimeout %v is too tight to be worth detaching for", cloudFallbackTimeout)
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
