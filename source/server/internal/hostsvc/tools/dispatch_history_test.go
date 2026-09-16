package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"cercano/source/server/internal/agenttools"
	"cercano/source/server/internal/dispatch"
	"cercano/source/server/internal/inference"
	"cercano/source/server/internal/llm"
	"cercano/source/server/internal/locus"
	"cercano/source/server/pkg/config"
)

// Record requests after two tool batches: successful tool execution alone does
// not demonstrate that the model receives usable continuation history.
type historyProbeProvider struct {
	requests []llm.ChatRequest
	answer   string
}

func (*historyProbeProvider) Name() string { return "history-probe" }
func (*historyProbeProvider) Capabilities() inference.Capabilities {
	return inference.Capabilities{SupportsTools: true}
}
func (*historyProbeProvider) Chat(context.Context, llm.ChatRequest) (llm.ChatResponse, error) {
	return llm.ChatResponse{}, fmt.Errorf("unused")
}
func (p *historyProbeProvider) StreamChat(_ context.Context, r llm.ChatRequest) (llm.StreamReader, error) {
	p.requests = append(p.requests, r)
	events := []llm.StreamEvent{{Type: llm.EventMessageStart}}
	if len(p.requests) <= 2 {
		name := []string{"Read", "Grep"}[len(p.requests)-1]
		events = append(events, llm.StreamEvent{Type: llm.EventToolUseStart, ToolName: name, ToolUseID: fmt.Sprintf("history-%d", len(p.requests))}, llm.StreamEvent{Type: llm.EventToolUseInputDelta, TextDelta: "{}"}, llm.StreamEvent{Type: llm.EventToolUseStop})
	} else {
		events = append(events, llm.StreamEvent{Type: llm.EventTextDelta, TextDelta: p.answer})
	}
	events = append(events, llm.StreamEvent{Type: llm.EventMessageStop, StopReason: "end_turn"})
	return &sliceStream{events: events}, nil
}
func assertHistoryFormat(t *testing.T, p *historyProbeProvider, native bool) {
	t.Helper()
	if len(p.requests) != 3 {
		t.Fatalf("requests=%d, want 3", len(p.requests))
	}
	for i, r := range p.requests[1:] {
		calls, results, synthetic := 0, 0, false
		for _, m := range r.Messages {
			for _, b := range m.Blocks {
				if b.Type == llm.BlockToolUse {
					calls++
				}
				if b.Type == llm.BlockToolResult {
					results++
				}
				if b.Type == llm.BlockText && strings.Contains(b.Text, "I used the granted tools:") {
					synthetic = true
				}
			}
		}
		if native && (calls != i+1 || results != i+1 || synthetic) {
			t.Errorf("request %d: calls=%d results=%d synthetic=%t; want native history", i+2, calls, results, synthetic)
		}
		if !native && (calls != 0 || results != 0 || !synthetic) {
			t.Errorf("request %d: local compatibility history changed", i+2)
		}
	}
}
func TestDispatchHistoryCloudNativeLocalCompatible(t *testing.T) {
	for _, cloud := range []bool{true, false} {
		t.Run(fmt.Sprintf("cloud=%t", cloud), func(t *testing.T) {
			p := &historyProbeProvider{answer: "The configuration is loaded in config.go:42."}
			svc := New(nil, nil, nil, nil)
			installTestFailureLog(t, svc)
			svc.SetRegistry(historyProbeRegistry())
			res, err := svc.RunAgenticDispatch(t.Context(), dispatch.Spec{Mode: dispatch.Agentic, Task: "Trace configuration loading.", Tools: []string{"Read", "Grep"}, MaxIterations: 4}, inference.Selection{Provider: p, IsCloud: cloud}, "same-model")
			if err != nil || res.Text != p.answer {
				t.Fatalf("result=%+v error=%v", res, err)
			}
			assertHistoryFormat(t, p, cloud)
		})
	}
}
func TestDispatchHistoryRejectsSyntheticOnlyCompletion(t *testing.T) {
	p := &historyProbeProvider{answer: "I used the granted tools: Read, Grep."}
	svc := New(nil, nil, nil, nil)
	logPath := installTestFailureLog(t, svc)
	svc.SetRegistry(historyProbeRegistry())
	res, err := svc.RunAgenticDispatch(t.Context(), dispatch.Spec{Mode: dispatch.Agentic, Task: "Trace configuration loading.", Tools: []string{"Read", "Grep"}, MaxIterations: 4}, inference.Selection{Provider: p, IsCloud: true}, "same-model")
	if err == nil || !res.Suspicious {
		t.Fatalf("tools-only completion accepted: result=%+v error=%v", res, err)
	}
	if !strings.Contains(readFailureLog(t, logPath), `"error_class":"suspicious_noop"`) {
		t.Fatal("missing validation failure log")
	}
}
func TestSyntheticCompletionDetectionIsNarrow(t *testing.T) {
	for _, tc := range []struct {
		text string
		want bool
	}{
		{"I used the granted tools: Read, Grep.", true},
		{" I used the granted tools: LS, Glob, Grep.\n", true},
		{"I used the granted tools: mcp__service__read, git_status.", true},
		{"I used the granted tools: Read, Grep.\nThe setting is in config.go:42.", false},
		{"I used the granted tools: Read. The setting is in config.go:42.", false},
		{"The file contains the text 'I used the granted tools: Read.'", false},
		{"Done.", false}, {"", false},
	} {
		t.Run(tc.text, func(t *testing.T) {
			got, _ := detectSuspiciousNoOp(tc.text, map[string]bool{"Read": true, "Grep": true}, nil)
			if got != tc.want {
				t.Fatalf("got %t want %t", got, tc.want)
			}
		})
	}
}

// Startup fallback changes the serving location inside a loop, rather than
// invoking RunAgenticDispatch again with a new selection.
func TestDispatchHistoryStartupFallbackUsesNativeCloudContinuation(t *testing.T) {
	local := &localThenStartupFailure{calls: 1} // fail before any tool work
	cloud := &historyProbeProvider{answer: "The configuration is loaded in config.go:42."}
	svc := New(nil, nil, nil, nil)
	installTestFailureLog(t, svc)
	svc.SetRegistry(historyProbeRegistry())
	eng := dispatch.NewEngine(func() inference.Tiers { return inference.Tiers{Open: local, Cloud: cloud} }, func() locus.Mode { return locus.OpenPrimary }, nil)
	eng.SetModelFor(func(isCloud bool, _ config.Tier) string {
		if isCloud {
			return "claude-sonnet-4-6"
		}
		return "local-model"
	})
	svc.SetEngine(eng)
	res, err := eng.Dispatch(t.Context(), dispatch.Spec{Mode: dispatch.Agentic, RoutingTask: config.TaskChat, Task: "Trace configuration loading.", Tools: []string{"Read", "Grep"}, MaxIterations: 4})
	if err != nil || !res.IsCloud {
		t.Fatalf("result=%+v error=%v", res, err)
	}
	assertHistoryFormat(t, cloud, true)
}

// permStub only describes permissions; this probe actually executes tools.
type historyProbeTool struct{ permStub }

func (historyProbeTool) Execute(context.Context, json.RawMessage) (*agenttools.Result, error) {
	return &agenttools.Result{Type: agenttools.ResultText, Text: "config.go:42 loads configuration"}, nil
}
func historyProbeRegistry() *agenttools.Registry {
	r := agenttools.NewRegistry()
	for _, name := range []string{"Read", "Grep"} {
		r.MustRegister(historyProbeTool{permStub{name, agenttools.PermR}})
	}
	return r
}
