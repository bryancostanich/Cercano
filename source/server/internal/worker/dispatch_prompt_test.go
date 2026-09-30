package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"cercano/source/server/internal/agenttools"
	projectctx "cercano/source/server/internal/context"
	"cercano/source/server/internal/dispatch"
	toolssvc "cercano/source/server/internal/hostsvc/tools"
	"cercano/source/server/internal/inference"
	"cercano/source/server/internal/llm"
)

// Use the real worker service and compactor, with scripted providers and a
// bounded synthetic inspection tool. No live inference or repository reads.
type workerPromptProvider struct {
	scriptedSummaryProvider
	sent  []llm.ChatRequest
	calls int
}

func (p *workerPromptProvider) Capabilities() inference.Capabilities {
	return inference.Capabilities{SupportsTools: true}
}
func (p *workerPromptProvider) RuntimeContext(context.Context, string, bool) (llm.RuntimeContext, error) {
	return llm.RuntimeContext{Window: 32768, InstanceID: "worker-prompt-fixture"}, nil
}
func (p *workerPromptProvider) StreamChat(_ context.Context, r llm.ChatRequest) (llm.StreamReader, error) {
	data, err := json.Marshal(r)
	if err != nil {
		return nil, err
	}
	var saved llm.ChatRequest
	if err := json.Unmarshal(data, &saved); err != nil {
		return nil, err
	}
	p.sent = append(p.sent, saved)
	p.calls++
	if p.calls%5 == 0 {
		return &compactProbeStream{events: []llm.StreamEvent{{Type: llm.EventMessageStart}, {Type: llm.EventTextDelta, TextDelta: "Verified the synthetic source evidence."}, {Type: llm.EventMessageStop, StopReason: "end_turn"}}}, nil
	}
	id := fmt.Sprintf("read-%d", p.calls)
	return &compactProbeStream{events: []llm.StreamEvent{
		{Type: llm.EventMessageStart},
		{Type: llm.EventToolUseStart, ToolUseID: id, ToolName: "Read"},
		{Type: llm.EventToolUseInputDelta, ToolUseID: id, TextDelta: `{"path":"synthetic.txt"}`},
		{Type: llm.EventToolUseStop, ToolUseID: id},
		{Type: llm.EventMessageStop, StopReason: "tool_use"},
	}}, nil
}

func TestWorkerDispatchPinsOriginalTaskAcrossRealCompactions(t *testing.T) {
	cfg := compactionTestConfig()
	cfg.Compaction.ActivationFloorTokens = 150
	cfg.Compaction.SegmentTokens = 100
	p := &workerPromptProvider{scriptedSummaryProvider: scriptedSummaryProvider{name: "ollama"}}
	svc := buildWorkerToolSvc(nil, nil, projectctx.NewLoader(), nil, p, cfg, nil, nil, nil, nil, nil).(*toolssvc.Service)
	registry := agenttools.NewRegistry()
	registry.MustRegister(workerPromptRead{})
	svc.SetRegistry(registry)
	for _, task := range []string{
		"Implement ONLY phase one.\nNo global downloads; no arbitrary calibration — α.\nReturn blockers before architecture changes.\n",
		"Independently inspect the second task.\nDo not edit files — β.\n",
	} {
		beforeRequests, beforeSummaries := len(p.sent), p.chats
		_, err := svc.RunAgenticDispatch(t.Context(), dispatch.Spec{Mode: dispatch.Agentic, Task: task, Tools: []string{"Read"}, MaxIterations: 6}, inference.Selection{Provider: p}, "probe-model")
		if err != nil {
			t.Fatal(err)
		}
		if len(p.sent)-beforeRequests != 5 || p.chats-beforeSummaries < 2 {
			t.Fatalf("requests=%d summarizations=%d", len(p.sent)-beforeRequests, p.chats-beforeSummaries)
		}
		summariesSeen := 0
		for _, r := range p.sent[beforeRequests:] {
			if len(r.Messages) == 0 || r.Messages[0].Role != llm.RoleUser || len(r.Messages[0].Blocks) != 1 || r.Messages[0].Blocks[0].Text != task {
				t.Fatal("worker request lost or rewrote original task")
			}
			count := 0
			for _, m := range r.Messages {
				for _, b := range m.Blocks {
					if b.Text == task {
						count++
					}
					if strings.Contains(b.Text, "[conversation summary]") {
						summariesSeen++
					}
				}
			}
			if count != 1 {
				t.Fatalf("task occurrences=%d", count)
			}
			if !llm.IsValidPairing(r.Messages) {
				t.Fatal("worker request broke tool pairing")
			}
		}
		if summariesSeen < 2 {
			t.Fatalf("only %d requests contained actual summaries", summariesSeen)
		}
		for _, r := range p.reqs[beforeSummaries:] {
			for _, m := range r.Messages {
				for _, b := range m.Blocks {
					if strings.Contains(b.Text, task) {
						t.Fatal("original task leaked into summarizer input")
					}
				}
			}
			if strings.Contains(r.System, task) {
				t.Fatal("task leaked into summarizer system prompt")
			}
		}
	}
}

type workerPromptRead struct{}

func (workerPromptRead) Name() string                      { return "Read" }
func (workerPromptRead) Description() string               { return "Read synthetic evidence" }
func (workerPromptRead) Permission() agenttools.Permission { return agenttools.PermR }
func (workerPromptRead) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}}}`)
}
func (workerPromptRead) Execute(context.Context, json.RawMessage) (*agenttools.Result, error) {
	return &agenttools.Result{Type: agenttools.ResultText, Text: strings.Repeat("bounded source evidence remains available ", 80)}, nil
}
