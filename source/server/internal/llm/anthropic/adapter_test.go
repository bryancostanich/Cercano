package anthropic

import (
	"encoding/json"
	"testing"

	sdk "github.com/anthropics/anthropic-sdk-go"

	"cercano/source/server/internal/llm"
)

func TestToSDKBlock_Text(t *testing.T) {
	got := blockToSDK(llm.Block{Type: llm.BlockText, Text: "hello"})
	if got.OfText == nil || got.OfText.Text != "hello" {
		t.Errorf("text block: %+v", got)
	}
}

func TestToSDKBlock_ToolUse(t *testing.T) {
	got := blockToSDK(llm.Block{
		Type:      llm.BlockToolUse,
		ToolUseID: "u1",
		ToolName:  "read_file",
		ToolInput: json.RawMessage(`{"path":"x"}`),
	})
	if got.OfToolUse == nil || got.OfToolUse.ID != "u1" || got.OfToolUse.Name != "read_file" {
		t.Errorf("tool_use block: %+v", got)
	}
}

func TestToSDKBlock_ToolResult(t *testing.T) {
	got := blockToSDK(llm.Block{
		Type:       llm.BlockToolResult,
		ToolUseRef: "u1",
		Content:    "32 lines",
		IsError:    false,
	})
	if got.OfToolResult == nil || got.OfToolResult.ToolUseID != "u1" {
		t.Errorf("tool_result block: %+v", got)
	}
}

func TestFromSDKBlock_Text(t *testing.T) {
	in := sdk.ContentBlockUnion{Type: "text", Text: "world"}
	got := blockFromSDK(in)
	if got.Type != llm.BlockText || got.Text != "world" {
		t.Errorf("text from SDK: %+v", got)
	}
}

func TestFromSDKBlock_ToolUse(t *testing.T) {
	in := sdk.ContentBlockUnion{
		Type:  "tool_use",
		ID:    "u1",
		Name:  "read_file",
		Input: json.RawMessage(`{"path":"x"}`),
	}
	got := blockFromSDK(in)
	if got.Type != llm.BlockToolUse || got.ToolName != "read_file" {
		t.Errorf("tool_use from SDK: %+v", got)
	}
}

func TestRoundTrip(t *testing.T) {
	original := llm.Block{
		Type: llm.BlockToolUse, ToolUseID: "u1", ToolName: "read_file",
		ToolInput: json.RawMessage(`{"path":"main.go"}`),
	}
	p := blockToSDK(original)
	resp := sdk.ContentBlockUnion{
		Type: "tool_use",
		ID:   p.OfToolUse.ID,
		Name: p.OfToolUse.Name,
	}
	if raw, ok := p.OfToolUse.Input.(json.RawMessage); ok {
		resp.Input = raw
	}
	out := blockFromSDK(resp)
	if out.ToolUseID != original.ToolUseID || out.ToolName != original.ToolName {
		t.Errorf("round-trip: in=%+v out=%+v", original, out)
	}
}
