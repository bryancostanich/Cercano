// Package conversation persists multi-turn chat history to SQLite so the CLI
// (and any other client) can /history list and /resume across server restarts.
//
// Schema lives in schema.sql, embedded into the binary. Default DB path is
// ~/.config/cercano/conversations.db; override with $CERCANO_CONVERSATIONS_DB.
package conversation

import (
	"context"
	"crypto/rand"
	_ "embed"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"

	"database/sql"
)

//go:embed schema.sql
var schemaSQL string

// Info is the lightweight conversation summary returned by List.
type Info struct {
	ID         string
	Title      string
	ProjectDir string
	Model      string
	StartedAt  time.Time
	LastTurnAt time.Time
	TurnCount  int

	Recap          string
	RecapUpdatedAt time.Time
}

// Turn is one persisted role-emission inside a conversation.
type Turn struct {
	ID             string
	ConversationID string
	Role           string // "user" | "assistant" | "system"
	Content        string
	BlocksJSON     string // Anthropic-format block array for tool_use/tool_result turns; empty for text-only.
	TokensIn       int
	TokensOut      int
	LatencyMs      int
	CreatedAt      time.Time
}

// Store is the persistent conversation store interface. The runtime
// implementation is SQLite-backed (modernc.org/sqlite, pure Go — no cgo).
type Store interface {
	// EnsureConversation idempotently creates the conversation row. Title is
	// auto-derived from the first user turn (deferred to first Append).
	EnsureConversation(ctx context.Context, id, projectDir, model string) error

	// Append records one turn. Updates the conversation's last_turn_at. If
	// this is the first user turn and the conversation has no title, derives
	// one algorithmically from the content.
	Append(ctx context.Context, t Turn) error

	// List returns conversations matching projectDir (empty matches all),
	// sorted by last_turn_at desc, capped at limit (0 = no cap).
	List(ctx context.Context, projectDir string, limit int) ([]Info, error)

	// GetTurns returns all turns for a conversation, ordered by created_at.
	GetTurns(ctx context.Context, conversationID string) ([]Turn, error)

	// Delete removes a conversation and (via FK cascade) all its turns.
	Delete(ctx context.Context, conversationID string) error

	// Rename sets a custom title for a conversation. Distinct from the
	// auto-derived title set on first user turn — a user-chosen title is
	// never overwritten by subsequent appends.
	Rename(ctx context.Context, conversationID, title string) error

	// SetGeneratedTitle sets an auto-generated title, but only when the
	// conversation's title was not user-chosen (title_source != 'user').
	// A no-op for user-renamed conversations. Idempotent.
	SetGeneratedTitle(ctx context.Context, conversationID, title string) error

	// UpdateRecap sets the LLM-generated living recap and its timestamp.
	UpdateRecap(ctx context.Context, conversationID, recap string) error

	// DeleteTurns removes the named turns from a conversation. Unknown ids are
	// ignored (idempotent); other conversations are never affected.
	DeleteTurns(ctx context.Context, conversationID string, ids []string) error

	// Get returns a single conversation's Info, or an error if not found.
	Get(ctx context.Context, conversationID string) (Info, error)

	// Close releases the underlying DB handle.
	Close() error
}

// DefaultPath returns the default conversations DB location:
// $CERCANO_CONVERSATIONS_DB if set, otherwise ~/.config/cercano/conversations.db.
// The parent directory is created if missing.
func DefaultPath() (string, error) {
	if v := os.Getenv("CERCANO_CONVERSATIONS_DB"); v != "" {
		return v, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, ".config", "cercano")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return filepath.Join(dir, "conversations.db"), nil
}

// sqliteStore is the modernc.org/sqlite-backed Store.
type sqliteStore struct {
	mu sync.Mutex
	db *sql.DB
}

