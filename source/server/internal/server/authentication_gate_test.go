package server

import (
	"context"
	"testing"
	"time"

	cfgsvc "cercano/source/server/internal/hostsvc/config"
	"cercano/source/server/internal/llm"
	"cercano/source/server/internal/runner"
	"cercano/source/server/internal/secrets"
	"cercano/source/server/pkg/config"
	"cercano/source/server/pkg/proto"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type authGateSink struct{ events chan runner.Event }

func (s authGateSink) Emit(e runner.Event) { s.events <- e }

type authGateResult struct {
	choice llm.AuthDecision
	err    error
}

func beginGate(t *testing.T, s *Server, conversation, profile string) (*runner.Authentication, <-chan authGateResult, context.CancelFunc) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	t.Cleanup(cancel)
	sink := authGateSink{events: make(chan runner.Event, 4)}
	done := make(chan authGateResult, 1)
	go func() {
		choice, err := s.authenticationRequester(conversation, sink)(ctx, llm.AuthChallenge{Provider: "anthropic", Profile: profile, Fallback: "backup", RetrySafe: true})
		done <- authGateResult{choice, err}
	}()
	select {
	case e := <-sink.events:
		return e.Authentication, done, cancel
	case <-ctx.Done():
		t.Fatal("gate did not publish")
		return nil, nil, cancel
	}
}
func TestAuthenticationGateOwnershipAndSingleUse(t *testing.T) {
	s := &Server{cfgSvc: cfgsvc.New("", config.Defaults(), secrets.NewMemory())}
	gate, done, _ := beginGate(t, s, "owner", "work")
	request := &proto.AuthenticationDecisionRequest{ConversationId: "wrong", RequestId: gate.RequestID, Decision: "fallback"}
	if _, err := s.ResolveAuthentication(context.Background(), request); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("wrong conversation accepted: %v", err)
	}
	request.ConversationId = "owner"
	request.Decision = "unknown"
	if _, err := s.ResolveAuthentication(context.Background(), request); status.Code(err) != codes.InvalidArgument {
		t.Fatal(err)
	}
	request.Decision = "fallback"
	if _, err := s.ResolveAuthentication(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	if result := <-done; result.err != nil || result.choice != llm.AuthFallback {
		t.Fatalf("result=%+v", result)
	}
	if _, err := s.ResolveAuthentication(context.Background(), request); status.Code(err) != codes.FailedPrecondition {
		t.Fatal("duplicate response accepted")
	}
}
func TestOneLoginWakesConcurrentRequestsWithoutRevivingCanceledOnes(t *testing.T) {
	s := &Server{cfgSvc: cfgsvc.New("", config.Defaults(), secrets.NewMemory())}
	_, first, cancel := beginGate(t, s, "first", "work")
	_, second, _ := beginGate(t, s, "second", "work")
	other, third, _ := beginGate(t, s, "third", "other")
	cancel()
	if result := <-first; result.err == nil {
		t.Fatal("canceled request remained active")
	}
	login, err := s.cfgSvc.Credentials().BeginLogin(context.Background(), "work", "anthropic")
	if err != nil {
		t.Fatal(err)
	}
	defer login.Close()
	if err := login.Commit("synthetic-login"); err != nil {
		t.Fatal(err)
	}
	if result := <-second; result.err != nil || result.choice != llm.AuthLogin {
		t.Fatalf("shared login not observed: %+v", result)
	}
	select {
	case <-third:
		t.Fatal("unrelated profile resumed")
	default:
	}
	if _, err := s.ResolveAuthentication(context.Background(), &proto.AuthenticationDecisionRequest{ConversationId: "third", RequestId: other.RequestID, Decision: "cancel"}); err != nil {
		t.Fatal(err)
	}
	if result := <-third; result.choice != llm.AuthCancel {
		t.Fatalf("cancel=%+v", result)
	}
	count := 0
	s.authenticationWaiters.Range(func(any, any) bool { count++; return true })
	if count != 0 {
		t.Fatalf("orphan waiters=%d", count)
	}
}
func TestAuthenticationGateRejectsUnavailableFallback(t *testing.T) {
	s := &Server{cfgSvc: cfgsvc.New("", config.Defaults(), secrets.NewMemory())}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.authenticationWaiters.Store("c\x00id", &authenticationWaiter{ctx: ctx, routing: authenticationRoutingIdentity(s.cfgSvc.Get()), challenge: llm.AuthChallenge{RetrySafe: false}, reply: make(chan llm.AuthDecision, 1)})
	_, err := s.ResolveAuthentication(ctx, &proto.AuthenticationDecisionRequest{ConversationId: "c", RequestId: "id", Decision: "fallback"})
	if status.Code(err) != codes.PermissionDenied {
		t.Fatal(err)
	}
	if _, ok := s.authenticationWaiters.Load("c\x00id"); !ok {
		t.Fatal("invalid choice consumed gate")
	}
}

func TestAuthenticationFallbackRejectsChangedRouting(t *testing.T) {
	s := &Server{cfgSvc: cfgsvc.New("", config.Defaults(), secrets.NewMemory())}
	gate, done, _ := beginGate(t, s, "owner", "work")
	changed := s.cfgSvc.Get()
	changed.LocusMode = "open_only"
	if err := s.cfgSvc.Set(changed); err != nil {
		t.Fatal(err)
	}
	_, err := s.ResolveAuthentication(context.Background(), &proto.AuthenticationDecisionRequest{ConversationId: "owner", RequestId: gate.RequestID, Decision: "fallback"})
	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("changed routing was authorized: %v", err)
	}
	if result := <-done; result.choice != llm.AuthCancel {
		t.Fatalf("stale route gate left waiting: %+v", result)
	}
}
