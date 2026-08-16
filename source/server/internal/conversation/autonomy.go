package conversation

import (
	"context"
	"errors"
	"strings"
	"time"
)

const defaultAutonomyState = "proposed"

// SaveAutonomyRun upserts the autonomy ledger row for a conversation. The
// caller owns the JSON payloads so this layer can stay lightweight while the
// autonomous protocol settles.
func (s *sqliteStore) SaveAutonomyRun(ctx context.Context, r AutonomyRun) error {
	if strings.TrimSpace(r.ConversationID) == "" {
		return errors.New("conversation id required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	if r.State == "" {
		r.State = defaultAutonomyState
	}
	if r.CreatedAt.IsZero() {
		r.CreatedAt = now
	}
	if r.UpdatedAt.IsZero() {
		r.UpdatedAt = now
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO autonomy_runs (
			conversation_id, state, source_kind, source_plan_path, source_spec_path,
			brief_json, revisions_json, decisions_json, review_json, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(conversation_id) DO UPDATE SET
			state=excluded.state,
			source_kind=excluded.source_kind,
			source_plan_path=excluded.source_plan_path,
			source_spec_path=excluded.source_spec_path,
			brief_json=excluded.brief_json,
			revisions_json=excluded.revisions_json,
			decisions_json=excluded.decisions_json,
			review_json=excluded.review_json,
			updated_at=excluded.updated_at`,
		r.ConversationID, r.State, r.SourceKind, r.SourcePlanPath, r.SourceSpecPath,
		r.BriefJSON, r.RevisionsJSON, r.DecisionsJSON, r.ReviewJSON,
		r.CreatedAt.Unix(), r.UpdatedAt.Unix())
	return err
}

// GetAutonomyRun fetches the persisted autonomy ledger row for a conversation.
func (s *sqliteStore) GetAutonomyRun(ctx context.Context, conversationID string) (AutonomyRun, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var r AutonomyRun
	var created, updated int64
	err := s.db.QueryRowContext(ctx, `
		SELECT conversation_id, state, source_kind, source_plan_path, source_spec_path,
		       brief_json, revisions_json, decisions_json, review_json, created_at, updated_at
		FROM autonomy_runs WHERE conversation_id = ?`, conversationID).Scan(
		&r.ConversationID, &r.State, &r.SourceKind, &r.SourcePlanPath, &r.SourceSpecPath,
		&r.BriefJSON, &r.RevisionsJSON, &r.DecisionsJSON, &r.ReviewJSON, &created, &updated)
	if err != nil {
		return AutonomyRun{}, err
	}
	r.CreatedAt = time.Unix(created, 0)
	r.UpdatedAt = time.Unix(updated, 0)
	return r, nil
}
