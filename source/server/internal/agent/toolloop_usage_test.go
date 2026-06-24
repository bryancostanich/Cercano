package agent

import (
	"context"
	"encoding/json"
	"testing"

	"cercano/source/server/internal/agenttools"
	"cercano/source/server/internal/llm"
)

// usageProvider streams scripted block sequences, each with its own usage.
type usageProvider struct {
	scripts [][]llm.Block
	usages  [][2]int // {input, output} per call
	calls   int
}

func (p *usageProvider) Name() string                   { return "usage" }
func (p *usageProvider) Capabilities() llm.Capabilities { return llm.Capabilities{SupportsTools: true} }
func (p *usageProvider) Chat(context.Context, llm.ChatRequest) (llm.ChatResponse, error) {
	return llm.ChatResponse{}, nil
}
func (p *usageProvider) StreamChat(_ context.Context, _ llm.ChatRequest) (llm.StreamReader, error) {
	i := p.calls
	p.calls++
	evs := []llm.StreamEvent{{Type: llm.EventMessageStart, InputTokens: p.usages[i][0]}}
	for _, b := range p.scripts[i] {
		switch b.Type {
		case llm.BlockText:
			evs = append(evs, llm.StreamEvent{Type: llm.EventTextDelta, TextDelta: b.Text})
		case llm.BlockToolUse:
			evs = append(evs,
				llm.StreamEvent{Type: llm.EventToolUseStart, ToolUseID: b.ToolUseID, ToolName: b.ToolName},
				llm.StreamEvent{Type: llm.EventToolUseInputDelta, TextDelta: string(b.ToolInput)},
				llm.StreamEvent{Type: llm.EventToolUseStop})
		}
	}
	evs = append(evs, llm.StreamEvent{Type: llm.EventMessageStop, StopReason: "end_turn", OutputTokens: p.usages[i][1]})
	return &fakeReader{events: evs}, nil
}

func TestRunToolLoop_ReturnsLastCallUsage(t *testing.T) {
	prov := &usageProvider{
		scripts: [][]llm.Block{
			{{Type: llm.BlockToolUse, ToolUseID: "u1", ToolName: "LS", ToolInput: json.RawMessage(`{"path":"."}`)}},
			{{Type: llm.BlockText, Text: "done"}},
		},
		usages: [][2]int{{100, 10}, {250, 20}},
	}
	res, err := RunToolLoop(context.Background(), ToolLoopInput{
		Provider: prov, Registry: agenttools.DefaultRegistry(), UserInput: "list",
	})
	if err != nil {
		t.Fatalf("RunToolLoop: %v", err)
	}
	if res.InputTokens != 250 || res.OutputTokens != 20 {
		t.Fatalf("usage = (%d,%d), want last call (250,20)", res.InputTokens, res.OutputTokens)
	}
}
