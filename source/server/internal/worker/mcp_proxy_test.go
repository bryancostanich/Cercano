package worker

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"cercano/source/server/internal/agent"
	"cercano/source/server/internal/agenttools"
	"cercano/source/server/internal/llm"
	"cercano/source/server/pkg/proto"
)

// The single most dangerous failure mode in worker MCP proxying: a proxy tool
// that does not implement agenttools.Originer. OriginOf defaults to
// OriginBuiltin, so third-party MCP code would be gated as first-party —
// silently, in the UNSAFE direction. Assert origin at the same seam the tool
// loop uses (OriginOf), not via the concrete method.
func TestProxyToolReportsMCPOriginThroughOriginOf(t *testing.T) {
	var tool agenttools.Tool = newMCPProxyTool(&proto.McpToolDescriptor{Name: "srv__do"}, nil)

	if got := agenttools.OriginOf(tool); got != agenttools.OriginMCP {
		t.Fatalf("OriginOf(proxy) = %q, want %q — MCP tools would be gated as first-party", got, agenttools.OriginMCP)
	}
	if got := tool.Permission(); got != agenttools.PermW {
		t.Errorf("Permission() = %q, want W — R-tier tools bypass the gate entirely", got)
	}
}

// The gate must reach the same verdict in the worker as on the host for every
// combination that matters. This is the cross-boundary composition check: it
// exercises GateDecisionForMCP with the origin the proxy reports and the
// allowlist the worker reconstitutes from StartTurn.
func TestWorkerGateMatchesHostForProxiedMCPTools(t *testing.T) {
	const allowed, blocked = "srv__allowed", "srv__other"

	// The worker builds its store from the host's shipped patterns.
	store := agent.NewStaticPermissionStoreWithMCPAllow(agent.ModePermissive, []string{allowed})

	cases := []struct {
		name     string
		tool     string
		tier     llm.Permission
		wantGate bool
	}{
		{"allowlisted W tool does not prompt", allowed, llm.PermW, false},
		{"non-allowlisted W tool prompts", blocked, llm.PermW, true},
		{"X tool always prompts even when allowlisted", allowed, llm.PermX, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var tool agenttools.Tool = newMCPProxyTool(&proto.McpToolDescriptor{Name: tc.tool}, nil)
			isMCP := agenttools.OriginOf(tool) == agenttools.OriginMCP
			got := agent.GateDecisionForMCP(agent.ModePermissive, tc.tier, isMCP, store.IsMCPAllowed(tc.tool))
			if got != tc.wantGate {
				t.Errorf("gate = %v, want %v", got, tc.wantGate)
			}
		})
	}
}

// Regression for the bug this work uncovered: a worker built WITHOUT the host's
// allowlist reports nothing as allowlisted, so an allowlisted tool that runs
// silently in-process would prompt on every worker turn.
func TestWorkerWithoutShippedAllowlistWouldRegress(t *testing.T) {
	const name = "srv__allowed"
	withList := agent.NewStaticPermissionStoreWithMCPAllow(agent.ModePermissive, []string{name})
	withoutList := agent.NewStaticPermissionStore(agent.ModePermissive)

	if !withList.IsMCPAllowed(name) {
		t.Fatal("shipped allowlist not honored")
	}
	if withoutList.IsMCPAllowed(name) {
		t.Fatal("store without an allowlist must report nothing allowlisted")
	}
	// The gate verdicts must differ — proving the allowlist is load-bearing and
	// that shipping it is what keeps worker and host behavior identical.
	gateWith := agent.GateDecisionForMCP(agent.ModePermissive, llm.PermW, true, withList.IsMCPAllowed(name))
	gateWithout := agent.GateDecisionForMCP(agent.ModePermissive, llm.PermW, true, withoutList.IsMCPAllowed(name))
	if gateWith == gateWithout {
		t.Fatal("expected the shipped allowlist to change the gate verdict")
	}
}

// fakeSender captures what the proxy puts on the wire.
type capturedSend struct{ msgs chan *proto.WorkerToHost }

func (c *capturedSend) send(m *proto.WorkerToHost) { c.msgs <- m }

