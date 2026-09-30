package compaction

import "cercano/source/server/internal/llm"

// ToolSafePrefix returns the last boundary at or before limit with no pending
// tool calls. A parallel batch is indivisible until ALL its results arrive.
// Unlike RepairPairing this never drops blocks to make an unsafe cut look valid.
func ToolSafePrefix(messages []llm.Message, limit int) int {
	if limit > len(messages) {
		limit = len(messages)
	}
	pending := map[string]bool{}
	safe := 0
	for i := 0; i < limit; i++ {
		for _, b := range messages[i].Blocks {
			switch b.Type {
			case llm.BlockToolUse:
				pending[b.ToolUseID] = true
			case llm.BlockToolResult:
				delete(pending, b.ToolUseRef)
			}
		}
		if len(pending) == 0 {
			safe = i + 1
		}
	}
	return safe
}

// toolGroups preserves order and content, including an incomplete final group.
// The compactor holds incomplete groups in its live tail rather than freezing
// them. Summary packing refuses incomplete groups instead of inventing state.
func toolGroups(messages []llm.Message) [][]llm.Message {
	var groups [][]llm.Message
	pending := map[string]bool{}
	start := 0
	for i, m := range messages {
		for _, b := range m.Blocks {
			switch b.Type {
			case llm.BlockToolUse:
				pending[b.ToolUseID] = true
			case llm.BlockToolResult:
				delete(pending, b.ToolUseRef)
			}
		}
		if len(pending) == 0 {
			groups = append(groups, messages[start:i+1])
			start = i + 1
		}
	}
	if start < len(messages) {
		groups = append(groups, messages[start:])
	}
	return groups
}
