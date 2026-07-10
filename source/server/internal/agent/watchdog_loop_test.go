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

// fakeWTool is a minimal PermW (non-MCP) tool that records whether it ran.
type fakeWTool struct {
	executed bool
}

func (*fakeWTool) Name() string                      { return "edit_file" }
func (*fakeWTool) Description() string               { return "edits a file" }
func (*fakeWTool) Permission() agenttools.Permission { return agenttools.PermW }
func (*fakeWTool) Schema() json.RawMessage           { return json.RawMessage(`{"type":"object"}`) }
func (m *fakeWTool) Execute(_ context.Context, _ json.RawMessage) (*agenttools.Result, error) {
	m.executed = true
	return agenttools.NewTextResult("ok"), nil
}

// TestWatchdogGate_ChallengeSkipsExecution: when the WatchdogGate returns
// "challenge", the W-tier tool must NOT execute and a "watchdog" tool-result is
// injected so the model can react.
func TestWatchdogGate_ChallengeSkipsExecution(t *testing.T) {
	prov := &mockProvider{
		scripts: [][]llm.Block{
			{{Type: llm.BlockToolUse, ToolUseID: "u1", ToolName: "edit_file",
				ToolInput: json.RawMessage(`{}`)}},
			{{Type: llm.BlockText, Text: "understood"}},
		},
		caps: llm.Capabilities{SupportsTools: true},
	}
	tool := &fakeWTool{}
	reg := agenttools.NewRegistry()
	reg.MustRegister(tool)
	perms, _ := LoadPermissionStore(t.TempDir() + "/perms.yaml")

	result, err := RunToolLoop(t.Context(), ToolLoopInput{
		Provider: prov, Registry: reg, Permissions: perms, UserInput: "edit it",
		// A permissive PermissionRequester would allow execution — the gate must
		// preempt it entirely.
		PermissionRequester: func(_ context.Context, _, _ string, _ json.RawMessage, _ llm.Permission, _ bool) (bool, error) {
			return true, nil
		},
		WatchdogGate: func(_ context.Context, _ string, _ json.RawMessage, _ []llm.Message) WatchdogDecision {
			return WatchdogDecision{Action: "challenge", Protocol: "systematic-debugging", Challenge: "no evidence"}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if tool.executed {
		t.Fatal("challenge must skip tool execution")
	}
	var found bool
	for _, m := range result.History {
		for _, b := range m.Blocks {
			if b.Type == llm.BlockToolResult && b.ToolUseRef == "u1" &&
				strings.Contains(b.Content, "Challenge — comply or justify.") &&
				strings.Contains(b.Content, "watchdog") {
				found = true
			}
		}
	}
	if !found {
		t.Fatal("expected an injected tool-result with Challenge — comply or justify watchdog text")
	}
}

// TestWatchdogBlockDoesNotTripErrorAbort: a watchdog that always blocks must not
// accumulate "consecutive tool errors" and abort the turn. Without the
// watchdogIntervened guard, 3 blocked turns would hit the 3-strike abort;
// with the fix the loop runs to the iteration cap and degrades gracefully.
func TestWatchdogBlockDoesNotTripErrorAbort(t *testing.T) {
	// mockProvider with 5 edit_file tool-call turns + 1 final text turn.
	// The watchdog blocks every edit_file call, so without the fix the
	// 3-strike consecutive-error abort would fire on turn 3.
	scripts := make([][]llm.Block, 6)
	for i := 0; i < 5; i++ {
		scripts[i] = []llm.Block{{
			Type:      llm.BlockToolUse,
			ToolUseID: fmt.Sprintf("u%d", i+1),
			ToolName:  "edit_file",
			ToolInput: json.RawMessage(`{}`),
		}}
	}
	scripts[5] = []llm.Block{{Type: llm.BlockText, Text: "gave up"}}
	blockProv := &mockProvider{scripts: scripts, caps: llm.Capabilities{SupportsTools: true}}

	tool := &fakeWTool{}
	reg := agenttools.NewRegistry()
	reg.MustRegister(tool)
	perms, _ := LoadPermissionStore(t.TempDir() + "/perms.yaml")

	_, err := RunToolLoop(t.Context(), ToolLoopInput{
		Provider:    blockProv,
		Registry:    reg,
		Permissions: perms,
		UserInput:   "do it",
		PermissionRequester: func(_ context.Context, _, _ string, _ json.RawMessage, _ llm.Permission, _ bool) (bool, error) {
			return true, nil
		},
		WatchdogGate: func(_ context.Context, _ string, _ json.RawMessage, _ []llm.Message) WatchdogDecision {
			return WatchdogDecision{Action: "block", Protocol: "systematic-debugging", Challenge: "blocked"}
		},
		MaxIterations: 5,
	})
	if err != nil && strings.Contains(err.Error(), "3 consecutive iterations of tool errors") {
		t.Fatalf("watchdog blocks must not trip the consecutive-error abort, got: %v", err)
	}
	if tool.executed {
		t.Fatal("blocked tool must not have executed")
	}
}

// scriptedProvider is a minimal llm.Provider that returns successive plain-text
// replies from a slice. SupportsTools is required by RunToolLoop.
type scriptedProvider struct {
	replies []string
	calls   int
}

func (p *scriptedProvider) Name() string { return "scripted" }
func (p *scriptedProvider) Capabilities() llm.Capabilities {
	return llm.Capabilities{SupportsTools: true}
}
func (p *scriptedProvider) Chat(_ context.Context, _ llm.ChatRequest) (llm.ChatResponse, error) {
	return llm.ChatResponse{}, nil
}
func (p *scriptedProvider) StreamChat(_ context.Context, _ llm.ChatRequest) (llm.StreamReader, error) {
	if p.calls >= len(p.replies) {
		return nil, fmt.Errorf("scriptedProvider: no reply for call %d", p.calls)
	}
	text := p.replies[p.calls]
	p.calls++
	return &scriptedStream{events: blocksToEvents([]llm.Block{{Type: llm.BlockText, Text: text}})}, nil
}

// emptyRegistry returns an agenttools.Registry with no tools registered.
func emptyRegistry(_ *testing.T) *agenttools.Registry {
	return agenttools.NewRegistry()
}

// TestWatchdogTurnEnd_ChallengeReopensTurn: when the gate challenges the first
// reply, the loop reopens the turn and the model provides a clean second reply.
func TestWatchdogTurnEnd_ChallengeReopensTurn(t *testing.T) {
	prov := &scriptedProvider{replies: []string{"jargon-laden reply here", "clean reply"}}
	calls := 0
	gate := func(_ context.Context, finalText string, _ []llm.Message) WatchdogDecision {
		calls++
		if calls == 1 {
			return WatchdogDecision{Action: "challenge", Protocol: "plain-english", Challenge: "too much jargon"}
		}
		return WatchdogDecision{Action: "allow"}
	}
	res, err := RunToolLoop(t.Context(), ToolLoopInput{
		Provider: prov, Registry: emptyRegistry(t), Permissions: nil,
		UserInput: "hi", WatchdogTurnEnd: gate, MaxIterations: 5,
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.FinalText != "clean reply" {
		t.Fatalf("expected the revised reply to be returned, got %q", res.FinalText)
	}
	if calls != 2 {
		t.Fatalf("gate should fire on both turn ends, got %d", calls)
	}
}

// TestWatchdogTurnEnd_AllowReturns: when the gate allows, the reply is returned
// unchanged without reopening the turn.
func TestWatchdogTurnEnd_AllowReturns(t *testing.T) {
	prov := &scriptedProvider{replies: []string{"a perfectly fine substantive reply"}}
	gate := func(_ context.Context, _ string, _ []llm.Message) WatchdogDecision {
		return WatchdogDecision{Action: "allow"}
	}
	res, err := RunToolLoop(t.Context(), ToolLoopInput{
		Provider: prov, Registry: emptyRegistry(t), UserInput: "hi", WatchdogTurnEnd: gate, MaxIterations: 5,
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.FinalText != "a perfectly fine substantive reply" {
		t.Fatalf("allow must return the reply unchanged, got %q", res.FinalText)
	}
}

// TestWatchdogTurnEnd_BlockReopensWithoutOverride: when the gate blocks the
// first reply, the loop reopens the turn and the injected note must state that
// no override is available (and must NOT offer `justify`).
func TestWatchdogTurnEnd_BlockReopensWithoutOverride(t *testing.T) {
	prov := &scriptedProvider{replies: []string{"first reply", "revised reply"}}
	calls := 0
	gate := func(_ context.Context, _ string, _ []llm.Message) WatchdogDecision {
		calls++
		if calls == 1 {
			return WatchdogDecision{Action: "block", Protocol: "plain-english", Challenge: "rewrite it"}
		}
		return WatchdogDecision{Action: "allow"}
	}
	res, err := RunToolLoop(t.Context(), ToolLoopInput{
		Provider: prov, Registry: emptyRegistry(t), UserInput: "hi",
		WatchdogTurnEnd: gate, MaxIterations: 5,
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.FinalText != "revised reply" {
		t.Fatalf("block must reopen the turn; got %q", res.FinalText)
	}
	// The injected revise note must state no override is available and must
	// NOT offer justify.
	var note string
	for _, m := range res.History {
		for _, b := range m.Blocks {
			if b.Type == llm.BlockText && strings.Contains(b.Text, "watchdog") {
				note = b.Text
			}
		}
	}
	if !strings.Contains(note, "no override") || strings.Contains(note, "justify") {
		t.Fatalf("block note wrong: %q", note)
	}
}

// TestWatchdogTurnEnd_EscalateReturnsReply: when the gate escalates, the loop
// must return the reply unchanged and emit a LoopWatchdogEscalate event.
func TestWatchdogTurnEnd_EscalateReturnsReply(t *testing.T) {
	prov := &scriptedProvider{replies: []string{"the reply"}}
	gate := func(_ context.Context, _ string, _ []llm.Message) WatchdogDecision {
		return WatchdogDecision{Action: "escalate", Protocol: "plain-english", Challenge: "again"}
	}
	var kinds []LoopEventKind
	res, err := RunToolLoop(t.Context(), ToolLoopInput{
		Provider: prov, Registry: emptyRegistry(t), UserInput: "hi",
		WatchdogTurnEnd: gate, MaxIterations: 5,
		EventSink: func(ev LoopEvent) { kinds = append(kinds, ev.Kind) },
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.FinalText != "the reply" {
		t.Fatalf("escalate must return the reply unchanged, got %q", res.FinalText)
	}
	found := false
	for _, k := range kinds {
		if k == LoopWatchdogEscalate {
			found = true
		}
	}
	if !found {
		t.Fatal("escalate must emit LoopWatchdogEscalate")
	}
}

// TestWatchdogGate_AllowExecutes: when the gate returns "allow", the tool runs
// exactly as it would with no gate (subject to the normal permission gate).
func TestWatchdogGate_AllowExecutes(t *testing.T) {
	prov := &mockProvider{
		scripts: [][]llm.Block{
			{{Type: llm.BlockToolUse, ToolUseID: "u1", ToolName: "edit_file",
				ToolInput: json.RawMessage(`{}`)}},
			{{Type: llm.BlockText, Text: "done"}},
		},
		caps: llm.Capabilities{SupportsTools: true},
	}
	tool := &fakeWTool{}
	reg := agenttools.NewRegistry()
	reg.MustRegister(tool)
	perms, _ := LoadPermissionStore(t.TempDir() + "/perms.yaml")

	_, err := RunToolLoop(t.Context(), ToolLoopInput{
		Provider: prov, Registry: reg, Permissions: perms, UserInput: "edit it",
		PermissionRequester: func(_ context.Context, _, _ string, _ json.RawMessage, _ llm.Permission, _ bool) (bool, error) {
			return true, nil
		},
		WatchdogGate: func(_ context.Context, _ string, _ json.RawMessage, _ []llm.Message) WatchdogDecision {
			return WatchdogDecision{Action: "allow"}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !tool.executed {
		t.Fatal("allow must let the tool execute")
	}
}

// TestWatchdogTurnEnd_ChallengeUsesReviseInstruction: the reopening note must
// carry the check's own corrective instruction, not a hardcoded plain-english
// rewrite line — a follow-through challenge needs "do the work now", not
// "rewrite your prose".
func TestWatchdogTurnEnd_ChallengeUsesReviseInstruction(t *testing.T) {
	prov := &scriptedProvider{replies: []string{"Let me check the log now.", "checked; here is the result"}}
	calls := 0
	revise := "Perform the action you announced NOW, in this same turn, using tool calls"
	gate := func(_ context.Context, _ string, _ []llm.Message) WatchdogDecision {
		calls++
		if calls == 1 {
			return WatchdogDecision{Action: "challenge", Protocol: "follow-through",
				Challenge: "you announced a check you never ran", Revise: revise}
		}
		return WatchdogDecision{Action: "allow"}
	}
	res, err := RunToolLoop(t.Context(), ToolLoopInput{
		Provider: prov, Registry: emptyRegistry(t), UserInput: "hi",
		WatchdogTurnEnd: gate, MaxIterations: 5,
	})
	if err != nil {
		t.Fatal(err)
	}
	var note string
	for _, m := range res.History {
		if m.Role == llm.RoleUser {
			for _, b := range m.Blocks {
				if b.Type == llm.BlockText && strings.Contains(b.Text, "watchdog (follow-through)") {
					note = b.Text
				}
			}
		}
	}
	if note == "" {
		t.Fatal("expected a follow-through watchdog note in history")
	}
	if !strings.Contains(note, "Challenge — comply or justify.") {
		t.Errorf("note must start with the challenge intervention phrase, got: %q", note)
	}
	if !strings.Contains(note, revise) {
		t.Errorf("note must carry the check's revise instruction, got: %q", note)
	}
	if strings.Contains(note, "plain, colleague-level English") {
		t.Errorf("note must not carry the plain-english rewrite line for a follow-through challenge: %q", note)
	}
}
