package llm

// RepairPairing removes orphaned tool_use / tool_result blocks so the array is
// always valid to send. A tool_use is kept only if a tool_result referencing
// its id appears in a LATER message; a tool_result is kept only if a tool_use
// declaring its id appears in an EARLIER message. Messages left with no blocks
// are dropped. Order-preserving and pure.
func RepairPairing(msgs []Message) []Message {
	useIdx := map[string]int{}
	for i, m := range msgs {
		for _, b := range m.Blocks {
			if b.Type == BlockToolUse {
				if _, ok := useIdx[b.ToolUseID]; !ok {
					useIdx[b.ToolUseID] = i
				}
			}
		}
	}
	resolvedAfter := map[string]bool{}
	for i, m := range msgs {
		for _, b := range m.Blocks {
			if b.Type == BlockToolResult {
				if j, ok := useIdx[b.ToolUseRef]; ok && i > j {
					resolvedAfter[b.ToolUseRef] = true
				}
			}
		}
	}
	out := make([]Message, 0, len(msgs))
	for i, m := range msgs {
		kept := make([]Block, 0, len(m.Blocks))
		for _, b := range m.Blocks {
			switch b.Type {
			case BlockToolUse:
				if !resolvedAfter[b.ToolUseID] {
					continue
				}
			case BlockToolResult:
				if j, ok := useIdx[b.ToolUseRef]; !ok || i <= j {
					continue
				}
			}
			kept = append(kept, b)
		}
		if len(kept) == 0 {
			continue
		}
		out = append(out, Message{Role: m.Role, Blocks: kept})
	}
	return out
}

// IsValidPairing reports whether msgs already satisfies the use/result pairing
// rule — i.e. RepairPairing would drop nothing.
func IsValidPairing(msgs []Message) bool {
	repaired := RepairPairing(msgs)
	if len(repaired) != len(msgs) {
		return false
	}
	for i := range msgs {
		if len(repaired[i].Blocks) != len(msgs[i].Blocks) {
			return false
		}
	}
	return true
}
