package conversation

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

const defaultAutonomyState = "proposed"

func migrateAutonomyRunsToAppendOnly(db *sql.DB) error {
	exists, err := tableExists(db, "autonomy_runs")
	if err != nil || !exists {
		return err
	}
	hasRunID, err := tableHasColumn(db, "autonomy_runs", "run_id")
	if err != nil || hasRunID {
		return err
	}

	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`
		CREATE TABLE autonomy_runs_new (
			run_id            TEXT PRIMARY KEY,
			conversation_id   TEXT NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
			state             TEXT NOT NULL DEFAULT 'proposed',
			source_kind       TEXT NOT NULL DEFAULT '',
			source_plan_path  TEXT NOT NULL DEFAULT '',
			source_spec_path  TEXT NOT NULL DEFAULT '',
			brief_json        TEXT NOT NULL DEFAULT '',
			revisions_json    TEXT NOT NULL DEFAULT '',
			decisions_json    TEXT NOT NULL DEFAULT '',
			review_json       TEXT NOT NULL DEFAULT '',
			created_at        INTEGER NOT NULL,
			updated_at        INTEGER NOT NULL
		)`); err != nil {
		return err
	}
	if _, err := tx.Exec(`
		INSERT INTO autonomy_runs_new (
			run_id, conversation_id, state, source_kind, source_plan_path, source_spec_path,
			brief_json, revisions_json, decisions_json, review_json, created_at, updated_at
		)
		SELECT lower(hex(randomblob(12))), conversation_id, state, source_kind, source_plan_path, source_spec_path,
		       brief_json, revisions_json, decisions_json, review_json, created_at, updated_at
		FROM autonomy_runs
	`); err != nil {
		return err
	}
	if _, err := tx.Exec(`DROP TABLE autonomy_runs`); err != nil {
		return err
	}
	if _, err := tx.Exec(`ALTER TABLE autonomy_runs_new RENAME TO autonomy_runs`); err != nil {
		return err
	}
	if _, err := tx.Exec(`CREATE INDEX idx_autonomy_runs_conversation_updated ON autonomy_runs(conversation_id, updated_at DESC, created_at DESC, run_id DESC)`); err != nil {
		return err
	}
	if _, err := tx.Exec(`CREATE UNIQUE INDEX idx_autonomy_runs_one_active ON autonomy_runs(conversation_id) WHERE state IN ('running', 'review_pending')`); err != nil {
		return err
	}
	return tx.Commit()
}

