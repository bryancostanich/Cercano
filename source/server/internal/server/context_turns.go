package server

import (
	"context"

	persistsvc "cercano/source/server/internal/hostsvc/persistence"
	"cercano/source/server/internal/contextmeter"
	"cercano/source/server/internal/conversation"
	"cercano/source/server/pkg/proto"
)

// GetConversationTurns implements proto.AgentServer — delegates to persistSvc.
func (s *Server) GetConversationTurns(ctx context.Context, req *proto.GetConversationTurnsRequest) (*proto.GetConversationTurnsResponse, error) {
	return s.persistSvc.GetConversationTurns(ctx, req)
}

// contextTurnView is a test shim — the canonical implementation lives in
// hostsvc/persistence as ContextTurnView. Tests in this package call
// contextTurnView (unexported); this delegates so they keep compiling.
func contextTurnView(t conversation.Turn, tok contextmeter.Tokenizer) *proto.ContextTurn {
	return persistsvc.ContextTurnView(t, tok)
}

// ctTruncate is a test shim — canonical implementation is persistence.CtTruncate.
func ctTruncate(s string, max int) string { return persistsvc.CtTruncate(s, max) }
