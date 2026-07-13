package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cercano/source/server/internal/llm"
)

// A re-emitted tool_use id across turns is the resume-replay fingerprint; the
// turn-assembly instrument must flag it exactly once to the anomaly log.
func TestNoteAssembledTurn_FlagsReplayedToolUseID(t *testing.T) {
	tmp := filepath.Join(t.TempDir(), "anom.jsonl")
	t.Setenv("CERCANO_ANOMALY_LOG", tmp)
	seen := map[string]bool{}
	blk := []llm.Block{{Type: llm.BlockToolUse, ToolUseID: "toolu_A", ToolName: "checkpoint", ToolInput: json.RawMessage(`{"subject":"x"}`)}}
	noteAssembledTurn("conv-x", blk, seen) // first: genuine, no anomaly
	noteAssembledTurn("conv-x", blk, seen) // replay of same id: anomaly
	data, err := os.ReadFile(tmp)
	if err != nil {
		t.Fatalf("read anomaly log: %v", err)
	}
	if !strings.Contains(string(data), "replayed_tool_use") || !strings.Contains(string(data), "toolu_A") {
		t.Fatalf("expected replay anomaly for toolu_A, got: %s", data)
	}
	if got := strings.Count(string(data), "\n"); got != 1 {
		t.Fatalf("expected exactly one anomaly line, got %d: %s", got, data)
	}
}

// A fresh tool_use id must never be flagged.
func TestNoteAssembledTurn_IgnoresDistinctIDs(t *testing.T) {
	tmp := filepath.Join(t.TempDir(), "anom.jsonl")
	t.Setenv("CERCANO_ANOMALY_LOG", tmp)
	seen := map[string]bool{}
	noteAssembledTurn("c", []llm.Block{{Type: llm.BlockToolUse, ToolUseID: "toolu_A", ToolName: "Bash"}}, seen)
	noteAssembledTurn("c", []llm.Block{{Type: llm.BlockToolUse, ToolUseID: "toolu_B", ToolName: "Bash"}}, seen)
	if _, err := os.Stat(tmp); err == nil {
		data, _ := os.ReadFile(tmp)
		t.Fatalf("expected no anomaly file, but got: %s", data)
	}
}
