package ui

import (
	"testing"

	"cercano/source/server/pkg/agentclient"
)

// Lossless persistence stores tool_use (assistant) and tool_result (user) turns
// whose Content is empty — their payload lives in content_json. Rendering those
// as scrollback entries produces blank gaps with floating ▶ markers on resume.
// resumeEntries must drop them and keep only prose turns.
func TestResumeEntries_SkipsEmptyToolTurns(t *testing.T) {
	turns := []agentclient.PersistedTurn{
		{Role: "user", Content: "list the files"},
		{Role: "assistant", Content: ""},    // tool_use turn (blocks only)
		{Role: "user", Content: ""},         // tool_result turn (blocks only)
		{Role: "assistant", Content: "   "}, // whitespace-only — also non-prose
		{Role: "assistant", Content: "Here are the files."},
	}
	got := resumeEntries(turns, 0) // frozenThrough=0: no compaction divider
	if len(got) != 2 {
		t.Fatalf("got %d entries, want 2 (empty/whitespace tool turns dropped)", len(got))
	}
	if got[0].Role != RoleUser || got[0].Content != "list the files" {
		t.Errorf("entry0 = %+v", got[0])
	}
	if got[1].Role != RoleAssistant || got[1].Content != "Here are the files." {
		t.Errorf("entry1 = %+v", got[1])
	}
}