// Open returns a Store backed by SQLite at the given path. ":memory:" works
// for tests. Schema is applied on every Open (CREATE IF NOT EXISTS).
func Open(path string) (Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite at %s: %w", path, err)
	}
	// WAL gives concurrent readers while one writer commits — useful when
	// the agent writes turns while a UI fetches /history.
	if _, err := db.Exec("PRAGMA journal_mode = WAL; PRAGMA foreign_keys = ON;"); err != nil {
		db.Close()
		return nil, fmt.Errorf("pragma setup: %w", err)
	}
	if _, err := db.Exec(schemaSQL); err != nil {
		db.Close()
		return nil, fmt.Errorf("apply schema: %w", err)
	}
	if err := migrateContentJSON(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate content_json: %w", err)
	}
	// Migrate pre-existing DBs that predate the recap columns. ALTER fails
	// with "duplicate column name" once applied — ignore that case.
	for _, alter := range []string{
		`ALTER TABLE conversations ADD COLUMN recap TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE conversations ADD COLUMN recap_updated_at INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE conversations ADD COLUMN title_source TEXT NOT NULL DEFAULT 'user'`,
	} {
		if _, err := db.Exec(alter); err != nil && !strings.Contains(err.Error(), "duplicate column") {
			db.Close()
			return nil, fmt.Errorf("migrate recap columns: %w", err)
		}
	}
	return &sqliteStore{db: db}, nil
}

