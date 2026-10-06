package runner

// Integration regression guards for the inference resilience engine's
// pre-commit tool-call buffering (see internal/inference/resilience).
//
// A tool-call stream has not delivered anything the model committed to until
// the response completes: the engine buffers tool_use start/input_delta/stop
// events while the stream is uncommitted, so a retryable busy/network failure
// arriving after tool-only output can discard the fragments and re-serve the
// SAME model request under the existing retry policy. Text and reasoning
// deltas remain delivered content: they still latch the stream live and close
// the retry gate.
//
// These tests run the full runner stack (RunTurn → agent tool loop →
// resilience-wrapped provider) to prove the end-to-end contract:
//   - a mid-stream failure after tool-only output retries the same request
//     and the dead attempt's tool never executes (nor reaches the sink);
//   - prior completed tools are not re-executed by the retry;
//   - the retried attempt's tool executes exactly once;
//   - the runner's whole-turn replay gate stays closed (no turn-level
//     rerun happens on top of the in-stream retry).

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

// scriptedStream yields its events in order, then either fails with err
// (mid-stream or at end, before a clean close) or ends cleanly.
type scriptedStream struct {
	events []llm.StreamEvent
	fail   error // returned once, after events are exhausted
}

func (s *scriptedStream) Next() (llm.StreamEvent, bool, error) {
	if len(s.events) > 0 {
		e := s.events[0]
		s.events = s.events[1:]
		return e, true, nil
	}
	if s.fail != nil {
		err := s.fail
		s.fail = nil
		return llm.StreamEvent{}, false, err
	}
	return llm.StreamEvent{}, false, nil
}

func (s *scriptedStream) Close() error { return nil }

// toolRetryProvider scripts a two-tool turn:
//   call 1: complete tool call (tool-step-1) — executes once;
//   call 2: tool-only output (tool-step-2-dead), then dies mid-stream with a
//           retryable busy error before any text/reasoning;
//   call 3: the resilience engine's SAME-request retry — complete tool call
//           (tool-step-2-live) — executes once;
//   call 4+: final end_turn answer.
type toolRetryProvider struct {
	mu       sync.Mutex
	calls    int
	requests []llm.ChatRequest
}

func (p *toolRetryProvider) Name() string { return "openai-responses" }
func (p *toolRetryProvider) Capabilities() inference.Capabilities {
	return inference.Capabilities{SupportsTools: true}
}
func (p *toolRetryProvider) Chat(context.Context, llm.ChatRequest) (llm.ChatResponse, error) {
	panic("streaming test")
}

