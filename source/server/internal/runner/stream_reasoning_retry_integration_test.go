package runner

// Integration regression guards for the inference resilience engine's
// pre-commit REASONING buffering (see internal/inference/resilience).
//
// Reasoning items are pre-commit content: no consumer streams them live
// (CollectStream folds them into response blocks), so the engine can hold
// them alongside tool-call fragments until the stream commits. A retryable
// busy/network failure after reasoning-only output therefore discards the
// interrupted thinking with the dead attempt and re-serves the SAME model
// request under the existing retry policy — without ever concatenating the
// dead attempt's thinking with the retry's output.
//
// These tests run the full runner stack (RunTurn → agent tool loop →
// resilience-wrapped provider) to prove the end-to-end contract:
//   - a mid-stream failure after reasoning-only output retries the same
//     request; the dead attempt's reasoning never executes anything, never
//     reaches the sink, and never enters persisted or model-facing history;
//   - prior completed tools are not re-executed by the retry;
//   - the retried attempt's reasoning and tool execute exactly once and the
//     SUCCESSFUL reasoning stays preserved (persisted and carried forward);
//   - retry exhaustion after reasoning-only output surfaces without leaking
//     the dead thinking anywhere.

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"cercano/source/server/internal/agenttools"
	"cercano/source/server/internal/inference"
	"cercano/source/server/internal/inference/resilience"
	"cercano/source/server/internal/llm"
)

// reasoningRetryProvider scripts a two-tool turn with a reasoning-only dead
// attempt in the middle:
//
//	call 1: complete tool call (tool-step-1) — executes once;
//	call 2: reasoning item (rs-dead) + unfinished tool data, then a
//	        retryable network error — nothing delivered, engine retries;
//	call 3: the engine's SAME-request retry — live reasoning (rs-live),
//	        then a complete tool call (tool-step-2-live) — executes once;
//	call 4+: final end_turn answer.
type reasoningRetryProvider struct {
	mu       sync.Mutex
	calls    int
	requests []llm.ChatRequest
}

func (p *reasoningRetryProvider) Name() string { return "openai-responses" }
func (p *reasoningRetryProvider) Capabilities() inference.Capabilities {
	return inference.Capabilities{SupportsTools: true}
}
func (p *reasoningRetryProvider) Chat(context.Context, llm.ChatRequest) (llm.ChatResponse, error) {
	panic("streaming test")
}

func (p *reasoningRetryProvider) StreamChat(_ context.Context, req llm.ChatRequest) (llm.StreamReader, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls++
	p.requests = append(p.requests, req)
	switch p.calls {
	case 1:
		return &scriptedStream{events: []llm.StreamEvent{
			{Type: llm.EventMessageStart},
			{Type: llm.EventTextDelta, TextDelta: "reading the file"},
			{Type: llm.EventToolUseStart, ToolUseID: "tool-step-1", ToolName: "Read", ToolInputRaw: json.RawMessage(`{}`)},
			{Type: llm.EventToolUseStop},
			{Type: llm.EventMessageStop, StopReason: "tool_use"},
		}}, nil
	case 2:
		// Reasoning plus unfinished tool data, then a retryable network
		// failure: nothing was committed (no answer text), so the engine
		// must discard the interrupted thinking AND the buffered tool
		// fragments, then retry the SAME request.
		return &scriptedStream{
			events: []llm.StreamEvent{
				{Type: llm.EventMessageStart},
				{Type: llm.EventReasoning, ReasoningID: "rs-dead", ReasoningData: "dead-opaque-thinking"},
				{Type: llm.EventToolUseStart, ToolUseID: "tool-step-2-dead", ToolName: "Read"},
				{Type: llm.EventToolUseInputDelta, TextDelta: `{"path":"a`},
			},
			fail: &llm.Error{Class: llm.ErrNetwork, Provider: "openai-responses", Err: errors.New("connection reset mid-thinking")},
		}, nil
	case 3:
		// The retried attempt: its own reasoning and tool call only.
		return &scriptedStream{events: []llm.StreamEvent{
			{Type: llm.EventMessageStart},
			{Type: llm.EventReasoning, ReasoningID: "rs-live", ReasoningData: "live-opaque-thinking"},
			{Type: llm.EventTextDelta, TextDelta: "trying again"},
			{Type: llm.EventToolUseStart, ToolUseID: "tool-step-2-live", ToolName: "Read", ToolInputRaw: json.RawMessage(`{}`)},
			{Type: llm.EventToolUseStop},
			{Type: llm.EventMessageStop, StopReason: "tool_use"},
		}}, nil
	default:
		return &scriptedStream{events: []llm.StreamEvent{
			{Type: llm.EventMessageStart},
			{Type: llm.EventTextDelta, TextDelta: "done"},
			{Type: llm.EventMessageStop, StopReason: "end_turn"},
		}}, nil
	}
}

