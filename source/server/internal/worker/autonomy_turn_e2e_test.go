package worker_test

// autonomy_turn_e2e_test.go — one-turn integration regression for the worker
// autonomy ledger fix. A scripted model inside a REAL worker process (full
// production capability stack, bufconn gRPC stream to the host) calls the
// preauthorized X-tier session-control capability request_autonomous_execution
// and then performs its next action in the SAME turn.
//
// Pre-fix, the capability failed with "autonomy ledger is not available"
// (the worker had no ledger), and because it is a session-control tool the
// agent loop treated the execution error as a control-boundary failure and
// terminated the turn — the approved autonomous entry was lost and the model
// never got its next request without a second user message.
//
// Post-fix the ledger proxies to the host over the stream, so this test
// asserts:
//  1. the RUNNING ledger row is durably persisted in the host's conversation
//     store (single ledger owner),
//  2. the host session profile switched to "autonomous",
//  3. the turn CONTINUED — the model received its second request and produced
//     final text, all within the single RunTurn (no second user message).

import (
	"context"
	"encoding/json"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"

	"cercano/source/server/internal/conversation"
	cfgsvc "cercano/source/server/internal/hostsvc/config"
	"cercano/source/server/internal/hostsvc/providers"
	"cercano/source/server/internal/inference"
	"cercano/source/server/internal/llm"
	"cercano/source/server/internal/runner"
	"cercano/source/server/internal/secrets"
	"cercano/source/server/internal/worker"
	"cercano/source/server/pkg/config"
	"cercano/source/server/pkg/proto"
)

// autonomyEntryProvider scripts the two model requests of one autonomous
// worker turn:
//
//	request 1: call request_autonomous_execution with a run brief,
//	request 2: the model's next action — a plain text continuation.
type autonomyEntryProvider struct {
	calls atomic.Int32
	mu    sync.Mutex
	// toolsSeen records the tool names advertised on the second request, the
	// proof the loop continued after the session-control capability.
	toolsSeen [][]string
}

func (p *autonomyEntryProvider) Name() string { return "scripted-autonomy" }
func (p *autonomyEntryProvider) Capabilities() inference.Capabilities {
	return inference.Capabilities{SupportsTools: true}
}
func (p *autonomyEntryProvider) Chat(context.Context, llm.ChatRequest) (llm.ChatResponse, error) {
	return llm.ChatResponse{StopReason: "end_turn"}, nil
}
func (p *autonomyEntryProvider) StreamChat(_ context.Context, req llm.ChatRequest) (llm.StreamReader, error) {
	names := make([]string, 0, len(req.Tools))
	for _, t := range req.Tools {
		names = append(names, t.Name)
	}
	p.mu.Lock()
	p.toolsSeen = append(p.toolsSeen, names)
	p.mu.Unlock()

	if p.calls.Add(1) == 1 {
		return &scriptedReader{events: []llm.StreamEvent{
			{Type: llm.EventMessageStart, InputTokens: 1},
			{Type: llm.EventToolUseStart, ToolUseID: "call-auto-1", ToolName: "request_autonomous_execution",
				ToolInputRaw: json.RawMessage(`{"goal":"ship the demo regression","done_when":["ledger row persisted","turn continues"]}`)},
			{Type: llm.EventToolUseStop},
			{Type: llm.EventMessageStop, StopReason: "tool_use", OutputTokens: 1},
		}}, nil
	}
	return &scriptedReader{events: []llm.StreamEvent{
		{Type: llm.EventMessageStart, InputTokens: 1},
		{Type: llm.EventTextDelta, TextDelta: "autonomous run underway"},
		{Type: llm.EventMessageStop, StopReason: "end_turn", OutputTokens: 1},
	}}, nil
}

