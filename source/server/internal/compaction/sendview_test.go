package compaction

import (
	"strings"
	"testing"

	"cercano/source/server/internal/llm"
)

func TestAssembleSendView_PreambleThenBodyValid(t *testing.T) {
	summary := StructuredSummary{Goal: "do the thing", State: "wrapping up"}
	body := []llm.Message{
		{Role: llm.RoleAssistant, Blocks: []llm.Block{toolUse("u1", "read", `{"path":"a"}`)}},
		{Role: llm.RoleUser, Blocks: []llm.Block{toolResult("u1", "data")}},
	}
	view := AssembleSendView(summary, body)
	if len(view) != 3 {
		t.Fatalf("expected preamble + 2 body messages, got %d", len(view))
	}
	if !strings.Contains(view[0].Blocks[0].Text, "do the thing") {
		t.Errorf("first message should be the summary preamble, got %+v", view[0])
	}
	if !llm.IsValidPairing(view) {
		t.Error("assembled send-view must be pairing-valid")
	}
}

func TestAssembleSendView_EmptySummaryNoPreamble(t *testing.T) {
	body := []llm.Message{textMsg(llm.RoleUser, "hi")}
	view := AssembleSendView(StructuredSummary{}, body)
	if len(view) != 1 {
		t.Fatalf("empty summary should add no preamble, got %d messages", len(view))
	}
}
