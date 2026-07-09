package worker_test

// host_test.go — tests for the host-side workerRunner (Task 3).
//
// Strategy:
//   - All logic tests (drive/bridge/credential/crash-detect) use a real
//     WorkerServer registered on a bufconn — no spawned process needed,
//     deterministic and fast.
//   - spawn.go integration tests (real binary + crash) are deferred to
//     host_integration_test.go (//go:build integration).

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	"cercano/source/server/internal/agenttools"
	"cercano/source/server/internal/agent"
	cfgsvc "cercano/source/server/internal/hostsvc/config"
	"cercano/source/server/internal/engine"
	providers "cercano/source/server/internal/hostsvc/providers"
	"cercano/source/server/internal/hostsvc/permissions"
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

// ─── fakeHistory ─────────────────────────────────────────────────────────────

type fakeHistory struct {
	history    []llm.Message
	projectCtx string
}

func (h *fakeHistory) AssembleHistory(_ context.Context, _ string) []llm.Message { return h.history }
func (h *fakeHistory) PersistTurn(_ context.Context, _ string, _ llm.Message)    {}
func (h *fakeHistory) LoadProjectContext(_ string) string                         { return h.projectCtx }

// ─── hostTestSecrets ─────────────────────────────────────────────────────────

type hostTestSecrets struct {
	keys map[string]string
}

func newHostSecrets(kv map[string]string) *hostTestSecrets {
	if kv == nil {
		kv = make(map[string]string)
	}
	return &hostTestSecrets{keys: kv}
}
func (s *hostTestSecrets) Get(p string) (string, error) {
	if v, ok := s.keys[p]; ok {
		return v, nil
	}
	return "", errors.New("not found")
}
func (s *hostTestSecrets) Set(p, v string) error { s.keys[p] = v; return nil }
func (s *hostTestSecrets) Delete(p string) error { delete(s.keys, p); return nil }
func (s *hostTestSecrets) List() ([]string, error) {
	out := make([]string, 0, len(s.keys))
	for k := range s.keys {
		out = append(out, k)
	}
	return out, nil
}

// ─── echoProvider / echoReader ────────────────────────────────────────────────

type echoProvider struct{ text string }

func (p *echoProvider) Name() string                   { return "echo" }
func (p *echoProvider) Capabilities() llm.Capabilities { return llm.Capabilities{SupportsTools: true} }
func (p *echoProvider) Chat(_ context.Context, _ llm.ChatRequest) (llm.ChatResponse, error) {
	return llm.ChatResponse{StopReason: "end_turn"}, nil
}
func (p *echoProvider) StreamChat(_ context.Context, _ llm.ChatRequest) (llm.StreamReader, error) {
	return &echoReader{text: p.text}, nil
}

type echoReader struct {
	text   string
	events []llm.StreamEvent
	pos    int
}

func (r *echoReader) Next() (llm.StreamEvent, bool, error) {
	if r.events == nil {
		r.events = []llm.StreamEvent{
			{Type: llm.EventMessageStart, InputTokens: 1},
			{Type: llm.EventTextDelta, TextDelta: r.text},
			{Type: llm.EventMessageStop, StopReason: "end_turn", OutputTokens: 2},
		}
	}
	if r.pos >= len(r.events) {
		return llm.StreamEvent{}, false, nil
	}
	ev := r.events[r.pos]
	r.pos++
	return ev, true, nil
}
func (r *echoReader) Close() error { return nil }

// ─── echoResolver ────────────────────────────────────────────────────────────
// Full providers.Resolver implementation (mirrors fakeResolver in worker_test.go).

type echoResolver struct{ prov llm.Provider }

func (f *echoResolver) Main() (llm.Provider, bool, bool, error) { return f.prov, false, false, nil }
func (f *echoResolver) MainModel(_ bool) string                  { return "fake-model" }
func (f *echoResolver) PrimaryModel() string                     { return "fake-model" }
func (f *echoResolver) Rebuild() error                           { return nil }
func (f *echoResolver) InstallAbsentCloud(_ string)              {}
func (f *echoResolver) Cloud() llm.Provider                      { return nil }
func (f *echoResolver) Open() llm.Provider                       { return nil }
func (f *echoResolver) ActiveCloudModel() string                 { return "" }
func (f *echoResolver) LocusMode() string                        { return "" }
func (f *echoResolver) Router() providers.RouterCloudUpdater     { return nil }
func (f *echoResolver) Registry() *engine.EngineRegistry         { return nil }
func (f *echoResolver) CatalogManager() *ollamacatalog.Manager   { return nil }
func (f *echoResolver) OpenLegacy() *legacymodels.OpenModelProvider { return nil }
func (f *echoResolver) Reconfigure(_ providers.ReconfigureArgs)  {}
func (f *echoResolver) SetCloudLLMProvider(_ llm.Provider)       {}
func (f *echoResolver) SetOpenLLMProvider(_ llm.Provider)        {}
func (f *echoResolver) SetOpenProviderFactory(_ func(config.Config) llm.Provider) {}
func (f *echoResolver) CloudLLMProvider() llm.Provider           { return nil }
func (f *echoResolver) OpenLLMProvider() llm.Provider            { return nil }
func (f *echoResolver) SetCatalogManager(_ *ollamacatalog.Manager) {}
func (f *echoResolver) SetUsageSink(_ func(usage.Usage))         {}