// TestWorkerAutonomousEntryPersistsLedgerAndContinues is the end-to-end
// regression: one RunTurn, the model calls the preauthorized
// request_autonomous_execution, the running ledger row lands in the host's
// conversation store, and the model's next action runs in the same turn.
func TestWorkerAutonomousEntryPersistsLedgerAndContinues(t *testing.T) {
	const convID = "autonomy-turn-e2e"

	prov := &autonomyEntryProvider{}
	provSvc := &echoResolver{prov: prov}

	// Real worker: nil tools factory means the production capability stack is
	// built, including the stream autonomy-ledger proxy and profile controller.
	ws := worker.NewWithFactories(
		func(*proto.StartTurn) (providers.Resolver, error) { return provSvc, nil },
		nil,
	)

	lis := bufconn.Listen(1 << 20)
	grpcSrv := grpc.NewServer()
	proto.RegisterWorkerServer(grpcSrv, ws)
	go func() { _ = grpcSrv.Serve(lis) }()
	defer grpcSrv.Stop()

	dial := func(ctx context.Context) (*grpc.ClientConn, error) {
		return grpc.NewClient("passthrough:///bufconn",
			grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
				return lis.DialContext(ctx)
			}),
			grpc.WithTransportCredentials(insecure.NewCredentials()))
	}

	cfg := cfgsvc.New("", config.Config{LocusMode: "open_only"}, secrets.NewMemory())
	r := worker.NewWorkerRunnerWithDial(&recordingHistory{}, cfg, newTestBroker(), dial)

	// Wire the host-owned ledger: the same adapter the server wires in
	// production (server.go: ledger.SetAutonomyLedger(worker.HostAutonomyLedger(...))).
	store, err := conversation.Open(":memory:")
	if err != nil {
		t.Fatalf("open conversation store: %v", err)
	}
	defer store.Close()
	if err := store.EnsureConversation(context.Background(), convID, t.TempDir(), "fake-model"); err != nil {
		t.Fatalf("ensure conversation: %v", err)
	}
	ledgerSetter, ok := r.(worker.AutonomyLedgerSetter)
	if !ok {
		t.Fatal("dial-built worker runner does not implement AutonomyLedgerSetter")
	}
	ledgerSetter.SetAutonomyLedger(worker.HostAutonomyLedger(store))

	// Wire the host profile handler through the test injection seam; without it
	// the approved entry would abandon the run (no profile broker on the host).
	var profileMu sync.Mutex
	var enteredProfile []string
	profileSetter, ok := r.(worker.ProfileHandlerSetter)
	if !ok {
		t.Fatal("dial-built worker runner does not implement ProfileHandlerSetter")
	}
	profileSetter.SetProfileHandler(func(_ context.Context, _, name string) error {
		profileMu.Lock()
		enteredProfile = append(enteredProfile, name)
		profileMu.Unlock()
		return nil
	})

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	var permAsks atomic.Int32
	res, err := r.RunTurn(ctx, runner.Request{
		ConversationID: convID,
		Input:          "run it autonomously",
		WorkDir:        t.TempDir(),
		Gen:            1,
	}, &sinkCollector{},
		runner.PermissionRequester(func(context.Context, string, string, json.RawMessage, llm.Permission, bool) (bool, error) {
			permAsks.Add(1)
			return true, nil // preauthorize the X-tier entry gate
		}),
		func(llm.Message) {},
	)
	if err != nil {
		t.Fatalf("RunTurn: %v", err)
	}

	// 1. The X-tier entry gate round-tripped to the host and was preauthorized.
	if n := permAsks.Load(); n != 1 {
		t.Errorf("permission requests = %d, want exactly 1 (the preauthorized entry gate)", n)
	}

	// 2. The RUNNING ledger row is durably persisted in the host's store.
	run, err := store.GetActiveAutonomyRun(ctx, convID)
	if err != nil {
		t.Fatalf("GetActiveAutonomyRun after entry: %v", err)
	}
	if run.State != "running" {
		t.Errorf("persisted autonomy run state = %q, want \"running\"", run.State)
	}
	if run.RunID == "" {
		t.Error("persisted autonomy run has no run id")
	}
	if !strings.Contains(run.BriefJSON, "ship the demo regression") {
		t.Errorf("persisted brief = %q, want it to carry the scripted goal", run.BriefJSON)
	}

	// 3. The host session profile switched to autonomous.
	profileMu.Lock()
	profiles := append([]string(nil), enteredProfile...)
	profileMu.Unlock()
	if len(profiles) != 1 || profiles[0] != "autonomous" {
		t.Errorf("entered profiles = %v, want exactly [autonomous]", profiles)
	}

	// 4. The turn CONTINUED after the session-control capability: the model got
	// its second request inside the same RunTurn — no second user message.
	if n := prov.calls.Load(); n != 2 {
		t.Errorf("model requests = %d, want exactly 2 (entry call, then next action in the same turn)", n)
	}
	if res.FinalText == "" {
		t.Error("turn produced no final text after the autonomous entry")
	}
	if !strings.Contains(res.FinalText, "autonomous run underway") {
		t.Errorf("final text = %q, want the scripted next action's text", res.FinalText)
	}
}
