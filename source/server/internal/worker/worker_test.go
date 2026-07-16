package worker_test

import (
	"context"
	"net"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"

	"cercano/source/server/internal/agenttools"
	"cercano/source/server/internal/engine"
	cfgsvc "cercano/source/server/internal/hostsvc/config"
	providers "cercano/source/server/internal/hostsvc/providers"
	"cercano/source/server/internal/inference"
	"cercano/source/server/internal/legacymodels"
	"cercano/source/server/internal/llm"
	"cercano/source/server/internal/ollamacatalog"
	"cercano/source/server/internal/runner"
	"cercano/source/server/internal/secrets"
	"cercano/source/server/internal/usage"
	"cercano/source/server/internal/worker"
	"cercano/source/server/pkg/config"
	proto "cercano/source/server/pkg/proto"
)

// ─── fake provider wiring ─────────────────────────────────────────────────────

// fixedProvider is an inference.Provider that immediately returns end_turn with a
// fixed text response. It satisfies the RunToolLoop exit condition after
// exactly one model round.
type fixedProvider struct {
	text string
}

func (p *fixedProvider) Name() string { return "fixed" }
func (p *fixedProvider) Capabilities() inference.Capabilities {
	return inference.Capabilities{SupportsTools: true}
}
func (p *fixedProvider) Chat(_ context.Context, _ llm.ChatRequest) (llm.ChatResponse, error) {
	return llm.ChatResponse{StopReason: "end_turn"}, nil
}
func (p *fixedProvider) StreamChat(_ context.Context, _ llm.ChatRequest) (llm.StreamReader, error) {
	return &fixedReader{text: p.text}, nil
}

// fixedReader emits a content_block_delta + message_stop then EOF.
type fixedReader struct {
	text   string
	events []llm.StreamEvent
	pos    int
}

func (r *fixedReader) Next() (llm.StreamEvent, bool, error) {
	if r.events == nil {
		r.events = []llm.StreamEvent{
			{Type: llm.EventMessageStart},
			{Type: llm.EventTextDelta, TextDelta: r.text},
			{Type: llm.EventMessageStop, StopReason: "end_turn"},
		}
	}
	if r.pos >= len(r.events) {
		return llm.StreamEvent{}, false, nil
	}
	ev := r.events[r.pos]
	r.pos++
	return ev, true, nil
}
func (r *fixedReader) Close() error { return nil }

// fakeResolver is a minimal providers.Resolver that serves fixedProvider.
type fakeResolver struct{ prov inference.Provider }

func (f *fakeResolver) Main() (inference.Provider, bool, bool, error) {
	return f.prov, false, false, nil
}
func (f *fakeResolver) MainModel(_ bool) string                                         { return "fake-model" }
func (f *fakeResolver) PrimaryModel() string                                            { return "fake-model" }
func (f *fakeResolver) Rebuild() error                                                  { return nil }
func (f *fakeResolver) InstallAbsentCloud(_ string)                                     {}
func (f *fakeResolver) Cloud() inference.Provider                                       { return nil }
func (f *fakeResolver) Open() inference.Provider                                        { return nil }
func (f *fakeResolver) ActiveCloudModel() string                                        { return "" }
func (f *fakeResolver) LocusMode() string                                               { return "" }
func (f *fakeResolver) Router() providers.RouterCloudUpdater                            { return nil }
func (f *fakeResolver) Registry() *engine.EngineRegistry                                { return nil }
func (f *fakeResolver) CatalogManager() *ollamacatalog.Manager                          { return nil }
func (f *fakeResolver) OpenLegacy() *legacymodels.OpenModelProvider                     { return nil }
func (f *fakeResolver) Reconfigure(_ providers.ReconfigureArgs)                         {}
func (f *fakeResolver) SetCloudLLMProvider(_ inference.Provider)                        {}
func (f *fakeResolver) SetOpenLLMProvider(_ inference.Provider)                         {}
func (f *fakeResolver) SetOpenProviderFactory(_ func(config.Config) inference.Provider) {}
func (f *fakeResolver) CloudLLMProvider() inference.Provider                            { return nil }
func (f *fakeResolver) OpenLLMProvider() inference.Provider                             { return nil }
func (f *fakeResolver) SetCatalogManager(_ *ollamacatalog.Manager)                      {}
func (f *fakeResolver) SetUsageSink(_ func(usage.Usage))                                {}

