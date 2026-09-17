package worker_test

// mcp_e2e_test.go — end-to-end proof that a host MCP tool is callable from
// inside a worker process. The worker holds NO MCP connection: it receives tool
// descriptors in StartTurn, advertises them to the model, and proxies the
// invocation back over the turn stream for the host to execute.
//
// This drives a real worker gRPC server over a real bidi stream (bufconn), with
// a scripted provider that issues one tool call. Nothing about the proxy path is
// stubbed on the worker side.

import (
	"context"
	"encoding/json"
	"net"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"

	"cercano/source/server/internal/agenttools"
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

// toolCallProvider emits one tool call for toolName on the first turn, then a
// plain text answer, so the worker's tool loop actually invokes the proxy.
type toolCallProvider struct {
	toolName string
	calls    atomic.Int32
	// sawTool records the tool names advertised to the model, proving the
	// proxied MCP tool reached the request.
	sawTool atomic.Value // []string
}

func (p *toolCallProvider) Name() string { return "scripted" }
func (p *toolCallProvider) Capabilities() inference.Capabilities {
	return inference.Capabilities{SupportsTools: true}
}
func (p *toolCallProvider) Chat(context.Context, llm.ChatRequest) (llm.ChatResponse, error) {
	return llm.ChatResponse{StopReason: "end_turn"}, nil
}
func (p *toolCallProvider) StreamChat(_ context.Context, req llm.ChatRequest) (llm.StreamReader, error) {
	names := make([]string, 0, len(req.Tools))
	for _, t := range req.Tools {
		names = append(names, t.Name)
	}
	p.sawTool.Store(names)

	if p.calls.Add(1) == 1 {
		return &scriptedReader{events: []llm.StreamEvent{
			{Type: llm.EventMessageStart, InputTokens: 1},
			{Type: llm.EventToolUseStart, ToolUseID: "call-1", ToolName: p.toolName, ToolInputRaw: json.RawMessage(`{"q":"ping"}`)},
			{Type: llm.EventToolUseStop},
			{Type: llm.EventMessageStop, StopReason: "tool_use", OutputTokens: 1},
		}}, nil
	}
	return &scriptedReader{events: []llm.StreamEvent{
		{Type: llm.EventMessageStart, InputTokens: 1},
		{Type: llm.EventTextDelta, TextDelta: "done"},
		{Type: llm.EventMessageStop, StopReason: "end_turn", OutputTokens: 1},
	}}, nil
}

type scriptedReader struct {
	events []llm.StreamEvent
	pos    int
}

func (r *scriptedReader) Next() (llm.StreamEvent, bool, error) {
	if r.pos >= len(r.events) {
		return llm.StreamEvent{}, false, nil
	}
	ev := r.events[r.pos]
	r.pos++
	return ev, true, nil
}
func (r *scriptedReader) Close() error { return nil }

// TestWorkerCallsHostMCPToolThroughProxy is the end-to-end contract: the model
// running inside the worker calls an MCP tool, and the HOST executes it.
func TestWorkerCallsHostMCPToolThroughProxy(t *testing.T) {
	const toolName = "fixture__ping"

	prov := &toolCallProvider{toolName: toolName}
	provSvc := &echoResolver{prov: prov}

	// The worker's own tool service holds only built-ins — no MCP tool. The
	// proxy must be what makes toolName callable.
	ws := worker.NewWithFactories(
		func(*proto.StartTurn) (providers.Resolver, error) { return provSvc, nil },
		func(*proto.StartTurn) (runner.ToolSvc, error) {
			return &hostTestToolSvc{reg: agenttools.NewRegistry()}, nil
		},
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

	// Wire the host bridge: advertisement + execution both stay on the host.
	var hostExecuted atomic.Int32
	var gotArgs atomic.Value
	bridge, ok := r.(worker.McpBridgeSetter)
	if !ok {
		t.Fatal("dial-built worker runner does not implement McpBridgeSetter")
	}
	bridge.SetMCPBridge(
		func() []worker.McpToolAdvert {
			return []worker.McpToolAdvert{{
				Name:        toolName,
				Description: "fixture ping",
				Schema:      json.RawMessage(`{"type":"object","properties":{"q":{"type":"string"}}}`),
			}}
		},
		func(_ context.Context, name string, args json.RawMessage) (json.RawMessage, error) {
			hostExecuted.Add(1)
			gotArgs.Store(string(args))
			if name != toolName {
				t.Errorf("host asked to run %q, want %q", name, toolName)
			}
			return json.Marshal(agenttools.NewTextResult("pong from host"))
		},
	)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	res, err := r.RunTurn(ctx, runner.Request{
		ConversationID: "mcp-proxy-e2e",
		Input:          "call the tool",
		WorkDir:        t.TempDir(),
		Gen:            1,
	}, &sinkCollector{},
		runner.PermissionRequester(func(context.Context, string, string, json.RawMessage, llm.Permission, bool) (bool, error) {
			return true, nil // approve; gating itself is covered in mcp_proxy_test.go
		}),
		func(llm.Message) {},
	)
	if err != nil {
		t.Fatalf("RunTurn: %v", err)
	}

	// 1. The proxied tool was advertised to the model inside the worker.
	advertised, _ := prov.sawTool.Load().([]string)
	if !containsName(advertised, toolName) {
		t.Errorf("advertised tools = %v, want to include %q — descriptors did not reach the worker", advertised, toolName)
	}

	// 2. The HOST executed it — the worker never opened an MCP connection.
	if n := hostExecuted.Load(); n != 1 {
		t.Errorf("host executed the tool %d times, want exactly 1", n)
	}

	// 3. Arguments survived the round trip.
	if s, _ := gotArgs.Load().(string); !strings.Contains(s, "ping") {
		t.Errorf("host received args %q, want them to contain \"ping\"", s)
	}

	// 4. The result came back into the worker's loop and the turn completed.
	if res.FinalText == "" {
		t.Error("turn produced no text after the proxied tool call")
	}
}

// A worker that receives NO descriptors must not expose the tool at all — the
// pre-proxy behavior, and what a host with no MCP servers should still produce.
func TestWorkerWithoutAdvertisedToolsExposesNoMCP(t *testing.T) {
	prov := &toolCallProvider{toolName: "unused"}
	provSvc := &echoResolver{prov: prov}

	ws := worker.NewWithFactories(
		func(*proto.StartTurn) (providers.Resolver, error) { return provSvc, nil },
		func(*proto.StartTurn) (runner.ToolSvc, error) {
			return &hostTestToolSvc{reg: agenttools.NewRegistry()}, nil
		},
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
	// No SetMCPBridge call at all — the nil-bridge default.
	r := worker.NewWorkerRunnerWithDial(&recordingHistory{}, cfg, newTestBroker(), dial)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := r.RunTurn(ctx, runner.Request{
		ConversationID: "mcp-proxy-none",
		Input:          "hi",
		WorkDir:        t.TempDir(),
		Gen:            1,
	}, &sinkCollector{},
		runner.PermissionRequester(func(context.Context, string, string, json.RawMessage, llm.Permission, bool) (bool, error) {
			return true, nil
		}),
		func(llm.Message) {},
	); err != nil {
		t.Fatalf("RunTurn: %v", err)
	}

	advertised, _ := prov.sawTool.Load().([]string)
	for _, n := range advertised {
		if strings.Contains(n, "fixture__") {
			t.Errorf("worker advertised MCP tool %q with no bridge configured", n)
		}
	}
}

func containsName(names []string, want string) bool {
	for _, n := range names {
		if n == want {
			return true
		}
	}
	return false
}
