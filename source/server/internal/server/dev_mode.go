package server

import (
	"context"
	"fmt"
	"path/filepath"

	"cercano/source/server/pkg/proto"
)

// Persist the client's explicit /dev state before running its turn. Ordinary
// requests (including older clients) cannot silently erase a saved dev session.
func (s *Server) persistDevMode(ctx context.Context, req *proto.ProcessRequestRequest) error {
	if !req.GetDebugMode() || req.GetConversationId() == "" || req.GetWorkDir() == "" {
		return nil
	}
	if !filepath.IsAbs(req.GetWorkDir()) {
		return fmt.Errorf("development mode requires an absolute working directory")
	}
	if s.agent == nil || s.agent.PersistentStore() == nil {
		return nil
	}
	store := s.agent.PersistentStore()
	if err := store.EnsureConversation(ctx, req.GetConversationId(), req.GetWorkDir(), ""); err != nil {
		return fmt.Errorf("persist dev mode: %w", err)
	}
	if err := store.SetDevWorkDir(ctx, req.GetConversationId(), req.GetWorkDir()); err != nil {
		return fmt.Errorf("persist dev mode: %w", err)
	}
	return nil
}
