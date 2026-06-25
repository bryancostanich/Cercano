package compaction

import (
	"cercano/source/server/internal/contextmeter"
	"cercano/source/server/internal/llm"
)

// MessageTokens estimates the token cost of a message by counting its block
// text/content/tool-input. Approximate but deterministic for a given tokenizer.
func MessageTokens(tok contextmeter.Tokenizer, m llm.Message) int {
	n := 0
	for _, b := range m.Blocks {
		if b.Text != "" {
			n += tok.Count(b.Text)
		}
		if b.Content != "" {
			n += tok.Count(b.Content)
		}
		if len(b.ToolInput) > 0 {
			n += tok.Count(string(b.ToolInput))
		}
		if b.ToolName != "" {
			n += tok.Count(b.ToolName)
		}
	}
	return n
}

// TotalTokens sums MessageTokens over msgs.
func TotalTokens(tok contextmeter.Tokenizer, msgs []llm.Message) int {
	n := 0
	for _, m := range msgs {
		n += MessageTokens(tok, m)
	}
	return n
}

// SegmentByTokens splits msgs into contiguous segments, each accumulating up to
// perSegment tokens (a single oversized message becomes its own segment). Never
// drops or reorders messages.
func SegmentByTokens(msgs []llm.Message, tok contextmeter.Tokenizer, perSegment int) []Segment {
	if perSegment < 1 {
		perSegment = 1
	}
	var segs []Segment
	var cur []llm.Message
	curTok := 0
	flush := func() {
		if len(cur) > 0 {
			segs = append(segs, Segment{Messages: cur, Tokens: curTok})
			cur = nil
			curTok = 0
		}
	}
	for _, m := range msgs {
		mt := MessageTokens(tok, m)
		if curTok > 0 && curTok+mt > perSegment {
			flush()
		}
		cur = append(cur, m)
		curTok += mt
	}
	flush()
	return segs
}
