package server

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"cercano/source/server/pkg/proto"

	"google.golang.org/grpc"
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

// ---------------------------------------------------------------------------
// gRPC admission interceptors: default-count Agent request lifetimes at the
// transport boundary. These tests use in-memory fakes only — no live agent,
// models, or network.
// ---------------------------------------------------------------------------

// updateAdmissionFakeStream is a minimal grpc.ServerStream: the interceptors
// (and the fake handlers below) only ever touch Context(), so embedding the
// interface covers the rest.
type updateAdmissionFakeStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (f *updateAdmissionFakeStream) Context() context.Context { return f.ctx }

func updateAdmissionActive(s *Server) uint64 {
	s.updateWork.mu.Lock()
	defer s.updateWork.mu.Unlock()
	return s.updateWork.active
}

func TestUpdateAdmissionUnaryInterceptorRefusesBeforeHandlerSideEffects(t *testing.T) {
	s := NewServer(nil, nil, nil, nil, nil)
	release, err := s.updateWork.pauseWhenIdle(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	called := false
	icpt := s.UpdateAdmissionUnaryInterceptor()
	_, err = icpt(context.Background(), nil,
		&grpc.UnaryServerInfo{FullMethod: proto.Agent_ProcessRequest_FullMethodName},
		func(ctx context.Context, req any) (any, error) {
			called = true
			return "side effects ran", nil
		})
	if status.Code(err) != codes.Unavailable {
		t.Fatalf("paused unary not refused: %v", err)
	}
	if called {
		t.Fatal("handler ran before admission check")
	}
}

func TestUpdateAdmissionUnaryInterceptorReleasesOnHandlerReturn(t *testing.T) {
	s := NewServer(nil, nil, nil, nil, nil)
	icpt := s.UpdateAdmissionUnaryInterceptor()
	info := &grpc.UnaryServerInfo{FullMethod: proto.Agent_ListConversations_FullMethodName}
	inHandler := make(chan struct{})
	if _, err := icpt(context.Background(), nil, info, func(ctx context.Context, req any) (any, error) {
		close(inHandler)
		if got := updateAdmissionActive(s); got != 1 {
			t.Errorf("handler lifetime not counted: %d", got)
		}
		return nil, errors.New("handler error")
	}); err == nil {
		t.Fatal("handler error swallowed")
	}
	<-inHandler
	if got := updateAdmissionActive(s); got != 0 {
		t.Fatalf("count not released after handler error: %d", got)
	}
	if _, err := icpt(context.Background(), nil, info, func(ctx context.Context, req any) (any, error) {
		return nil, nil
	}); err != nil {
		t.Fatal(err)
	}
	if got := updateAdmissionActive(s); got != 0 {
		t.Fatalf("count not released after handler success: %d", got)
	}
}

func TestUpdateAdmissionUnaryInterceptorReleasesPanicThroughRecovery(t *testing.T) {
	s := NewServer(nil, nil, nil, nil, nil)
	admission := s.UpdateAdmissionUnaryInterceptor()
	info := &grpc.UnaryServerInfo{FullMethod: proto.Agent_InvokeTool_FullMethodName}
	// Chain exactly as main wires it: recovery OUTERMOST, admission inner.
	_, chained := RecoveryUnaryInterceptor()(context.Background(), nil, info,
		func(ctx context.Context, req any) (any, error) {
			return admission(ctx, req, info, func(ctx context.Context, req any) (any, error) {
				if got := updateAdmissionActive(s); got != 1 {
					t.Errorf("panicking handler lifetime not counted: %d", got)
				}
				panic("boom")
			})
		})
	if status.Code(chained) != codes.Internal {
		t.Fatalf("panic not converted to Internal: %v", chained)
	}
	if got := updateAdmissionActive(s); got != 0 {
		t.Fatalf("count not released after panic: %d", got)
	}
}

func TestUpdateAdmissionStreamInterceptorHoldsUntilHandlerReturn(t *testing.T) {
	s := NewServer(nil, nil, nil, nil, nil)
	icpt := s.UpdateAdmissionStreamInterceptor()
	ctx, cancel := context.WithCancel(context.Background())
	entered := make(chan struct{})
	proceed := make(chan struct{})
	done := make(chan error, 1)
	handler := func(srv any, ss grpc.ServerStream) error {
		close(entered)
		<-ss.Context().Done() // observe the client-side cancel…
		<-proceed             // …but the handler is still running
		return nil
	}
	info := &grpc.StreamServerInfo{FullMethod: proto.Agent_StreamProcessRequest_FullMethodName}
	go func() {
		done <- icpt(nil, &updateAdmissionFakeStream{ctx: ctx}, info, handler)
	}()
	<-entered
	cancel()
	// The stream context is canceled, but the count must hold until the
	// handler actually returns.
	time.Sleep(25 * time.Millisecond)
	if got := updateAdmissionActive(s); got != 1 {
		t.Fatalf("stream count released on ctx cancel before handler return: %d", got)
	}
	close(proceed)
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("stream handler never returned")
	}
	if got := updateAdmissionActive(s); got != 0 {
		t.Fatalf("stream count not released after handler return: %d", got)
	}
}