func (p *toolRetryProvider) StreamChat(_ context.Context, req llm.ChatRequest) (llm.StreamReader, error) {
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
		// Tool-only output, then a retryable busy failure mid-stream: no text
		// or reasoning was delivered, so the engine must discard the buffered
		// tool fragments and retry the SAME request.
		return &scriptedStream{
			events: []llm.StreamEvent{
				{Type: llm.EventMessageStart},
				{Type: llm.EventToolUseStart, ToolUseID: "tool-step-2-dead", ToolName: "Read"},
				{Type: llm.EventToolUseInputDelta, TextDelta: `{"path":"a`},
			},
			fail: &llm.Error{Class: llm.ErrBusy, Provider: "openai-responses", Err: errors.New("connection reset mid-tool-call")},
		}, nil
	case 3:
		// The retried attempt replays the same tool call — and only its own
		// events may reach the caller.
		return &scriptedStream{events: []llm.StreamEvent{
			{Type: llm.EventMessageStart},
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

// TestToolOnlyStreamFailureRetriesSameRequestAndExecutesToolOnce proves the
// full-stack contract for a mid-stream retryable failure after tool-only
// output: the buffered dead-attempt fragments never execute nor reach the
// sink, the SAME model request is retried, prior completed tools are not
// re-run, and the retried tool executes exactly once.
func TestToolOnlyStreamFailureRetriesSameRequestAndExecutesToolOnce(t *testing.T) {
	primary := &toolRetryProvider{}
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

	_, err := New(deps).RunTurn(context.Background(), Request{
		ConversationID: "tool-buffer",
		Input:          "read twice",
		WorkDir:        t.TempDir(),
	}, sink, nil, nil)
	if err != nil {
		t.Fatalf("turn should complete after the in-stream retry, got %v", err)
	}

	// The engine retried exactly once, at the pre-content stage, with no
	// content delivered by the dead attempt.
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

	// Prior completed tools were not re-run, and the retried attempt's tool
	// executed exactly once — the dead attempt's tool never executed.
	if got := effects.Load(); got != 2 {
		t.Fatalf("tool executions = %d, want 2 (one per completed model round; dead attempt must not execute)", got)
	}

	// The retry re-served the SAME model request: identical messages, model,
	// and tool catalog. No whole-turn replay: only call 2 was repeated.
	if primary.calls != 4 {
		t.Fatalf("provider calls = %d, want 4 (round1, dead round2, retried round2, final answer)", primary.calls)
	}
	dead, retried := primary.requests[1], primary.requests[2]
	if dead.Model != retried.Model || !reflect.DeepEqual(dead.Messages, retried.Messages) {
		t.Fatalf("retry did not re-serve the same request:\ndead:    model=%q msgs=%d\nretried: model=%q msgs=%d",
			dead.Model, len(dead.Messages), retried.Model, len(retried.Messages))
	}

	// The dead attempt's tool fragment never reached the sink: only the
	// completed rounds' tool calls appear, in order.
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
}

// TestToolOnlyStreamFailureWithoutRetryGateSurfacesCleanly pins the negative:
// when the retry gate is closed by exhaustion (the retry already used), a
// second tool-only failure surfaces to the caller WITHOUT executing the
// buffered tool or leaking its fragment to the sink — the turn fails rather
// than partially running work.
type oneRetryThenDeadProvider struct {
	mu    sync.Mutex
	calls int
}

func (p *oneRetryThenDeadProvider) Name() string { return "openai-responses" }
func (p *oneRetryThenDeadProvider) Capabilities() inference.Capabilities {
	return inference.Capabilities{SupportsTools: true}
}
func (p *oneRetryThenDeadProvider) Chat(context.Context, llm.ChatRequest) (llm.ChatResponse, error) {
	panic("streaming test")
}
func (p *oneRetryThenDeadProvider) StreamChat(_ context.Context, _ llm.ChatRequest) (llm.StreamReader, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls++
	return &scriptedStream{
		events: []llm.StreamEvent{
			{Type: llm.EventMessageStart},
			{Type: llm.EventToolUseStart, ToolUseID: "tool-dead-" + string(rune('a'+p.calls)), ToolName: "Read"},
			{Type: llm.EventToolUseInputDelta, TextDelta: `{"path":"a`},
		},
		fail: &llm.Error{Class: llm.ErrBusy, Provider: "openai-responses", Err: errors.New("still busy")},
	}, nil
}

func TestToolOnlyStreamRetryExhaustionSurfacesWithoutExecutingTool(t *testing.T) {
	primary := &oneRetryThenDeadProvider{}
	wrapped := resilience.New(primary, resilience.Options{
		RetryWait:    time.Millisecond,
		RetryWaitCap: 5 * time.Millisecond,
	})
	deps := buildDeps(wrapped)
	var effects atomic.Int32
	deps.Tools.Registry().Register(countedRecoveryTool{testTool: testTool{name: "Read", perm: agenttools.PermR}, calls: &effects})
	sink := &captureSink{}

	_, err := New(deps).RunTurn(context.Background(), Request{
		ConversationID: "tool-exhaust",
		Input:          "read",
		WorkDir:        t.TempDir(),
	}, sink, nil, nil)
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
	// one in-stream busy retry (resilience engine), and the runner's whole-
	// turn same-provider retry may then re-run the turn once because NOTHING
	// was delivered (replay gate closed — no text, no tool execution). That
	// runner-level retry is pre-existing policy for undelivered turns, not a
	// loosening: 2 stream attempts × (turn + turn-retry) = 4 calls.
	if primary.calls != 4 {
		t.Fatalf("provider calls = %d, want 4 (2 per turn attempt × turn + whole-turn retry)", primary.calls)
	}
	for _, ev := range sink.events {
		if ev.Kind == EventToolUseStart || ev.Kind == EventToolExecStart {
			t.Fatalf("failed attempt's tool fragment leaked to sink: %+v", ev)
		}
	}
}
