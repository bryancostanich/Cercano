package runner

// Integration regression guards for the inference resilience engine's
// mid-answer LIVE RESTART (see internal/inference/resilience).
//
// Once visible text has streamed to the user, a retryable failure before the
// message completes cannot silently pretend nothing happened: the engine
// restarts the SAME request on the SAME provider once, narrates the
// interruption to the user (in-band EventNotice, surfaced by the runner as a
// progress event BEFORE the retry wait), and guarantees the fresh attempt's
// message_start resets the collector so the persisted response carries only
// the successful attempt's content.
//
// These tests run the full runner stack (RunTurn → agent tool loop →
// resilience-wrapped provider) to prove the end-to-end contract:
//   - an earlier completed tool executes exactly once across the restart;
//   - the interrupted attempt's visible text stays displayed but never
//     contaminates the persisted assistant message, and its fragments
//     never execute nor reach the sink as tools;
//   - only the successful attempt's current tool executes (exactly once),
//     with the interruption notice visible to the client between the
//     interrupted text and the fresh answer;
//   - the retry re-serves the SAME model request and no whole-turn replay
//     (or provider switch) stacks on top of the in-stream restart.

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

// liveRestartProvider scripts a two-tool turn whose second round is
// interrupted mid-answer by a retryable network failure:
//
//	call 1: complete tool call (tool-step-1) — executes once;
//	call 2: visible text ("checking the second file"), then a retryable
//	        network error BEFORE any tool or message_stop — live restart;
//	call 3: the engine's SAME-request restart — visible restart text
//	        ("trying again"), then a complete tool call (tool-step-2-live)
//	        — executes once;
//	call 4+: final end_turn answer.
type liveRestartProvider struct {
	mu       sync.Mutex
	calls    int
	requests []llm.ChatRequest
}

func (p *liveRestartProvider) Name() string { return "openai-responses" }
func (p *liveRestartProvider) Capabilities() inference.Capabilities {
	return inference.Capabilities{SupportsTools: true}
}
func (p *liveRestartProvider) Chat(context.Context, llm.ChatRequest) (llm.ChatResponse, error) {
	panic("streaming test")
}

