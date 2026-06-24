package agent

import (
	"encoding/json"

	"cercano/source/server/internal/conversation"
	"cercano/source/server/internal/llm"
)

// BuildLLMHistory reconstructs a window-valid llm.Message slice from stored
// turns. It is pure (no I/O) and order-preserving. To keep the result always
// safe to send to the provider, it drops any tool_use block with no following
// tool_result and any tool_result block with no matching tool_use (e.g. legacy
// data persisted before tool_result turns were saved), then drops any message
// left with no blocks.
func BuildLLMHistory(turns []conversation.Turn) []llm.Message {
	msgs := make([]llm.Message, 0, len(turns))
	for _, t := range turns {
		role := llm.RoleUser
		switch t.Role {
		case string(llm.RoleAssistant):
			role = llm.RoleAssistant
		case string(llm.RoleSystem):
			role = llm.RoleSystem
		}
		var blocks []llm.Block
		if t.BlocksJSON != "" {
			if err := json.Unmarshal([]byte(t.BlocksJSON), &blocks); err != nil {
				blocks = nil
			}
		}
		if len(blocks) == 0 {
			if t.Content == "" {
				continue
			}
			blocks = []llm.Block{{Type: llm.BlockText, Text: t.Content}}
		}
		msgs = append(msgs, llm.Message{Role: role, Blocks: blocks})
	}
	return repairPairing(msgs)
}

// repairPairing removes orphaned tool_use / tool_result blocks so the array is
// always valid to send. A tool_use is kept only if a tool_result referencing
// its id appears in a LATER message; a tool_result is kept only if a tool_use
// declaring its id appears in an EARLIER message. This positional rule (not a
// global presence check) guarantees the use-before-result ordering the provider
// requires, even if stored data is out of order.
func repairPairing(msgs []llm.Message) []llm.Message {
	// First occurrence (message index) of each declared tool_use id.
	useIdx := map[string]int{}
	for i, m := range msgs {
		for _, b := range m.Blocks {
			if b.Type == llm.BlockToolUse {
				if _, ok := useIdx[b.ToolUseID]; !ok {
					useIdx[b.ToolUseID] = i
				}
			}
		}
	}
	// tool_use ids that have a matching tool_result in a strictly later message.
	resolvedAfter := map[string]bool{}
	for i, m := range msgs {
		for _, b := range m.Blocks {
			if b.Type == llm.BlockToolResult {
				if j, ok := useIdx[b.ToolUseRef]; ok && i > j {
					resolvedAfter[b.ToolUseRef] = true
				}
			}
		}
	}
	out := make([]llm.Message, 0, len(msgs))
	for i, m := range msgs {
		kept := make([]llm.Block, 0, len(m.Blocks))
		for _, b := range m.Blocks {
			switch b.Type {
			case llm.BlockToolUse:
				if !resolvedAfter[b.ToolUseID] {
					continue
				}
			case llm.BlockToolResult:
				if j, ok := useIdx[b.ToolUseRef]; !ok || i <= j {
					continue
				}
			}
			kept = append(kept, b)
		}
		if len(kept) == 0 {
			continue
		}
		out = append(out, llm.Message{Role: m.Role, Blocks: kept})
	}
	return out
}
