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
	if len(msgs) == 0 {
		return msgs, 0
	}
	// Cost each message once and keep a running total. MessageTokens is
	// independent per message, so a running subtraction is exactly equal to
	// re-summing the whole view on every drop -- but the naive form was
	// quadratic in both tokenization and allocation, re-counting every
	// surviving message and rebuilding the joined slice on each iteration.
	// On a large conversation that meant thousands of full passes over tens
	// of megabytes to discard one message at a time.
	counts := make([]int, len(msgs))
	total := 0
	for i, m := range msgs {
		counts[i] = MessageTokens(tok, m)
		total += counts[i]
	}
	if total <= limit {
		return msgs, 0
	}
	if preserveLeading > len(msgs) {
		preserveLeading = len(msgs)
	}
	if preserveLeading < 0 {
		preserveLeading = 0
	}
	head := msgs[:preserveLeading]
	tail := msgs[preserveLeading:]
	// first indexes tail[0] within msgs, so counts stay aligned as we drop.
	first := preserveLeading
	dropped := 0
	for len(tail) > 1 && total > limit {
		total -= counts[first]
		first++
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
