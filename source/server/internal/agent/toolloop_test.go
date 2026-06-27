package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"cercano/source/server/internal/agenttools"
	"cercano/source/server/internal/llm"
)

type mockProvider struct {
	scripts [][]llm.Block
	caps    llm.Capabilities
	calls   int
}

func (m *mockProvider) Name() string                   { return "mock" }
func (m *mockProvider) Capabilities() llm.Capabilities { return m.caps }
func (m *mockProvider) Chat(ctx context.Context, req llm.ChatRequest) (llm.ChatResponse, error) {
	out := llm.ChatResponse{Blocks: m.scripts[m.calls]}
	m.calls++
	return out, nil
}
func (m *mockProvider) StreamChat(ctx context.Context, req llm.ChatRequest) (llm.StreamReader, error) {
	if m.calls >= len(m.scripts) {
		return nil, fmt.Errorf("mockProvider: no script for call %d", m.calls)
	}
	blocks := m.scripts[m.calls]
	m.calls++
	return &scriptedStream{events: blocksToEvents(blocks)}, nil
}

type scriptedStream struct {
	events []llm.StreamEvent
	idx    int
}

func (s *scriptedStream) Next() (llm.StreamEvent, bool, error) {
	if s.idx >= len(s.events) {
		return llm.StreamEvent{}, false, nil
	}
	ev := s.events[s.idx]
	s.idx++
	return ev, true, nil
}

func (s *scriptedStream) Close() error { return nil }

func blocksToEvents(blocks []llm.Block) []llm.StreamEvent {
	events := []llm.StreamEvent{{Type: llm.EventMessageStart}}
	for _, b := range blocks {
		switch b.Type {
		case llm.BlockText:
			events = append(events, llm.StreamEvent{
				Type: llm.EventTextDelta, TextDelta: b.Text,
			})
		case llm.BlockToolUse:
			events = append(events, llm.StreamEvent{
				Type:      llm.EventToolUseStart,
				ToolUseID: b.ToolUseID, ToolName: b.ToolName,
			})
			events = append(events, llm.StreamEvent{
				Type: llm.EventToolUseInputDelta, TextDelta: string(b.ToolInput),
			})
			events = append(events, llm.StreamEvent{Type: llm.EventToolUseStop})
		}
	}
	events = append(events, llm.StreamEvent{Type: llm.EventMessageStop, StopReason: "end_turn"})
	return events
}

// loopingProvider requests a (succeeding) tool whenever tools are offered, and
// answers in text once tools are withheld — driving the loop to its cap and
// exercising the final no-tools degradation pass.
type loopingProvider struct{ calls int }

func (p *loopingProvider) Name() string                   { return "looping" }
func (p *loopingProvider) Capabilities() llm.Capabilities { return llm.Capabilities{SupportsTools: true} }
func (p *loopingProvider) Chat(ctx context.Context, req llm.ChatRequest) (llm.ChatResponse, error) {
	return llm.ChatResponse{}, nil
}
func (p *loopingProvider) StreamChat(ctx context.Context, req llm.ChatRequest) (llm.StreamReader, error) {
	p.calls++
	var blocks []llm.Block
	if len(req.Tools) > 0 {
		blocks = []llm.Block{{Type: llm.BlockToolUse, ToolUseID: fmt.Sprintf("u%d", p.calls),
			ToolName: "LS", ToolInput: json.RawMessage(`{"path":"."}`)}}
	} else {
		blocks = []llm.Block{{Type: llm.BlockText, Text: "Here's my best answer."}}
	}
	return &scriptedStream{events: blocksToEvents(blocks)}, nil
}

func TestToolLoop_HitsCap_DegradesToFinalAnswer(t *testing.T) {
	prov := &loopingProvider{}
	reg := agenttools.DefaultRegistry()
	perms, _ := LoadPermissionStore(t.TempDir() + "/perms.yaml")

	result, err := RunToolLoop(t.Context(), ToolLoopInput{
		Provider: prov, Registry: reg, Permissions: perms, UserInput: "do a lot of work",
	})
	if err != nil {
		t.Fatalf("hitting the iteration cap should degrade to a final answer, not error: %v", err)
	}
	if result.FinalText != "Here's my best answer." {
		t.Errorf("expected the final no-tools answer, got %q", result.FinalText)
	}
	if prov.calls != MaxToolLoopIterations+1 {
		t.Errorf("expected %d calls (cap tool turns + 1 final), got %d", MaxToolLoopIterations+1, prov.calls)
	}
}

func TestToolLoop_PlainText_TerminatesImmediately(t *testing.T) {
	prov := &mockProvider{
		scripts: [][]llm.Block{{
			{Type: llm.BlockText, Text: "Done."},
		}},
		caps: llm.Capabilities{SupportsTools: true},
	}
	reg := agenttools.DefaultRegistry()
	perms, _ := LoadPermissionStore(t.TempDir() + "/perms.yaml")

	result, err := RunToolLoop(t.Context(), ToolLoopInput{
		Provider: prov, Registry: reg, Permissions: perms,
		ConvHistory: nil, UserInput: "hi",
	})
	if err != nil {
		t.Fatal(err)
	}
	if prov.calls != 1 {
		t.Errorf("plain text turn should make 1 call, made %d", prov.calls)
	}
	if result.FinalText != "Done." {
		t.Errorf("final text: %q", result.FinalText)
	}
}

