package server

import (
	"context"
	"errors"

	"cercano/source/server/internal/agent"
	"cercano/source/server/internal/contextedit"
	"cercano/source/server/internal/contextmeter"
	"cercano/source/server/internal/llm"
	"cercano/source/server/pkg/proto"
)

// ProposeContextEdit runs the picker model over the conversation's turn
// summaries and returns a validated deletion proposal. Read-only.
func (s *Server) ProposeContextEdit(ctx context.Context, req *proto.ProposeContextEditRequest) (*proto.ProposeContextEditResponse, error) {
	if s.agent == nil {
		return &proto.ProposeContextEditResponse{}, nil
	}
	store := s.agent.PersistentStore()
	convID := req.GetConversationId()
	if store == nil || convID == "" {
		return &proto.ProposeContextEditResponse{}, nil
	}
	turns, err := store.GetTurns(ctx, convID)
	if err != nil {
		return nil, err
	}
	tok := contextmeter.Default()
	summaries := make([]contextedit.TurnSummary, 0, len(turns))
	for _, t := range turns {
		ct := contextTurnView(t, tok)
		summaries = append(summaries, contextedit.TurnSummary{
			ID: ct.GetId(), Role: ct.GetRole(), Kind: ct.GetKind(), Preview: ct.GetPreview(),
		})
	}

	var local, cloud contextedit.CompleteFunc
	if s.localProvider != nil {
		local = func(ctx context.Context, prompt string) (string, error) {
			resp, err := s.localProvider.Process(ctx, &agent.Request{Input: prompt})
			if err != nil {
				return "", err
			}
			return resp.Output, nil
		}
	}
	if s.cloudLLMProvider != nil {
		cloudModel := s.activeCloudModel()
		cloud = func(ctx context.Context, prompt string) (string, error) {
			resp, err := s.cloudLLMProvider.Chat(ctx, llm.ChatRequest{
				Model:     cloudModel,
				Messages:  []llm.Message{{Role: llm.RoleUser, Blocks: []llm.Block{{Type: llm.BlockText, Text: prompt}}}},
				MaxTokens: 1024,
			})
			if err != nil {
				return "", err
			}
			var out string
			for _, b := range resp.Blocks {
				if b.Type == llm.BlockText {
					out += b.Text
				}
			}
			return out, nil
		}
	}
	if local == nil && cloud == nil {
		return nil, errors.New("no model available for context editing")
	}

	p, err := contextedit.Propose(ctx, req.GetInstruction(), summaries, local, cloud)
	if err != nil {
		return nil, err
	}
	return &proto.ProposeContextEditResponse{DeleteIds: p.DeleteIDs, Rationale: p.Rationale}, nil
}

// DeleteConversationTurns hard-deletes the named turns. The next tool-loop turn
// rebuilds history from the store, so the context shrinks automatically.
func (s *Server) DeleteConversationTurns(ctx context.Context, req *proto.DeleteConversationTurnsRequest) (*proto.DeleteConversationTurnsResponse, error) {
	if s.agent == nil {
		return &proto.DeleteConversationTurnsResponse{}, nil
	}
	store := s.agent.PersistentStore()
	convID := req.GetConversationId()
	if store == nil || convID == "" {
		return &proto.DeleteConversationTurnsResponse{}, nil
	}
	if err := store.DeleteTurns(ctx, convID, req.GetTurnId()); err != nil {
		return nil, err
	}
	return &proto.DeleteConversationTurnsResponse{Deleted: int32(len(req.GetTurnId()))}, nil
}
