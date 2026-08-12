package agent

import (
	"encoding/base64"
	"fmt"

	"cercano/source/server/internal/llm"
	"cercano/source/server/internal/visionattach"
)

// VisionAttachStore is the subset of visionattach.Store the placeholder rewriter
// needs. Declared as an interface so the rewriter is testable with a fake and so
// callers that have no vision store configured can pass nil.
type VisionAttachStore interface {
	Add(convID, mediaType string, data []byte) visionattach.AddResult
}

// RewriteImagesToPlaceholders replaces every image block in msgs with a text
// placeholder that names a stable image ID and tells the model to use the
// inspect_image tool. Each image is registered in the attachment store under the
// given conversation ID so inspect_image can later resolve it.
//
// This is the vision-as-tool path for a text reasoning model: the model never
// receives raw image bytes, only placeholders, so a no-vision backend is safe
// and the tool affordance is explicit. Images that the store rejects (cap
// exceeded) become an "omitted" placeholder so nothing is silently dropped.
//
// A nil store means vision-as-tool is not configured; in that case image blocks
// are left untouched (the downstream provider capability gate still strips them
// for text-only models). The input slice is not mutated (copy-on-write per
// message), matching resolveImageURLs / stripImagesForTextOnly.
func RewriteImagesToPlaceholders(store VisionAttachStore, convID string, msgs []llm.Message) []llm.Message {
	if store == nil || convID == "" {
		return msgs
	}
	out := make([]llm.Message, len(msgs))
	for i, m := range msgs {
		out[i] = m
		hasImage := false
		for _, b := range m.Blocks {
			if b.Type == llm.BlockImage {
				hasImage = true
				break
			}
		}
		if !hasImage {
			continue
		}
		blocks := make([]llm.Block, 0, len(m.Blocks))
		for _, b := range m.Blocks {
			if b.Type != llm.BlockImage {
				blocks = append(blocks, b)
				continue
			}
			blocks = append(blocks, llm.Block{Type: llm.BlockText, Text: placeholderFor(store, convID, b)})
		}
		out[i].Blocks = blocks
	}
	return out
}

// placeholderFor registers one image block's bytes and returns the model-facing
// placeholder text. A URL-only image (no inline bytes) or a store rejection
// yields an "omitted" placeholder rather than a resolvable ID.
func placeholderFor(store VisionAttachStore, convID string, b llm.Block) string {
	data, err := base64.StdEncoding.DecodeString(b.ImageData)
	if err != nil || len(data) == 0 {
		return "[image omitted: not available for inspection in this conversation]"
	}
	res := store.Add(convID, b.MediaType, data)
	if res.Rejected || res.Attachment == nil {
		reason := res.RejectReason
		if reason == "" {
			reason = "unavailable"
		}
		return fmt.Sprintf("[image omitted: %s]", reason)
	}
	return fmt.Sprintf(
		"[Image %s attached for this live conversation. Use inspect_image(image_id=%q, question=...) "+
			"to ask focused visual questions about it. If it is unavailable, ask the user to reattach the image.]",
		res.Attachment.ID, res.Attachment.ID,
	)
}
