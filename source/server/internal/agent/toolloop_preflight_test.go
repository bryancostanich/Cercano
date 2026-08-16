package agent

import (
	"context"
	"strings"
	"testing"

	"cercano/source/server/internal/inference"
	"cercano/source/server/internal/llm"
)

// callCountingProvider records how many times StreamChat was invoked so a test
// can prove the pre-flight guard short-circuits BEFORE any provider round-trip.
// A single successful text turn is returned if it ever is called.
type callCountingProvider struct {
	calls   int
	lastReq llm.ChatRequest
}

func (p *callCountingProvider) Name() string { return "counting" }
func (p *callCountingProvider) Capabilities() inference.Capabilities {
	return inference.Capabilities{SupportsTools: true}
}
func (p *callCountingProvider) Chat(ctx context.Context, req llm.ChatRequest) (llm.ChatResponse, error) {
	return llm.ChatResponse{}, nil
}
func (p *callCountingProvider) StreamChat(ctx context.Context, req llm.ChatRequest) (llm.StreamReader, error) {
	p.calls++
	p.lastReq = req
	events := []llm.StreamEvent{
		{Type: llm.EventMessageStart},
		{Type: llm.EventTextDelta, TextDelta: "ok"},
		{Type: llm.EventMessageStop, StopReason: "end_turn"},
	}
	return &scriptedStream{events: events}, nil
}

// An oversized prompt against a small ContextWindow must fail fast with a
// classified ErrContextOverflow and never reach the provider.
func TestToolLoop_Preflight_OversizedPrompt_FailsFastNoProviderCall(t *testing.T) {
	prov := &callCountingProvider{}
	reg := testDefaultRegistry()
	perms, _ := LoadPermissionStore(t.TempDir() + "/perms.yaml")

	_, err := RunToolLoop(t.Context(), ToolLoopInput{
		Provider:      prov,
		Registry:      reg,
		Permissions:   perms,
		UserInput:     strings.Repeat("x", 8000), // ~2000 tokens
		ContextWindow: 1000,                      // budget ~900 tokens
	})
	if err == nil {
		t.Fatal("oversized prompt must return an error")
	}
	if llm.ClassOf(err) != llm.ErrContextOverflow {
		t.Fatalf("want ErrContextOverflow, got %q (%v)", llm.ClassOf(err), err)
	}
	if prov.calls != 0 {
		t.Errorf("guard must short-circuit before the provider; got %d provider calls", prov.calls)
	}
}

// A prompt that fits under the window must run normally and reach the provider.
func TestToolLoop_Preflight_FittingPrompt_RunsNormally(t *testing.T) {
	prov := &callCountingProvider{}
	reg := testDefaultRegistry()
	perms, _ := LoadPermissionStore(t.TempDir() + "/perms.yaml")

	res, err := RunToolLoop(t.Context(), ToolLoopInput{
		Provider:      prov,
		Registry:      reg,
		Permissions:   perms,
		UserInput:     "small task",
		ContextWindow: 32768,
	})
	if err != nil {
		t.Fatal(err)
	}
	if prov.calls == 0 {
		t.Error("a fitting prompt must reach the provider")
	}
	if res.FinalText != "ok" {
		t.Errorf("want final text %q, got %q", "ok", res.FinalText)
	}
}

// ContextWindow 0 (the interactive loop's default) disables the guard entirely,
// even for a large prompt — the provider is still called.
func TestToolLoop_Preflight_TrimsOversizedHistoryBeforeProviderCall(t *testing.T) {
	prov := &callCountingProvider{}
	reg := testDefaultRegistry()
	perms, _ := LoadPermissionStore(t.TempDir() + "/perms.yaml")
	history := []llm.Message{
		{Role: llm.RoleUser, Blocks: []llm.Block{{Type: llm.BlockText, Text: strings.Repeat("old", 8000)}}},
		{Role: llm.RoleAssistant, Blocks: []llm.Block{{Type: llm.BlockText, Text: strings.Repeat("middle", 8000)}}},
	}

	_, err := RunToolLoop(t.Context(), ToolLoopInput{
		Provider:      prov,
		Registry:      reg,
		Permissions:   perms,
		ConvHistory:   history,
		UserInput:     "current task",
		ContextWindow: 32768,
	})
	if err != nil {
		t.Fatal(err)
	}
	if prov.calls == 0 {
		t.Fatal("expected provider call after trimming")
	}
	if len(prov.lastReq.Messages) >= len(history)+1 {
		t.Fatalf("expected history to be trimmed before provider call, got %d messages", len(prov.lastReq.Messages))
	}
	last := prov.lastReq.Messages[len(prov.lastReq.Messages)-1]
	if last.Role != llm.RoleUser || last.Blocks[0].Text != "current task" {
		t.Fatalf("current user message must be preserved, got %+v", last)
	}
}

func TestToolLoop_Preflight_ZeroWindow_Disabled(t *testing.T) {
	prov := &callCountingProvider{}
	reg := testDefaultRegistry()
	perms, _ := LoadPermissionStore(t.TempDir() + "/perms.yaml")

	_, err := RunToolLoop(t.Context(), ToolLoopInput{
		Provider:      prov,
		Registry:      reg,
		Permissions:   perms,
		UserInput:     strings.Repeat("x", 8000),
		ContextWindow: 0,
	})
	if err != nil {
		t.Fatal(err)
	}
	if prov.calls == 0 {
		t.Error("window 0 must disable the guard and let the call through")
	}
}
