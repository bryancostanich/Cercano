package compaction

import (
	"cercano/source/server/internal/contextmeter"
	"cercano/source/server/internal/llm"
	"strings"
	"testing"
)

func pairedMessages() []llm.Message {
	return []llm.Message{
		{Role: llm.RoleAssistant, Blocks: []llm.Block{{Type: llm.BlockToolUse, ToolUseID: "a", ToolName: "Grep", ToolInput: []byte(`{"pattern":"x"}`)}, {Type: llm.BlockToolUse, ToolUseID: "b", ToolName: "Read", ToolInput: []byte(`{}`)}}},
		{Role: llm.RoleUser, Blocks: []llm.Block{{Type: llm.BlockToolResult, ToolUseRef: "b", Content: strings.Repeat("result ", 100)}}},
		{Role: llm.RoleUser, Blocks: []llm.Block{{Type: llm.BlockToolResult, ToolUseRef: "a", Content: strings.Repeat("found ", 100)}}},
	}
}
func TestSegmentKeepsCompleteParallelToolExchange(t *testing.T) {
	msgs := pairedMessages()
	segs := SegmentByTokens(msgs, contextmeter.Default(), 20)
	if len(segs) != 1 || len(segs[0].Messages) != 3 {
		t.Fatalf("split completed exchange across %d summaries", len(segs))
	}
}
func TestSummaryChunksDoNotSeparateCallsAndResults(t *testing.T) {
	msgs := pairedMessages()
	msgs = append([]llm.Message{{Role: llm.RoleUser, Blocks: []llm.Block{{Type: llm.BlockText, Text: strings.Repeat("intro ", 300)}}}}, msgs...)
	chunks, err := PackSummaryChunks(msgs, 2400, 1024)
	if err != nil {
		t.Fatal(err)
	}
	for _, chunk := range chunks {
		uses, results := 0, 0
		for _, m := range chunk {
			for _, b := range m.Blocks {
				if b.Type == llm.BlockToolUse {
					uses++
				}
				if b.Type == llm.BlockToolResult {
					results++
				}
			}
		}
		if uses != results {
			t.Fatalf("split group uses=%d results=%d", uses, results)
		}
	}
}

func TestToolSafePrefixHoldsPendingAndParallelCalls(t *testing.T) {
	msgs := pairedMessages()
	for n, want := range []int{0, 0, 0, 3} {
		if got := ToolSafePrefix(msgs, n); got != want {
			t.Fatalf("limit=%d got=%d want=%d", n, got, want)
		}
	}
}
func TestOversizedExchangeDefersInsteadOfSeparatingEvidence(t *testing.T) {
	messages := pairedMessages()
	window := 0
	for _, m := range messages {
		b := EstimateSummaryBudget(BuildSummaryPrompt([]llm.Message{m}), 1024, 0)
		window = max(window, b.PromptTokens+b.OutputReserve)
	}
	// Every individual message fits, but the complete exchange does not. This
	// distinguishes atomic deferral from an impossibly tiny context fixture.
	whole := EstimateSummaryBudget(BuildSummaryPrompt(messages), 1024, window)
	if whole.Fits {
		t.Fatal("fixture exchange must exceed the window")
	}
	if _, err := PackSummaryChunks(messages, window, 1024); err == nil {
		t.Fatal("oversized tool exchange was split into misleading summaries")
	}
}
