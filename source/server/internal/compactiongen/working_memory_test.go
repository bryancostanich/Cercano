package compactiongen

import (
	"encoding/json"
	"testing"
	"time"

	"cercano/source/server/internal/agent"
	"cercano/source/server/internal/compaction"
	"cercano/source/server/internal/conversation"
	"cercano/source/server/internal/llm"
)

func memoryTurns() []conversation.Turn {
	ts := bigTurns(12, 1000)
	for i := range ts {
		ts[i].Role = "assistant"
	}
	ts[0].Role = "user"
	ts[0].Content = "Implement reuse; preserve cancellation"
	return ts
}
func TestUserIntentHintIgnoresToolWrappersAndPreambles(t *testing.T) {
	ts := memoryTurns()
	b, _ := json.Marshal([]llm.Block{{Type: llm.BlockToolResult, ToolUseRef: "read", Content: "not a new task"}})
	ts = append(ts, conversation.Turn{Role: "user", BlocksJSON: string(b), CreatedAt: time.Unix(200, 0)}, conversation.Turn{Role: "user", Content: "[conversation summary]\nGoal: old", CreatedAt: time.Unix(201, 0)})
	if got := compaction.LatestUserMessage(agent.BuildLLMHistory(ts)); got != ts[0].Content {
		t.Fatal(got)
	}
}