// TestReasoningOnlyStreamFailureRetriesSameRequestDiscardsDeadThinking proves
// the full-stack contract: interrupted thinking from a failed attempt never
// reaches the caller (sink, persisted history, or the next model request),
// while the successful attempt's reasoning stays fully preserved.
func TestReasoningOnlyStreamFailureRetriesSameRequestDiscardsDeadThinking(t *testing.T) {
	primary := &reasoningRetryProvider{}
	var engineEvents []resilience.Event
	wrapped := resilience.New(primary, resilience.Options{
		RetryWait:    time.Millisecond,
		RetryWaitCap: 5 * time.Millisecond,
		OnEvent:      func(ev resilience.Event) { engineEvents = append(engineEvents, ev) },
	})
	deps := buildDeps(wrapped)
	var effects atomic.Int32
	deps.Tools.Registry().Register(countedRecoveryTool{testTool: testTool{name: "Read", perm: agenttools.PermR}, calls: &effects})
	sink := &captureSink{}
	var persisted []llm.Message

	_, err := New(deps).RunTurn(context.Background(), Request{
		ConversationID: "reasoning-buffer",
		Input:          "read twice",
		WorkDir:        t.TempDir(),
	}, sink, nil, func(m llm.Message) { persisted = append(persisted, m) })
	if err != nil {
		t.Fatalf("turn should complete after the in-stream retry, got %v", err)
	}

	// The engine retried exactly once, recording the dead attempt (which
	// streamed reasoning) as fully undelivered — reasoning is pre-commit
	// content and must not latch the retry gate.
	var retries []resilience.Event
	for _, ev := range engineEvents {
		if ev.Action == resilience.ActionRetry {
			retries = append(retries, ev)
		}
	}
	if len(retries) != 1 {
		t.Fatalf("engine retry events = %d, want exactly 1: %+v", len(retries), engineEvents)
	}
	if retries[0].Emitted || retries[0].EmittedText || retries[0].EmittedReasoning || retries[0].EmittedToolCall {
		t.Fatalf("dead attempt must be recorded as undelivered: %+v", retries[0])
	}

	// No duplicated prior tool execution: the completed round-1 tool ran
	// once, the dead attempt's buffered tool never ran, and the retried
	// attempt's tool ran exactly once.
	if got := effects.Load(); got != 2 {
		t.Fatalf("tool executions = %d, want 2 (dead attempt's tool must not execute)", got)
	}

	// The retry re-served the SAME model request and no whole-turn replay
	// happened on top of the in-stream retry.
	if primary.calls != 4 {
		t.Fatalf("provider calls = %d, want 4 (round1, dead round2, retried round2, final answer)", primary.calls)
	}
	dead, retried := primary.requests[1], primary.requests[2]
	if dead.Model != retried.Model || !reflect.DeepEqual(dead.Messages, retried.Messages) {
		t.Fatalf("retry did not re-serve the same request:\ndead:    model=%q msgs=%d\nretried: model=%q msgs=%d",
			dead.Model, len(dead.Messages), retried.Model, len(retried.Messages))
	}

	// The dead attempt's fragments never reached the sink.
	var toolStarts, toolExecs []string
	var notices []string
	for _, ev := range sink.events {
		switch ev.Kind {
		case EventToolUseStart:
			toolStarts = append(toolStarts, ev.ToolUseID)
		case EventToolExecStart:
			toolExecs = append(toolExecs, ev.ToolUseID)
		case EventProgress:
			if strings.Contains(ev.Text, "trying once more") {
				notices = append(notices, ev.Text)
			}
		}
	}
	want := []string{"tool-step-1", "tool-step-2-live"}
	if !reflect.DeepEqual(toolStarts, want) {
		t.Fatalf("sink tool_use_start ids = %v, want %v (dead attempt's fragment must never surface)", toolStarts, want)
	}
	if !reflect.DeepEqual(toolExecs, want) {
		t.Fatalf("sink tool exec ids = %v, want %v", toolExecs, want)
	}
	if len(notices) != 1 {
		t.Fatalf("retry narration events = %d, want 1: %v", len(notices), notices)
	}

	// Dead reasoning is discarded end-to-end: it never enters persisted
	// history, and it is absent from the model-facing history of every
	// later round. Live reasoning is preserved: it IS persisted and carried
	// forward into the next round's request.
	assertDeadThinkingNowhere(t, persisted, primary.requests)
	var livePersisted bool
	for _, m := range persisted {
		for _, b := range m.Blocks {
			if b.Type == llm.BlockReasoning && b.ReasoningData == "live-opaque-thinking" {
				livePersisted = true
			}
		}
	}
	if !livePersisted {
		t.Error("successful attempt's reasoning was not preserved in persisted history")
	}
	if !containsReasoningData(primary.requests[3], "live-opaque-thinking") {
		t.Error("successful attempt's reasoning was not carried into the next round's model request")
	}
}

