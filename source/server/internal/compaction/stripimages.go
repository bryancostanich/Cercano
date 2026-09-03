package compaction

import "cercano/source/server/internal/llm"

// ImagePlaceholderText is the text substituted for an image block before a
// span is summarized. It matches what BuildSummaryPrompt already renders for
// llm.BlockImage, so stripping changes the payload carried through the
// pipeline without changing the prompt the summarizer ultimately sees.
const ImagePlaceholderText = "[image]"

// StripImagesForSummary replaces every image block with an equivalent text
// placeholder. Compaction never needs image bytes: BuildSummaryPrompt renders
// image blocks as "[image]" and discards the payload, so base64 that reaches
// segmentation is pure overhead: SegmentByTokens charges it via ImageTokens at
// one token per four encoded bytes, so a 7.4 MB screenshot bills ~1.9M tokens
// against an 8,000-token segment budget. That one turn swallows entire passes
// and starves the segmenter of real content to summarize.
//
// Substituting the placeholder at pass entry makes the measured cost match the
// ~3 tokens the prompt actually spends. Note this is a sizing fix, not a
// splitting fix: an image block alone already fits the summary prompt, so
// splitOversizedMessageForSummary peels it into its own part rather than
// deferring (see TestImageBlockAloneAlwaysFitsSummaryPrompt). Oversized
// tool_use blocks remain the unsplittable case and still defer.
//
// Message count and order are preserved exactly, including messages whose only
// content was an image — callers map summarized message spans back to turn
// timestamps positionally, so dropping a message would desynchronize the
// frozen boundary from the content behind it. Pure: input is not mutated.
//
// This is deliberately NOT the inspect_image placeholder used on the send path
// (see internal/visionattach): that store is in-memory and holds only images
// from the current process lifetime, so historical turns would be rewritten to
// reference ids that resolve to nothing.
func StripImagesForSummary(msgs []llm.Message) ([]llm.Message, int) {
	stripped := 0
	out := make([]llm.Message, len(msgs))
	for i, m := range msgs {
		has := false
		for _, b := range m.Blocks {
			if b.Type == llm.BlockImage {
				has = true
				break
			}
		}
		if !has {
			out[i] = m
			continue
		}
		blocks := make([]llm.Block, len(m.Blocks))
		for j, b := range m.Blocks {
			if b.Type != llm.BlockImage {
				blocks[j] = b
				continue
			}
			blocks[j] = llm.Block{Type: llm.BlockText, Text: ImagePlaceholderText}
			stripped++
		}
		sm := m
		sm.Blocks = blocks
		out[i] = sm
	}
	return out, stripped
}
