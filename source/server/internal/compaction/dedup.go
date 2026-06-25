package compaction

import (
	"fmt"

	"cercano/source/server/internal/llm"
)

// ElideSupersededToolResults rewrites the Content of tool_result blocks whose
// originating tool call (same ToolName + identical ToolInput bytes) recurs later
// in the history. Only the LAST occurrence of each identical call keeps its full
// result; earlier ones are replaced with a one-line stub. All blocks and ids are
// preserved, so pairing stays valid. Returns the rewritten messages and the
// number of results stubbed. Pure: input is not mutated.
func ElideSupersededToolResults(msgs []llm.Message) ([]llm.Message, int) {
	// key -> last tool_use id for that identical call.
	lastUseForKey := map[string]string{}
	for _, m := range msgs {
		for _, b := range m.Blocks {
			if b.Type == llm.BlockToolUse {
				lastUseForKey[toolKey(b)] = b.ToolUseID
			}
		}
	}
	// id -> key, so a tool_result can find whether its use is the last for its key.
	keyForUse := map[string]string{}
	for _, m := range msgs {
		for _, b := range m.Blocks {
			if b.Type == llm.BlockToolUse {
				keyForUse[b.ToolUseID] = toolKey(b)
			}
		}
	}

	collapsed := 0
	out := make([]llm.Message, len(msgs))
	for i, m := range msgs {
		blocks := make([]llm.Block, len(m.Blocks))
		for j, b := range m.Blocks {
			if b.Type == llm.BlockToolResult {
				if key, ok := keyForUse[b.ToolUseRef]; ok && lastUseForKey[key] != b.ToolUseRef {
					b.Content = fmt.Sprintf("[elided: superseded result, %d chars]", len(b.Content))
					collapsed++
				}
			}
			blocks[j] = b
		}
		out[i] = llm.Message{Role: m.Role, Blocks: blocks}
	}
	return out, collapsed
}

func toolKey(b llm.Block) string {
	return b.ToolName + "\x00" + string(b.ToolInput)
}
