package tools

import (
	"context"
	"strings"
	"testing"

	"cercano/source/server/internal/agent"
	"cercano/source/server/internal/agenttools"
	"cercano/source/server/internal/inference"
	"cercano/source/server/internal/llm"
)

// Field repro from the LUNIE CUT/INSERT session: a sub-agent on glm-4.5-air
// (16384-token window) with a realistic task. Before the clamp this died in
// preflight with context_overflow because the 8192 output reserve consumed
// half the window.
func TestDispatch_LunieSizedTaskFitsSmallLocalWindow(t *testing.T) {
	prov := &recordingProvider{}
	perms, _ := agent.LoadPermissionStore(t.TempDir() + "/perms.yaml")
	reg := agenttools.NewRegistry()

	// ~6300 message tokens, matching the observed failing dispatches.
	task := strings.Repeat("analyze the cut and insert adversarial thread behavior in detail. ", 400)

	_, err := agent.RunToolLoop(context.Background(), agent.ToolLoopInput{
		Provider: prov, Registry: reg, Permissions: perms,
		Model: "glm-4.5-air", UserInput: task,
		ContextWindow: 16384, ContextWindowKnown: true,
	})
	if err != nil {
		t.Fatalf("realistic sub-agent task still fails on a 16K local window: %v", err)
	}
	if len(prov.reqs) == 0 {
		t.Fatal("no provider request recorded")
	}
	if got := prov.reqs[0].MaxTokens; got != 4096 {
		t.Errorf("output reserve = %d, want 4096 on a 16K window", got)
	}
}

type recordingProvider struct{ reqs []llm.ChatRequest }

func (recordingProvider) Name() string { return "llama_server" }
func (recordingProvider) Capabilities() inference.Capabilities {
	return inference.Capabilities{SupportsTools: true}
}
func (p *recordingProvider) Chat(_ context.Context, req llm.ChatRequest) (llm.ChatResponse, error) {
	p.reqs = append(p.reqs, req)
	return llm.ChatResponse{Blocks: []llm.Block{{Type: llm.BlockText, Text: "done"}}, StopReason: "end_turn"}, nil
}
func (p *recordingProvider) StreamChat(_ context.Context, req llm.ChatRequest) (llm.StreamReader, error) {
	p.reqs = append(p.reqs, req)
	return &sliceStream{events: []llm.StreamEvent{
		{Type: llm.EventMessageStart},
		{Type: llm.EventTextDelta, TextDelta: "done"},
		{Type: llm.EventMessageStop, StopReason: "end_turn"},
	}}, nil
}

func (recordingProvider) RuntimeContext(context.Context, string, bool) (llm.RuntimeContext, error) {
	return llm.RuntimeContext{Window: 16384, InstanceID: "recording-fixture"}, nil
}
