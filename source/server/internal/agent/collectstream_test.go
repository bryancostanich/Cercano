package agent

import (
	"context"
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

func TestCollectStream_CapturesUsage(t *testing.T) {
	rdr := &fakeReader{events: []llm.StreamEvent{
		{Type: llm.EventMessageStart, InputTokens: 1234},
		{Type: llm.EventTextDelta, TextDelta: "hi"},
		{Type: llm.EventMessageStop, StopReason: "end_turn", OutputTokens: 56},
	}}
	resp, err := collectStream(context.Background(), rdr)
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
