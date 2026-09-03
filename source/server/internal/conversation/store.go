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

	// Kind is "main" for user-facing conversations, "subagent" for persisted
	// dispatch tool loops. ParentID links a subagent loop to the conversation
	// whose dispatch spawned it (empty when unknown).
	Kind     string
	ParentID string

	// PrecursorID links a rolled-over conversation to the one it succeeded:
	// temporal succession ("this session picked up where precursor_id left
	// off"), NOT the containment meaning ParentID carries for sub-agents.
	// Empty for conversations that were not created by a rollover. Walk it
	// backward (A<-B<-C) to reconstruct a rollover lineage.
	PrecursorID string

	// GrantedTools is the tool set a subagent dispatch loop was granted, so a
	// resumed CLI can reopen each sub-agent tab with the same tools it showed
	// live. Populated only by ListChildren (persisted as a comma-joined string
	// in the granted_tools column); nil for main conversations and for the
	// summary paths (List/Get) that don't select the column.
	GrantedTools []string
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

// AutonomyBrief is the lightweight approved contract for autonomous mode. It is
// deliberately smaller than a plan: enough goal/boundary structure for the agent
// to work hands-off, not another planning document.
type AutonomyBrief struct {
	Goal         string   `json:"goal"`
	DoneWhen     []string `json:"done_when,omitempty"`
	Constraints  []string `json:"constraints,omitempty"`
	ReviewPoints []string `json:"review_points,omitempty"`
}

// AutonomyBriefRevision preserves each approved brief change so later decision
// review can answer what goal/boundaries were active at the time.
type AutonomyBriefRevision struct {
	Number    int           `json:"number"`
	Actor     string        `json:"actor"`
	Reason    string        `json:"reason"`
	Timestamp time.Time     `json:"timestamp"`
	Brief     AutonomyBrief `json:"brief"`
}

// AutonomyDecisionOption records one real option considered at a meaningful
// autonomous fork. The fields intentionally mirror the design-decision protocol
// so the model has to evaluate trade-offs instead of taking the shortest hack.
type AutonomyDecisionOption struct {
	Title       string   `json:"title"`
	Cost        string   `json:"cost"`
	Risk        string   `json:"risk"`
	Reward      string   `json:"reward"`
	SideEffects string   `json:"side_effects"`
	HackFlags   []string `json:"hack_flags,omitempty"`
}

// AutonomyDecisionCounterargument captures the strongest case against or for an
// option so final review can see the decision was not rubber-stamped.
type AutonomyDecisionCounterargument struct {
	Option        string `json:"option"`
	StrongestCase string `json:"strongest_case"`
}

// AutonomyDecision is one structured design-decision entry in the autonomous
// ledger. Sequence and Timestamp are assigned by the capture_decision tool.
type AutonomyDecision struct {
	Sequence         int                               `json:"sequence"`
	Timestamp        time.Time                         `json:"timestamp"`
	DecisionPoint    string                            `json:"decision_point"`
	Options          []AutonomyDecisionOption          `json:"options"`
	Counterarguments []AutonomyDecisionCounterargument `json:"counterarguments"`
	Recommendation   string                            `json:"recommendation"`
	ChosenPath       string                            `json:"chosen_path"`
	WhyCleanest      string                            `json:"why_cleanest"`
	Reversibility    string                            `json:"reversibility"`
	StopRequired     bool                              `json:"stop_required"`
	StopReason       string                            `json:"stop_reason,omitempty"`
}

