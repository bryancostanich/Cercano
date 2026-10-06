package conversation

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"cercano/source/server/internal/chatroute"
)

func (s *sqliteStore) ChatRoute(ctx context.Context, id string) (*chatroute.Route, error) {
	var route chatroute.Route
	err := s.db.QueryRowContext(ctx, `SELECT profile, model FROM conversation_chat_routes WHERE conversation_id=?`, id).Scan(&route.Profile, &route.Model)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &route, nil
}

func (s *sqliteStore) SetChatRoute(ctx context.Context, id string, route *chatroute.Route) error {
	if id == "" {
		return fmt.Errorf("conversation id required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if route == nil {
		_, err := s.db.ExecContext(ctx, `DELETE FROM conversation_chat_routes WHERE conversation_id=?`, id)
		return err
	}
	var exists int
	if err := s.db.QueryRowContext(ctx, `SELECT 1 FROM conversations WHERE id=?`, id).Scan(&exists); err != nil {
		return fmt.Errorf("conversation %q: %w", id, err)
	}
	if route.Profile == "" || route.Model == "" {
		return fmt.Errorf("profile and model required")
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO conversation_chat_routes (conversation_id, profile, model) VALUES (?, ?, ?) ON CONFLICT(conversation_id) DO UPDATE SET profile=excluded.profile, model=excluded.model`, id, route.Profile, route.Model)
	return err
}

var _ chatroute.Store = (*sqliteStore)(nil)
