package agent

import (
	"cercano/source/server/internal/agenttools"
	"cercano/source/server/internal/inference"
	"cercano/source/server/internal/llm"
	"context"
	"fmt"
	"testing"
)

type runtimeBudgetProvider struct {
	*mockProvider
	window int
	fail   bool
}

func (p *runtimeBudgetProvider) Name() string { return "llama_server" }
func (p *runtimeBudgetProvider) RuntimeContext(context.Context, string, bool) (llm.RuntimeContext, error) {
	if p.fail {
		return llm.RuntimeContext{}, fmt.Errorf("unconfirmed")
	}
	return llm.RuntimeContext{Window: p.window, InstanceID: "fixture"}, nil
}

func TestToolLoopBudgetUsesConfirmedCapacity(t *testing.T) {
	for _, window := range []int{8192, 65536, 131072} {
		p := &runtimeBudgetProvider{mockProvider: &mockProvider{scripts: [][]llm.Block{{{Type: llm.BlockText, Text: "ok"}}}, caps: inference.Capabilities{SupportsTools: true}}, window: window}
		var accounting LoopEvent
		_, err := RunToolLoop(t.Context(), ToolLoopInput{Provider: p, Registry: agenttools.NewRegistry(), UserInput: "hi", Model: "fixture", ContextWindow: 16384, ContextWindowKnown: true, MaxTokensPerTurn: 512, EventSink: func(ev LoopEvent) {
			if ev.Kind == LoopRequestAccounting {
				accounting = ev
			}
		}})
		if err != nil {
			t.Fatal(err)
		}
		if accounting.ContextWindow != window {
			t.Fatalf("budget=%d want confirmed %d", accounting.ContextWindow, window)
		}
	}
}

func TestToolLoopUnknownCapacityDoesNotSend(t *testing.T) {
	p := &runtimeBudgetProvider{mockProvider: &mockProvider{}, fail: true}
	_, err := RunToolLoop(t.Context(), ToolLoopInput{Provider: p, Registry: agenttools.NewRegistry(), UserInput: "hi", ContextWindow: 131072, ContextWindowKnown: true})
	if err == nil || len(p.reqs) != 0 {
		t.Fatalf("unknown runtime sent request: %v", err)
	}
}
