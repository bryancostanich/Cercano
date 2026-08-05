package agent

import (
	"testing"

	"cercano/source/server/internal/agenttools"
	"cercano/source/server/internal/llm"
)

// TestToolResultBlocks_NoImages: a plain text result yields exactly the
// tool_result block, unchanged.
func TestToolResultBlocks_NoImages(t *testing.T) {
	out := llm.Block{Type: llm.BlockToolResult, ToolUseRef: "t1", Content: "done"}
	res := &agenttools.Result{Type: agenttools.ResultText, Text: "done"}
	blocks := toolResultBlocks(out, res, true)
	if len(blocks) != 1 {
		t.Fatalf("blocks = %d, want 1", len(blocks))
	}
	if blocks[0].Type != llm.BlockToolResult || blocks[0].Content != "done" {
		t.Fatalf("unexpected block %+v", blocks[0])
	}
}

// TestToolResultBlocks_VisionAppendsSiblings: with a vision-capable model, tool
// images ride as sibling BlockImage blocks after the tool_result, in order.
func TestToolResultBlocks_VisionAppendsSiblings(t *testing.T) {
	out := llm.Block{Type: llm.BlockToolResult, ToolUseRef: "t1", Content: "chart:"}
	res := &agenttools.Result{
		Type: agenttools.ResultText,
		Text: "chart:",
		Images: []llm.Block{
			{Type: llm.BlockImage, MediaType: "image/png", ImageData: "AAA"},
			{Type: llm.BlockImage, MediaType: "image/png", ImageData: "BBB"},
		},
	}
	blocks := toolResultBlocks(out, res, true)
	if len(blocks) != 3 {
		t.Fatalf("blocks = %d, want 3", len(blocks))
	}
	if blocks[0].Type != llm.BlockToolResult {
		t.Fatalf("blocks[0] = %q, want tool_result", blocks[0].Type)
	}
	if blocks[0].Content != "chart:" {
		t.Fatalf("tool_result content mutated: %q", blocks[0].Content)
	}
	if blocks[1].Type != llm.BlockImage || blocks[1].ImageData != "AAA" {
		t.Fatalf("blocks[1] = %+v", blocks[1])
	}
	if blocks[2].Type != llm.BlockImage || blocks[2].ImageData != "BBB" {
		t.Fatalf("blocks[2] = %+v", blocks[2])
	}
}

// TestToolResultBlocks_NonVisionStubs: without vision support, images are
// dropped and a stub is folded into the tool_result text.
func TestToolResultBlocks_NonVisionStubs(t *testing.T) {
	out := llm.Block{Type: llm.BlockToolResult, ToolUseRef: "t1", Content: "chart:"}
	res := &agenttools.Result{
		Type: agenttools.ResultText,
		Text: "chart:",
		Images: []llm.Block{
			{Type: llm.BlockImage, MediaType: "image/png", ImageData: "AAA"},
		},
	}
	blocks := toolResultBlocks(out, res, false)
	if len(blocks) != 1 {
		t.Fatalf("blocks = %d, want 1 (images dropped)", len(blocks))
	}
	if blocks[0].Type != llm.BlockToolResult {
		t.Fatalf("blocks[0] = %q, want tool_result", blocks[0].Type)
	}
	if blocks[0].Content == "chart:" {
		t.Fatalf("expected stub folded into content, got unchanged %q", blocks[0].Content)
	}
	if want := "image(s) omitted"; !containsSub(blocks[0].Content, want) {
		t.Fatalf("content %q missing stub marker %q", blocks[0].Content, want)
	}
}

// TestToolResultBlocks_NonVisionEmptyContent: a stub replaces empty content
// outright (no stray leading newline).
func TestToolResultBlocks_NonVisionEmptyContent(t *testing.T) {
	out := llm.Block{Type: llm.BlockToolResult, ToolUseRef: "t1", Content: ""}
	res := &agenttools.Result{
		Type:   agenttools.ResultText,
		Images: []llm.Block{{Type: llm.BlockImage, MediaType: "image/png", ImageData: "AAA"}},
	}
	blocks := toolResultBlocks(out, res, false)
	if len(blocks) != 1 {
		t.Fatalf("blocks = %d, want 1", len(blocks))
	}
	if blocks[0].Content == "" {
		t.Fatalf("expected stub content, got empty")
	}
	if blocks[0].Content[0] == '\n' {
		t.Fatalf("stub has stray leading newline: %q", blocks[0].Content)
	}
}

func containsSub(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
