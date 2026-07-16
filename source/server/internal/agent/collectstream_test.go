package agent

import (
	"context"
	"encoding/json"
	"testing"

	"cercano/source/server/internal/llm"
)

// fakeReader yields a fixed event sequence, then EOF.
type fakeReader struct {
	events []llm.StreamEvent
	i      int
}

func (r *fakeReader) Next() (llm.StreamEvent, bool, error) {
	if r.i >= len(r.events) {
		return llm.StreamEvent{}, false, nil
	}
	ev := r.events[r.i]
	r.i++
	return ev, true, nil
}
func (r *fakeReader) Close() error { return nil }

func TestCollectStream_ForwardsTextDeltas(t *testing.T) {
	rdr := &fakeReader{events: []llm.StreamEvent{
		{Type: llm.EventMessageStart, InputTokens: 1},
		{Type: llm.EventTextDelta, TextDelta: "Hel"},
		{Type: llm.EventTextDelta, TextDelta: "lo"},
		{Type: llm.EventMessageStop, OutputTokens: 2},
	}}
	var got []string
	if _, err := collectStream(context.Background(), rdr, func(s string) { got = append(got, s) }, nil); err != nil {
		t.Fatalf("collectStream: %v", err)
	}
	if len(got) != 2 || got[0] != "Hel" || got[1] != "lo" {
		t.Errorf("onText deltas = %v, want [Hel lo]", got)
	}
}

func TestCollectStream_CapturesUsage(t *testing.T) {
	rdr := &fakeReader{events: []llm.StreamEvent{
		{Type: llm.EventMessageStart, InputTokens: 1234},
		{Type: llm.EventTextDelta, TextDelta: "hi"},
		{Type: llm.EventMessageStop, StopReason: "end_turn", OutputTokens: 56},
	}}
	resp, err := collectStream(context.Background(), rdr, nil, nil)
	if err != nil {
		t.Fatalf("collectStream: %v", err)
	}
	if resp.InputTokens != 1234 {
		t.Errorf("InputTokens = %d, want 1234", resp.InputTokens)
	}
	if resp.OutputTokens != 56 {
		t.Errorf("OutputTokens = %d, want 56", resp.OutputTokens)
	}
}

func TestCollectStream_TrailingStopDoesNotClobberUsage(t *testing.T) {
	rdr := &fakeReader{events: []llm.StreamEvent{
		{Type: llm.EventMessageStart, InputTokens: 1234},
		{Type: llm.EventTextDelta, TextDelta: "hi"},
		{Type: llm.EventMessageStop, StopReason: "end_turn", OutputTokens: 56}, // from message_delta
		{Type: llm.EventMessageStop},                                           // trailing message_stop, zero usage
	}}
	resp, err := collectStream(context.Background(), rdr, nil, nil)
	if err != nil {
		t.Fatalf("collectStream: %v", err)
	}
	if resp.InputTokens != 1234 {
		t.Errorf("InputTokens = %d, want 1234", resp.InputTokens)
	}
	if resp.OutputTokens != 56 {
		t.Errorf("OutputTokens = %d, want 56 (trailing stop must not clobber)", resp.OutputTokens)
	}
}

func TestCollectStream_SeedsWholeInputFromStartEvent(t *testing.T) {
	// Ollama delivers tool_calls complete on the start event (ToolInputRaw),
	// not as input-delta fragments. The collector must not drop them.
	rdr := &fakeReader{events: []llm.StreamEvent{
		{Type: llm.EventMessageStart},
		{Type: llm.EventToolUseStart, ToolUseID: "t1", ToolName: "Bash", ToolInputRaw: json.RawMessage(`{"cmd":["ls","-la"]}`)},
		{Type: llm.EventToolUseStop},
		{Type: llm.EventMessageStop},
	}}
	resp, err := collectStream(context.Background(), rdr, nil, nil)
	if err != nil {
		t.Fatalf("collectStream: %v", err)
	}
	if len(resp.Blocks) != 1 || resp.Blocks[0].Type != llm.BlockToolUse {
		t.Fatalf("blocks = %+v, want one tool_use", resp.Blocks)
	}
	if got := string(resp.Blocks[0].ToolInput); got != `{"cmd":["ls","-la"]}` {
		t.Errorf("ToolInput = %s, want the whole-input payload preserved", got)
	}
}

