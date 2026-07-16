package compactor

import (
	"encoding/json"
	"fmt"
	"strings"

	"cercano/source/server/internal/conversation"
	"cercano/source/server/internal/llm"
)

// StubToolResultsThrough returns a copy of turns in which every tool_result
// block belonging to a turn with CreatedAt.Unix() <= floor has its content
// replaced by a short stub, plus the number of results stubbed. Block
// structure (types, ids, pairing refs) is preserved so the resulting history
// is always pairing-valid; already-stubbed results are left alone. floor <= 0
// is a no-op. The input slice is never mutated — this backs the in-memory
// /elide-context floor, applied at send-view assembly time, so the stored raw
// turns stay intact.
func StubToolResultsThrough(turns []conversation.Turn, floor int64) ([]conversation.Turn, int) {
	if floor <= 0 {
		return turns, 0
	}
	out := make([]conversation.Turn, len(turns))
	copy(out, turns)
	stubbed := 0
	for i, t := range turns {
		if t.CreatedAt.Unix() > floor || t.BlocksJSON == "" ||
			!strings.Contains(t.BlocksJSON, string(llm.BlockToolResult)) {
			continue
		}
		var blocks []llm.Block
		if err := json.Unmarshal([]byte(t.BlocksJSON), &blocks); err != nil {
			continue // undecodable blocks pass through; BuildLLMHistory handles them
		}
		changed := false
		for j, b := range blocks {
			if b.Type != llm.BlockToolResult || b.Content == "" ||
				strings.HasPrefix(b.Content, "[elided:") {
				continue
			}
			blocks[j].Content = fmt.Sprintf("[elided: tool result, %d chars]", len(b.Content))
			stubbed++
			changed = true
		}
		if !changed {
			continue
		}
		enc, err := json.Marshal(blocks)
		if err != nil {
			continue
		}
		out[i].BlocksJSON = string(enc)
	}
	return out, stubbed
}
