package agent

import (
	"cercano/source/server/internal/agenttools"
	"cercano/source/server/internal/llm"
	"context"
	"fmt"
	"strings"
	"testing"
)

func TestProgressNoticesRequireRepeatedUnchangedEvidence(t *testing.T) {
	p := &executionProgress{detect: true}
	call := llm.Block{ToolName: "Read", ToolInput: []byte(`{"path":"f","start":1}`)}
	result := llm.Block{Content: "evidence"}
	for i := 0; i < 6; i++ {
		p.observe(call, result)
		if p.notice() != "" {
			t.Fatal("premature notice")
		}
	}
	call.ToolInput = []byte(`{"start":1,"path":"f"}`)
	p.observe(call, result)
	if p.notice() == "" || p.notice() != "" {
		t.Fatal("notice should fire once per stagnant episode")
	}
	result.Content = "changed"
	p.observe(call, result)
	if p.notice() != "" {
		t.Fatal("changed evidence flagged")
	}
	for i := 0; i < 30; i++ {
		call.ToolInput = []byte(fmt.Sprintf(`{"path":"new-%d"}`, i))
		p.observe(call, result)
		if p.notice() != "" {
			t.Fatal("novel read-only work flagged")
		}
	}
}
func TestProgressMutationAndInstanceIsolation(t *testing.T) {
	p := &executionProgress{detect: true}
	call := llm.Block{ToolName: "Read", ToolInput: []byte(`{"path":"f"}`)}
	result := llm.Block{Content: "same"}
	for i := 0; i < 8; i++ {
		p.observe(call, result)
	}
	p.observe(llm.Block{ToolName: "RunCommand", ToolInput: []byte(`{"cmd":["test"]}`)}, result)
	p.observe(call, result)
	if p.notice() != "" {
		t.Fatal("did not reset after potential mutation")
	}
	other := &executionProgress{detect: true}
	other.observe(call, result)
	if other.notice() != "" {
		t.Fatal("state leaked")
	}
	plain := &executionProgress{}
	for i := 0; i < 30; i++ {
		plain.observe(call, result)
	}
	if plain.notice() != "" {
		t.Fatal("main loop unexpectedly monitored")
	}
}
func TestPartialHandoffContainsOnlyObservedWork(t *testing.T) {
	p := &executionProgress{}
	p.observe(llm.Block{ToolName: "Edit", ToolInput: []byte(`{"path":"f","new_string":"SECRET-BODY"}`)}, llm.Block{})
	p.observe(llm.Block{ToolName: "RunCommand", ToolInput: []byte(`{"cmd":["go","test","./x"]}`)}, llm.Block{Content: "PRIVATE-OUTPUT", IsError: true})
	h := p.handoff([]llm.Block{{ToolName: "Write"}})
	for _, want := range []string{"task NOT completed", "2 completed", "1 reported errors", "Edit", "go test ./x", "Write", "not executed"} {
		if !strings.Contains(h, want) {
			t.Fatalf("missing %q: %s", want, h)
		}
	}
	if strings.Contains(h, "SECRET-BODY") || strings.Contains(h, "PRIVATE-OUTPUT") {
		t.Fatal("unnecessary content leaked")
	}
	for i := 0; i < 1000; i++ {
		p.observe(llm.Block{ToolName: "Edit", ToolInput: []byte(`{"path":"f"}`)}, llm.Block{})
	}
	if len(p.handoff(nil)) > 5000 {
		t.Fatal("unbounded handoff")
	}
}

type repeatReadTool struct{ countingProbeTool }

func (repeatReadTool) Name() string { return "Read" }

type repeatReadProvider struct {
	budgetProbeProvider
	requests []llm.ChatRequest
}

func (p *repeatReadProvider) StreamChat(_ context.Context, r llm.ChatRequest) (llm.StreamReader, error) {
	p.requests = append(p.requests, r)
	blocks := []llm.Block{{Type: llm.BlockToolUse, ToolName: "Read", ToolUseID: fmt.Sprint(len(p.requests)), ToolInput: []byte(`{}`)}}
	if len(p.requests) == 9 {
		blocks = []llm.Block{{Type: llm.BlockText, Text: "Specific blocker: need guidance."}}
	}
	return &scriptedStream{events: blocksToEvents(blocks)}, nil
}
func TestLoopSteersRepeatedReadsWithoutAborting(t *testing.T) {
	for _, flatten := range []bool{false, true} {
		p := &repeatReadProvider{}
		executed := 0
		r := agenttools.NewRegistry()
		r.MustRegister(repeatReadTool{countingProbeTool{executed: &executed}})
		notices := 0
		_, err := RunToolLoop(t.Context(), ToolLoopInput{Provider: p, Registry: r, UserInput: "inspect", MaxIterations: 10, DetectNonProgress: true, FlattenToolResults: flatten, EventSink: func(e LoopEvent) {
			if e.Kind == LoopNotice && strings.Contains(e.Summary, "dispatch progress check") {
				notices++
			}
		}})
		if err != nil {
			t.Fatal(err)
		}
		if notices != 1 || executed != 8 {
			t.Fatalf("flatten=%v notices=%d executions=%d", flatten, notices, executed)
		}
		found := false
		for _, m := range p.requests[7].Messages {
			for _, b := range m.Blocks {
				if strings.Contains(b.Text, "dispatch progress check") {
					found = true
				}
			}
		}
		if !found {
			t.Fatal("model did not receive the steering notice")
		}
	}
}
