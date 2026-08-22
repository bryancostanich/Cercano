package compaction

import (
	"cercano/source/server/internal/contextmeter"
	"cercano/source/server/internal/llm"
)

const imageTokenOverhead = 64

// MessageTokens estimates the token cost of a message by counting its block
// text/content/tool-input plus a conservative charge for inline image payloads.
// Approximate but deterministic for a given tokenizer.
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
		if b.Type == llm.BlockImage {
			n += ImageTokens(tok, b)
		}
	}
	return n
}

// ImageTokens returns a conservative, deterministic budget charge for an image
// block. Inline base64 is expensive even if later provider-facing history is
// rewritten to inspect_image placeholders: the stored live tail still needs to
// trigger compaction before megabytes of payload accumulate. One token per four
// encoded bytes mirrors common text-token approximations and the fixed overhead
// accounts for media/type framing. URL-only images are much cheaper but nonzero.
func ImageTokens(tok contextmeter.Tokenizer, b llm.Block) int {
	if b.Type != llm.BlockImage {
		return 0
	}
	n := imageTokenOverhead
	if b.ImageData != "" {
		n += (len(b.ImageData) + 3) / 4
	}
	if b.ImageURL != "" {
		n += tok.Count(b.ImageURL)
	}
	if b.MediaType != "" {
		n += tok.Count(b.MediaType)
	}
	return n
}

// TotalTokens sums MessageTokens over msgs. It is intentionally conservative
// for compaction pressure and raw send-view safety checks: inline image payloads
// are charged by encoded byte length so image-heavy tails cannot look cheap.
func TotalTokens(tok contextmeter.Tokenizer, msgs []llm.Message) int {
	n := 0
	for _, m := range msgs {
		n += MessageTokens(tok, m)
	}
	return n
}

// ProviderMessageTokens estimates the provider-facing token cost of a message
// after history images have been converted to references/placeholders. Unlike
// MessageTokens, inline image bytes are not charged by payload size. Use this
// for UI sent-context meters and hard-override decisions that are meant to
// mirror the compacted prompt, not raw storage pressure.
func ProviderMessageTokens(tok contextmeter.Tokenizer, m llm.Message) int {
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
		if b.Type == llm.BlockImage {
			n += ProviderImageTokens(tok, b)
		}
	}
	return n
}

// ProviderImageTokens charges an image as the small textual reference the model
// should see after placeholder rewriting, rather than charging inline base64.
// It is deliberately nonzero so image-heavy histories remain visible in the
// sent-context meter without exploding to raw-storage size.
func ProviderImageTokens(tok contextmeter.Tokenizer, b llm.Block) int {
	if b.Type != llm.BlockImage {
		return 0
	}
	n := tok.Count("[image attached; use inspect_image]")
	if b.MediaType != "" {
		n += tok.Count(b.MediaType)
	}
	if b.ImageURL != "" {
		n += tok.Count(b.ImageURL)
	}
	return n
}

// ProviderTotalTokens sums ProviderMessageTokens over msgs.
func ProviderTotalTokens(tok contextmeter.Tokenizer, msgs []llm.Message) int {
	n := 0
	for _, m := range msgs {
		n += ProviderMessageTokens(tok, m)
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
