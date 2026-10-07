package server

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestUpdateAdmissionWaitsForActualLifetimes(t *testing.T) {
	var g updateAdmission
	first, e := g.enter()
	if e != nil {
		t.Fatal(e)
	}
	second, e := g.enter()
	if e != nil {
		t.Fatal(e)
	}
	first()
	first() // retiring one lifetime cannot retire the other
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	if release, e := g.pauseWhenIdle(ctx); !errors.Is(e, context.DeadlineExceeded) {
		if release != nil {
			release()
		}
		t.Fatalf("active work considered idle: %v", e)
	}
	// The attempted update did not block nested or new work while waiting.
	nested, e := g.enter()
	if e != nil {
		t.Fatal(e)
	}
	nested()
	second()
	release, e := g.pauseWhenIdle(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	if _, e = g.enter(); !errors.Is(e, errUpdateAdmissionPaused) {
		t.Fatalf("admission not sealed: %v", e)
	}
	release()
	release()
	next, e := g.enter()
	if e != nil {
		t.Fatal(e)
	}
	next()
}
func TestUpdateAdmissionCancelledWaitDoesNotPause(t *testing.T) {
	var g updateAdmission
	work, _ := g.enter()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		release, e := g.pauseWhenIdle(ctx)
		if release != nil {
			release()
		}
		done <- e
	}()
	cancel()
	work()
	if e := <-done; !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
	release, e := g.enter()
	if e != nil {
		t.Fatal(e)
	}
	release()
}
func TestUpdateAdmissionAtomicAgainstNewRequests(t *testing.T) {
	for i := 0; i < 25; i++ {
		var g updateAdmission
		old, _ := g.enter()
		start := make(chan struct{})
		var wg sync.WaitGroup
		for n := 0; n < 16; n++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				release, e := g.enter()
				if e != nil {
					if !errors.Is(e, errUpdateAdmissionPaused) {
						t.Error(e)
					}
					return
				}
				defer release()
				g.mu.Lock()
				if g.paused {
					t.Error("active work overlaps paused boundary")
				}
				g.mu.Unlock()
			}()
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		held := make(chan func(), 1)
		errs := make(chan error, 1)
		go func() {
			release, e := g.pauseWhenIdle(ctx)
			if e != nil {
				errs <- e
				return
			}
			held <- release
		}()
		close(start)
		old()
		wg.Wait()
		select {
		case release := <-held:
			g.mu.Lock()
			if g.active != 0 || !g.paused {
				t.Error("pause was not atomic")
			}
			g.mu.Unlock()
			release()
		case e := <-errs:
			t.Error(e)
		case <-ctx.Done():
			t.Error(ctx.Err())
		}
		cancel()
	}
}
func TestUpdateAdmissionPrimaryEntrypointsRefuseBeforeSideEffects(t *testing.T) {
	s := &Server{}
	release, e := s.updateWork.pauseWhenIdle(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	defer release()
	// Nil backend/request/stream is deliberate: rejection must happen before
	// persistence, provider access, logging request text, or starting a turn.
	if _, e = s.ProcessRequest(context.Background(), nil); status.Code(e) != codes.Unavailable {
		t.Fatalf("unary: %v", e)
	}
	if e = s.StreamProcessRequest(nil, nil); status.Code(e) != codes.Unavailable {
		t.Fatalf("stream: %v", e)
	}
	if out, err := s.InvokeTool(context.Background(), nil); err != nil || out == nil || out.Error == "" {
		t.Fatalf("legacy tool admitted: %+v %v", out, err)
	}
	if out, err := s.StartRuntimeModel(context.Background(), nil); err != nil || out == nil || out.Ok || out.Error == "" {
		t.Fatalf("runtime start admitted: %+v %v", out, err)
	}
	if out, err := s.DownloadRuntimeModel(context.Background(), nil); err != nil || out == nil || out.Ok || out.Error == "" {
		t.Fatalf("download admitted: %+v %v", out, err)
	}
	if err := s.InstallOpenRuntime(nil, nil); status.Code(err) != codes.Unavailable {
		t.Fatalf("runtime installation admitted: %v", err)
	}
	if err := s.StartChatGPTLogin(nil, nil); status.Code(err) != codes.Unavailable {
		t.Fatalf("login admitted: %v", err)
	}
	if err := s.StartClaudeLogin(nil, nil); status.Code(err) != codes.Unavailable {
		t.Fatalf("login admitted: %v", err)
	}
	if err := s.RegenerateContext(nil, nil); status.Code(err) != codes.Unavailable {
		t.Fatalf("compaction admitted: %v", err)
	}
	out, e := s.InvokeCapability(context.Background(), nil)
	if e != nil || out == nil || !out.IsError {
		t.Fatalf("tool: %+v %v", out, e)
	}
}
func TestUpdateAdmissionConcurrentPreparersDoNotReleaseOthersBarrier(t *testing.T) {
	var g updateAdmission
	release, e := g.pauseWhenIdle(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	defer release()
	other, e := g.pauseWhenIdle(context.Background())
	if other != nil || !errors.Is(e, errUpdateAdmissionPaused) {
		t.Fatalf("second preparer: %v", e)
	}
	if _, e = g.enter(); !errors.Is(e, errUpdateAdmissionPaused) {
		t.Fatal("second preparation disturbed first")
	}
}
