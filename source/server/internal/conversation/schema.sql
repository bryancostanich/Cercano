-- conversations: one row per started conversation. project_dir lets the CLI
-- filter /history to the current project. last_turn_at sorts the picker.
CREATE TABLE IF NOT EXISTS conversations (
    id           TEXT PRIMARY KEY,
    title        TEXT NOT NULL DEFAULT '',
    project_dir  TEXT NOT NULL DEFAULT '',
    model        TEXT NOT NULL DEFAULT '',
    title_source TEXT NOT NULL DEFAULT 'user',
    started_at   INTEGER NOT NULL,
    last_turn_at INTEGER NOT NULL,
    recap            TEXT NOT NULL DEFAULT '',
    recap_updated_at INTEGER NOT NULL DEFAULT 0,
    -- kind: 'main' for user-facing conversations, 'subagent' for persisted
    -- dispatch tool loops (hidden from List). parent_id links a subagent loop
    -- to the conversation whose dispatch spawned it.
    kind      TEXT NOT NULL DEFAULT 'main',
    parent_id TEXT NOT NULL DEFAULT '',
    -- precursor_id: temporal succession, distinct from parent_id's sub-agent
    -- containment. When a long session rolls over, a new 'main' conversation is
    -- created whose only inherited context is a handoff turn, and precursor_id
    -- points at the session it succeeded. The old conversation is left intact on
    -- disk; walk precursor_id backward (A<-B<-C) to reconstruct the lineage.
    precursor_id TEXT NOT NULL DEFAULT '',
    -- granted_tools: for subagent rows, the comma-joined tool names the
    -- dispatch loop was granted. Tool names are identifiers with no commas, so
    -- a plain join round-trips cleanly. Lets a resumed CLI reopen each
    -- sub-agent tab with the same tool set it showed live.
    granted_tools TEXT NOT NULL DEFAULT '',
    -- dismissed: 1 when a sub-agent tab was closed or swept in the CLI, so a
    -- resumed CLI skips reopening it (ListChildren filters dismissed = 0).
    dismissed INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_conv_project ON conversations(project_dir, last_turn_at DESC);
CREATE INDEX IF NOT EXISTS idx_conv_last_turn ON conversations(last_turn_at DESC);

-- turns: one row per role-emission (user or assistant). created_at sorts in
-- order within a conversation. token counts and latency are optional metrics.
CREATE TABLE IF NOT EXISTS turns (
    id              TEXT PRIMARY KEY,
    conversation_id TEXT NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
    role            TEXT NOT NULL,           -- 'user' | 'assistant' | 'system'
    content         TEXT NOT NULL,
    tokens_in       INTEGER NOT NULL DEFAULT 0,
    tokens_out      INTEGER NOT NULL DEFAULT 0,
    latency_ms      INTEGER NOT NULL DEFAULT 0,
    created_at      INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_turns_conv ON turns(conversation_id, created_at);

-- Migration: content_json holds the ordered Anthropic-format block array for
-- assistant turns with tool_use/tool_result. Text-only turns leave this empty
-- and use `content`. Idempotency is handled in Go (PRAGMA table_info check)
-- because SQLite ALTER TABLE ADD COLUMN has no IF NOT EXISTS in the embedded
-- modernc.org/sqlite version.

-- conversation_compaction: the derived compaction layer (1:1 with a
-- conversation). Holds opaque JSON summaries + the frozen boundary; raw turns
-- remain the source of truth. CREATE IF NOT EXISTS runs on every Open, so this
-- table is created for both fresh and pre-existing DBs (no separate migration).
CREATE TABLE IF NOT EXISTS conversation_compaction (
    conversation_id   TEXT PRIMARY KEY REFERENCES conversations(id) ON DELETE CASCADE,
    frozen_through    INTEGER NOT NULL DEFAULT 0,
    segment_summaries TEXT    NOT NULL DEFAULT '',
    consolidated      TEXT    NOT NULL DEFAULT '',
    compacted_tokens  INTEGER NOT NULL DEFAULT 0,
    updated_at        INTEGER NOT NULL DEFAULT 0
);

-- autonomy_runs: append-only autonomous-run ledger. The profile remains
-- conversation-scoped, but each approved autonomous run gets its own durable row
-- so later runs do not overwrite earlier briefs, decisions, or review records.
-- Briefs, revisions, decisions, and review state are JSON so V1 stays compact;
-- the table boundary leaves room to normalize later if the review UI needs it.
CREATE TABLE IF NOT EXISTS autonomy_runs (
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
);
CREATE INDEX IF NOT EXISTS idx_autonomy_runs_conversation_updated
    ON autonomy_runs(conversation_id, updated_at DESC, created_at DESC, run_id DESC);
CREATE UNIQUE INDEX IF NOT EXISTS idx_autonomy_runs_one_active
    ON autonomy_runs(conversation_id)
    WHERE state IN ('running', 'review_pending');
