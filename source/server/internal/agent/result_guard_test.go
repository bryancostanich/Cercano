package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"cercano/source/server/internal/agenttools"
	"cercano/source/server/internal/inference"
	"cercano/source/server/internal/llm"
)

func TestCapToolResultForWindow_BoundsAgainstWindow(t *testing.T) {
	// 32k window at 1/8 => ~16 KB of result body.
	content := strings.Repeat("x", 200_000)

	got, capped := capToolResultForWindow(content, 32768)

	if !capped {
		t.Fatal("oversized result must be capped")
	}
	limit := (32768 / maxResultWindowFraction) * 4
	if len(got) > limit+256 { // +note
		t.Fatalf("capped result = %d bytes, want <= ~%d", len(got), limit)
	}
	if !strings.Contains(got, "truncated") {
		t.Error("the model must be told the result was truncated")
	}
	if !strings.Contains(got, "200000") {
		t.Error("note should report the original size so the model can judge how much it is missing")
	}
}

func TestCapToolResultForWindow_UnknownWindowIsNoOp(t *testing.T) {
	// window 0 means "could not resolve" — guessing would truncate
	// cloud-sized results on no evidence.
	content := strings.Repeat("x", 500_000)
	for _, w := range []int{0, -1} {
		got, capped := capToolResultForWindow(content, w)
		if capped || got != content {
			t.Fatalf("window %d must be a no-op", w)
		}
	}
}

func TestCapToolResultForWindow_SmallResultUntouched(t *testing.T) {
	content := "3 matches found"
	got, capped := capToolResultForWindow(content, 32768)
	if capped || got != content {
		t.Fatalf("small result must pass through unchanged, got capped=%v %q", capped, got)
	}
}

func TestCapToolResultForWindow_RuneSafe(t *testing.T) {
	content := strings.Repeat("é", 100_000)
	got, capped := capToolResultForWindow(content, 32768)
	if !capped {
		t.Fatal("expected capping")
	}
	if strings.ContainsRune(got, '\uFFFD') {
		t.Fatal("truncation split a rune")
	}
}

// bigResultTool returns a payload of the incident's size on its first call,
// standing in for the unscoped Grep that produced ~346 KB.
type bigResultTool struct{ payload string }

func (bigResultTool) Name() string                      { return "Grep" }
func (bigResultTool) Description() string               { return "search" }
func (bigResultTool) Permission() agenttools.Permission { return agenttools.PermR }
func (bigResultTool) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"pattern":{"type":"string"}}}`)
}
func (t bigResultTool) Execute(context.Context, json.RawMessage) (*agenttools.Result, error) {
	return &agenttools.Result{Type: agenttools.ResultText, Text: t.payload}, nil
}

// scriptedToolProvider calls Grep once, then finishes.
type scriptedToolProvider struct {
	calls int
	reqs  []llm.ChatRequest
}

func (p *scriptedToolProvider) Name() string { return "scripted" }
func (p *scriptedToolProvider) Capabilities() inference.Capabilities {
	return inference.Capabilities{SupportsTools: true}
}
func (p *scriptedToolProvider) Chat(context.Context, llm.ChatRequest) (llm.ChatResponse, error) {
	return llm.ChatResponse{}, nil
}
func (p *scriptedToolProvider) StreamChat(_ context.Context, req llm.ChatRequest) (llm.StreamReader, error) {
	p.calls++
	p.reqs = append(p.reqs, req)
	if p.calls == 1 {
		return &scriptedStream{events: []llm.StreamEvent{
			{Type: llm.EventMessageStart},
			{Type: llm.EventToolUseStart, ToolUseID: "t1", ToolName: "Grep"},
			{Type: llm.EventToolUseInputDelta, ToolUseID: "t1", ToolInputRaw: json.RawMessage(`{"pattern":"x"}`)},
			{Type: llm.EventToolUseStop, ToolUseID: "t1"},
			{Type: llm.EventMessageStop, StopReason: "tool_use"},
		}}, nil
	}
	return &scriptedStream{events: []llm.StreamEvent{
		{Type: llm.EventMessageStart},
		{Type: llm.EventTextDelta, TextDelta: "done"},
		{Type: llm.EventMessageStop, StopReason: "end_turn"},
	}}, nil
}

// End-to-end reconstruction of the production failure: a small sub-agent turn
// whose first tool call returns a payload far larger than its window. Before
// the guard this produced "preflight context_overflow (94632 vs 32768)" on
// iteration 2 and the sub-agent could never recover.
func TestToolLoop_OversizedToolResultDoesNotOverflowWindow(t *testing.T) {
	reg := agenttools.NewRegistry()
	if err := reg.Register(bigResultTool{payload: strings.Repeat("z", 346_000)}); err != nil {
		t.Fatal(err)
	}
	perms, _ := LoadPermissionStore(t.TempDir() + "/perms.yaml")
	prov := &scriptedToolProvider{}

	res, err := RunToolLoop(t.Context(), ToolLoopInput{
		Provider:      prov,
		Registry:      reg,
		Permissions:   perms,
		UserInput:     "find where llama-server is spawned",
		ContextWindow: 32768,
	})
	if err != nil {
		t.Fatalf("the turn must survive an oversized tool result, got: %v", err)
	}
	if res.FinalText != "done" {
		t.Fatalf("FinalText = %q, want \"done\"", res.FinalText)
	}
	if prov.calls < 2 {
		t.Fatalf("expected a second iteration after the tool call, got %d provider calls", prov.calls)
	}

	// The tool result that entered history must be bounded.
	var biggest int
	for _, m := range prov.reqs[len(prov.reqs)-1].Messages {
		for _, b := range m.Blocks {
			if len(b.Content) > biggest {
				biggest = len(b.Content)
			}
		}
	}
	limit := (32768 / maxResultWindowFraction) * 4
	if biggest > limit+256 {
		t.Fatalf("tool result in history = %d bytes, want <= ~%d", biggest, limit)
	}
	t.Logf("incident shape: 346000-byte result entered history as %d bytes", biggest)
}