// ─── hostTestToolSvc ─────────────────────────────────────────────────────────

type hostTestToolSvc struct{ reg *agenttools.Registry }

func (t *hostTestToolSvc) Registry() *agenttools.Registry { return t.reg }
func (t *hostTestToolSvc) GrantedRegistry(_ []string) (*agenttools.Registry, []string, []string, error) {
	return t.reg, nil, nil, nil
}

// ─── startBufconnWorker ──────────────────────────────────────────────────────

func startBufconnWorker(t *testing.T, text string) (func(context.Context) (*grpc.ClientConn, error), func()) {
	t.Helper()

	prov := &echoProvider{text: text}
	provSvc := &echoResolver{prov: prov}

	ws := worker.NewWithFactories(
		func(_ *proto.StartTurn) (providers.Resolver, error) { return provSvc, nil },
		func(_ *proto.StartTurn) (runner.ToolSvc, error) {
			return &hostTestToolSvc{reg: agenttools.NewRegistry()}, nil
		},
	)

	lis := bufconn.Listen(1 << 20)
	grpcSrv := grpc.NewServer()
	proto.RegisterWorkerServer(grpcSrv, ws)
	go func() { _ = grpcSrv.Serve(lis) }()

	dial := func(ctx context.Context) (*grpc.ClientConn, error) {
		return grpc.NewClient(
			"passthrough:///bufconn",
			grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
				return lis.DialContext(ctx)
			}),
			grpc.WithTransportCredentials(insecure.NewCredentials()),
		)
	}
	return dial, grpcSrv.Stop
}

// newTestBroker returns a minimal permissions.Broker.
func newTestBroker() permissions.Broker {
	store, _ := agent.LoadPermissionStore("")
	return permissions.New(store, nil, nil)
}

// ─── sinkCollector ───────────────────────────────────────────────────────────

type sinkCollector struct{ events []runner.Event }

func (s *sinkCollector) Emit(ev runner.Event) { s.events = append(s.events, ev) }

// ─── TestWorkerRunner_EndToEnd ─────────────────────────────────────────────────

func TestWorkerRunner_EndToEnd(t *testing.T) {
	const wantText = "hello from worker"

	dial, stop := startBufconnWorker(t, wantText)
	defer stop()

	hist := &fakeHistory{projectCtx: "my project context"}
	cfgSvc := cfgsvc.New("", config.Config{LocusMode: "open_primary"}, secrets.NewMemory())

	var persisted []llm.Message
	var permCalled atomic.Bool

	wr := worker.NewWorkerRunnerForTest(hist, cfgSvc, newTestBroker(), newHostSecrets(nil), dial)

	sink := &sinkCollector{}
	persistFn := runner.PersistFunc(func(m llm.Message) { persisted = append(persisted, m) })
	requester := runner.PermissionRequester(func(_ context.Context, _, _ string, _ json.RawMessage, _ llm.Permission, _ bool) (bool, error) {
		permCalled.Store(true)
		return true, nil
	})

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	result, err := wr.RunTurn(ctx, runner.Request{
		ConversationID: "test-conv",
		Input:          "hello",
		WorkDir:        t.TempDir(),
		Gen:            1,
	}, sink, requester, persistFn)
	if err != nil {
		t.Fatalf("RunTurn: %v", err)
	}
	if result.FinalText != wantText {
		t.Errorf("FinalText = %q, want %q", result.FinalText, wantText)
	}
	if len(sink.events) == 0 {
		t.Error("no events emitted to sink")
	}
	if permCalled.Load() {
		t.Error("requester was unexpectedly called (no tool use in this turn)")
	}
	_ = persisted // persist called by worker core
}

// ─── TestWorkerRunner_CredentialResolution ────────────────────────────────────

func TestWorkerRunner_CredentialResolutionStaticKey(t *testing.T) {
	const profileName = "myprofile"
	const wantKey = "sk-test-key-xyz"

	st := newHostSecrets(map[string]string{profileName: wantKey})
	cfg := config.Config{
		CloudProfiles:      []config.CloudProfile{{Name: profileName, Flavor: "anthropic", Model: "claude-3"}},
		ActiveCloudProfile: profileName,
	}

	token, account, err := worker.ResolveCredentialForTest(context.Background(), cfg, st, profileName)
	if err != nil {
		t.Fatalf("resolve credential: %v", err)
	}
	if token != wantKey {
		t.Errorf("token = %q, want %q", token, wantKey)
	}
	if account != "" {
		t.Errorf("account = %q, want empty for static-key", account)
	}
}

