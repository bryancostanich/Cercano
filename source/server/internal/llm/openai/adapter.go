package openai

import (
	"fmt"

	goopenai "github.com/sashabaranov/go-openai"

	"cercano/source/server/internal/llm"
)

// messagesToOpenAI maps the block-based llm messages to OpenAI chat messages.
// system (when non-empty) becomes a leading system message.
func messagesToOpenAI(msgs []llm.Message, system string) []goopenai.ChatCompletionMessage {
	out := make([]goopenai.ChatCompletionMessage, 0, len(msgs)+1)
	if system != "" {
		out = append(out, goopenai.ChatCompletionMessage{Role: goopenai.ChatMessageRoleSystem, Content: system})
	}
	for _, m := range msgs {
		// A tool_result turn carries only tool results — OpenAI requires each to be its
		// own role:"tool" message. By protocol invariant such a message holds no other
		// block types; if it ever did, those would be skipped by the continue below.
		// A tool_result block becomes its own role:"tool" message.
		isToolResult := false
		for _, b := range m.Blocks {
			if b.Type == llm.BlockToolResult {
				out = append(out, goopenai.ChatCompletionMessage{
					Role: goopenai.ChatMessageRoleTool, ToolCallID: b.ToolUseRef, Content: b.Content,
				})
				isToolResult = true
			}
		}
		if isToolResult {
			continue
		}

		cm := goopenai.ChatCompletionMessage{Role: roleToOpenAI(m.Role)}
		var text string
		var parts []goopenai.ChatMessagePart
		for _, b := range m.Blocks {
			switch b.Type {
			case llm.BlockText:
				text += b.Text
				parts = append(parts, goopenai.ChatMessagePart{Type: goopenai.ChatMessagePartTypeText, Text: b.Text})
			case llm.BlockImage:
				url := b.ImageURL
				if url == "" {
					url = fmt.Sprintf("data:%s;base64,%s", b.MediaType, b.ImageData)
				}
				parts = append(parts, goopenai.ChatMessagePart{
					Type:     goopenai.ChatMessagePartTypeImageURL,
					ImageURL: &goopenai.ChatMessageImageURL{URL: url},
				})
			case llm.BlockToolUse:
				cm.ToolCalls = append(cm.ToolCalls, goopenai.ToolCall{
					ID: b.ToolUseID, Type: goopenai.ToolTypeFunction,
					Function: goopenai.FunctionCall{Name: b.ToolName, Arguments: string(b.ToolInput)},
				})
			}
		}
		// Use MultiContent only when there's an image; otherwise plain Content
		// (some compat endpoints reject MultiContent for text-only messages).
		hasImage := false
		for _, p := range parts {
			if p.Type == goopenai.ChatMessagePartTypeImageURL {
				hasImage = true
			}
		}
		if hasImage {
			cm.MultiContent = parts
		} else {
			cm.Content = text
		}
		out = append(out, cm)
	}
	return out
}

func roleToOpenAI(r llm.Role) string {
	switch r {
	case llm.RoleAssistant:
		return goopenai.ChatMessageRoleAssistant
	case llm.RoleSystem:
		return goopenai.ChatMessageRoleSystem
	default:
		return goopenai.ChatMessageRoleUser
	}
}

func toolsToOpenAI(tools []llm.Tool) []goopenai.Tool {
	out := make([]goopenai.Tool, 0, len(tools))
	for _, t := range tools {
		out = append(out, goopenai.Tool{
			Type: goopenai.ToolTypeFunction,
			Function: &goopenai.FunctionDefinition{
				Name: t.Name, Description: t.Description, Parameters: t.Schema,
			},
		})
	}
	return out
}

// blocksFromOpenAI maps a completed assistant message to llm blocks.
func blocksFromOpenAI(m goopenai.ChatCompletionMessage) []llm.Block {
	var blocks []llm.Block
	if m.Content != "" {
		blocks = append(blocks, llm.Block{Type: llm.BlockText, Text: m.Content})
	}
	for _, tc := range m.ToolCalls {
		blocks = append(blocks, llm.Block{
			Type: llm.BlockToolUse, ToolUseID: tc.ID, ToolName: tc.Function.Name,
			ToolInput: []byte(tc.Function.Arguments),
		})
	}
	return blocks
}
