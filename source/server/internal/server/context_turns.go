package server

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"cercano/source/server/internal/contextmeter"
	"cercano/source/server/internal/conversation"
	"cercano/source/server/internal/llm"
	"cercano/source/server/pkg/proto"
)

const contextTurnPreviewMax = 120

// GetConversationTurns returns side-effect-free, display-ready summaries of a
// conversation's turns for the /c context viewer. Reads the store only.
func (s *Server) GetConversationTurns(ctx context.Context, req *proto.GetConversationTurnsRequest) (*proto.GetConversationTurnsResponse, error) {
	out := &proto.GetConversationTurnsResponse{}
	if s.agent == nil {
		return out, nil
	}
	store := s.agent.PersistentStore()
	convID := req.GetConversationId()
	if store == nil || convID == "" {
		return out, nil
	}
	turns, err := store.GetTurns(ctx, convID)
	if err != nil {
		return nil, fmt.Errorf("get turns: %w", err)
	}
	tok := contextmeter.Default()
	for _, t := range turns {
		out.Turns = append(out.Turns, contextTurnView(t, tok))
	}
	return out, nil
}

// contextTurnView derives a display summary (kind, preview, token estimate) from
// a stored turn. Pure — no I/O. tool turns synthesize a label since their
// Content may be empty.
func contextTurnView(t conversation.Turn, tok contextmeter.Tokenizer) *proto.ContextTurn {
	kind := "text"
	preview := t.Content
	tokenSrc := t.Content

	if t.BlocksJSON != "" {
		var blocks []llm.Block
		if err := json.Unmarshal([]byte(t.BlocksJSON), &blocks); err == nil {
			tokenSrc = t.BlocksJSON
			for _, b := range blocks {
				switch b.Type {
				case llm.BlockToolUse:
					kind = "tool_use"
					preview = b.ToolName + " " + ctPreview(string(b.ToolInput))
				case llm.BlockToolResult:
					kind = "tool_result"
					preview = "→ " + ctPreview(b.Content)
				case llm.BlockText:
					if preview == "" {
						preview = b.Text
					}
				}
			}
		}
	}

	return &proto.ContextTurn{
		Role:      t.Role,
		Kind:      kind,
		Preview:   ctTruncate(ctPreview(preview), contextTurnPreviewMax),
		EstTokens: int32(tok.Count(tokenSrc)),
	}
}

func ctPreview(s string) string { return strings.Join(strings.Fields(s), " ") }

func ctTruncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}
