package bedrock

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/document"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"

	"cercano/source/server/internal/llm"
)

// jsonToDocument wraps raw JSON as a Converse document (for request bodies).
func jsonToDocument(raw json.RawMessage) document.Interface {
	if len(raw) == 0 {
		return document.NewLazyDocument(map[string]any{})
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return document.NewLazyDocument(map[string]any{})
	}
	return document.NewLazyDocument(v)
}

// documentToJSON serializes a Converse document back to JSON. It uses
// MarshalSmithyDocument — the only round-trip-safe method (UnmarshalSmithyDocument
// into any/json.RawMessage errors and corrupts numbers to strings).
func documentToJSON(d document.Interface) json.RawMessage {
	if d == nil {
		return json.RawMessage("{}")
	}
	b, err := d.MarshalSmithyDocument()
	if err != nil || len(b) == 0 {
		return json.RawMessage("{}")
	}
	return json.RawMessage(b)
}

// imageFormat maps a media type (or sniffed bytes) to a Converse image format.
func imageFormat(mediaType string, data []byte) types.ImageFormat {
	mt := mediaType
	if mt == "" {
		mt = http.DetectContentType(data)
	}
	switch {
	case strings.Contains(mt, "png"):
		return types.ImageFormatPng
	case strings.Contains(mt, "jpeg"), strings.Contains(mt, "jpg"):
		return types.ImageFormatJpeg
	case strings.Contains(mt, "gif"):
		return types.ImageFormatGif
	case strings.Contains(mt, "webp"):
		return types.ImageFormatWebp
	default:
		return types.ImageFormatPng
	}
}

// messagesToConverse maps llm messages to Converse messages, resolving image
// blocks to raw bytes (Converse takes bytes, not URLs/base64).
func messagesToConverse(ctx context.Context, msgs []llm.Message) ([]types.Message, error) {
	out := make([]types.Message, 0, len(msgs))
	for _, m := range msgs {
		role := types.ConversationRoleUser
		if m.Role == llm.RoleAssistant {
			role = types.ConversationRoleAssistant
		}
		var content []types.ContentBlock
		for _, b := range m.Blocks {
			switch b.Type {
			case llm.BlockText:
				content = append(content, &types.ContentBlockMemberText{Value: b.Text})
			case llm.BlockImage:
				data, err := llm.ResolveImageBytes(ctx, b)
				if err != nil {
					return nil, fmt.Errorf("bedrock: resolve image: %w", err)
				}
				content = append(content, &types.ContentBlockMemberImage{Value: types.ImageBlock{
					Format: imageFormat(b.MediaType, data),
					Source: &types.ImageSourceMemberBytes{Value: data},
				}})
			case llm.BlockToolUse:
				content = append(content, &types.ContentBlockMemberToolUse{Value: types.ToolUseBlock{
					ToolUseId: aws.String(b.ToolUseID),
					Name:      aws.String(b.ToolName),
					Input:     jsonToDocument(b.ToolInput),
				}})
			case llm.BlockToolResult:
				status := types.ToolResultStatusSuccess
				if b.IsError {
					status = types.ToolResultStatusError
				}
				content = append(content, &types.ContentBlockMemberToolResult{Value: types.ToolResultBlock{
					ToolUseId: aws.String(b.ToolUseRef),
					Status:    status,
					Content:   []types.ToolResultContentBlock{&types.ToolResultContentBlockMemberText{Value: b.Content}},
				}})
			}
		}
		// A message whose blocks were all foreign is dropped whole: Converse
		// rejects empty content with a ValidationException.
		if len(content) == 0 {
			continue
		}
		out = append(out, types.Message{Role: role, Content: content})
	}
	return out, nil
}

func systemBlocks(system string) []types.SystemContentBlock {
	if system == "" {
		return nil
	}
	return []types.SystemContentBlock{&types.SystemContentBlockMemberText{Value: system}}
}

func toolsToConverse(tools []llm.Tool) *types.ToolConfiguration {
	if len(tools) == 0 {
		return nil
	}
	out := make([]types.Tool, 0, len(tools))
	for _, t := range tools {
		out = append(out, &types.ToolMemberToolSpec{Value: types.ToolSpecification{
			Name:        aws.String(t.Name),
			Description: aws.String(t.Description),
			InputSchema: &types.ToolInputSchemaMemberJson{Value: jsonToDocument(t.Schema)},
		}})
	}
	return &types.ToolConfiguration{Tools: out}
}

func inferenceConfig(req llm.ChatRequest) *types.InferenceConfiguration {
	if req.MaxTokens <= 0 && req.Temperature <= 0 {
		return nil
	}
	ic := &types.InferenceConfiguration{}
	if req.MaxTokens > 0 {
		ic.MaxTokens = aws.Int32(int32(req.MaxTokens))
	}
	if req.Temperature > 0 {
		t := float32(req.Temperature)
		ic.Temperature = &t
	}
	return ic
}

// blocksFromConverse maps a Converse output message to llm blocks.
func blocksFromConverse(m types.Message) []llm.Block {
	var blocks []llm.Block
	for _, c := range m.Content {
		switch v := c.(type) {
		case *types.ContentBlockMemberText:
			if v.Value != "" {
				blocks = append(blocks, llm.Block{Type: llm.BlockText, Text: v.Value})
			}
		case *types.ContentBlockMemberToolUse:
			blocks = append(blocks, llm.Block{
				Type:      llm.BlockToolUse,
				ToolUseID: aws.ToString(v.Value.ToolUseId),
				ToolName:  aws.ToString(v.Value.Name),
				ToolInput: documentToJSON(v.Value.Input),
			})
		}
	}
	return blocks
}