// reasoningOnlyDeadProvider always streams a reasoning item and unfinished
// tool data, then dies with a busy error.
type reasoningOnlyDeadProvider struct {
	mu       sync.Mutex
	calls    int
	requests []llm.ChatRequest
}

func (p *reasoningOnlyDeadProvider) Name() string { return "openai-responses" }
func (p *reasoningOnlyDeadProvider) Capabilities() inference.Capabilities {
	return inference.Capabilities{SupportsTools: true}
}
func (p *reasoningOnlyDeadProvider) Chat(context.Context, llm.ChatRequest) (llm.ChatResponse, error) {
	panic("unused")
}

func (p *reasoningOnlyDeadProvider) StreamChat(_ context.Context, req llm.ChatRequest) (llm.StreamReader, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls++
	p.requests = append(p.requests, req)
	return &scriptedStream{
		events: []llm.StreamEvent{
			{Type: llm.EventMessageStart},
			{Type: llm.EventReasoning, ReasoningID: "rs-dead", ReasoningData: "dead-opaque-thinking"},
			{Type: llm.EventToolUseStart, ToolUseID: "tool-dead", ToolName: "Read"},
			{Type: llm.EventToolUseInputDelta, TextDelta: `{"path":"a`},
		},
		fail: &llm.Error{Class: llm.ErrBusy, Provider: "openai-responses", Err: errors.New("still busy")},
	}, nil
}

// TestReasoningOnlyStreamRetryExhaustionSurfacesWithoutLeakingThinking pins
// exhaustion: the busy error surfaces, no tool ever executes, and the dead
// attempts' reasoning reaches nowhere — not the sink, not persisted history,
// not any later model request.
func TestReasoningOnlyStreamRetryExhaustionSurfacesWithoutLeakingThinking(t *testing.T) {
	primary := &reasoningOnlyDeadProvider{}
	wrapped := resilience.New(primary, resilience.Options{
		RetryWait:    time.Millisecond,
		RetryWaitCap: 5 * time.Millisecond,
	})
	deps := buildDeps(wrapped)
	var effects atomic.Int32
	deps.Tools.Registry().Register(countedRecoveryTool{testTool: testTool{name: "Read", perm: agenttools.PermR}, calls: &effects})
	sink := &captureSink{}
	var persisted []llm.Message

	_, err := New(deps).RunTurn(context.Background(), Request{
		ConversationID: "reasoning-exhaust",
		Input:          "read",
		WorkDir:        t.TempDir(),
	}, sink, nil, func(m llm.Message) { persisted = append(persisted, m) })
	if err == nil {
		t.Fatal("exhausted retry must surface the busy error")
	}
	if llm.ClassOf(err) != llm.ErrBusy {
		t.Fatalf("surfaced error class = %s, want busy", llm.ClassOf(err))
	}
	if got := effects.Load(); got != 0 {
		t.Fatalf("buffered tool executed %d times on failed attempts, want 0", got)
	}
	// Call accounting under the EXISTING policy: each provider attempt gets
	// one in-stream busy retry, and the runner's whole-turn retry may re-run
	// the turn because NOTHING was delivered — 2 stream attempts × (turn +
	// turn-retry) = 4 calls.
	if primary.calls != 4 {
		t.Fatalf("provider calls = %d, want 4 (2 per turn attempt × turn + whole-turn retry)", primary.calls)
	}
	for _, ev := range sink.events {
		if ev.Kind == EventToolUseStart || ev.Kind == EventToolExecStart {
			t.Fatalf("failed attempt's tool fragment leaked to sink: %+v", ev)
		}
	}
	assertDeadThinkingNowhere(t, persisted, primary.requests)
}

// assertDeadThinkingNowhere fails if the dead attempts' opaque reasoning
// payload appears in any persisted message or any model request — proving
// interrupted thinking is discarded, never concatenated with retries.
func assertDeadThinkingNowhere(t *testing.T, persisted []llm.Message, requests []llm.ChatRequest) {
	t.Helper()
	for _, m := range persisted {
		for _, b := range m.Blocks {
			if b.Type == llm.BlockReasoning && b.ReasoningData == "dead-opaque-thinking" {
				t.Fatalf("dead attempt's reasoning leaked into persisted history: %+v", b)
			}
		}
	}
	for i, req := range requests {
		if containsReasoningData(req, "dead-opaque-thinking") {
			t.Fatalf("dead attempt's reasoning leaked into model request %d", i)
		}
	}
}

func containsReasoningData(req llm.ChatRequest, data string) bool {
	for _, m := range req.Messages {
		for _, b := range m.Blocks {
			if b.Type == llm.BlockReasoning && b.ReasoningData == data {
				return true
			}
		}
	}
	return false
}
