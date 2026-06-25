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
	return llm.RepairPairing(msgs)
}