func TestUpdateAdmissionStreamInterceptorReleasesPanicThroughRecovery(t *testing.T) {
	s := NewServer(nil, nil, nil, nil, nil)
	admission := s.UpdateAdmissionStreamInterceptor()
	info := &grpc.StreamServerInfo{FullMethod: proto.Agent_InstallOpenRuntime_FullMethodName}
	// Chain exactly as main wires it: recovery OUTERMOST, admission inner.
	chained := RecoveryStreamInterceptor()(nil,
		&updateAdmissionFakeStream{ctx: context.Background()}, info,
		func(srv any, ss grpc.ServerStream) error {
			return admission(srv, ss, info, func(srv any, ss grpc.ServerStream) error {
				if got := updateAdmissionActive(s); got != 1 {
					t.Errorf("panicking stream lifetime not counted: %d", got)
				}
				panic("boom")
			})
		})
	if status.Code(chained) != codes.Internal {
		t.Fatalf("stream panic not converted to Internal: %v", chained)
	}
	if got := updateAdmissionActive(s); got != 0 {
		t.Fatalf("stream count not released after panic: %d", got)
	}
}

func TestUpdateAdmissionObserverStreamsNeverHoldWorkCount(t *testing.T) {
	s := NewServer(nil, nil, nil, nil, nil)
	release, err := s.updateWork.pauseWhenIdle(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	icpt := s.UpdateAdmissionStreamInterceptor()
	for _, fullMethod := range []string{
		proto.Agent_SubscribeEvents_FullMethodName,
		proto.Agent_AttachConversation_FullMethodName,
		proto.Agent_StreamRuntimeLogs_FullMethodName,
	} {
		called := false
		err := icpt(nil, &updateAdmissionFakeStream{ctx: context.Background()},
			&grpc.StreamServerInfo{FullMethod: fullMethod},
			func(srv any, ss grpc.ServerStream) error {
				called = true
				if got := updateAdmissionActive(s); got != 0 {
					t.Errorf("%s pinned the work count: %d", fullMethod, got)
				}
				return nil
			})
		if err != nil {
			t.Fatalf("%s: %v", fullMethod, err)
		}
		if !called {
			t.Fatalf("%s: observer handler not invoked while paused", fullMethod)
		}
	}
}

func TestUpdateAdmissionExemptsShutdownAgentWhilePaused(t *testing.T) {
	s := NewServer(nil, nil, nil, nil, nil)
	release, err := s.updateWork.pauseWhenIdle(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	called := false
	_, err = s.UpdateAdmissionUnaryInterceptor()(context.Background(), nil,
		&grpc.UnaryServerInfo{FullMethod: proto.Agent_ShutdownAgent_FullMethodName},
		func(ctx context.Context, req any) (any, error) {
			called = true
			return nil, nil
		})
	if err != nil {
		t.Fatalf("administrative shutdown bounce refused while paused: %v", err)
	}
	if !called {
		t.Fatal("ShutdownAgent handler not invoked")
	}
	if got := updateAdmissionActive(s); got != 0 {
		t.Fatalf("ShutdownAgent pinned the work count: %d", got)
	}
}

func TestUpdateAdmissionCountsUnlistedAgentMethodsByDefault(t *testing.T) {
	s := NewServer(nil, nil, nil, nil, nil)
	prefix := agentFullMethodPrefix()
	// A future, not-yet-listed Agent method still counts — nothing silently
	// escapes coverage when a new RPC ships.
	futureMethod := prefix + "SomeFutureMethod"
	called := false
	err := s.UpdateAdmissionStreamInterceptor()(nil,
		&updateAdmissionFakeStream{ctx: context.Background()},
		&grpc.StreamServerInfo{FullMethod: futureMethod},
		func(srv any, ss grpc.ServerStream) error {
			called = true
			return nil
		})
	if err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("unlisted Agent method not admitted")
	}
	// And it is refused while update preparation holds the pause.
	release, rerr := s.updateWork.pauseWhenIdle(context.Background())
	if rerr != nil {
		t.Fatal(rerr)
	}
	defer release()
	called = false
	err = s.UpdateAdmissionStreamInterceptor()(nil,
		&updateAdmissionFakeStream{ctx: context.Background()},
		&grpc.StreamServerInfo{FullMethod: futureMethod},
		func(srv any, ss grpc.ServerStream) error {
			called = true
			return nil
		})
	if status.Code(err) != codes.Unavailable {
		t.Fatalf("unlisted Agent method not refused while paused: %v", err)
	}
	if called {
		t.Fatal("unlisted Agent handler ran while paused")
	}
}

func TestUpdateAdmissionLeavesNonAgentMethodsUntouched(t *testing.T) {
	s := NewServer(nil, nil, nil, nil, nil)
	release, err := s.updateWork.pauseWhenIdle(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	unaryCalled := false
	if _, err := s.UpdateAdmissionUnaryInterceptor()(context.Background(), nil,
		&grpc.UnaryServerInfo{FullMethod: proto.Worker_RunTurnWithAuthentication_FullMethodName},
		func(ctx context.Context, req any) (any, error) {
			unaryCalled = true
			return nil, nil
		}); err != nil {
		t.Fatalf("non-Agent unary refused while paused: %v", err)
	}
	if !unaryCalled {
		t.Fatal("non-Agent unary handler not invoked")
	}
	streamCalled := false
	if err := s.UpdateAdmissionStreamInterceptor()(nil,
		&updateAdmissionFakeStream{ctx: context.Background()},
		&grpc.StreamServerInfo{FullMethod: proto.Worker_RunTurn_FullMethodName},
		func(srv any, ss grpc.ServerStream) error {
			streamCalled = true
			return nil
		}); err != nil {
		t.Fatalf("non-Agent stream refused while paused: %v", err)
	}
	if !streamCalled {
		t.Fatal("non-Agent stream handler not invoked")
	}
	if got := updateAdmissionActive(s); got != 0 {
		t.Fatalf("non-Agent call pinned the work count: %d", got)
	}
}

func TestUpdateAdmissionInterceptorDoubleCountsSafelyWithHandlerGuard(t *testing.T) {
	s := NewServer(nil, nil, nil, nil, nil)
	icpt := s.UpdateAdmissionUnaryInterceptor()
	info := &grpc.UnaryServerInfo{FullMethod: proto.Agent_ProcessRequest_FullMethodName}
	// The real handler keeps its own per-method guard (admitUpdateWork).
	// The nested lifetimes must retire in reverse order and leave zero.
	if _, err := icpt(context.Background(), nil, info, func(ctx context.Context, req any) (any, error) {
		release, err := s.admitUpdateWork()
		if err != nil {
			return nil, err
		}
		defer release()
		if got := updateAdmissionActive(s); got != 2 {
			t.Errorf("nested guard not counted: %d", got)
		}
		return nil, nil
	}); err != nil {
		t.Fatal(err)
	}
	if got := updateAdmissionActive(s); got != 0 {
		t.Fatalf("nested lifetimes leaked a count: %d", got)
	}
	// The pause boundary is a boolean: retiring the outer (interceptor)
	// lifetime while the handler's own release is still pending must not
	// un-pause anything — only a fully idle gate pauses.
	release, err := s.updateWork.pauseWhenIdle(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	release()
}