func TestWorkerRunner_CredentialResolutionMissingProfile(t *testing.T) {
	cfg := config.Config{CloudProfiles: nil}
	_, _, err := worker.ResolveCredentialForTest(context.Background(), cfg, newHostSecrets(nil), "no-such-profile")
	if err == nil {
		t.Fatal("expected error for missing profile, got nil")
	}
}

// ─── TestWorkerRunner_CrashDetect ────────────────────────────────────────────

func TestWorkerRunner_CrashDetect(t *testing.T) {
	lis := bufconn.Listen(1 << 20)
	grpcSrv := grpc.NewServer()
	proto.RegisterWorkerServer(grpcSrv, &hangingWorkerServer{})
	go func() { _ = grpcSrv.Serve(lis) }()

	dial := func(ctx context.Context) (*grpc.ClientConn, error) {
		return grpc.NewClient(
			"passthrough:///bufconn",
			grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
				return lis.DialContext(ctx)
			}),
			grpc.WithTransportCredentials(insecure.NewCredentials()),
		)
	}

	cfgSvc := cfgsvc.New("", config.Config{LocusMode: "open_primary"}, secrets.NewMemory())
	wr := worker.NewWorkerRunnerForTest(&fakeHistory{}, cfgSvc, newTestBroker(), newHostSecrets(nil), dial)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Kill the server 100ms into the turn to simulate a crash.
	go func() {
		time.Sleep(100 * time.Millisecond)
		grpcSrv.Stop()
	}()

	_, err := wr.RunTurn(ctx, runner.Request{
		ConversationID: "crash-test",
		Input:          "hi",
		WorkDir:        t.TempDir(),
		Gen:            99,
	}, &sinkCollector{},
		runner.PermissionRequester(func(_ context.Context, _, _ string, _ json.RawMessage, _ llm.Permission, _ bool) (bool, error) {
			return true, nil
		}),
		func(llm.Message) {},
	)
	if err == nil {
		t.Fatal("expected error after worker crash, got nil")
	}
	// Must be Unavailable (or context-derived) — not a hang.
	st, ok := status.FromError(err)
	if !ok {
		t.Logf("non-gRPC error (acceptable): %v", err)
		return
	}
	if st.Code() != codes.Unavailable && st.Code() != codes.Canceled && st.Code() != codes.DeadlineExceeded {
		t.Errorf("error code = %v, want Unavailable", st.Code())
	}
}

// hangingWorkerServer blocks indefinitely after receiving StartTurn.
type hangingWorkerServer struct {
	proto.UnimplementedWorkerServer
}

func (h *hangingWorkerServer) RunTurn(stream proto.Worker_RunTurnServer) error {
	_, _ = stream.Recv()
	<-stream.Context().Done()
	return stream.Context().Err()
}

// ─── TestWorkerRunner_ContextCancel ──────────────────────────────────────────

func TestWorkerRunner_ContextCancel(t *testing.T) {
	lis := bufconn.Listen(1 << 20)
	grpcSrv := grpc.NewServer()
	proto.RegisterWorkerServer(grpcSrv, &hangingWorkerServer{})
	go func() { _ = grpcSrv.Serve(lis) }()
	defer grpcSrv.Stop()

	dial := func(ctx context.Context) (*grpc.ClientConn, error) {
		return grpc.NewClient(
			"passthrough:///bufconn",
			grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
				return lis.DialContext(ctx)
			}),
			grpc.WithTransportCredentials(insecure.NewCredentials()),
		)
	}

	cfgSvc := cfgsvc.New("", config.Config{LocusMode: "open_primary"}, secrets.NewMemory())
	wr := worker.NewWorkerRunnerForTest(&fakeHistory{}, cfgSvc, newTestBroker(), newHostSecrets(nil), dial)

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(80 * time.Millisecond)
		cancel()
	}()

	_, err := wr.RunTurn(ctx, runner.Request{
		ConversationID: "cancel-test",
		Input:          "hi",
		WorkDir:        t.TempDir(),
		Gen:            42,
	}, &sinkCollector{},
		runner.PermissionRequester(func(_ context.Context, _, _ string, _ json.RawMessage, _ llm.Permission, _ bool) (bool, error) {
			return true, nil
		}),
		func(llm.Message) {},
	)
	if err == nil {
		t.Fatal("expected error after cancel, got nil")
	}
	if !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
		if st, ok := status.FromError(err); ok {
			switch st.Code() {
			case codes.Unavailable, codes.Canceled, codes.DeadlineExceeded:
				return
			}
		}
		t.Errorf("unexpected error: %v", err)
	}
}
