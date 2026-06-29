package dispatch

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"cercano/source/server/internal/capabilities"
	"cercano/source/server/internal/engine"
)

type scriptedEngine struct {
	responses []engine.ChatResponse
	errs      []error
	calls     []engine.ChatRequest
}

func (s *scriptedEngine) Complete(context.Context, string, string, string) (engine.CompletionResult, error) {
	return engine.CompletionResult{}, errors.New("not used")
}
func (s *scriptedEngine) CompleteStream(context.Context, string, string, string, func(string)) (engine.CompletionResult, error) {
	return engine.CompletionResult{}, errors.New("not used")
}
func (s *scriptedEngine) ListModels(context.Context) ([]engine.ModelInfo, error) {
	return nil, errors.New("not used")
}
func (s *scriptedEngine) Name() string { return "scripted" }

func (s *scriptedEngine) ChatWithTools(_ context.Context, req engine.ChatRequest) (engine.ChatResponse, error) {
	s.calls = append(s.calls, req)
	if len(s.responses) == 0 {
		return engine.ChatResponse{}, errors.New("no scripted response remaining")
	}
	r := s.responses[0]
	s.responses = s.responses[1:]
	var e error
	if len(s.errs) > 0 {
		e = s.errs[0]
		s.errs = s.errs[1:]
	}
	return r, e
}

// echoCapability is a test capability that echoes its raw args back.
type echoCapability struct{}

func (echoCapability) Name() string        { return "echo" }
func (echoCapability) Description() string { return "echo arg" }
func (echoCapability) Tier() capabilities.Tier { return capabilities.TierR }
func (echoCapability) Schema() capabilities.Schema {
	return json.RawMessage(`{"type":"object"}`)
}
func (echoCapability) Surfaces() capabilities.Surface { return capabilities.SurfaceAgent }
func (echoCapability) Execute(_ context.Context, call *capabilities.Call) (*capabilities.Result, error) {
	return capabilities.NewTextResult("echoed:" + string(call.Args)), nil
}

// erroringCapability always returns an error.
type erroringCapability struct{}

func (erroringCapability) Name() string        { return "bad" }
func (erroringCapability) Description() string { return "" }
func (erroringCapability) Tier() capabilities.Tier { return capabilities.TierR }
func (erroringCapability) Schema() capabilities.Schema {
	return json.RawMessage(`{"type":"object"}`)
}
func (erroringCapability) Surfaces() capabilities.Surface { return capabilities.SurfaceAgent }
func (erroringCapability) Execute(_ context.Context, _ *capabilities.Call) (*capabilities.Result, error) {
	return nil, errors.New("boom")
}

func collectEvents(ch <-chan Event) []Event {
	var out []Event
	for ev := range ch {
		out = append(out, ev)
	}
	return out
}

func newCapRegistry(caps ...capabilities.Capability) *capabilities.Registry {
	r := capabilities.NewRegistry(capabilities.Services{})
	for _, c := range caps {
		_ = r.Register(c)
	}
	return r
}

func TestLoop_PlainTextResponse(t *testing.T) {
	eng := &scriptedEngine{responses: []engine.ChatResponse{{Content: "hello world"}}}
	loop := NewLoop(eng, newCapRegistry(), []string{}, "qwen3-coder", 50)
	ch, _ := loop.Run(context.Background(), nil, "hi")
	events := collectEvents(ch)
	if len(events) != 2 {
		t.Fatalf("got %d events, want 2 (TextChunk + Done): %+v", len(events), events)
	}
	if events[0].Kind != EventTextChunk || events[0].Text != "hello world" {
		t.Errorf("event[0] = %+v", events[0])
	}
	if events[1].Kind != EventDone || events[1].Cancelled || events[1].DoneError != "" {
		t.Errorf("event[1] = %+v", events[1])
	}
}

func TestLoop_SingleToolCallThenText(t *testing.T) {
	eng := &scriptedEngine{responses: []engine.ChatResponse{
		{ToolCalls: []engine.ToolCall{
			{ID: "tc_1", Function: engine.ToolCallFunc{Name: "echo", Arguments: json.RawMessage(`{"a":1}`)}},
		}},
		{Content: "done"},
	}}
	loop := NewLoop(eng, newCapRegistry(echoCapability{}), []string{"echo"}, "qwen3-coder", 50)
	ch, _ := loop.Run(context.Background(), nil, "do it")
	events := collectEvents(ch)
	if len(events) != 4 {
		t.Fatalf("got %d events, want 4: %+v", len(events), events)
	}
	if events[0].Kind != EventToolCall || events[0].ToolName != "echo" {
		t.Errorf("event[0] = %+v", events[0])
	}
	if events[1].Kind != EventToolResult || !events[1].ToolOK || !strings.Contains(events[1].ToolResult, "echoed:") {
		t.Errorf("event[1] = %+v", events[1])
	}
	if events[2].Kind != EventTextChunk || events[2].Text != "done" {
		t.Errorf("event[2] = %+v", events[2])
	}
	if events[3].Kind != EventDone {
		t.Errorf("event[3] = %+v", events[3])
	}
}

