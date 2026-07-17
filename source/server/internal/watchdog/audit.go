package watchdog

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

// AuditEvent is one durable record of a watchdog check evaluation or
// resolution. Text-heavy fields are optional so callers can choose whether to
// persist prompts/responses or only hashes and summaries.
type AuditEvent struct {
	ID              string
	Timestamp       time.Time
	ConversationID  string
	EventType       string // "evaluation" | "resolution"
	Check           string
	ActionKind      string
	ToolName        string
	ToolArgsHash    string
	ToolArgsSummary string
	Applies         bool
	Protocol        string
	Violation       bool
	Challenge       string
	Revise          string
	Decision        string
	Key             string
	PromptHash      string
	Prompt          string
	ResponseHash    string
	Response        string
	LatencyMS       int64
	Error           string
	Resolution      string // "allow" | "challenge" | "block" | "escalate" | "justify"
	Reason          string
}

// AuditLogger records watchdog audit events. Implementations must be
// best-effort: callers log failures but watchdog enforcement should never fail
// closed because audit persistence is unavailable.
type AuditLogger interface {
	Record(ctx context.Context, e AuditEvent) error
	Close() error
}

// SQLiteAuditLog persists watchdog audit events to a local SQLite database.
type SQLiteAuditLog struct {
	db *sql.DB
}

// OpenSQLiteAuditLog opens or creates a watchdog audit database at path.
func OpenSQLiteAuditLog(path string) (*SQLiteAuditLog, error) {
	if path == "" {
		return nil, errors.New("watchdog audit path is empty")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return nil, fmt.Errorf("create watchdog audit dir: %w", err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open watchdog audit db: %w", err)
	}
	log := &SQLiteAuditLog{db: db}
	if err := log.init(context.Background()); err != nil {
		_ = db.Close()
		return nil, err
	}
	return log, nil
}

func (l *SQLiteAuditLog) init(ctx context.Context) error {
	_, err := l.db.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS watchdog_events (
  id TEXT PRIMARY KEY,
  ts TEXT NOT NULL,
  conversation_id TEXT NOT NULL,
  event_type TEXT NOT NULL,
  check_name TEXT,
  action_kind TEXT,
  tool_name TEXT,
  tool_args_hash TEXT,
  tool_args_summary TEXT,
  applies INTEGER NOT NULL DEFAULT 0,
  protocol TEXT,
  violation INTEGER NOT NULL DEFAULT 0,
  challenge TEXT,
  revise TEXT,
  decision TEXT,
  action_key TEXT,
  prompt_hash TEXT,
  prompt TEXT,
  response_hash TEXT,
  response TEXT,
  latency_ms INTEGER NOT NULL DEFAULT 0,
  error TEXT,
  resolution TEXT,
  reason TEXT
);
CREATE INDEX IF NOT EXISTS watchdog_events_ts_idx ON watchdog_events(ts);
CREATE INDEX IF NOT EXISTS watchdog_events_conversation_idx ON watchdog_events(conversation_id, ts);
CREATE INDEX IF NOT EXISTS watchdog_events_check_idx ON watchdog_events(check_name, ts);
CREATE INDEX IF NOT EXISTS watchdog_events_resolution_idx ON watchdog_events(resolution, ts);
`)
	if err != nil {
		return fmt.Errorf("init watchdog audit db: %w", err)
	}
	return nil
}

// Record appends one audit event.
func (l *SQLiteAuditLog) Record(ctx context.Context, e AuditEvent) error {
	if e.Timestamp.IsZero() {
		e.Timestamp = time.Now().UTC()
	}
	_, err := l.db.ExecContext(ctx, `
INSERT INTO watchdog_events (
  id, ts, conversation_id, event_type, check_name, action_kind, tool_name,
  tool_args_hash, tool_args_summary, applies, protocol, violation, challenge,
  revise, decision, action_key, prompt_hash, prompt, response_hash, response,
  latency_ms, error, resolution, reason
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
`,
		e.ID,
		e.Timestamp.UTC().Format(time.RFC3339Nano),
		e.ConversationID,
		e.EventType,
		e.Check,
		e.ActionKind,
		e.ToolName,
		e.ToolArgsHash,
		e.ToolArgsSummary,
		boolInt(e.Applies),
		e.Protocol,
		boolInt(e.Violation),
		e.Challenge,
		e.Revise,
		e.Decision,
		e.Key,
		e.PromptHash,
		e.Prompt,
		e.ResponseHash,
		e.Response,
		e.LatencyMS,
		e.Error,
		e.Resolution,
		e.Reason,
	)
	if err != nil {
		return fmt.Errorf("record watchdog audit event: %w", err)
	}
	return nil
}

// Close closes the underlying SQLite handle.
func (l *SQLiteAuditLog) Close() error { return l.db.Close() }

func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}