// migrateContentJSON adds the content_json column if missing. SQLite's
// ALTER TABLE ADD COLUMN has no IF NOT EXISTS, so we probe PRAGMA first.
func migrateContentJSON(db *sql.DB) error {
	rows, err := db.Query(`PRAGMA table_info(turns)`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, ctype string
		var notnull, pk int
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			return err
		}
		if name == "content_json" {
			return nil
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	_, err = db.Exec(`ALTER TABLE turns ADD COLUMN content_json TEXT NOT NULL DEFAULT ''`)
	return err
}

func newID() string {
	var b [12]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// DeriveTitle returns a short title from the first user prompt. Algorithmic
// (no LLM): drop leading slash commands, trim, take up to 60 chars at the
// last whitespace boundary, append `…` if truncated.
func DeriveTitle(prompt string) string {
	p := strings.TrimSpace(prompt)
	if strings.HasPrefix(p, "/") {
		// Slash commands shouldn't seed conversation titles.
		return ""
	}
	const maxChars = 60
	if len(p) <= maxChars {
		return p
	}
	cut := p[:maxChars]
	if idx := strings.LastIndex(cut, " "); idx > 20 {
		cut = cut[:idx]
	}
	return cut + "…"
}

func (s *sqliteStore) EnsureConversation(ctx context.Context, id, projectDir, model string) error {
	if id == "" {
		return errors.New("conversation id required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().Unix()
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO conversations (id, project_dir, model, title_source, started_at, last_turn_at)
		VALUES (?, ?, ?, 'auto', ?, ?)
		ON CONFLICT(id) DO NOTHING`,
		id, projectDir, model, now, now)
	return err
}

func (s *sqliteStore) Append(ctx context.Context, t Turn) error {
	if t.ConversationID == "" {
		return errors.New("conversation id required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	turnID := t.ID
	if turnID == "" {
		turnID = newID()
	}
	createdAt := t.CreatedAt.Unix()
	if t.CreatedAt.IsZero() {
		createdAt = time.Now().Unix()
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO turns (id, conversation_id, role, content, content_json, tokens_in, tokens_out, latency_ms, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		turnID, t.ConversationID, t.Role, t.Content, t.BlocksJSON, t.TokensIn, t.TokensOut, t.LatencyMs, createdAt,
	); err != nil {
		return fmt.Errorf("insert turn: %w", err)
	}

	// Update conversation last_turn_at; derive title from the first user turn
	// if missing.
	if _, err := tx.ExecContext(ctx, `
		UPDATE conversations SET last_turn_at = ? WHERE id = ?`,
		createdAt, t.ConversationID,
	); err != nil {
		return fmt.Errorf("update conv last_turn_at: %w", err)
	}
	// Auto-derive a title from the first user turn only if no title exists
	// yet. Once set (either auto-derived or user-renamed), it sticks — never
	// overwritten by later turns.
	if t.Role == "user" {
		title := DeriveTitle(t.Content)
		if title != "" {
			if _, err := tx.ExecContext(ctx, `
				UPDATE conversations SET title = ?
				WHERE id = ? AND (title IS NULL OR title = '')`,
				title, t.ConversationID,
			); err != nil {
				return fmt.Errorf("update title: %w", err)
			}
		}
	}

	return tx.Commit()
}

func (s *sqliteStore) List(ctx context.Context, projectDir string, limit int) ([]Info, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	query := `
		SELECT c.id, c.title, c.project_dir, c.model, c.started_at, c.last_turn_at,
		       c.recap, c.recap_updated_at,
		       (SELECT COUNT(*) FROM turns t WHERE t.conversation_id = c.id) AS turn_count
		FROM conversations c
		WHERE (? = '' OR c.project_dir = ?)
		ORDER BY c.last_turn_at DESC`
	args := []any{projectDir, projectDir}
	if limit > 0 {
		query += " LIMIT ?"
		args = append(args, limit)
	}

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Info
	for rows.Next() {
		var info Info
		var startedAt, lastTurnAt, recapAt int64
		if err := rows.Scan(&info.ID, &info.Title, &info.ProjectDir, &info.Model,
			&startedAt, &lastTurnAt, &info.Recap, &recapAt, &info.TurnCount); err != nil {
			return nil, err
		}
		info.StartedAt = time.Unix(startedAt, 0)
		info.LastTurnAt = time.Unix(lastTurnAt, 0)
		if recapAt > 0 {
			info.RecapUpdatedAt = time.Unix(recapAt, 0)
		}
		out = append(out, info)
	}
	return out, rows.Err()
}

func (s *sqliteStore) GetTurns(ctx context.Context, conversationID string) ([]Turn, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	rows, err := s.db.QueryContext(ctx, `
		SELECT id, conversation_id, role, content, content_json, tokens_in, tokens_out, latency_ms, created_at
		FROM turns
		WHERE conversation_id = ?
		ORDER BY created_at ASC, rowid ASC`, conversationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Turn
	for rows.Next() {
		var t Turn
		var createdAt int64
		if err := rows.Scan(&t.ID, &t.ConversationID, &t.Role, &t.Content, &t.BlocksJSON,
			&t.TokensIn, &t.TokensOut, &t.LatencyMs, &createdAt); err != nil {
			return nil, err
		}
		t.CreatedAt = time.Unix(createdAt, 0)
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s *sqliteStore) Delete(ctx context.Context, conversationID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.ExecContext(ctx, `DELETE FROM conversations WHERE id = ?`, conversationID)
	return err
}

func (s *sqliteStore) DeleteTurns(ctx context.Context, conversationID string, ids []string) error {
	if conversationID == "" {
		return errors.New("conversation id required")
	}
	if len(ids) == 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	placeholders := make([]string, len(ids))
	args := make([]any, 0, len(ids)+1)
	args = append(args, conversationID)
	for i, id := range ids {
		placeholders[i] = "?"
		args = append(args, id)
	}
	query := "DELETE FROM turns WHERE conversation_id = ? AND id IN (" + strings.Join(placeholders, ",") + ")"
	_, err := s.db.ExecContext(ctx, query, args...)
	return err
}

func (s *sqliteStore) Rename(ctx context.Context, conversationID, title string) error {
	if conversationID == "" {
		return errors.New("conversation id required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.ExecContext(ctx,
		`UPDATE conversations SET title = ?, title_source = 'user' WHERE id = ?`,
		title, conversationID)
	return err
}

func (s *sqliteStore) SetGeneratedTitle(ctx context.Context, conversationID, title string) error {
	if conversationID == "" {
		return errors.New("conversation id required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.ExecContext(ctx,
		`UPDATE conversations SET title = ? WHERE id = ? AND title_source != 'user'`,
		title, conversationID)
	return err
}

func (s *sqliteStore) UpdateRecap(ctx context.Context, conversationID, recap string) error {
	if conversationID == "" {
		return errors.New("conversation id required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.ExecContext(ctx,
		`UPDATE conversations SET recap = ?, recap_updated_at = ? WHERE id = ?`,
		recap, time.Now().Unix(), conversationID)
	return err
}

func (s *sqliteStore) Get(ctx context.Context, conversationID string) (Info, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var info Info
	var startedAt, lastTurnAt, recapAt int64
	err := s.db.QueryRowContext(ctx, `
		SELECT c.id, c.title, c.project_dir, c.model, c.started_at, c.last_turn_at,
		       c.recap, c.recap_updated_at,
		       (SELECT COUNT(*) FROM turns t WHERE t.conversation_id = c.id) AS turn_count
		FROM conversations c WHERE c.id = ?`, conversationID).
		Scan(&info.ID, &info.Title, &info.ProjectDir, &info.Model,
			&startedAt, &lastTurnAt, &info.Recap, &recapAt, &info.TurnCount)
	if err != nil {
		return Info{}, err
	}
	info.StartedAt = time.Unix(startedAt, 0)
	info.LastTurnAt = time.Unix(lastTurnAt, 0)
	if recapAt > 0 {
		info.RecapUpdatedAt = time.Unix(recapAt, 0)
	}
	return info, nil
}

func (s *sqliteStore) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.db.Close()
}
