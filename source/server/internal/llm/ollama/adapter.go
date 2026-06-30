package ollama

import (
	"context"
	"encoding/json"
	"fmt"

	api "github.com/ollama/ollama/api"

	"cercano/source/server/internal/llm"
)

func messageToOllama(ctx context.Context, m llm.Message) (api.Message, error) {
	for _, b := range m.Blocks {
		if b.Type == llm.BlockToolResult {
			return api.Message{Role: "tool", Content: b.Content, ToolCallID: b.ToolUseRef}, nil
		}
	}

	out := api.Message{}
	switch m.Role {
	case llm.RoleAssistant:
		out.Role = "assistant"
	case llm.RoleSystem:
		out.Role = "system"
	default:
		out.Role = "user"
	}

	var text string
	for _, b := range m.Blocks {
		switch b.Type {
		case llm.BlockText:
			text += b.Text
		case llm.BlockImage:
			data, err := llm.ResolveImageBytes(ctx, b)
			if err != nil {
				return api.Message{}, fmt.Errorf("ollama image: %w", err)
			}
			out.Images = append(out.Images, api.ImageData(data))
		case llm.BlockToolUse:
			args := api.NewToolCallFunctionArguments()
			if len(b.ToolInput) > 0 {
				var raw map[string]any
				if err := json.Unmarshal(b.ToolInput, &raw); err != nil {
					panic(fmt.Sprintf("ollama: tool_use %q has invalid JSON input: %v", b.ToolName, err))
				}
				for k, v := range raw {
					args.Set(k, v)
				}
			}
			out.ToolCalls = append(out.ToolCalls, api.ToolCall{
				Function: api.ToolCallFunction{Name: b.ToolName, Arguments: args},
			})
		}
	}
	out.Content = text
	return out, nil
}

func toolsToOllama(tools []llm.Tool) []api.Tool {
	out := make([]api.Tool, 0, len(tools))
	for _, t := range tools {
		var schema map[string]any
		if err := json.Unmarshal(t.Schema, &schema); err != nil {
			panic(fmt.Sprintf("ollama: tool %q has invalid JSON schema: %v", t.Name, err))
		}
		params := api.ToolFunctionParameters{Type: "object"}
		if s, ok := schema["type"].(string); ok {
			params.Type = s
		}
		if req, ok := schema["required"].([]any); ok {
			for _, r := range req {
				if s, ok := r.(string); ok {
					params.Required = append(params.Required, s)
				}
			}
		}
		if props, ok := schema["properties"].(map[string]any); ok {
			pm := api.NewToolPropertiesMap()
			for name, raw := range props {
				bts, err := json.Marshal(raw)
				if err != nil {
					panic(fmt.Sprintf("ollama: tool %q property %q marshal: %v", t.Name, name, err))
				}
				var prop api.ToolProperty
				if err := json.Unmarshal(bts, &prop); err != nil {
					panic(fmt.Sprintf("ollama: tool %q property %q unmarshal: %v", t.Name, name, err))
				}
				pm.Set(name, prop)
			}
			params.Properties = pm
		}
		out = append(out, api.Tool{
			Type: "function",
			Function: api.ToolFunction{
				Name:        t.Name,
				Description: t.Description,
				Parameters:  params,
			},
		})
	}
	return out
}