// A round trip must carry args out and a decoded Result back, including images
// (MCP tools can return image parts, and dropping them would silently degrade
// vision-capable turns).
func TestProxyCallRoundTripsResultAndImages(t *testing.T) {
	ctl := newStreamMCPControl(nil)
	captured := make(chan *proto.McpCallRequest, 1)
	ctl.sendFn = func(m *proto.WorkerToHost) { captured <- m.GetMcpRequest() }

	want := &agenttools.Result{
		Type:   agenttools.ResultText,
		Text:   "hello",
		Detail: "mcp",
		Images: []llm.Block{{Type: llm.BlockImage, MediaType: "image/png", ImageData: "abc"}},
	}
	encoded, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}

	done := make(chan struct{})
	var got *agenttools.Result
	var callErr error
	go func() {
		defer close(done)
		got, callErr = ctl.call(context.Background(), "srv__do", json.RawMessage(`{"k":"v"}`))
	}()

	select {
	case req := <-captured:
		if req.GetName() != "srv__do" {
			t.Errorf("name = %q, want srv__do", req.GetName())
		}
		if string(req.GetArgsJson()) != `{"k":"v"}` {
			t.Errorf("args = %s, want {\"k\":\"v\"}", req.GetArgsJson())
		}
		ctl.deliver(&proto.McpCallResponse{Id: req.GetId(), ResultJson: encoded})
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for the proxy to send a request")
	}

	<-done
	if callErr != nil {
		t.Fatalf("call: %v", callErr)
	}
	if got.Text != "hello" || got.Detail != "mcp" {
		t.Errorf("result = %+v, want text=hello detail=mcp", got)
	}
	if len(got.Images) != 1 || got.Images[0].ImageData != "abc" {
		t.Errorf("images not round-tripped: %+v", got.Images)
	}
}

// A host-side tool error must surface as a Go error, not a successful empty
// result — otherwise the model sees a blank success and loops.
func TestProxyCallSurfacesHostError(t *testing.T) {
	ctl := newStreamMCPControl(nil)
	captured := make(chan *proto.McpCallRequest, 1)
	ctl.sendFn = func(m *proto.WorkerToHost) { captured <- m.GetMcpRequest() }

	done := make(chan struct{})
	var callErr error
	go func() {
		defer close(done)
		_, callErr = ctl.call(context.Background(), "srv__do", nil)
	}()

	select {
	case req := <-captured:
		ctl.deliver(&proto.McpCallResponse{Id: req.GetId(), Error: "server unavailable"})
	case <-time.After(2 * time.Second):
		t.Fatal("timed out")
	}
	<-done
	if callErr == nil || callErr.Error() != "server unavailable" {
		t.Fatalf("err = %v, want \"server unavailable\"", callErr)
	}
}

// Cancellation must not hang: the host may never answer if it dies mid-call.
func TestProxyCallHonorsContextCancellation(t *testing.T) {
	ctl := newStreamMCPControl(nil)
	ctl.sendFn = func(*proto.WorkerToHost) {} // swallow; never respond

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := ctl.call(ctx, "srv__do", nil)
		done <- err
	}()
	cancel()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("call did not return after cancellation")
	}
}

// Built-ins must win a name collision, matching host ordering.
func TestRegisterProxiesDoesNotClobberBuiltins(t *testing.T) {
	reg := agenttools.NewRegistry()
	reg.MustRegister(&stubBuiltin{name: "collide"})

	n := registerMCPProxies(reg, []*proto.McpToolDescriptor{
		{Name: "collide"},
		{Name: "fresh"},
		{Name: ""}, // skipped
	}, newStreamMCPControl(nil))

	if n != 1 {
		t.Errorf("registered %d, want 1 (collision and empty name skipped)", n)
	}
	got, _ := reg.Get("collide")
	if agenttools.OriginOf(got) != agenttools.OriginBuiltin {
		t.Error("a proxy clobbered a built-in tool")
	}
	if _, ok := reg.Get("fresh"); !ok {
		t.Error("non-colliding proxy was not registered")
	}
}

type stubBuiltin struct{ name string }

func (s *stubBuiltin) Name() string                      { return s.name }
func (s *stubBuiltin) Description() string               { return "" }
func (s *stubBuiltin) Permission() agenttools.Permission { return agenttools.PermR }
func (s *stubBuiltin) Schema() json.RawMessage           { return json.RawMessage(`{}`) }
func (s *stubBuiltin) Execute(context.Context, json.RawMessage) (*agenttools.Result, error) {
	return nil, nil
}
