package openai

import (
	"encoding/json"
	"fmt"
	"strings"

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
		// A message whose blocks were all foreign (no representable content,
		// no tool calls) must be dropped whole: strict compat endpoints
		// reject empty messages, and empty turns are junk context elsewhere.
		if cm.Content == "" && len(cm.MultiContent) == 0 && len(cm.ToolCalls) == 0 {
			continue
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
	// Some local models (notably qwen3 served via mistral.rs) occasionally emit
	// their tool call twice: once as a correctly structured tool_call, and once
	// as a leading text block containing the raw JSON the chat template asked
	// for — a `[{"name": ..., "arguments": ...}]` array. The structured copy is
	// authoritative; the text copy is noise that would otherwise render in the
	// tab and, worse, poison the model on the next turn by feeding its own
	// malformed pattern back to it. Drop the text block when it parses as a
	// tool-call array and the same message carries a real tool_call.
	if m.Content != "" && !(len(m.ToolCalls) > 0 && looksLikeToolCallJSON(m.Content)) {
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

// looksLikeToolCallJSON reports whether s is (only) a JSON array of tool-call
// objects shaped like {"name": ..., "arguments": ...} — the format qwen3's chat
// template instructs the model to emit inside <tool_call> tags. It requires a
// clean full parse and a non-empty name on every element, so ordinary prose
// (which fails json.Unmarshal) is never suppressed. The "arguments" key is the
// template's wording; real parsed tool calls carry "input", never "arguments",
// so a legitimate assistant text block cannot match.
func looksLikeToolCallJSON(s string) bool {
	trimmed := strings.TrimSpace(s)
	if !strings.HasPrefix(trimmed, "[") {
		return false
	}
	var calls []struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal([]byte(trimmed), &calls); err != nil || len(calls) == 0 {
		return false
	}
	for _, c := range calls {
		if c.Name == "" {
			return false
		}
	}
	return true
}
