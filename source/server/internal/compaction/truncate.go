package compaction

import (
	"cercano/source/server/internal/contextmeter"
	"cercano/source/server/internal/llm"
)

// TruncateOldestToFit drops whole messages from the front of msgs (after the
// first preserveLeading messages, which are kept unconditionally — the
// consolidated-summary preamble) until the total fits under limit. It never
// splits a message. After the size cut it keeps dropping while the first
// non-preserved message carries tool_result content, so the view never begins
// with an orphaned tool result. Returns the fitted view and how many messages
// were dropped. A view that already fits is returned unchanged.
func TruncateOldestToFit(msgs []llm.Message, tok contextmeter.Tokenizer, limit, preserveLeading int) ([]llm.Message, int) {
	if TotalTokens(tok, msgs) <= limit || len(msgs) == 0 {
		return msgs, 0
	}
	if preserveLeading > len(msgs) {
		preserveLeading = len(msgs)
	}
	head := msgs[:preserveLeading]
	tail := msgs[preserveLeading:]
	dropped := 0
	for len(tail) > 1 && TotalTokens(tok, append(append([]llm.Message{}, head...), tail...)) > limit {
		tail = tail[1:]
		dropped++
	}
	// Pairing validity: never lead with tool_result content.
	for len(tail) > 1 && hasToolResult(tail[0]) {
		tail = tail[1:]
		dropped++
	}
	return append(append([]llm.Message{}, head...), tail...), dropped
}

func hasToolResult(m llm.Message) bool {
	for _, b := range m.Blocks {
		if b.Type == llm.BlockToolResult {
			return true
		}
	}
	return false
}