func (p *liveRestartProvider) StreamChat(_ context.Context, req llm.ChatRequest) (llm.StreamReader, error) {
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
		// Visible text streams to the user, then the transport dies
		// mid-answer: no tool call, no message_stop. The engine must
		// restart the SAME request with an explicit interruption notice.
		return &scriptedStream{
			events: []llm.StreamEvent{
				{Type: llm.EventMessageStart},
				{Type: llm.EventTextDelta, TextDelta: "checking the second file"},
			},
			fail: &llm.Error{Class: llm.ErrNetwork, Provider: "openai-responses", Err: errors.New("connection reset mid-answer")},
		}, nil
	case 3:
		// The restarted attempt re-frames (message_start), so the
		// collector discards the interrupted partial message and the
		// persisted response carries only this attempt's content.
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

// TestLiveRestartMidAnswerRunsToolOnceWithCleanPersistedResponse proves the
// full-stack mid-answer restart contract inside the agent tool loop: the
// earlier completed tool executes once and is never re-run, only the
// restarted attempt's tool executes (exactly once), the client sees the
// interruption notice between the interrupted text and the fresh answer, and
// the persisted assistant response is clean — only the successful attempt's
// text and tool call, never the interrupted fragments.
func TestLiveRestartMidAnswerRunsToolOnceWithCleanPersistedResponse(t *testing.T) {
	primary := &liveRestartProvider{}
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
		ConversationID: "live-restart",
		Input:          "read twice",
		WorkDir:        t.TempDir(),
	}, sink, nil, func(m llm.Message) { persisted = append(persisted, m) })
	if err != nil {
		t.Fatalf("turn should complete after the mid-answer restart, got %v", err)
	}

	// The engine performed exactly one live restart, classified as such:
	// emitted-text recorded, content_emitted gate overcome, same provider.
	var retries []resilience.Event
	for _, ev := range engineEvents {
		if ev.Action == resilience.ActionRetry {
			retries = append(retries, ev)
		}
	}
	if len(retries) != 1 {
		t.Fatalf("engine retry events = %d, want exactly 1: %+v", len(retries), engineEvents)
	}
	r := retries[0]
	if r.Stage != "stream_live" || r.Reason != resilience.ReasonContentEmitted {
		t.Fatalf("retry event stage/reason = %q/%q, want stream_live/content_emitted", r.Stage, r.Reason)
	}
	if !r.Emitted || !r.EmittedText {
		t.Fatalf("retry event must record emitted text: %+v", r)
	}
	if r.From != r.To || r.From != primary.Name() {
		t.Fatalf("retry must stay on the same provider, got %q → %q", r.From, r.To)
	}

	// The earlier completed tool executed exactly once (never re-run by the
	// restart) and only the restarted attempt's current tool executed —
	// the interrupted attempt's round completed no tool at all.
	if got := effects.Load(); got != 2 {
		t.Fatalf("tool executions = %d, want 2 (step-1 and the restarted step-2 only)", got)
	}

	// The restart re-served the SAME model request, and no whole-turn replay
	// or provider switch stacked on top of the in-stream restart.
	if primary.calls != 4 {
		t.Fatalf("provider calls = %d, want 4 (round1, dead round2, restarted round2, final answer)", primary.calls)
	}
	dead, retried := primary.requests[1], primary.requests[2]
	if dead.Model != retried.Model || !reflect.DeepEqual(dead.Messages, retried.Messages) {
		t.Fatalf("restart did not re-serve the same request:\ndead:     model=%q msgs=%d\nrestarted: model=%q msgs=%d",
			dead.Model, len(dead.Messages), retried.Model, len(retried.Messages))
	}

	// Sink: only the two completed tools surface, in order; the interrupted
	// attempt contributed text but no tool indicator; the interruption
	// notice reaches the client between the interrupted text and the
	// fresh answer (it is emitted before the retry wait, so the user sees
	// why the reply restarted while the fresh attempt connects).
	var toolStarts, toolExecs []string
	noticeIdx, interruptedIdx, freshIdx := -1, -1, -1
	var shown strings.Builder
	for i, ev := range sink.events {
		switch ev.Kind {
		case EventToolUseStart:
			toolStarts = append(toolStarts, ev.ToolUseID)
		case EventToolExecStart:
			toolExecs = append(toolExecs, ev.ToolUseID)
		case EventProgress:
			if strings.Contains(ev.Text, "restarting the reply") {
				if noticeIdx != -1 {
					t.Fatalf("restart notice emitted %d times, want 1", noticeIdx+2)
				}
				noticeIdx = i
			}
		case EventToken:
			shown.WriteString(ev.Text)
			if ev.Text == "checking the second file" && interruptedIdx == -1 {
				interruptedIdx = i
			}
			if ev.Text == "trying again" && freshIdx == -1 {
				freshIdx = i
			}
		}
	}
	want := []string{"tool-step-1", "tool-step-2-live"}
	if !reflect.DeepEqual(toolStarts, want) {
		t.Fatalf("sink tool_use_start ids = %v, want %v", toolStarts, want)
	}
	if !reflect.DeepEqual(toolExecs, want) {
		t.Fatalf("sink tool exec ids = %v, want %v", toolExecs, want)
	}
	if noticeIdx == -1 {
		t.Fatal("interruption notice never reached the client sink")
	}
	if interruptedIdx == -1 || freshIdx == -1 {
		t.Fatalf("interrupted/fresh text not streamed to client: %q", shown.String())
	}
	if !(interruptedIdx < noticeIdx && noticeIdx < freshIdx) {
		t.Fatalf("client saw notice out of order: interrupted=%d notice=%d fresh=%d — notice must land between the interrupted text and the fresh answer, before the wait", interruptedIdx, noticeIdx, freshIdx)
	}

	// The persisted assistant message for the restarted round is clean:
	// only the successful attempt's text and tool call. The interrupted
	// text — which stayed visible in the UI — must never be persisted,
	// concatenated, or replayed into history.
	var round2Text strings.Builder
	var round2Tools []string
	for _, m := range persisted {
		var hasFresh bool
		for _, b := range m.Blocks {
			if b.Type == llm.BlockText && strings.Contains(b.Text, "trying again") {
				hasFresh = true
			}
		}
		if !hasFresh {
			continue
		}
		for _, b := range m.Blocks {
			switch b.Type {
			case llm.BlockText:
				round2Text.WriteString(b.Text)
			case llm.BlockToolUse:
				round2Tools = append(round2Tools, b.ToolUseID)
			}
		}
	}
	if round2Text.Len() == 0 {
		t.Fatal("restarted round's assistant message was not persisted")
	}
	if got := round2Text.String(); got != "trying again" {
		t.Fatalf("persisted restarted-round text = %q, want only the successful attempt's text", got)
	}
	if !reflect.DeepEqual(round2Tools, []string{"tool-step-2-live"}) {
		t.Fatalf("persisted restarted-round tools = %v, want [tool-step-2-live] only", round2Tools)
	}
	for _, m := range persisted {
		for _, b := range m.Blocks {
			if b.Type == llm.BlockText && strings.Contains(b.Text, "checking the second file") {
				t.Fatalf("interrupted text leaked into persisted history: %q", b.Text)
			}
		}
	}
}
