package tools

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"cercano/source/server/internal/agenttools"
	"cercano/source/server/internal/dispatch"
	"cercano/source/server/internal/inference"
	"cercano/source/server/internal/llm"
	"cercano/source/server/internal/locus"
	"cercano/source/server/pkg/config"
)

type countingTool struct{ runs *int }

func (countingTool) Name() string                      { return "Edit" }
func (countingTool) Description() string               { return "stub editor" }
func (countingTool) Permission() agenttools.Permission { return agenttools.PermW }
func (countingTool) Schema() json.RawMessage           { return json.RawMessage(`{"type":"object"}`) }
func (c countingTool) Execute(context.Context, json.RawMessage) (*agenttools.Result, error) {
	*c.runs++
	return &agenttools.Result{Type: agenttools.ResultText, Text: "edited"}, nil
}

// localThenStartupFailure serves one tool-calling turn, then refuses to start.
type localThenStartupFailure struct{ calls int }

func (localThenStartupFailure) Name() string { return "llama_server" }
func (localThenStartupFailure) Capabilities() inference.Capabilities {
	return inference.Capabilities{SupportsTools: true}
}
func (p *localThenStartupFailure) Chat(context.Context, llm.ChatRequest) (llm.ChatResponse, error) {
	return llm.ChatResponse{}, errors.New("unused")
}
func (p *localThenStartupFailure) StreamChat(context.Context, llm.ChatRequest) (llm.StreamReader, error) {
	p.calls++
	if p.calls == 1 {
		return &sliceStream{events: []llm.StreamEvent{
			{Type: llm.EventMessageStart},
			{Type: llm.EventToolUseStart, ToolName: "Edit", ToolUseID: "call-1"},
			{Type: llm.EventToolUseInputDelta, TextDelta: "{}"},
			{Type: llm.EventToolUseStop},
			{Type: llm.EventMessageStop, StopReason: "tool_use"},
		}}, nil
	}
	return nil, &llm.LocalStartupError{Provider: "llama_server", Model: "glm-local", Err: errors.New("memory guard refused startup")}
}

type cloudFinisher struct {
	calls            int
	sawCompletedWork bool
}

func (cloudFinisher) Name() string { return "anthropic" }
func (cloudFinisher) Capabilities() inference.Capabilities {
	return inference.Capabilities{SupportsTools: true}
}
func (p *cloudFinisher) Chat(context.Context, llm.ChatRequest) (llm.ChatResponse, error) {
	return llm.ChatResponse{}, errors.New("unused")
}
func (p *cloudFinisher) StreamChat(_ context.Context, req llm.ChatRequest) (llm.StreamReader, error) {
	p.calls++
	for _, m := range req.Messages {
		for _, b := range m.Blocks {
			// Native tool results store their payload in Content, not Text.
			// Also accept text history from compatibility-only providers.
			if b.Type == llm.BlockToolResult && strings.Contains(b.Content, "edited") {
				p.sawCompletedWork = true
			}
			if b.Type == llm.BlockText && strings.Contains(b.Text, "edited") {
				p.sawCompletedWork = true
			}
		}
	}
	return &sliceStream{events: []llm.StreamEvent{
		{Type: llm.EventMessageStart},
		{Type: llm.EventTextDelta, TextDelta: "Done."},
		{Type: llm.EventMessageStop, StopReason: "end_turn"},
	}}, nil
}

// A local startup failure mid tool loop must continue on cloud with the work
// already done: the tool must not run twice and its result must survive.
func TestAgenticDispatch_MidLoopStartupFailureContinuesWithoutReplay(t *testing.T) {
	runs := 0
	svc := New(nil, nil, nil, nil)
	installTestFailureLog(t, svc)
	reg := agenttools.NewRegistry()
	reg.MustRegister(countingTool{runs: &runs})
	svc.SetRegistry(reg)

	local, cloud := &localThenStartupFailure{}, &cloudFinisher{}
	eng := dispatch.NewEngine(
		func() inference.Tiers { return inference.Tiers{Open: local, Cloud: cloud} },
		func() locus.Mode { return locus.OpenPrimary },
		nil,
	)
	eng.SetModelFor(func(isCloud bool, _ config.Tier) string {
		if isCloud {
			return "claude-sonnet-4-6"
		}
		return "glm-local"
	})
	svc.SetEngine(eng)

	res, err := eng.Dispatch(t.Context(), dispatch.Spec{
		Mode:           dispatch.Agentic,
		RoutingTask:    config.TaskChat,
		Task:           "Edit the file.",
		Tools:          []string{"Edit"},
		MaxIterations:  4,
		ConversationID: "parent",
	})
	if err != nil {
		t.Fatalf("dispatch stranded by local startup failure: %v", err)
	}
	if runs != 1 {
		t.Fatalf("completed tool action replayed: runs=%d", runs)
	}
	if !cloud.sawCompletedWork {
		t.Fatal("cloud continuation lost the completed tool result")
	}
	if res.Text != "Done." || !res.IsCloud || res.Model != "claude-sonnet-4-6" {
		t.Fatalf("wrong result/attribution: %+v", res)
	}
}

func (localThenStartupFailure) RuntimeContext(context.Context, string, bool) (llm.RuntimeContext, error) {
	return llm.RuntimeContext{Window: 65536, InstanceID: "startup-fixture"}, nil
}
