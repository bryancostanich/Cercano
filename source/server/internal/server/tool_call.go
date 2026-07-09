package server

import (
	"context"
	"encoding/json"
	"fmt"

	"cercano/source/server/internal/llm"
	"cercano/source/server/pkg/proto"
)

// GetToolCall returns the full args and result body for one tool call in a
// conversation, looked up by tool_use_id. It reads content_json only (no side
// effects), and backs the CLI's lazy expand-on-click of a scrollback tool
// entry so large args/results are fetched on demand instead of streamed on
// every turn.
//
// found reports whether the tool_use block was located. Result may be empty
// even when found — the call can be in flight (tool_use persisted, tool_result
// not yet).
func (s *Server) GetToolCall(ctx context.Context, req *proto.GetToolCallRequest) (*proto.GetToolCallResponse, error) {
	out := &proto.GetToolCallResponse{}
	store := s.toolSvc.GetToolCallStore()
	convID := req.GetConversationId()
	useID := req.GetToolUseId()
	if store == nil || convID == "" || useID == "" {
		return out, nil
	}
	turns, err := store.GetTurns(ctx, convID)
	if err != nil {
		return nil, fmt.Errorf("get turns: %w", err)
	}
	for _, t := range turns {
		if t.BlocksJSON == "" {
			continue
		}
		var blocks []llm.Block
		if json.Unmarshal([]byte(t.BlocksJSON), &blocks) != nil {
			continue
		}
		for _, b := range blocks {
			switch b.Type {
			case llm.BlockToolUse:
				if b.ToolUseID == useID {
					out.Found = true
					out.ToolName = b.ToolName
					out.ArgsJson = string(b.ToolInput)
				}
			case llm.BlockToolResult:
				if b.ToolUseRef == useID {
					out.Result = b.Content
					out.IsError = b.IsError
					out.StartLine = int32(b.StartLine)
				}
			}
		}
	}
	return out, nil
}
