package anthropic

import (
	"encoding/json"
	"fmt"

	sdk "github.com/anthropics/anthropic-sdk-go"

	"cercano/source/server/internal/llm"
)

// blockToSDK translates one internal block to the SDK union. ok=false means
// the block has no Anthropic representation — e.g. reasoning blocks recorded
// while the conversation ran on another provider — and must be skipped: the
// zero-value union marshals to a JSON null, which the Messages API rejects.
func blockToSDK(b llm.Block) (sdk.ContentBlockParamUnion, bool) {
	switch b.Type {
	case llm.BlockText:
		return sdk.NewTextBlock(b.Text), true
	case llm.BlockToolUse:
		return sdk.NewToolUseBlock(b.ToolUseID, b.ToolInput, b.ToolName), true
	case llm.BlockToolResult:
		return sdk.NewToolResultBlock(b.ToolUseRef, b.Content, b.IsError), true
	case llm.BlockImage:
		if b.ImageURL != "" {
			return sdk.NewImageBlock(sdk.URLImageSourceParam{URL: b.ImageURL}), true
		}
		return sdk.NewImageBlockBase64(b.MediaType, b.ImageData), true
	}
	return sdk.ContentBlockParamUnion{}, false
}

func blockFromSDK(in sdk.ContentBlockUnion) llm.Block {
	switch in.Type {
	case "text":
		return llm.Block{Type: llm.BlockText, Text: in.Text}
	case "tool_use":
		return llm.Block{
			Type:      llm.BlockToolUse,
			ToolUseID: in.ID,
			ToolName:  in.Name,
			ToolInput: in.Input,
		}
	}
	return llm.Block{}
}

func messagesToSDK(msgs []llm.Message) []sdk.MessageParam {
	out := make([]sdk.MessageParam, 0, len(msgs))
	for _, m := range msgs {
		blocks := make([]sdk.ContentBlockParamUnion, 0, len(m.Blocks))
		for _, b := range m.Blocks {
			if sb, ok := blockToSDK(b); ok {
				blocks = append(blocks, sb)
			}
		}
		// A message whose blocks were all skipped must be dropped whole:
		// the Messages API rejects empty content arrays.
		if len(blocks) == 0 {
			continue
		}
		switch m.Role {
		case llm.RoleUser:
			out = append(out, sdk.NewUserMessage(blocks...))
		case llm.RoleAssistant:
			out = append(out, sdk.NewAssistantMessage(blocks...))
		}
	}
	return out
}

func toolsToSDK(tools []llm.Tool) []sdk.ToolUnionParam {
	out := make([]sdk.ToolUnionParam, 0, len(tools))
	for _, t := range tools {
		var schema map[string]any
		if err := json.Unmarshal(t.Schema, &schema); err != nil {
			panic(fmt.Sprintf("anthropic: tool %q has invalid JSON schema: %v", t.Name, err))
		}
		schemaParam := sdk.ToolInputSchemaParam{
			Properties: schema["properties"],
		}
		if req, ok := schema["required"].([]any); ok {
			for _, r := range req {
				if s, ok := r.(string); ok {
					schemaParam.Required = append(schemaParam.Required, s)
				}
			}
		}
		out = append(out, sdk.ToolUnionParam{
			OfTool: &sdk.ToolParam{
				Name:        t.Name,
				Description: sdk.String(t.Description),
				InputSchema: schemaParam,
			},
		})
	}
	return out
}