func TestToolLoop_SingleToolCall_FeedsResultAndContinues(t *testing.T) {
	prov := &mockProvider{
		scripts: [][]llm.Block{
			{
				{Type: llm.BlockText, Text: "Reading..."},
				{Type: llm.BlockToolUse, ToolUseID: "u1", ToolName: "LS",
					ToolInput: json.RawMessage(`{"path":"."}`)},
			},
			{{Type: llm.BlockText, Text: "Got it."}},
		},
		caps: llm.Capabilities{SupportsTools: true},
	}
	reg := agenttools.DefaultRegistry()
	perms, _ := LoadPermissionStore(t.TempDir() + "/perms.yaml")

	result, err := RunToolLoop(t.Context(), ToolLoopInput{
		Provider: prov, Registry: reg, Permissions: perms,
		UserInput: "list this dir",
	})
	if err != nil {
		t.Fatal(err)
	}
	if prov.calls != 2 {
		t.Errorf("expected 2 calls (tool + continuation), got %d", prov.calls)
	}
	if result.FinalText != "Got it." {
		t.Errorf("final: %q", result.FinalText)
	}
}

func TestToolLoop_RTierRunsConcurrently(t *testing.T) {
	prov := &mockProvider{
		scripts: [][]llm.Block{
			{
				{Type: llm.BlockToolUse, ToolUseID: "u1", ToolName: "LS",
					ToolInput: json.RawMessage(`{"path":"."}`)},
				{Type: llm.BlockToolUse, ToolUseID: "u2", ToolName: "LS",
					ToolInput: json.RawMessage(`{"path":"."}`)},
			},
			{{Type: llm.BlockText, Text: "done"}},
		},
		caps: llm.Capabilities{SupportsTools: true, SupportsParallelTools: true},
	}
	reg := agenttools.DefaultRegistry()
	perms, _ := LoadPermissionStore(t.TempDir() + "/perms.yaml")

	result, err := RunToolLoop(t.Context(), ToolLoopInput{
		Provider: prov, Registry: reg, Permissions: perms, UserInput: "x",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.FinalText != "done" {
		t.Errorf("final: %q", result.FinalText)
	}

	var found1, found2 bool
	for _, m := range result.History {
		for _, b := range m.Blocks {
			if b.Type == llm.BlockToolResult && b.ToolUseRef == "u1" {
				found1 = true
			}
			if b.Type == llm.BlockToolResult && b.ToolUseRef == "u2" {
				found2 = true
			}
		}
	}
	if !found1 || !found2 {
		t.Errorf("missing tool results: u1=%v u2=%v", found1, found2)
	}
}

func TestToolLoop_UserDeniesWTier_TerminatesTurn(t *testing.T) {
	prov := &mockProvider{
		scripts: [][]llm.Block{{
			{Type: llm.BlockToolUse, ToolUseID: "u1", ToolName: "Write",
				ToolInput: json.RawMessage(`{"path":"/tmp/x","content":"x"}`)},
		}},
		caps: llm.Capabilities{SupportsTools: true},
	}
	reg := agenttools.DefaultRegistry()
	dir := t.TempDir()
	perms, _ := LoadPermissionStore(dir + "/perms.yaml")
	_ = perms.SetMode(ModeStrict)

	requester := func(ctx context.Context, toolUseID, name string, args json.RawMessage, tier llm.Permission, destructive bool) (bool, error) {
		return false, nil
	}

	result, err := RunToolLoop(t.Context(), ToolLoopInput{
		Provider: prov, Registry: reg, Permissions: perms,
		PermissionRequester: requester, UserInput: "write x",
	})
	if err != nil {
		t.Fatal(err)
	}
	if prov.calls != 1 {
		t.Errorf("denial should NOT cause another loop iteration; calls=%d", prov.calls)
	}
	last := result.History[len(result.History)-1]
	if last.Role != llm.RoleUser || len(last.Blocks) == 0 || !last.Blocks[0].IsError {
		t.Errorf("expected error tool_result, got %+v", last)
	}
}

// register a single failing tool for cleaner scripted scenarios
type alwaysFailTool struct{}

func (alwaysFailTool) Name() string                      { return "always_fail" }
func (alwaysFailTool) Description() string               { return "always fails" }
func (alwaysFailTool) Permission() agenttools.Permission { return agenttools.PermR }
func (alwaysFailTool) Schema() json.RawMessage           { return json.RawMessage(`{"type":"object"}`) }
func (alwaysFailTool) Execute(ctx context.Context, args json.RawMessage) (*agenttools.Result, error) {
	return nil, fmt.Errorf("always-fail")
}

func TestToolLoop_3StrikeErrorGuard(t *testing.T) {
	prov := &mockProvider{
		scripts: [][]llm.Block{
			{{Type: llm.BlockToolUse, ToolUseID: "u1", ToolName: "always_fail",
				ToolInput: json.RawMessage(`{}`)}},
			{{Type: llm.BlockToolUse, ToolUseID: "u2", ToolName: "always_fail",
				ToolInput: json.RawMessage(`{}`)}},
			{{Type: llm.BlockToolUse, ToolUseID: "u3", ToolName: "always_fail",
				ToolInput: json.RawMessage(`{}`)}},
		},
		caps: llm.Capabilities{SupportsTools: true},
	}
	reg := agenttools.NewRegistry()
	reg.MustRegister(alwaysFailTool{})
	perms, _ := LoadPermissionStore(t.TempDir() + "/perms.yaml")

	_, err := RunToolLoop(t.Context(), ToolLoopInput{
		Provider: prov, Registry: reg, Permissions: perms, UserInput: "x",
	})
	if err == nil || !strings.Contains(err.Error(), "3 consecutive") {
		t.Errorf("expected 3-strike abort, got %v", err)
	}
	if prov.calls != 3 {
		t.Errorf("expected exactly 3 calls before abort, got %d", prov.calls)
	}
}

func TestToolLoop_EmitsExpectedEvents(t *testing.T) {
	prov := &mockProvider{
		scripts: [][]llm.Block{
			{{Type: llm.BlockToolUse, ToolUseID: "u1", ToolName: "LS",
				ToolInput: json.RawMessage(`{"path":"."}`)}},
			{{Type: llm.BlockText, Text: "done"}},
		},
		caps: llm.Capabilities{SupportsTools: true},
	}
	reg := agenttools.DefaultRegistry()
	perms, _ := LoadPermissionStore(t.TempDir() + "/perms.yaml")
	var events []LoopEventKind
	_, err := RunToolLoop(t.Context(), ToolLoopInput{
		Provider: prov, Registry: reg, Permissions: perms, UserInput: "x",
		EventSink: func(ev LoopEvent) { events = append(events, ev.Kind) },
	})
	if err != nil {
		t.Fatal(err)
	}
	wantKinds := []LoopEventKind{
		LoopToolUseStart, LoopToolExecStart, LoopToolExecComplete,
	}
	for _, k := range wantKinds {
		found := false
		for _, e := range events {
			if e == k {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("missing event %s", k)
		}
	}
}

// Cap enforcement + graceful degradation is covered by
// TestToolLoop_HitsCap_DegradesToFinalAnswer.

// fakeMCPTool is a minimal PermW tool that declares MCP origin.
type fakeMCPTool struct {
	executed bool
}

func (*fakeMCPTool) Name() string                       { return "mcp__x__do" }
func (*fakeMCPTool) Description() string                { return "d" }
func (*fakeMCPTool) Permission() agenttools.Permission  { return agenttools.PermW }
func (*fakeMCPTool) Origin() agenttools.Origin          { return agenttools.OriginMCP }
func (*fakeMCPTool) Schema() json.RawMessage            { return json.RawMessage(`{"type":"object"}`) }
func (m *fakeMCPTool) Execute(_ context.Context, _ json.RawMessage) (*agenttools.Result, error) {
	m.executed = true
	return agenttools.NewTextResult("ok"), nil
}

// TestToolLoopMCPConfirmsInPermissive verifies that an MCP-origin W tool
// triggers the PermissionRequester even under ModePermissive (confirm-by-default).
func TestToolLoopMCPConfirmsInPermissive(t *testing.T) {
	// Script: first turn emits an MCP tool call; second turn is a plain-text
	// terminator in case the gate doesn't fire (RED path: tool executes and loop
	// continues). After the fix the PermissionRequester denies and the loop
	// returns early — the second script is never reached.
	prov := &mockProvider{
		scripts: [][]llm.Block{
			{{Type: llm.BlockToolUse, ToolUseID: "u1", ToolName: "mcp__x__do",
				ToolInput: json.RawMessage(`{}`)}},
			{{Type: llm.BlockText, Text: "done"}},
		},
		caps: llm.Capabilities{SupportsTools: true},
	}
	tool := &fakeMCPTool{}
	reg := agenttools.NewRegistry()
	reg.MustRegister(tool)
	perms, _ := LoadPermissionStore(t.TempDir() + "/perms.yaml")
	// Default mode is ModePermissive — no explicit SetMode needed.

	denied := false
	_, err := RunToolLoop(t.Context(), ToolLoopInput{
		Provider:    prov,
		Registry:    reg,
		Permissions: perms,
		UserInput:   "go",
		PermissionRequester: func(_ context.Context, _, _ string, _ json.RawMessage, _ llm.Permission, _ bool) (bool, error) {
			denied = true // confirm was requested
			return false, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !denied {
		t.Fatal("MCP tool must trigger a permission request in permissive mode")
	}
}