func TestCollectStream_WrapsInvalidToolInput(t *testing.T) {
	// The unquoted-value shape observed 2026-07-06 (proxy re-serialization
	// bug): raw bytes would fail RawMessage marshaling and corrupt every
	// later request. They must come out wrapped in the valid envelope.
	bad := `{"cmd": find /Users -name "*.go"}`
	rdr := &fakeReader{events: []llm.StreamEvent{
		{Type: llm.EventMessageStart},
		{Type: llm.EventToolUseStart, ToolUseID: "t1", ToolName: "Bash"},
		{Type: llm.EventToolUseInputDelta, TextDelta: bad[:12]},
		{Type: llm.EventToolUseInputDelta, TextDelta: bad[12:]},
		{Type: llm.EventToolUseStop},
		{Type: llm.EventMessageStop},
	}}
	resp, err := collectStream(context.Background(), rdr, nil, nil)
	if err != nil {
		t.Fatalf("collectStream: %v", err)
	}
	if len(resp.Blocks) != 1 {
		t.Fatalf("blocks = %+v, want one tool_use", resp.Blocks)
	}
	input := resp.Blocks[0].ToolInput
	if !json.Valid(input) {
		t.Fatalf("ToolInput must always be valid JSON, got: %s", input)
	}
	raw, ok := llm.MalformedToolInput(input)
	if !ok {
		t.Fatalf("want malformed-input envelope, got: %s", input)
	}
	if raw != bad {
		t.Errorf("envelope raw = %q, want the original bytes %q", raw, bad)
	}
}

func TestCollectStream_TruncatedInputWrapped(t *testing.T) {
	// The truncation shape: stream cut mid-argument.
	bad := `{"cmd": ["/bin/zsh", "-c", "cd /`
	rdr := &fakeReader{events: []llm.StreamEvent{
		{Type: llm.EventMessageStart},
		{Type: llm.EventToolUseStart, ToolUseID: "t1", ToolName: "Bash"},
		{Type: llm.EventToolUseInputDelta, TextDelta: bad},
		{Type: llm.EventToolUseStop},
		{Type: llm.EventMessageStop},
	}}
	resp, err := collectStream(context.Background(), rdr, nil, nil)
	if err != nil {
		t.Fatalf("collectStream: %v", err)
	}
	raw, ok := llm.MalformedToolInput(resp.Blocks[0].ToolInput)
	if !ok || raw != bad {
		t.Fatalf("want envelope carrying %q, got ok=%v raw=%q", bad, ok, raw)
	}
}

func TestCollectStream_ValidDeltasUntouched(t *testing.T) {
	// Well-formed input must pass through byte-identical — no envelope.
	good := `{"cmd":["/bin/zsh","-c","echo hi"]}`
	rdr := &fakeReader{events: []llm.StreamEvent{
		{Type: llm.EventMessageStart},
		{Type: llm.EventToolUseStart, ToolUseID: "t1", ToolName: "Bash"},
		{Type: llm.EventToolUseInputDelta, TextDelta: good[:10]},
		{Type: llm.EventToolUseInputDelta, TextDelta: good[10:]},
		{Type: llm.EventToolUseStop},
		{Type: llm.EventMessageStop},
	}}
	resp, err := collectStream(context.Background(), rdr, nil, nil)
	if err != nil {
		t.Fatalf("collectStream: %v", err)
	}
	if got := string(resp.Blocks[0].ToolInput); got != good {
		t.Errorf("ToolInput = %s, want byte-identical passthrough", got)
	}
	if _, ok := llm.MalformedToolInput(resp.Blocks[0].ToolInput); ok {
		t.Error("valid input must not be flagged as the malformed envelope")
	}
}
