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

func TestMessageTokens_CountsImagePayloads(t *testing.T) {
	tok := contextmeter.Default()
	msg := llm.Message{Role: llm.RoleUser, Blocks: []llm.Block{{
		Type:      llm.BlockImage,
		MediaType: "image/png",
		ImageData: "AAAA",
	}}}
	if got := MessageTokens(tok, msg); got <= 0 {
		t.Fatalf("image-only message tokens = %d, want nonzero", got)
	}
}

func TestMessageTokens_LargeImageCostsMoreThanSmallText(t *testing.T) {
	tok := contextmeter.Default()
	textCost := MessageTokens(tok, textMsg(llm.RoleUser, "short text"))
	imgCost := MessageTokens(tok, llm.Message{Role: llm.RoleUser, Blocks: []llm.Block{{
		Type:      llm.BlockImage,
		MediaType: "image/png",
		ImageData: string(make([]byte, 4096)),
	}}})
	if imgCost <= textCost*100 {
		t.Fatalf("large image tokens = %d, text tokens = %d; image should dominate", imgCost, textCost)
	}
}

func TestSegmentByTokens_SplitsImageHeavyHistory(t *testing.T) {
	tok := contextmeter.Default()
	msgs := []llm.Message{
		textMsg(llm.RoleUser, "before"),
		{Role: llm.RoleUser, Blocks: []llm.Block{{Type: llm.BlockImage, MediaType: "image/png", ImageData: string(make([]byte, 2048))}}},
		textMsg(llm.RoleAssistant, "after"),
	}
	segs := SegmentByTokens(msgs, tok, 128)
	if len(segs) < 3 {
		t.Fatalf("expected image-heavy history to split under tight budget, got %d segments: %+v", len(segs), segs)
	}
}