// AutonomyRun is one durable append-only autonomous-mode run record. JSON fields
// stay opaque to the store until richer review APIs need normalization.
type AutonomyRun struct {
	RunID          string
	ConversationID string
	State          string
	SourceKind     string
	SourcePlanPath string
	SourceSpecPath string
	BriefJSON      string
	RevisionsJSON  string
	DecisionsJSON  string
	ReviewJSON     string
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// Compaction is the persisted derived compaction layer for a conversation.
// Summaries are opaque JSON here; the compactor package owns their structure.
type Compaction struct {
	ConversationID       string
	FrozenThrough        int64 // turns with CreatedAt.Unix() <= this are frozen
	SegmentSummariesJSON string
	ConsolidatedJSON     string
	CompactedTokens      int
	UpdatedAt            time.Time
}

// ContextUsage is a cached snapshot of a conversation's context accounting.
//
// It is a derived cache, not a source of truth: raw turns and Compaction are.
// It exists so the context meter can be served without assembling full history,
// which on large conversations exceeds the client's poll deadline.
//
// Source records how the snapshot was produced:
//
//	"turn"        exact provider-facing accounting captured while serving a turn
//	"compaction"  post-pass send-view totals a compaction pass already computed
//
// Model records the model whose window the snapshot was measured against, so a
// snapshot taken under a different route can be recognized instead of trusted.
type ContextUsage struct {
	ConversationID     string
	TokensUsed         int // the sent (compacted) size
	RawTokens          int // the uncompacted size
	MessageTokens      int
	SystemTokens       int
	ToolSchemaTokens   int
	OutputReserve      int
	EstimatedRequest   int
	ContextWindow      int
	ContextWindowKnown bool
	Model              string
	Source             string
	ComputedAt         time.Time
}

// PrunedBodyStub replaces a frozen turn's content when it ages out of raw
// retention; the consolidated summary carries the substance.
const PrunedBodyStub = "[pruned after 90 days — see summary]"

// Store is the persistent conversation store interface. The runtime
// implementation is SQLite-backed (modernc.org/sqlite, pure Go — no cgo).
type Store interface {
	// EnsureConversation idempotently creates the conversation row. Title is
	// auto-derived from the first user turn (deferred to first Append).
	EnsureConversation(ctx context.Context, id, projectDir, model string) error

	// EnsureSubagentConversation idempotently creates a conversation row of
	// kind "subagent", linked to the parent conversation whose dispatch
	// spawned it (parentID may be empty). grantedTools is the tool set the
	// dispatch loop was granted, stored comma-joined so a resumed CLI can
	// reopen the sub-agent tab with the same tools. Subagent conversations are
	// hidden from List but fully readable via Get/GetTurns/ListChildren for
	// post-mortems and tab restore.
	EnsureSubagentConversation(ctx context.Context, id, parentID, projectDir, model string, grantedTools []string) error

	// CreateRolledOver creates a new 'main' conversation that succeeds
	// precursorID (a session rollover), seeding it with handoffTurn as its sole
	// initial turn. The predecessor is untouched. Fails if id already exists.
	CreateRolledOver(ctx context.Context, id, projectDir, model, precursorID string, handoffTurn Turn) error

	// Precursor returns the precursor_id of a conversation ("" if it was not
	// created by a rollover), so a lineage can be walked backward.
	Precursor(ctx context.Context, id string) (string, error)

	// MarkSubagentDismissed flags a sub-agent conversation as dismissed so
	// ListChildren skips it. The CLI calls this when the user closes a
	// sub-agent tab or it's swept on turn completion, so a resumed CLI does not
	// reopen it. The transcript stays in the store; only the tab is hidden.
	MarkSubagentDismissed(ctx context.Context, conversationID string) error

	// Append records one turn. Updates the conversation's last_turn_at. If
	// this is the first user turn and the conversation has no title, derives
	// one algorithmically from the content.
	Append(ctx context.Context, t Turn) error

	// List returns conversations matching projectDir (empty matches all),
	// sorted by last_turn_at desc, capped at limit (0 = no cap).
	List(ctx context.Context, projectDir string, limit int) ([]Info, error)

	// GetTurns returns all turns for a conversation, ordered by created_at.
	GetTurns(ctx context.Context, conversationID string) ([]Turn, error)

	// CountTurns returns the number of turns in a conversation.
	CountTurns(ctx context.Context, conversationID string) (int, error)

	// GetTurnPage returns a chronological page of turns using zero-based logical
	// indices in the same order as GetTurns. start is clamped to the available
	// range; limit <= 0 returns no turns.
	GetTurnPage(ctx context.Context, conversationID string, start, limit int) ([]Turn, error)

	// GetTailTurns returns the newest limit turns in chronological order, plus
	// the zero-based start index of that tail and the total turn count.
	GetTailTurns(ctx context.Context, conversationID string, limit int) ([]Turn, int, int, error)

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

	// GetCompaction returns the derived compaction state, or a zero value
	// (FrozenThrough 0, empty JSON) if none exists yet.
	GetCompaction(ctx context.Context, conversationID string) (Compaction, error)
	// SaveCompaction upserts the derived compaction state.
	SaveCompaction(ctx context.Context, c Compaction) error

	// GetContextUsage returns the cached context-accounting snapshot for a
	// conversation. ok is false when no snapshot has been computed yet, which
	// callers must report as unknown rather than as zero usage.
	GetContextUsage(ctx context.Context, conversationID string) (usage ContextUsage, ok bool, err error)
	// SaveContextUsage upserts the cached context-accounting snapshot. Callers
	// pass values they already computed; this must never trigger assembly.
	SaveContextUsage(ctx context.Context, u ContextUsage) error

	// EstimateRawTokens returns the cheap len/4 uncompacted size estimate for a
	// conversation, computed as a SQL aggregate so turn bodies never leave the
	// database. Mirrors requestassembly.EstimateRawTokens, but bounded enough
	// to run on a UI poll for conversations far too large to assemble.
	EstimateRawTokens(ctx context.Context, conversationID string) (int, error)

	// CreateAutonomyRun inserts one append-only autonomous run record. It fails if
	// the new run would violate the one-active-run-per-conversation invariant.
	CreateAutonomyRun(ctx context.Context, r AutonomyRun) (AutonomyRun, error)
	// UpdateAutonomyRun updates an existing autonomous run by run id.
	UpdateAutonomyRun(ctx context.Context, r AutonomyRun) error
	// GetActiveAutonomyRun returns the current running/review_pending autonomous
	// run for a conversation, or sql.ErrNoRows when none is active.
	GetActiveAutonomyRun(ctx context.Context, conversationID string) (AutonomyRun, error)
	// GetLatestAutonomyRun returns the newest autonomous run for a conversation,
	// or sql.ErrNoRows when no autonomous run has been created for it.
	GetLatestAutonomyRun(ctx context.Context, conversationID string) (AutonomyRun, error)
	// ListAutonomyRuns returns all autonomous runs for a conversation, newest first.
	ListAutonomyRuns(ctx context.Context, conversationID string) ([]AutonomyRun, error)

	// SaveAutonomyRun and GetAutonomyRun are deprecated compatibility methods for
	// callers being migrated to append-only run semantics. Save creates when RunID
	// is empty and updates by RunID otherwise; Get returns the latest run.
	SaveAutonomyRun(ctx context.Context, r AutonomyRun) error
	GetAutonomyRun(ctx context.Context, conversationID string) (AutonomyRun, error)

	// DeleteTurns removes the named turns from a conversation. Unknown ids are
	// ignored (idempotent); other conversations are never affected.
	DeleteTurns(ctx context.Context, conversationID string, ids []string) error

	// PruneRawBodies stubs the content of frozen turns (created_at <=
	// frozenThrough) older than beforeUnix; returns the number changed.
	PruneRawBodies(ctx context.Context, conversationID string, beforeUnix, frozenThrough int64) (int, error)
	// CollapseConversation deletes the compaction row and all turns, keeping the
	// conversations identity row (title + recap).
	CollapseConversation(ctx context.Context, conversationID string) error

	// Get returns a single conversation's Info, or an error if not found.
	Get(ctx context.Context, conversationID string) (Info, error)

	// ListChildren returns the subagent conversations spawned under parentID,
	// ordered by started_at ascending (spawn order), with GrantedTools
	// populated from the granted_tools column. Backs the CLI's sub-agent tab
	// restore on resume.
	ListChildren(ctx context.Context, parentID string) ([]Info, error)

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
	// Pin the store to a single physical SQLite connection.
	//
	// database/sql otherwise maintains a *pool* of independent connections, and
	// modernc.org/sqlite opens the file once per pooled connection. In WAL mode
	// each connection latches its own read snapshot (the WAL end-mark it saw at
	// begin-read time). When another process checkpoints and truncates the WAL,
	// a pooled connection can keep serving a stale, pre-write snapshot — so the
	// /history picker intermittently sees zero conversations even though the
	// rows are committed on disk. That staleness became sticky across restarts
	// once the WAL was truncated, which is exactly the "restart no longer fixes
	// resume" failure. One connection means one consistent view; all callers
	// already serialize through s.mu, so single-conn costs no real concurrency.
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(0)
	// WAL for durability + crash safety; busy_timeout so the lone connection
	// waits out any cross-process writer lock instead of erroring; foreign_keys
	// for the ON DELETE CASCADE from conversations to turns.
	if _, err := db.Exec("PRAGMA journal_mode = WAL; PRAGMA busy_timeout = 5000; PRAGMA foreign_keys = ON;"); err != nil {
		db.Close()
		return nil, fmt.Errorf("pragma setup: %w", err)
	}
	if err := migrateAutonomyRunsToAppendOnly(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate autonomy runs: %w", err)
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
		`ALTER TABLE conversations ADD COLUMN kind TEXT NOT NULL DEFAULT 'main'`,
		`ALTER TABLE conversations ADD COLUMN parent_id TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE conversations ADD COLUMN precursor_id TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE conversations ADD COLUMN granted_tools TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE conversations ADD COLUMN dismissed INTEGER NOT NULL DEFAULT 0`,
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

// NewID mints a random conversation/turn id. Exported so callers creating
// conversations out-of-band (e.g. persisted dispatch sub-agent loops) mint
// ids in the same format the store uses internally.
func NewID() string { return newID() }

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
	// On conflict, backfill project_dir/model if the existing row left them
	// empty (e.g., an upserting Rename created the row before the first
	// turn, when the project context wasn't available). Non-empty values
	// are preserved — multiple sessions or a re-attached agent can't
	// silently rebrand an existing conversation. Title and title_source are
	// never touched here; Rename and SetGeneratedTitle own those.
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO conversations (id, project_dir, model, title_source, started_at, last_turn_at)
		VALUES (?, ?, ?, 'auto', ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			project_dir = CASE WHEN conversations.project_dir = '' THEN excluded.project_dir ELSE conversations.project_dir END,
			model       = CASE WHEN conversations.model       = '' THEN excluded.model       ELSE conversations.model       END`,
		id, projectDir, model, now, now)
	return err
}

func (s *sqliteStore) EnsureSubagentConversation(ctx context.Context, id, parentID, projectDir, model string, grantedTools []string) error {
	if id == "" {
		return errors.New("conversation id required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().Unix()
	// Tool names are identifiers with no commas, so a plain join round-trips
	// cleanly on read (see ListChildren, which splits on comma).
	granted := strings.Join(grantedTools, ",")
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO conversations (id, project_dir, model, title_source, kind, parent_id, granted_tools, started_at, last_turn_at)
		VALUES (?, ?, ?, 'auto', 'subagent', ?, ?, ?, ?)
		ON CONFLICT(id) DO NOTHING`,
		id, projectDir, model, parentID, granted, now, now)
	return err
}

func (s *sqliteStore) CreateRolledOver(ctx context.Context, id, projectDir, model, precursorID string, handoffTurn Turn) error {
	if id == "" {
		return errors.New("conversation id required")
	}
	if precursorID == "" {
		return errors.New("precursor id required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().Unix()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// Fail (not upsert) if the id already exists: a rollover mints a fresh id,
	// so a collision means a caller bug we want to surface rather than mask.
	res, err := tx.ExecContext(ctx, `
		INSERT INTO conversations (id, project_dir, model, title_source, kind, precursor_id, started_at, last_turn_at)
		VALUES (?, ?, ?, 'auto', 'main', ?, ?, ?)
		ON CONFLICT(id) DO NOTHING`,
		id, projectDir, model, precursorID, now, now)
	if err != nil {
		return fmt.Errorf("insert rolled-over conversation: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("conversation %q already exists", id)
	}

	// Seed the handoff as the first turn. Its timestamp anchors the new
	// session's timeline; the frozen boundary starts at zero so no
	// re-consolidation debt crosses the seam.
	turnID := handoffTurn.ID
	if turnID == "" {
		turnID = newID()
	}
	createdAt := handoffTurn.CreatedAt.Unix()
	if handoffTurn.CreatedAt.IsZero() {
		createdAt = now
	}
	role := handoffTurn.Role
	if role == "" {
		role = "user"
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO turns (id, conversation_id, role, content, content_json, tokens_in, tokens_out, latency_ms, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		turnID, id, role, handoffTurn.Content, handoffTurn.BlocksJSON,
		handoffTurn.TokensIn, handoffTurn.TokensOut, handoffTurn.LatencyMs, createdAt,
	); err != nil {
		return fmt.Errorf("seed handoff turn: %w", err)
	}
	return tx.Commit()
}

func (s *sqliteStore) Precursor(ctx context.Context, id string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var precursor string
	err := s.db.QueryRowContext(ctx,
		`SELECT precursor_id FROM conversations WHERE id = ?`, id).Scan(&precursor)
	if err != nil {
		return "", err
	}
	return precursor, nil
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
	// yet, and never overwrite an existing title here. The auto-derived title
	// keeps title_source='auto', so SetGeneratedTitle may later replace it with
	// a model-generated one; a user Rename sets title_source='user' and is
	// never touched by either path.
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

	// Subagent conversations (persisted dispatch loops) are hidden from the
	// picker; they stay reachable by id via Get/GetTurns.
	query := `
		SELECT c.id, c.title, c.project_dir, c.model, c.started_at, c.last_turn_at,
		       c.recap, c.recap_updated_at,
		       (SELECT COUNT(*) FROM turns t WHERE t.conversation_id = c.id) AS turn_count
		FROM conversations c
		WHERE (? = '' OR c.project_dir = ?) AND c.kind = 'main'
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

// ListChildren returns the subagent conversations spawned under parentID,
// ordered by started_at ascending so callers see them in spawn order (which
// lets the CLI recompute nested "sub 1", "sub 1.1" labels parent-before-child).
// GrantedTools is populated from the granted_tools column: the comma-joined
// string is split back into a slice, with the empty string mapping to nil.
func (s *sqliteStore) ListChildren(ctx context.Context, parentID string) ([]Info, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	rows, err := s.db.QueryContext(ctx, `
		SELECT c.id, c.title, c.project_dir, c.model, c.started_at, c.last_turn_at,
		       c.recap, c.recap_updated_at, c.kind, c.parent_id, c.granted_tools,
		       (SELECT COUNT(*) FROM turns t WHERE t.conversation_id = c.id) AS turn_count
		FROM conversations c
		WHERE c.parent_id = ? AND c.kind = 'subagent' AND c.dismissed = 0
		ORDER BY c.started_at ASC`, parentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Info
	for rows.Next() {
		var info Info
		var startedAt, lastTurnAt, recapAt int64
		var granted string
		if err := rows.Scan(&info.ID, &info.Title, &info.ProjectDir, &info.Model,
			&startedAt, &lastTurnAt, &info.Recap, &recapAt, &info.Kind, &info.ParentID,
			&granted, &info.TurnCount); err != nil {
			return nil, err
		}
		info.StartedAt = time.Unix(startedAt, 0)
		info.LastTurnAt = time.Unix(lastTurnAt, 0)
		if recapAt > 0 {
			info.RecapUpdatedAt = time.Unix(recapAt, 0)
		}
		// Empty string → nil (no tools granted); a plain comma split otherwise,
		// safe because tool names are identifiers with no embedded commas.
		if granted != "" {
			info.GrantedTools = strings.Split(granted, ",")
		}
		out = append(out, info)
	}
	return out, rows.Err()
}

func (s *sqliteStore) GetTurns(ctx context.Context, conversationID string) ([]Turn, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.getTurnPageLocked(ctx, conversationID, 0, -1)
}

func (s *sqliteStore) CountTurns(ctx context.Context, conversationID string) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.countTurnsLocked(ctx, conversationID)
}

func (s *sqliteStore) GetTurnPage(ctx context.Context, conversationID string, start, limit int) ([]Turn, error) {
	if limit <= 0 {
		return nil, nil
	}
	if start < 0 {
		start = 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.getTurnPageLocked(ctx, conversationID, start, limit)
}

func (s *sqliteStore) GetTailTurns(ctx context.Context, conversationID string, limit int) ([]Turn, int, int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	total, err := s.countTurnsLocked(ctx, conversationID)
	if err != nil {
		return nil, 0, 0, err
	}
	if limit <= 0 || total == 0 {
		return nil, total, total, nil
	}
	start := total - limit
	if start < 0 {
		start = 0
	}
	turns, err := s.getTurnPageLocked(ctx, conversationID, start, total-start)
	if err != nil {
		return nil, 0, 0, err
	}
	return turns, start, total, nil
}

func (s *sqliteStore) countTurnsLocked(ctx context.Context, conversationID string) (int, error) {
	var total int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM turns WHERE conversation_id = ?`, conversationID).Scan(&total); err != nil {
		return 0, err
	}
	return total, nil
}

func (s *sqliteStore) getTurnPageLocked(ctx context.Context, conversationID string, start, limit int) ([]Turn, error) {
	query := `
		SELECT id, conversation_id, role, content, content_json, tokens_in, tokens_out, latency_ms, created_at
		FROM turns
		WHERE conversation_id = ?
		ORDER BY created_at ASC, rowid ASC`
	args := []any{conversationID}
	if limit >= 0 {
		query += ` LIMIT ? OFFSET ?`
		args = append(args, limit, start)
	}
	rows, err := s.db.QueryContext(ctx, query, args...)
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

func (s *sqliteStore) PruneRawBodies(ctx context.Context, conversationID string, beforeUnix, frozenThrough int64) (int, error) {
	if conversationID == "" {
		return 0, errors.New("conversation id required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	res, err := s.db.ExecContext(ctx, `
		UPDATE turns SET content = ?, content_json = ''
		WHERE conversation_id = ? AND created_at <= ? AND created_at < ? AND content != ?`,
		PrunedBodyStub, conversationID, frozenThrough, beforeUnix, PrunedBodyStub)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

func (s *sqliteStore) CollapseConversation(ctx context.Context, conversationID string) error {
	if conversationID == "" {
		return errors.New("conversation id required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM conversation_compaction WHERE conversation_id = ?`, conversationID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM turns WHERE conversation_id = ?`, conversationID); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *sqliteStore) Rename(ctx context.Context, conversationID, title string) error {
	if conversationID == "" {
		return errors.New("conversation id required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	// Upsert. The previous UPDATE-only SQL silently no-op'd when /rename
	// fired before EnsureConversation had inserted the row (a fresh session
	// where the user renames before their first turn): 0 rows affected is
	// not an error, the CLI showed "renamed to: …", and later
	// SetGeneratedTitle from the recap layer won because title_source was
	// still 'auto' on the freshly-inserted row. Upserting reserves the row
	// with the user's title and title_source='user' so EnsureConversation's
	// later insert collapses to a no-op for title and SetGeneratedTitle's
	// guard (title_source != 'user') keeps the user's choice.
	now := time.Now().Unix()
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO conversations (id, title, title_source, started_at, last_turn_at)
		VALUES (?, ?, 'user', ?, ?)
		ON CONFLICT(id) DO UPDATE SET title = excluded.title, title_source = 'user'`,
		conversationID, title, now, now)
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

// MarkSubagentDismissed sets dismissed = 1 on a sub-agent conversation so
// ListChildren no longer returns it. Guarded to kind = 'subagent' so it can
// never hide a main conversation. Idempotent.
func (s *sqliteStore) MarkSubagentDismissed(ctx context.Context, conversationID string) error {
	if conversationID == "" {
		return errors.New("conversation id required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.ExecContext(ctx,
		`UPDATE conversations SET dismissed = 1 WHERE id = ? AND kind = 'subagent'`,
		conversationID)
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
		       c.recap, c.recap_updated_at, c.kind, c.parent_id, c.precursor_id,
		       (SELECT COUNT(*) FROM turns t WHERE t.conversation_id = c.id) AS turn_count
		FROM conversations c WHERE c.id = ?`, conversationID).
		Scan(&info.ID, &info.Title, &info.ProjectDir, &info.Model,
			&startedAt, &lastTurnAt, &info.Recap, &recapAt, &info.Kind, &info.ParentID, &info.PrecursorID, &info.TurnCount)
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

func (s *sqliteStore) GetCompaction(ctx context.Context, conversationID string) (Compaction, error) {
	if conversationID == "" {
		return Compaction{}, errors.New("conversation id required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	c := Compaction{ConversationID: conversationID}
	var updated int64
	err := s.db.QueryRowContext(ctx,
		`SELECT frozen_through, segment_summaries, consolidated, compacted_tokens, updated_at
		 FROM conversation_compaction WHERE conversation_id = ?`, conversationID).
		Scan(&c.FrozenThrough, &c.SegmentSummariesJSON, &c.ConsolidatedJSON, &c.CompactedTokens, &updated)
	if err == sql.ErrNoRows {
		return Compaction{ConversationID: conversationID}, nil
	}
	if err != nil {
		return Compaction{}, err
	}
	c.UpdatedAt = time.Unix(updated, 0)
	return c, nil
}

func (s *sqliteStore) GetContextUsage(ctx context.Context, conversationID string) (ContextUsage, bool, error) {
	if conversationID == "" {
		return ContextUsage{}, false, errors.New("conversation id required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	u := ContextUsage{ConversationID: conversationID}
	var windowKnown int
	var computed int64
	err := s.db.QueryRowContext(ctx,
		`SELECT tokens_used, raw_tokens, message_tokens, system_tokens, tool_schema_tokens,
		        output_reserve, estimated_request, context_window, window_known,
		        model, source, computed_at
		 FROM conversation_context_usage WHERE conversation_id = ?`, conversationID).
		Scan(&u.TokensUsed, &u.RawTokens, &u.MessageTokens, &u.SystemTokens, &u.ToolSchemaTokens,
			&u.OutputReserve, &u.EstimatedRequest, &u.ContextWindow, &windowKnown,
			&u.Model, &u.Source, &computed)
	if err == sql.ErrNoRows {
		// No snapshot yet. Report "unknown", never zero usage — a confident 0
		// on a large conversation is exactly the bug this cache fixes.
		return ContextUsage{ConversationID: conversationID}, false, nil
	}
	if err != nil {
		return ContextUsage{}, false, err
	}
	u.ContextWindowKnown = windowKnown != 0
	u.ComputedAt = time.Unix(computed, 0)
	return u, true, nil
}

// EstimateRawTokens mirrors requestassembly.EstimateRawTokens — per turn, the
// larger of the text and block-JSON body, summed, divided by four — but does
// the summing in SQLite so a 300MB transcript is never materialized in Go.
func (s *sqliteStore) EstimateRawTokens(ctx context.Context, conversationID string) (int, error) {
	if conversationID == "" {
		return 0, errors.New("conversation id required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var total sql.NullInt64
	err := s.db.QueryRowContext(ctx, `
		SELECT SUM(MAX(LENGTH(COALESCE(content, '')), LENGTH(COALESCE(content_json, ''))))
		FROM turns WHERE conversation_id = ?`, conversationID).Scan(&total)
	if err != nil {
		return 0, err
	}
	if !total.Valid || total.Int64 <= 0 {
		return 0, nil
	}
	return int((total.Int64 + 3) / 4), nil
}

func (s *sqliteStore) SaveContextUsage(ctx context.Context, u ContextUsage) error {
	if u.ConversationID == "" {
		return errors.New("conversation id required")
	}
	windowKnown := 0
	if u.ContextWindowKnown {
		windowKnown = 1
	}
	computed := u.ComputedAt
	if computed.IsZero() {
		computed = time.Now()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO conversation_context_usage
			(conversation_id, tokens_used, raw_tokens, message_tokens, system_tokens,
			 tool_schema_tokens, output_reserve, estimated_request, context_window,
			 window_known, model, source, computed_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(conversation_id) DO UPDATE SET
			tokens_used=excluded.tokens_used,
			raw_tokens=excluded.raw_tokens,
			message_tokens=excluded.message_tokens,
			system_tokens=excluded.system_tokens,
			tool_schema_tokens=excluded.tool_schema_tokens,
			output_reserve=excluded.output_reserve,
			estimated_request=excluded.estimated_request,
			context_window=excluded.context_window,
			window_known=excluded.window_known,
			model=excluded.model,
			source=excluded.source,
			computed_at=excluded.computed_at`,
		u.ConversationID, u.TokensUsed, u.RawTokens, u.MessageTokens, u.SystemTokens,
		u.ToolSchemaTokens, u.OutputReserve, u.EstimatedRequest, u.ContextWindow,
		windowKnown, u.Model, u.Source, computed.Unix())
	return err
}

func (s *sqliteStore) SaveCompaction(ctx context.Context, c Compaction) error {
	if c.ConversationID == "" {
		return errors.New("conversation id required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO conversation_compaction
			(conversation_id, frozen_through, segment_summaries, consolidated, compacted_tokens, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(conversation_id) DO UPDATE SET
			frozen_through=excluded.frozen_through,
			segment_summaries=excluded.segment_summaries,
			consolidated=excluded.consolidated,
			compacted_tokens=excluded.compacted_tokens,
			updated_at=excluded.updated_at`,
		c.ConversationID, c.FrozenThrough, c.SegmentSummariesJSON, c.ConsolidatedJSON,
		c.CompactedTokens, time.Now().Unix())
	return err
}
