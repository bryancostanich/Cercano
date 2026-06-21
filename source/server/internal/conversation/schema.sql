-- conversations: one row per started conversation. project_dir lets the CLI
-- filter /history to the current project. last_turn_at sorts the picker.
CREATE TABLE IF NOT EXISTS conversations (
    id           TEXT PRIMARY KEY,
    title        TEXT NOT NULL DEFAULT '',
    project_dir  TEXT NOT NULL DEFAULT '',
    model        TEXT NOT NULL DEFAULT '',
    started_at   INTEGER NOT NULL,
    last_turn_at INTEGER NOT NULL
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
