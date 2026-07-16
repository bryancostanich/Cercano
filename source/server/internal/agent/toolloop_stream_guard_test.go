package agent

import (
	"context"
	"strings"
	"testing"

	"cercano/source/server/internal/llm"
)

// scriptedStreamReader replays a fixed event sequence as an llm.StreamReader.
type scriptedStreamReader struct {
	events []llm.StreamEvent
	i      int
}

func (r *scriptedStreamReader) Next() (llm.StreamEvent, bool, error) {
	if r.i >= len(r.events) {
		return llm.StreamEvent{}, false, nil
	}
	ev := r.events[r.i]
	r.i++
	return ev, true, nil
}

func (r *scriptedStreamReader) Close() error { return nil }

func textOf(blocks []llm.Block) string {
	var b strings.Builder
	for _, blk := range blocks {
		if blk.Type == llm.BlockText {
			b.WriteString(blk.Text)
		}
	}
	return b.String()
}

// Regression for the 2026-07-04 message-tear incident
// (docs/bugs/2026-07-04-user-message-tear.md): after an agent+proxy restart,
// Meridian's session resume replayed the severed previous stream's unconsumed
// bytes as text deltas BEFORE this request's message_start. collectStream
// accumulated them into the assistant message, so user-composed text was
// persisted (and later replayed) as assistant output. Deltas arriving outside
// message framing are unattributable and must be dropped, not accumulated —
// and must not reach the display callback either.
func TestCollectStream_DropsDeltasBeforeMessageStart(t *testing.T) {
	rdr := &scriptedStreamReader{events: []llm.StreamEvent{
		{Type: llm.EventTextDelta, TextDelta: ".go, the settings UI — phantom replayed tail"},
		{Type: llm.EventMessageStart, InputTokens: 10},
		{Type: llm.EventTextDelta, TextDelta: "real reply"},
		{Type: llm.EventToolUseStart, ToolUseID: "toolu_1", ToolName: "get_protocol"},
		{Type: llm.EventToolUseInputDelta, TextDelta: `{"name":"design-decisions"}`},
		{Type: llm.EventToolUseStop},
		{Type: llm.EventMessageStop, OutputTokens: 5},
	}}
	var shown strings.Builder
	resp, err := collectStream(context.Background(), rdr, func(s string) { shown.WriteString(s) }, nil)
	if err != nil {
		t.Fatalf("collectStream: %v", err)
	}
	if got := textOf(resp.Blocks); got != "real reply" {
		t.Errorf("accumulated text = %q, want %q (orphan pre-start delta must be dropped)", got, "real reply")
	}
	if strings.Contains(shown.String(), "phantom") {
		t.Errorf("display callback received orphan pre-start delta: %q", shown.String())
	}
	var tools int
	for _, b := range resp.Blocks {
		if b.Type == llm.BlockToolUse {
			tools++
		}
	}
	if tools != 1 {
		t.Errorf("tool_use blocks = %d, want 1 (genuine tool call must survive)", tools)
	}
}

// A second message_start on one response stream is malformed (one response ==
// one message). Treat it as "the stream restarted": discard everything
// accumulated for the earlier message and keep only the newest one.
func TestCollectStream_SecondMessageStartResetsAccumulation(t *testing.T) {
	rdr := &scriptedStreamReader{events: []llm.StreamEvent{
		{Type: llm.EventMessageStart, InputTokens: 3},
		{Type: llm.EventTextDelta, TextDelta: "stale partial"},
		{Type: llm.EventMessageStart, InputTokens: 12},
		{Type: llm.EventTextDelta, TextDelta: "fresh reply"},
		{Type: llm.EventMessageStop, OutputTokens: 4},
	}}
	resp, err := collectStream(context.Background(), rdr, nil, nil)
	if err != nil {
		t.Fatalf("collectStream: %v", err)
	}
	if got := textOf(resp.Blocks); got != "fresh reply" {
		t.Errorf("accumulated text = %q, want %q (restarted message must reset accumulation)", got, "fresh reply")
	}
	if resp.InputTokens != 12 {
		t.Errorf("InputTokens = %d, want 12 (newest message_start wins)", resp.InputTokens)
	}
}

// Deltas after message_stop are equally unattributable — dropped.
func TestCollectStream_DropsDeltasAfterMessageStop(t *testing.T) {
	rdr := &scriptedStreamReader{events: []llm.StreamEvent{
		{Type: llm.EventMessageStart},
		{Type: llm.EventTextDelta, TextDelta: "reply"},
		{Type: llm.EventMessageStop},
		{Type: llm.EventTextDelta, TextDelta: " trailing junk"},
	}}
	resp, err := collectStream(context.Background(), rdr, nil, nil)
	if err != nil {
		t.Fatalf("collectStream: %v", err)
	}
	if got := textOf(resp.Blocks); got != "reply" {
		t.Errorf("accumulated text = %q, want %q (post-stop delta must be dropped)", got, "reply")
	}
}

// Happy path stays byte-identical.
func TestCollectStream_HappyPathUnchanged(t *testing.T) {
	rdr := &scriptedStreamReader{events: []llm.StreamEvent{
		{Type: llm.EventMessageStart, InputTokens: 7},
		{Type: llm.EventTextDelta, TextDelta: "hello "},
		{Type: llm.EventTextDelta, TextDelta: "world"},
		{Type: llm.EventMessageStop, OutputTokens: 2},
	}}
	var shown strings.Builder
	resp, err := collectStream(context.Background(), rdr, func(s string) { shown.WriteString(s) }, nil)
	if err != nil {
		t.Fatalf("collectStream: %v", err)
	}
	if got := textOf(resp.Blocks); got != "hello world" {
		t.Errorf("accumulated text = %q, want %q", got, "hello world")
	}
	if shown.String() != "hello world" {
		t.Errorf("displayed text = %q, want %q", shown.String(), "hello world")
	}
	if resp.InputTokens != 7 || resp.OutputTokens != 2 {
		t.Errorf("tokens = %d/%d, want 7/2", resp.InputTokens, resp.OutputTokens)
	}
}
