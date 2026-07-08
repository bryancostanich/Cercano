package responses

import (
	"encoding/json"
	"fmt"

	"cercano/source/server/internal/llm"
)

// messagesToInput maps llm messages to Responses input items, preserving order.
// Text and image blocks accumulate into a single "message" item per message;
// tool calls, tool results, and reasoning become their own items (flushing any
// pending message first so order is preserved).
func messagesToInput(msgs []llm.Message) []inputItem {
	var items []inputItem
	for _, m := range msgs {
		role := roleString(m.Role)
		// Assistant text replays as output_text: the Responses API models
		// assistant content as output, and the ChatGPT codex backend rejects
		// input_text on assistant messages outright (api.openai.com merely
		// tolerates it).
		textType := "input_text"
		if m.Role == llm.RoleAssistant {
			textType = "output_text"
		}
		var parts []contentPart
		flush := func() {
			if len(parts) > 0 {
				items = append(items, inputItem{Type: "message", Role: role, Content: parts})
				parts = nil
			}
		}
		for _, b := range m.Blocks {
			switch b.Type {
			case llm.BlockText:
				parts = append(parts, contentPart{Type: textType, Text: b.Text})
			case llm.BlockImage:
				url := b.ImageURL
				if url == "" {
					url = fmt.Sprintf("data:%s;base64,%s", b.MediaType, b.ImageData)
				}
				parts = append(parts, contentPart{Type: "input_image", ImageURL: url})
			case llm.BlockToolUse:
				flush()
				items = append(items, inputItem{Type: "function_call", CallID: b.ToolUseID, Name: b.ToolName, Arguments: string(b.ToolInput)})
			case llm.BlockToolResult:
				flush()
				items = append(items, inputItem{Type: "function_call_output", CallID: b.ToolUseRef, Output: b.Content})
			case llm.BlockReasoning:
				flush()
				items = append(items, inputItem{Type: "reasoning", ID: b.ReasoningID, EncryptedContent: b.ReasoningData})
			}
		}
		flush()
	}
	return items
}

func roleString(r llm.Role) string {
	if r == llm.RoleAssistant {
		return "assistant"
	}
	return "user"
}

func toolsToResponses(tools []llm.Tool) []tool {
	if len(tools) == 0 {
		return nil
	}
	out := make([]tool, 0, len(tools))
	for _, t := range tools {
		out = append(out, tool{Type: "function", Name: t.Name, Description: t.Description, Parameters: t.Schema})
	}
	return out
}

// blocksFromOutput maps Responses output items to llm blocks, preserving order.
func blocksFromOutput(out []outputItem) []llm.Block {
	var blocks []llm.Block
	for _, it := range out {
		switch it.Type {
		case "reasoning":
			blocks = append(blocks, llm.Block{Type: llm.BlockReasoning, ReasoningID: it.ID, ReasoningData: it.EncryptedContent})
		case "message":
			for _, c := range it.Content {
				if c.Type == "output_text" && c.Text != "" {
					blocks = append(blocks, llm.Block{Type: llm.BlockText, Text: c.Text})
				}
			}
		case "function_call":
			args := it.Arguments
			if args == "" {
				args = "{}"
			}
			blocks = append(blocks, llm.Block{Type: llm.BlockToolUse, ToolUseID: it.CallID, ToolName: it.Name, ToolInput: json.RawMessage(args)})
		}
	}
	return blocks
}
