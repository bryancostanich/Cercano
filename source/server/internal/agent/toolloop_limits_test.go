package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"cercano/source/server/internal/llm"
)

// alwaysToolProvider always returns a tool-call response regardless of whether
// tools are offered, so the loop can only stop via the iteration cap.
type alwaysToolProvider struct{ calls int }

func (p *alwaysToolProvider) Name() string { return "always-tool" }
func (p *alwaysToolProvider) Capabilities() llm.Capabilities {
	return llm.Capabilities{SupportsTools: true}
}
func (p *alwaysToolProvider) Chat(_ context.Context, _ llm.ChatRequest) (llm.ChatResponse, error) {
	return llm.ChatResponse{}, nil
}
func (p *alwaysToolProvider) StreamChat(_ context.Context, req llm.ChatRequest) (llm.StreamReader, error) {
	p.calls++
	// Always return a tool call so the loop never terminates on its own.
	// When the cap is hit and tools are withheld the loop sends a final
	// no-tools request — at that point we return text so it can exit cleanly.
	var blocks []llm.Block
	if len(req.Tools) > 0 {
		blocks = []llm.Block{{
			Type:      llm.BlockToolUse,
			ToolUseID: fmt.Sprintf("u%d", p.calls),
			ToolName:  "LS",
			ToolInput: json.RawMessage(`{"path":"."}`),
		}}
	} else {
		blocks = []llm.Block{{Type: llm.BlockText, Text: "Best answer."}}
	}
	return &scriptedStream{events: blocksToEvents(blocks)}, nil
}

// TestRunToolLoopRespectsMaxIterations verifies that MaxIterations=2 stops the
// loop after exactly 2 tool-call turns plus 1 final no-tools turn (= 3 total
// StreamChat calls), matching the degradation path used by the default cap.
func TestRunToolLoopRespectsMaxIterations(t *testing.T) {
	prov := &alwaysToolProvider{}
	reg := testDefaultRegistry()
	perms, _ := LoadPermissionStore(t.TempDir() + "/perms.yaml")

	result, err := RunToolLoop(t.Context(), ToolLoopInput{
		Provider:      prov,
		Registry:      reg,
		Permissions:   perms,
		UserInput:     "do work",
		MaxIterations: 2,
	})
	if err != nil {
		t.Fatalf("capped loop should degrade gracefully, not error: %v", err)
	}
	// 2 tool-call turns + 1 final no-tools pass = 3
	want := 2 + 1
	if prov.calls != want {
		t.Errorf("expected %d StreamChat calls (cap=%d tool turns + 1 final), got %d", want, 2, prov.calls)
	}
	if result.Iterations != 2 {
		t.Errorf("expected Iterations=2, got %d", result.Iterations)
	}
}

// TestRunToolLoopZeroMaxIterationsUsesDefault verifies that MaxIterations=0
// leaves behavior unchanged — the loop runs to the package default cap of 50.
func TestRunToolLoopZeroMaxIterationsUsesDefault(t *testing.T) {
	prov := &alwaysToolProvider{}
	reg := testDefaultRegistry()
	perms, _ := LoadPermissionStore(t.TempDir() + "/perms.yaml")

	_, err := RunToolLoop(t.Context(), ToolLoopInput{
		Provider:      prov,
		Registry:      reg,
		Permissions:   perms,
		UserInput:     "do work",
		MaxIterations: 0, // explicit zero — must use package default
	})
	if err != nil {
		t.Fatalf("zero MaxIterations should not error: %v", err)
	}
	// MaxToolLoopIterations tool turns + 1 final pass
	wantCalls := MaxToolLoopIterations + 1
	if prov.calls != wantCalls {
		t.Errorf("MaxIterations=0 should behave like default (%d): got %d StreamChat calls", wantCalls, prov.calls)
	}
}
