package responses

import (
	"encoding/json"
	"strings"
	"testing"

	"cercano/source/server/internal/llm"
)

func TestMessagesToInput_TextImageToolReasoning(t *testing.T) {
	msgs := []llm.Message{
		{Role: llm.RoleUser, Blocks: []llm.Block{
			{Type: llm.BlockText, Text: "hi"},
			{Type: llm.BlockImage, MediaType: "image/png", ImageData: "AAAA"},
		}},
		{Role: llm.RoleAssistant, Blocks: []llm.Block{
			{Type: llm.BlockReasoning, ReasoningID: "rs_1", ReasoningData: "ENC"},
			{Type: llm.BlockToolUse, ToolUseID: "call_1", ToolName: "get_weather", ToolInput: json.RawMessage(`{"city":"Paris"}`)},
		}},
		{Role: llm.RoleUser, Blocks: []llm.Block{
			{Type: llm.BlockToolResult, ToolUseRef: "call_1", Content: "sunny"},
		}},
	}
	items, err := messagesToInput(msgs)
	if err != nil {
		t.Fatalf("messagesToInput: %v", err)
	}

	// Expect, in order: message(user text+image), reasoning, function_call, function_call_output.
	if len(items) != 4 {
		t.Fatalf("want 4 items, got %d: %+v", len(items), items)
	}
	if items[0].Type != "message" || items[0].Role != "user" || len(items[0].Content) != 2 {
		t.Fatalf("item0 = %+v", items[0])
	}
	if items[0].Content[0].Type != "input_text" || items[0].Content[1].Type != "input_image" {
		t.Fatalf("content parts = %+v", items[0].Content)
	}
	if items[0].Content[1].ImageURL != "data:image/png;base64,AAAA" {
		t.Fatalf("image url = %q", items[0].Content[1].ImageURL)
	}
	if items[1].Type != "reasoning" || items[1].ID != "rs_1" || items[1].EncryptedContent != "ENC" {
		t.Fatalf("item1 = %+v", items[1])
	}
	if items[2].Type != "function_call" || items[2].CallID != "call_1" || items[2].Name != "get_weather" || items[2].Arguments != `{"city":"Paris"}` {
		t.Fatalf("item2 = %+v", items[2])
	}
	if items[3].Type != "function_call_output" || items[3].CallID != "call_1" || string(items[3].Output) != `"sunny"` {
		t.Fatalf("item3 = %+v", items[3])
	}
}

func TestMessagesToInput_EmptyToolResultKeepsOutput(t *testing.T) {
	// A tool that produced no output must still serialize an `output` field:
	// the Responses API rejects a function_call_output item that omits it
	// ("Missing required parameter: 'input[N].output'").
	msgs := []llm.Message{{Role: llm.RoleUser, Blocks: []llm.Block{
		{Type: llm.BlockToolResult, ToolUseRef: "call_9", Content: ""},
	}}}
	items, err := messagesToInput(msgs)
	if err != nil {
		t.Fatalf("messagesToInput: %v", err)
	}
	if len(items) != 1 || items[0].Type != "function_call_output" {
		t.Fatalf("items = %+v", items)
	}
	if string(items[0].Output) != `""` {
		t.Fatalf("empty tool result output = %q, want \"\"", string(items[0].Output))
	}
	b, err := json.Marshal(items[0])
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got map[string]json.RawMessage
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if _, ok := got["output"]; !ok {
		t.Fatalf("serialized function_call_output missing `output` key: %s", b)
	}
}

func TestMessagesToInput_ImageURLPassthrough(t *testing.T) {
	msgs := []llm.Message{{Role: llm.RoleUser, Blocks: []llm.Block{
		{Type: llm.BlockImage, ImageURL: "https://x/y.png"},
	}}}
	items, err := messagesToInput(msgs)
	if err != nil {
		t.Fatalf("messagesToInput: %v", err)
	}
	if items[0].Content[0].ImageURL != "https://x/y.png" {
		t.Fatalf("url = %q", items[0].Content[0].ImageURL)
	}
}

func TestMessagesToInput_AllowsLargeInlineImages(t *testing.T) {
	largeImage := string(make([]byte, 5*1024*1024))
	msgs := []llm.Message{{Role: llm.RoleUser, Blocks: []llm.Block{
		{Type: llm.BlockText, Text: "see attached"},
		{Type: llm.BlockImage, MediaType: "image/png", ImageData: largeImage},
	}}}
	items, err := messagesToInput(msgs)
	if err != nil {
		t.Fatalf("messagesToInput rejected an intentional inline vision payload: %v", err)
	}
	if got := items[0].Content[1].ImageURL; !strings.Contains(got, largeImage) {
		t.Fatal("large inline image payload was not serialized for the vision-capable request")
	}
}

func TestToolsToResponses(t *testing.T) {
	tools := []llm.Tool{{Name: "get_weather", Description: "w", Schema: json.RawMessage(`{"type":"object"}`)}}
	got := toolsToResponses(tools)
	if len(got) != 1 || got[0].Type != "function" || got[0].Name != "get_weather" || string(got[0].Parameters) != `{"type":"object"}` {
		t.Fatalf("tools = %+v", got)
	}
	if toolsToResponses(nil) != nil {
		t.Fatal("nil tools should map to nil")
	}
}

func TestBlocksFromOutput(t *testing.T) {
	out := []outputItem{
		{Type: "reasoning", ID: "rs_1", EncryptedContent: "ENC"},
		{Type: "message", Role: "assistant", Content: []outputContent{{Type: "output_text", Text: "hello"}}},
		{Type: "function_call", CallID: "call_1", Name: "get_weather", Arguments: `{"city":"Paris"}`},
	}
	blocks := blocksFromOutput(out)
	if len(blocks) != 3 {
		t.Fatalf("want 3 blocks, got %d: %+v", len(blocks), blocks)
	}
	if blocks[0].Type != llm.BlockReasoning || blocks[0].ReasoningID != "rs_1" || blocks[0].ReasoningData != "ENC" {
		t.Fatalf("block0 = %+v", blocks[0])
	}
	if blocks[1].Type != llm.BlockText || blocks[1].Text != "hello" {
		t.Fatalf("block1 = %+v", blocks[1])
	}
	if blocks[2].Type != llm.BlockToolUse || blocks[2].ToolUseID != "call_1" || blocks[2].ToolName != "get_weather" || string(blocks[2].ToolInput) != `{"city":"Paris"}` {
		t.Fatalf("block2 = %+v", blocks[2])
	}
}