// fakeConfig satisfies cfgsvc.Service.
type fakeConfig struct{}

func (c *fakeConfig) Get() config.Config                               { return config.Config{} }
func (c *fakeConfig) Path() string                                     { return "" }
func (c *fakeConfig) Secrets() secrets.Store                           { return nil }
func (c *fakeConfig) ActiveProfile() (config.CloudProfile, bool)       { return config.CloudProfile{}, false }
func (c *fakeConfig) Set(_ config.Config)                              {}
func (c *fakeConfig) SetPath(_ string)                                 {}
func (c *fakeConfig) SetSecrets(_ secrets.Store)                       {}
func (c *fakeConfig) SetActiveProfile(_ string) bool                   { return false }
func (c *fakeConfig) UpsertProfile(_ config.CloudProfile) (bool, bool) { return false, false }
func (c *fakeConfig) RemoveProfile(_ string) (bool, bool)              { return false, false }
func (c *fakeConfig) SetBackupProfile(_ string) bool                   { return false }
func (c *fakeConfig) ProfileInfo(_ string) (bool, bool)                { return false, false }
func (c *fakeConfig) Mutate(_ func(*config.Config))                    {}
func (c *fakeConfig) SetCloudModel(_ string)                           {}
func (c *fakeConfig) Persist()                                         {}

// fakeToolSvc returns an empty registry.
type fakeToolSvc struct{ reg *agenttools.Registry }

func (s *fakeToolSvc) Registry() *agenttools.Registry { return s.reg }
func (s *fakeToolSvc) GrantedRegistry(_ []string) (*agenttools.Registry, []string, []string, error) {
	return s.reg, nil, nil, nil
}

// ─── test ─────────────────────────────────────────────────────────────────────

func TestWorkerServer_RunTurn_FakeProvider(t *testing.T) {
	const wantText = "hello from worker"

	prov := &fixedProvider{text: wantText}
	provSvc := &fakeResolver{prov: prov}
	toolSvc := &fakeToolSvc{reg: agenttools.NewRegistry()}

	// Start in-process gRPC server using bufconn.
	lis := bufconn.Listen(1 << 20)
	grpcSrv := grpc.NewServer()
	ws := worker.NewWithFactories(
		func(_ *proto.StartTurn) (providers.Resolver, error) { return provSvc, nil },
		func(_ *proto.StartTurn) (runner.ToolSvc, error) { return toolSvc, nil },
	)
	proto.RegisterWorkerServer(grpcSrv, ws)
	go func() { _ = grpcSrv.Serve(lis) }()
	defer grpcSrv.Stop()

	conn, err := grpc.NewClient("passthrough:///bufconn",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return lis.DialContext(ctx)
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("grpc.NewClient: %v", err)
	}
	defer conn.Close()

	client := proto.NewWorkerClient(conn)
	stream, err := client.RunTurn(context.Background())
	if err != nil {
		t.Fatalf("RunTurn: %v", err)
	}

	// Send StartTurn.
	if err := stream.Send(&proto.HostToWorker{Msg: &proto.HostToWorker_Start{Start: &proto.StartTurn{
		ConversationId: "test-conv-1",
		Input:          "say hello",
		WorkDir:        t.TempDir(),
		Config: &proto.ConfigSnapshot{
			LocusMode: "open_primary",
		},
	}}}); err != nil {
		t.Fatalf("Send StartTurn: %v", err)
	}
	// Signal end of sends so the server's Recv loop sees EOF.
	if err := stream.CloseSend(); err != nil {
		t.Fatalf("CloseSend: %v", err)
	}

	// Drain messages until TurnDone or TurnError.
	var gotDone *proto.TurnDone
	for {
		msg, err := stream.Recv()
		if err != nil {
			t.Fatalf("Recv: %v", err)
		}
		switch m := msg.Msg.(type) {
		case *proto.WorkerToHost_Done:
			gotDone = m.Done
		case *proto.WorkerToHost_Error:
			t.Fatalf("got TurnError: %s", m.Error.GetMessage())
		case *proto.WorkerToHost_Event:
			// events expected; drain them
			if gotDone != nil {
				goto done
			}
			continue
		}
		if gotDone != nil {
			break
		}
	}
done:
	if gotDone == nil {
		t.Fatal("never received TurnDone")
	}
	if gotDone.GetFinalText() != wantText {
		t.Errorf("FinalText = %q, want %q", gotDone.GetFinalText(), wantText)
	}

	_ = cfgsvc.New // suppress unused import
}

