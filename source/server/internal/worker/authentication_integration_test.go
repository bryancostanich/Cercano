package worker_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"cercano/source/server/internal/agenttools"
	cfgsvc "cercano/source/server/internal/hostsvc/config"
	providers "cercano/source/server/internal/hostsvc/providers"
	"cercano/source/server/internal/inference/resilience"
	"cercano/source/server/internal/llm"
	"cercano/source/server/internal/runner"
	"cercano/source/server/internal/secrets"
	"cercano/source/server/internal/worker"
	"cercano/source/server/pkg/config"
	"cercano/source/server/pkg/proto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/test/bufconn"
)

type loginThenEchoProvider struct {
	echoProvider
	calls atomic.Int32
}

func (p *loginThenEchoProvider) StreamChat(ctx context.Context, req llm.ChatRequest) (llm.StreamReader, error) {
	if p.calls.Add(1) == 1 {
		return nil, &llm.CredentialError{Class: llm.ErrLoginRequired, Provider: "anthropic", Profile: "work", Method: llm.AuthSubscription, Reason: llm.CredentialExpired}
	}
	return p.echoProvider.StreamChat(ctx, req)
}
func TestWorkerAuthenticationRoundTripResumesSameInference(t *testing.T) {
	provider := &loginThenEchoProvider{echoProvider: echoProvider{text: "resumed"}}
	ws := worker.NewWithFactories(func(*proto.StartTurn) (providers.Resolver, error) {
		return &echoResolver{prov: resilience.New(provider, resilience.Options{})}, nil
	}, func(*proto.StartTurn) (runner.ToolSvc, error) {
		return &hostTestToolSvc{reg: agenttools.NewRegistry()}, nil
	})
	listener := bufconn.Listen(1 << 20)
	server := grpc.NewServer()
	proto.RegisterWorkerServer(server, ws)
	go server.Serve(listener)
	defer server.Stop()
	cfg := config.Defaults()
	store := secrets.NewMemory()
	host := worker.NewWorkerRunnerForTest(&fakeHistory{}, cfgsvc.New("", cfg, store), newTestBroker(), store, worker.BufconnDial(listener))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var prompts atomic.Int32
	result, err := host.RunTurn(ctx, runner.Request{ConversationID: "auth-conv", Gen: 1, Input: "hello", AuthRecovery: func(_ context.Context, c llm.AuthChallenge) (llm.AuthDecision, error) {
		prompts.Add(1)
		if c.Profile != "work" || provider.calls.Load() != 1 {
			t.Errorf("wrong/unpaused challenge: %+v", c)
		}
		return llm.AuthLogin, nil
	}}, nil, nil, nil)
	if err != nil || result.FinalText != "resumed" || prompts.Load() != 1 || provider.calls.Load() != 2 {
		t.Fatalf("result=%+v err=%v prompts=%d calls=%d", result, err, prompts.Load(), provider.calls.Load())
	}
}

type oldAuthenticationWorker struct {
	proto.UnimplementedWorkerServer
	legacyCalls atomic.Int32
}

func (w *oldAuthenticationWorker) RunTurn(proto.Worker_RunTurnServer) error {
	w.legacyCalls.Add(1)
	return nil
}
func TestSubscriptionRequestsRequireAuthenticationAwareWorker(t *testing.T) {
	old := &oldAuthenticationWorker{}
	listener := bufconn.Listen(1 << 20)
	server := grpc.NewServer()
	proto.RegisterWorkerServer(server, old)
	go server.Serve(listener)
	defer server.Stop()
	cfg := config.Defaults()
	cfg.ActiveCloudProfile = "work"
	cfg.CloudProfiles = []config.CloudProfile{{Name: "work", Flavor: "messages", Route: "subscription"}}
	store := secrets.NewMemory()
	host := worker.NewWorkerRunnerForTest(&fakeHistory{}, cfgsvc.New("", cfg, store), newTestBroker(), store, worker.BufconnDial(listener))
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, err := host.RunTurn(ctx, runner.Request{ConversationID: "legacy", Input: "hello"}, nil, nil, nil)
	if err == nil || old.legacyCalls.Load() != 0 {
		t.Fatalf("unsafe downgrade: legacy calls=%d err=%v", old.legacyCalls.Load(), err)
	}
}
