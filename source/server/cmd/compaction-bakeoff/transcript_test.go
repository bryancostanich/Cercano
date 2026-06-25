package main

import (
	"strings"
	"testing"

	"cercano/source/server/internal/contextmeter"
	"cercano/source/server/internal/llm"
)

const sampleTranscript = `{"type":"user","message":{"role":"user","content":"fix the bug in pager.go"}}
{"type":"system","message":{"role":"system","content":"ignored system line"}}
{"type":"assistant","message":{"role":"assistant","content":[{"type":"thinking","text":"hmm let me look"},{"type":"text","text":"I'll read the file"},{"type":"tool_use","id":"u1","name":"read","input":{"path":"pager.go"}}]}}
{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"u1","content":"package pager // file body"}]}}
{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"fixed the off-by-one"}]}}`

func TestParseTranscript_ConvertsAndPairs(t *testing.T) {
	tok := contextmeter.Default()
	msgs := parseTranscript(strings.NewReader(sampleTranscript), 0, tok)

	if len(msgs) != 4 {
		t.Fatalf("expected 4 messages (system skipped), got %d", len(msgs))
	}
	// First user message: plain-string content.
	if msgs[0].Role != llm.RoleUser || msgs[0].Blocks[0].Type != llm.BlockText ||
		!strings.Contains(msgs[0].Blocks[0].Text, "fix the bug") {
		t.Errorf("first message wrong: %+v", msgs[0])
	}
	// Assistant message: thinking dropped, text + tool_use kept.
	a := msgs[1]
	if len(a.Blocks) != 2 {
		t.Fatalf("assistant should keep text+tool_use (thinking dropped), got %d blocks", len(a.Blocks))
	}
	if a.Blocks[1].Type != llm.BlockToolUse || a.Blocks[1].ToolUseID != "u1" || a.Blocks[1].ToolName != "read" {
		t.Errorf("tool_use not converted: %+v", a.Blocks[1])
	}
	// tool_result paired to u1.
	if msgs[2].Blocks[0].Type != llm.BlockToolResult || msgs[2].Blocks[0].ToolUseRef != "u1" {
		t.Errorf("tool_result not converted/paired: %+v", msgs[2].Blocks[0])
	}
	if !llm.IsValidPairing(msgs) {
		t.Error("converted transcript must be pairing-valid")
	}
}

func TestParseTranscript_MaxTokensTruncates(t *testing.T) {
	tok := contextmeter.Default()
	full := parseTranscript(strings.NewReader(sampleTranscript), 0, tok)
	// A tiny budget keeps at least the first message but fewer than all.
	sliced := parseTranscript(strings.NewReader(sampleTranscript), 1, tok)
	if len(sliced) < 1 {
		t.Fatal("must keep at least the first message")
	}
	if len(sliced) >= len(full) {
		t.Errorf("maxTokens=1 should truncate: sliced=%d full=%d", len(sliced), len(full))
	}
}
