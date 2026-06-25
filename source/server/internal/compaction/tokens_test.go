package compaction

import (
	"testing"

	"cercano/source/server/internal/contextmeter"
	"cercano/source/server/internal/llm"
)

func textMsg(role llm.Role, s string) llm.Message {
	return llm.Message{Role: role, Blocks: []llm.Block{{Type: llm.BlockText, Text: s}}}
}

func TestSegmentByTokens_SplitsOnBudget(t *testing.T) {
	tok := contextmeter.Default()
	msgs := []llm.Message{
		textMsg(llm.RoleUser, "alpha beta gamma delta"),
		textMsg(llm.RoleAssistant, "epsilon zeta eta theta"),
		textMsg(llm.RoleUser, "iota kappa lambda mu"),
	}
	// A tiny per-segment budget forces (at least) one boundary.
	segs := SegmentByTokens(msgs, tok, MessageTokens(tok, msgs[0]))
	if len(segs) < 2 {
		t.Fatalf("expected multiple segments under a tight budget, got %d", len(segs))
	}
	// No message is lost.
	var n int
	for _, s := range segs {
		n += len(s.Messages)
	}
	if n != len(msgs) {
		t.Fatalf("segments dropped messages: got %d of %d", n, len(msgs))
	}
}
