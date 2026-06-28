package openai

import (
	"testing"

	goopenai "github.com/sashabaranov/go-openai"

	"cercano/source/server/internal/llm"
)

func TestMessagesToOpenAI_SystemAndText(t *testing.T) {
	msgs := messagesToOpenAI([]llm.Message{
		{Role: llm.RoleUser, Blocks: []llm.Block{{Type: llm.BlockText, Text: "hi"}}},
	}, "be terse")
	if len(msgs) != 2 || msgs[0].Role != goopenai.ChatMessageRoleSystem || msgs[0].Content != "be terse" {
		t.Fatalf("system msg wrong: %+v", msgs)
	}
	if msgs[1].Role != goopenai.ChatMessageRoleUser || msgs[1].Content != "hi" {
		t.Fatalf("user msg wrong: %+v", msgs[1])
	}
}

func TestMessagesToOpenAI_ImageURLAndBase64(t *testing.T) {
	msgs := messagesToOpenAI([]llm.Message{{Role: llm.RoleUser, Blocks: []llm.Block{
		{Type: llm.BlockText, Text: "look"},
		{Type: llm.BlockImage, ImageURL: "https://x/y.png"},
		{Type: llm.BlockImage, MediaType: "image/png", ImageData: "QUJD"},
	}}}, "")
	m := msgs[len(msgs)-1]
	if len(m.MultiContent) != 3 {
		t.Fatalf("want 3 parts, got %d", len(m.MultiContent))
	}
	if m.MultiContent[1].ImageURL.URL != "https://x/y.png" {
		t.Errorf("url part = %q", m.MultiContent[1].ImageURL.URL)
	}
	if m.MultiContent[2].ImageURL.URL != "data:image/png;base64,QUJD" {
		t.Errorf("data-uri part = %q", m.MultiContent[2].ImageURL.URL)
	}
}

func TestMessagesToOpenAI_ToolUseAndResult(t *testing.T) {
	msgs := messagesToOpenAI([]llm.Message{
		{Role: llm.RoleAssistant, Blocks: []llm.Block{{Type: llm.BlockToolUse, ToolUseID: "c1", ToolName: "read", ToolInput: []byte(`{"p":"x"}`)}}},
		{Role: llm.RoleUser, Blocks: []llm.Block{{Type: llm.BlockToolResult, ToolUseRef: "c1", Content: "FILE"}}},
	}, "")
	asst := msgs[0]
	if len(asst.ToolCalls) != 1 || asst.ToolCalls[0].ID != "c1" || asst.ToolCalls[0].Function.Name != "read" {
		t.Fatalf("assistant tool_call wrong: %+v", asst)
	}
	tool := msgs[1]
	if tool.Role != goopenai.ChatMessageRoleTool || tool.ToolCallID != "c1" || tool.Content != "FILE" {
		t.Fatalf("tool result wrong: %+v", tool)
	}
}