func tableExists(db *sql.DB, name string) (bool, error) {
	var n int
	err := db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='table' AND name=?`, name).Scan(&n)
	return n > 0, err
}

func tableHasColumn(db *sql.DB, table, column string) (bool, error) {
	rows, err := db.Query(`PRAGMA table_info(` + table + `)`)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, ctype string
		var notnull, pk int
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			return false, err
		}
		if name == column {
			return true, nil
		}
	}
	return false, rows.Err()
}

// CreateAutonomyRun inserts one append-only autonomy ledger row. The database
// unique index enforces at most one running/review_pending run per conversation.
func (s *sqliteStore) CreateAutonomyRun(ctx context.Context, r AutonomyRun) (AutonomyRun, error) {
	if strings.TrimSpace(r.ConversationID) == "" {
		return AutonomyRun{}, errors.New("conversation id required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	r = normalizeAutonomyRunForCreate(r)
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO autonomy_runs (
			run_id, conversation_id, state, source_kind, source_plan_path, source_spec_path,
			brief_json, revisions_json, decisions_json, review_json, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		r.RunID, r.ConversationID, r.State, r.SourceKind, r.SourcePlanPath, r.SourceSpecPath,
		r.BriefJSON, r.RevisionsJSON, r.DecisionsJSON, r.ReviewJSON,
		r.CreatedAt.Unix(), r.UpdatedAt.Unix())
	if err != nil {
		return AutonomyRun{}, err
	}
	return r, nil
}

// UpdateAutonomyRun updates an existing autonomy run by run id. It never creates
// a new row and never chooses a row by conversation id.
func (s *sqliteStore) UpdateAutonomyRun(ctx context.Context, r AutonomyRun) error {
	if strings.TrimSpace(r.RunID) == "" {
		return errors.New("autonomy run id required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	if r.State == "" {
		r.State = defaultAutonomyState
	}
	if r.UpdatedAt.IsZero() {
		r.UpdatedAt = time.Now()
	}
	res, err := s.db.ExecContext(ctx, `
		UPDATE autonomy_runs SET
			state=?, source_kind=?, source_plan_path=?, source_spec_path=?,
			brief_json=?, revisions_json=?, decisions_json=?, review_json=?, updated_at=?
		WHERE run_id=?`,
		r.State, r.SourceKind, r.SourcePlanPath, r.SourceSpecPath,
		r.BriefJSON, r.RevisionsJSON, r.DecisionsJSON, r.ReviewJSON, r.UpdatedAt.Unix(), r.RunID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func normalizeAutonomyRunForCreate(r AutonomyRun) AutonomyRun {
	now := time.Now()
	if strings.TrimSpace(r.RunID) == "" {
		r.RunID = newID()
	}
	if r.State == "" {
		r.State = defaultAutonomyState
	}
	if r.CreatedAt.IsZero() {
		r.CreatedAt = now
	}
	if r.UpdatedAt.IsZero() {
		r.UpdatedAt = r.CreatedAt
	}
	return r
}

// GetActiveAutonomyRun fetches the current running/review_pending autonomy run.
func (s *sqliteStore) GetActiveAutonomyRun(ctx context.Context, conversationID string) (AutonomyRun, error) {
	return s.getAutonomyRun(ctx, `
		SELECT run_id, conversation_id, state, source_kind, source_plan_path, source_spec_path,
		       brief_json, revisions_json, decisions_json, review_json, created_at, updated_at
		FROM autonomy_runs
		WHERE conversation_id = ? AND state IN ('running', 'review_pending')
		ORDER BY updated_at DESC, created_at DESC, run_id DESC
		LIMIT 1`, conversationID)
}

// GetLatestAutonomyRun fetches the newest autonomy run for a conversation.
func (s *sqliteStore) GetLatestAutonomyRun(ctx context.Context, conversationID string) (AutonomyRun, error) {
	return s.getAutonomyRun(ctx, `
		SELECT run_id, conversation_id, state, source_kind, source_plan_path, source_spec_path,
		       brief_json, revisions_json, decisions_json, review_json, created_at, updated_at
		FROM autonomy_runs
		WHERE conversation_id = ?
		ORDER BY updated_at DESC, created_at DESC, run_id DESC
		LIMIT 1`, conversationID)
}

// ListAutonomyRuns returns all autonomy runs for a conversation, newest first.
func (s *sqliteStore) ListAutonomyRuns(ctx context.Context, conversationID string) ([]AutonomyRun, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	rows, err := s.db.QueryContext(ctx, `
		SELECT run_id, conversation_id, state, source_kind, source_plan_path, source_spec_path,
		       brief_json, revisions_json, decisions_json, review_json, created_at, updated_at
		FROM autonomy_runs
		WHERE conversation_id = ?
		ORDER BY updated_at DESC, created_at DESC, run_id DESC`, conversationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AutonomyRun
	for rows.Next() {
		r, err := scanAutonomyRun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *sqliteStore) getAutonomyRun(ctx context.Context, query, conversationID string) (AutonomyRun, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return scanAutonomyRun(s.db.QueryRowContext(ctx, query, conversationID))
}

type autonomyRunScanner interface {
	Scan(dest ...any) error
}

func scanAutonomyRun(row autonomyRunScanner) (AutonomyRun, error) {
	var r AutonomyRun
	var created, updated int64
	if err := row.Scan(
		&r.RunID, &r.ConversationID, &r.State, &r.SourceKind, &r.SourcePlanPath, &r.SourceSpecPath,
		&r.BriefJSON, &r.RevisionsJSON, &r.DecisionsJSON, &r.ReviewJSON, &created, &updated,
	); err != nil {
		return AutonomyRun{}, err
	}
	r.CreatedAt = time.Unix(created, 0)
	r.UpdatedAt = time.Unix(updated, 0)
	return r, nil
}

// SaveAutonomyRun is a deprecated compatibility shim. It keeps append-only
// semantics: a missing RunID creates a new row; a present RunID updates that row.
func (s *sqliteStore) SaveAutonomyRun(ctx context.Context, r AutonomyRun) error {
	if strings.TrimSpace(r.RunID) == "" {
		_, err := s.CreateAutonomyRun(ctx, r)
		return err
	}
	return s.UpdateAutonomyRun(ctx, r)
}

// GetAutonomyRun is a deprecated compatibility shim that returns the latest run.
func (s *sqliteStore) GetAutonomyRun(ctx context.Context, conversationID string) (AutonomyRun, error) {
	r, err := s.GetLatestAutonomyRun(ctx, conversationID)
	if err != nil {
		return AutonomyRun{}, fmt.Errorf("get latest autonomy run: %w", err)
	}
	return r, nil
}