func TestLoop_ToolErrorFedBackContinues(t *testing.T) {
	eng := &scriptedEngine{responses: []engine.ChatResponse{
		{ToolCalls: []engine.ToolCall{
			{ID: "tc_1", Function: engine.ToolCallFunc{Name: "bad", Arguments: json.RawMessage(`{}`)}},
		}},
		{Content: "oh well"},
	}}
	loop := NewLoop(eng, newCapRegistry(erroringCapability{}), []string{"bad"}, "x", 50)
	ch, _ := loop.Run(context.Background(), nil, "try it")
	events := collectEvents(ch)
	var foundFail bool
	for _, e := range events {
		if e.Kind == EventToolResult && !e.ToolOK && strings.Contains(e.ToolResult, "boom") {
			foundFail = true
		}
	}
	if !foundFail {
		t.Errorf("expected ToolResult ok=false with 'boom', got %+v", events)
	}
}

func TestLoop_UnknownToolFedBack(t *testing.T) {
	eng := &scriptedEngine{responses: []engine.ChatResponse{
		{ToolCalls: []engine.ToolCall{
			{ID: "tc_1", Function: engine.ToolCallFunc{Name: "nope", Arguments: json.RawMessage(`{}`)}},
		}},
		{Content: "ok"},
	}}
	loop := NewLoop(eng, newCapRegistry(), []string{}, "x", 50)
	ch, _ := loop.Run(context.Background(), nil, "go")
	events := collectEvents(ch)
	var found bool
	for _, e := range events {
		if e.Kind == EventToolResult && !e.ToolOK && strings.Contains(e.ToolResult, "not registered") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected ToolResult ok=false mentioning 'not registered', got %+v", events)
	}
}

func TestLoop_InvalidJSONFedBack(t *testing.T) {
	eng := &scriptedEngine{responses: []engine.ChatResponse{
		{ToolCalls: []engine.ToolCall{
			{ID: "tc_1", Function: engine.ToolCallFunc{Name: "echo", Arguments: json.RawMessage(`{bad`)}},
		}},
		{Content: "k"},
	}}
	loop := NewLoop(eng, newCapRegistry(echoCapability{}), []string{"echo"}, "x", 50)
	ch, _ := loop.Run(context.Background(), nil, "")
	events := collectEvents(ch)
	var foundInvalidJSON bool
	for _, e := range events {
		if e.Kind == EventToolResult && !e.ToolOK && strings.Contains(e.ToolResult, "not valid JSON") {
			foundInvalidJSON = true
		}
	}
	if !foundInvalidJSON {
		t.Errorf("expected ToolResult ok=false with 'not valid JSON', got %+v", events)
	}
}

func TestLoop_Cancellation(t *testing.T) {
	eng := &scriptedEngine{responses: []engine.ChatResponse{
		{ToolCalls: []engine.ToolCall{
			{ID: "tc_1", Function: engine.ToolCallFunc{Name: "echo", Arguments: json.RawMessage(`{}`)}},
		}},
		{Content: "should not happen"},
	}}
	ctx, cancel := context.WithCancel(context.Background())
	loop := NewLoop(eng, newCapRegistry(echoCapability{}), []string{"echo"}, "x", 50)
	ch, _ := loop.Run(ctx, nil, "go")

	first := <-ch
	if first.Kind != EventToolCall {
		t.Fatalf("first event kind = %s, want tool_call", first.Kind)
	}
	cancel()
	remaining := collectEvents(ch)
	last := remaining[len(remaining)-1]
	if last.Kind != EventDone || !last.Cancelled {
		t.Errorf("last event = %+v, want Done{cancelled:true}", last)
	}
	time.Sleep(20 * time.Millisecond)
	if len(eng.calls) > 1 {
		t.Errorf("engine called %d times after cancel, want 1", len(eng.calls))
	}
}

func TestLoop_MaxTurnsCap(t *testing.T) {
	resp := []engine.ChatResponse{}
	for i := 0; i < 100; i++ {
		resp = append(resp, engine.ChatResponse{ToolCalls: []engine.ToolCall{
			{ID: "x", Function: engine.ToolCallFunc{Name: "echo", Arguments: json.RawMessage(`{}`)}},
		}})
	}
	eng := &scriptedEngine{responses: resp}
	loop := NewLoop(eng, newCapRegistry(echoCapability{}), []string{"echo"}, "x", 50)
	ch, _ := loop.Run(context.Background(), nil, "go")
	events := collectEvents(ch)
	last := events[len(events)-1]
	if last.Kind != EventDone || !strings.Contains(last.DoneError, "exceeded max turns") {
		t.Errorf("last event = %+v, want Done{error: exceeded max turns}", last)
	}
}

func TestLoop_HistoryAccumulates(t *testing.T) {
	eng := &scriptedEngine{responses: []engine.ChatResponse{
		{ToolCalls: []engine.ToolCall{
			{ID: "tc_1", Function: engine.ToolCallFunc{Name: "echo", Arguments: json.RawMessage(`{}`)}},
		}},
		{Content: "done"},
	}}
	loop := NewLoop(eng, newCapRegistry(echoCapability{}), []string{"echo"}, "x", 50)
	ch, finalHist := loop.Run(context.Background(), nil, "go")
	collectEvents(ch)
	hist := finalHist()
	if len(hist) != 4 {
		t.Fatalf("history len = %d, want 4: %+v", len(hist), hist)
	}
	if hist[0].Role != "user" || hist[1].Role != "assistant" || hist[2].Role != "tool" || hist[3].Role != "assistant" {
		t.Errorf("roles = %s/%s/%s/%s", hist[0].Role, hist[1].Role, hist[2].Role, hist[3].Role)
	}
	if len(hist[1].ToolCalls) != 1 {
		t.Errorf("assistant tool_calls = %+v", hist[1])
	}
}
