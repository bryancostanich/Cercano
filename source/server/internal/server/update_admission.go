package server

import (
	"context"
	"errors"
	"strings"
	"sync"

	"cercano/source/server/pkg/proto"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// updateAdmission counts actual admitted lifetimes, not broker generations or
// connected observers. Its zero value is ready. No public RPC arms it yet:
// background work sources must be covered before exposing update preparation.
type updateAdmission struct {
	mu      sync.Mutex
	active  uint64
	paused  bool
	changed chan struct{}
}

var errUpdateAdmissionPaused = errors.New("agent update preparation is active; retry shortly")

func (g *updateAdmission) notifyLocked() {
	if g.changed != nil {
		close(g.changed)
	}
	g.changed = make(chan struct{})
}
func (g *updateAdmission) enter() (func(), error) {
	g.mu.Lock()
	if g.paused {
		g.mu.Unlock()
		return nil, errUpdateAdmissionPaused
	}
	g.active++
	g.mu.Unlock()
	var once sync.Once
	return func() { once.Do(func() { g.mu.Lock(); g.active--; g.notifyLocked(); g.mu.Unlock() }) }, nil
}

// pauseWhenIdle does not cancel work or freeze nested calls while work exists.
// Once zero is observed under the admission mutex, new work is refused atomically.
// Cancellation while waiting leaves normal admission untouched. The successful
// caller owns release; its eventual stream/process lifetime will govern release.
func (g *updateAdmission) pauseWhenIdle(ctx context.Context) (func(), error) {
	for {
		g.mu.Lock()
		if err := ctx.Err(); err != nil {
			g.mu.Unlock()
			return nil, err
		}
		if g.paused {
			g.mu.Unlock()
			return nil, errUpdateAdmissionPaused
		}
		if g.active == 0 {
			g.paused = true
			g.mu.Unlock()
			var once sync.Once
			return func() { once.Do(func() { g.mu.Lock(); g.paused = false; g.notifyLocked(); g.mu.Unlock() }) }, nil
		}
		if g.changed == nil {
			g.changed = make(chan struct{})
		}
		changed := g.changed
		g.mu.Unlock()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-changed:
		}
	}
}

func (s *Server) admitUpdateWork() (func(), error) {
	release, err := s.updateWork.enter()
	if err != nil {
		return nil, status.Error(codes.Unavailable, err.Error())
	}
	return release, nil
}

// agentFullMethodPrefix derives the Agent service's gRPC full-method prefix
// from the generated Agent_ServiceDesc — the actual registered service name,
// never a guessed string — so admission coverage stays pinned to the real
// service identity if the proto is ever regenerated. Non-Agent services
// (worker accounting, anything else registered on the same gRPC server) never
// match the prefix and are left untouched.
func agentFullMethodPrefix() string {
	return "/" + proto.Agent_ServiceDesc.ServiceName + "/"
}

// updateAdmissionExemptUnary lists the DOCUMENTED administrative Agent unary
// methods that stay callable while update preparation holds the pause.
//
// ShutdownAgent is the client-driven bounce the update flow itself depends on
// to swap binaries: its handler only schedules a self-SIGTERM (scheduleSelfShutdown),
// so refusing it under the pause would strand the paused process with no way
// for a client to restart it. It is the only unary exemption.
//
// Deliberately NOT exempt: RestartRuntime and RestartMcpServer perform actual
// component work (stopping/starting inference instances; synchronous MCP child
// reconnects), which is exactly the work class update preparation waits for.
// GetAgentInfo does not exist in this proto (there is no such RPC on
// agent.Agent), so there is nothing to exempt under that name.
func updateAdmissionExemptUnary(prefix string) map[string]bool {
	return map[string]bool{prefix + "ShutdownAgent": true}
}

// updateAdmissionExemptStreams lists the DOCUMENTED passive Agent stream
// methods: pure observers that hold no update-relevant state and must never
// pin the work count — clients need to keep attaching and watching events
// (including the disconnect that tells them an update bounce is happening)
// while preparation is paused.
func updateAdmissionExemptStreams(prefix string) map[string]bool {
	return map[string]bool{
		prefix + "SubscribeEvents":    true,
		prefix + "AttachConversation": true,
		prefix + "StreamRuntimeLogs":  true,
	}
}

// UpdateAdmissionUnaryInterceptor returns a grpc.UnaryServerInterceptor that
// counts every Agent unary request lifetime against update admission by
// default. Unlisted (and future) Agent methods are admitted automatically —
// nothing silently escapes coverage when a new RPC ships. Non-Agent methods
// pass straight through to their handler.
//
// The count is held for the WHOLE handler lifetime and released when the
// handler returns (success or error); panics unwind through the defer too, so
// the chain must keep RecoveryUnaryInterceptor OUTERMOST.
//
// Handlers that already call admitUpdateWork keep their per-method guards:
// the nested enter/release pairs simply hold two counts for the same call
// (each release is once-guarded), and the pause boundary is a boolean, so the
// double count is safe — the call is only idle again when both are retired.
func (s *Server) UpdateAdmissionUnaryInterceptor() grpc.UnaryServerInterceptor {
	prefix := agentFullMethodPrefix()
	exempt := updateAdmissionExemptUnary(prefix)
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		if !strings.HasPrefix(info.FullMethod, prefix) || exempt[info.FullMethod] {
			return handler(ctx, req)
		}
		release, err := s.admitUpdateWork()
		if err != nil {
			return nil, err
		}
		defer release()
		return handler(ctx, req)
	}
}

// UpdateAdmissionStreamInterceptor returns a grpc.StreamServerInterceptor that
// counts every non-observer Agent stream lifetime against update admission,
// with the same default-admit semantics as the unary interceptor: unlisted and
// future Agent streams count; only the documented passive observer streams
// (SubscribeEvents, AttachConversation, StreamRuntimeLogs) pass through
// uncounted, and non-Agent services are untouched.
//
// The count is released only when the stream HANDLER returns — not when the
// stream's context is canceled. A canceled context still has to unwind
// through the handler (RecvMsg/SendMsg errors) before the lifetime retires,
// which is what pauseWhenIdle waits on. Existing per-stream guards double
// count safely, exactly as in the unary case.
func (s *Server) UpdateAdmissionStreamInterceptor() grpc.StreamServerInterceptor {
	prefix := agentFullMethodPrefix()
	exempt := updateAdmissionExemptStreams(prefix)
	return func(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		if !strings.HasPrefix(info.FullMethod, prefix) || exempt[info.FullMethod] {
			return handler(srv, ss)
		}
		release, err := s.admitUpdateWork()
		if err != nil {
			return err
		}
		defer release()
		return handler(srv, ss)
	}
}
