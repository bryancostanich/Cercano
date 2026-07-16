package compactor

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"cercano/source/server/internal/conversation"
	"cercano/source/server/internal/llm"
)

func turnWithBlocks(t *testing.T, id string, at int64, blocks []llm.Block) conversation.Turn {
	t.Helper()
	b, err := json.Marshal(blocks)
	if err != nil {
		t.Fatal(err)
	}
	return conversation.Turn{
		ID: id, Role: "user", BlocksJSON: string(b),
		CreatedAt: time.Unix(at, 0),
	}
}

func TestStubToolResultsThrough(t *testing.T) {
	body := strings.Repeat("tool output ", 200)
	turns := []conversation.Turn{
		turnWithBlocks(t, "t0", 100, []llm.Block{
			{Type: llm.BlockToolUse, ToolUseID: "u1", ToolName: "Read", ToolInput: json.RawMessage(`{}`)},
		}),
		turnWithBlocks(t, "t1", 101, []llm.Block{
			{Type: llm.BlockToolResult, ToolUseRef: "u1", Content: body},
		}),
		{ID: "t2", Role: "assistant", Content: "prose turn", CreatedAt: time.Unix(102, 0)},
		turnWithBlocks(t, "t3", 200, []llm.Block{
			{Type: llm.BlockToolResult, ToolUseRef: "u2", Content: body},
		}),
	}

	out, stubbed := StubToolResultsThrough(turns, 101)
	if stubbed != 1 {
		t.Fatalf("stubbed = %d, want 1 (only the tool result at/before the floor)", stubbed)
	}

	// The old tool result is stubbed; pairing fields survive.
	var blocks []llm.Block
	if err := json.Unmarshal([]byte(out[1].BlocksJSON), &blocks); err != nil {
		t.Fatal(err)
	}
	if blocks[0].Content == body || !strings.HasPrefix(blocks[0].Content, "[elided:") {
		t.Fatalf("old tool result not stubbed: %q", blocks[0].Content[:40])
	}
	if blocks[0].ToolUseRef != "u1" || blocks[0].Type != llm.BlockToolResult {
		t.Fatalf("pairing fields must survive stubbing: %+v", blocks[0])
	}

	// The tool result after the floor is untouched.
	if err := json.Unmarshal([]byte(out[3].BlocksJSON), &blocks); err != nil {
		t.Fatal(err)
	}
	if blocks[0].Content != body {
		t.Fatal("tool result after the floor must be untouched")
	}

	// Non-tool-result turns pass through as-is; the input slice is not mutated.
	if out[2].Content != "prose turn" {
		t.Fatal("prose turn must pass through")
	}
	if !strings.Contains(turns[1].BlocksJSON, "tool output") {
		t.Fatal("input slice must not be mutated")
	}
}

func TestStubToolResultsThrough_SkipsAlreadyStubbed(t *testing.T) {
	turns := []conversation.Turn{
		turnWithBlocks(t, "t0", 100, []llm.Block{
			{Type: llm.BlockToolResult, ToolUseRef: "u1", Content: "[elided: superseded result, 900 chars]"},
		}),
	}
	out, stubbed := StubToolResultsThrough(turns, 100)
	if stubbed != 0 {
		t.Fatalf("stubbed = %d, want 0 — already-stubbed results must not double-count", stubbed)
	}
	if out[0].BlocksJSON != turns[0].BlocksJSON {
		t.Fatal("already-stubbed turn must pass through unchanged")
	}
}

func TestStubToolResultsThrough_ZeroFloorIsNoOp(t *testing.T) {
	turns := []conversation.Turn{
		turnWithBlocks(t, "t0", 100, []llm.Block{
			{Type: llm.BlockToolResult, ToolUseRef: "u1", Content: "big output"},
		}),
	}
	out, stubbed := StubToolResultsThrough(turns, 0)
	if stubbed != 0 || out[0].BlocksJSON != turns[0].BlocksJSON {
		t.Fatalf("floor 0 must be a no-op: stubbed=%d", stubbed)
	}
}