// TestWorkerServer_RunTurn_NoHalfCloseNoDeadlock pins the C-review fix: the
// worker must send its terminal TurnDone WITHOUT waiting for the host to
// half-close the stream. Task 3's host keeps its send direction open until it
// has read TurnDone, so a worker that blocked the terminal send on the host's
// close would deadlock. This test never CloseSend()s until AFTER TurnDone.
func TestWorkerServer_RunTurn_NoHalfCloseNoDeadlock(t *testing.T) {
	const wantText = "done without half-close"

	provSvc := &fakeResolver{prov: &fixedProvider{text: wantText}}
	toolSvc := &fakeToolSvc{reg: agenttools.NewRegistry()}

	lis := bufconn.Listen(1 << 20)
	grpcSrv := grpc.NewServer()
	ws := worker.NewWithFactories(
		func(_ *proto.StartTurn) (providers.Resolver, error) { return provSvc, nil },
		func(_ *proto.StartTurn) (runner.ToolSvc, error) { return toolSvc, nil },
	)
	proto.RegisterWorkerServer(grpcSrv, ws)
	go func() { _ = grpcSrv.Serve(lis) }()
	defer grpcSrv.Stop()

	conn, err := grpc.NewClient("passthrough:///bufconn",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return lis.DialContext(ctx) }),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("grpc.NewClient: %v", err)
	}
	defer conn.Close()

	// Bounded ctx so a deadlock fails fast instead of hanging the suite.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	stream, err := proto.NewWorkerClient(conn).RunTurn(ctx)
	if err != nil {
		t.Fatalf("RunTurn: %v", err)
	}
	if err := stream.Send(&proto.HostToWorker{Msg: &proto.HostToWorker_Start{Start: &proto.StartTurn{
		ConversationId: "no-halfclose",
		Input:          "hi",
		WorkDir:        t.TempDir(),
		Config:         &proto.ConfigSnapshot{LocusMode: "open_primary"},
	}}}); err != nil {
		t.Fatalf("Send StartTurn: %v", err)
	}
	// Deliberately DO NOT CloseSend — keep the host->worker direction open,
	// exactly like the real host waiting to read TurnDone.

	var gotDone *proto.TurnDone
	for gotDone == nil {
		msg, err := stream.Recv()
		if err != nil {
			t.Fatalf("Recv (deadlock would surface as ctx timeout here): %v", err)
		}
		if d := msg.GetDone(); d != nil {
			gotDone = d
		} else if e := msg.GetError(); e != nil {
			t.Fatalf("got TurnError: %s", e.GetMessage())
		}
	}
	if gotDone.GetFinalText() != wantText {
		t.Errorf("FinalText = %q, want %q", gotDone.GetFinalText(), wantText)
	}
	_ = stream.CloseSend() // only now, after the terminal message
}
