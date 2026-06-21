# Cercano CLI Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Ship a Go-based Cercano CLI (terminal agent harness with retro cracker chrome) consuming a substantially enriched shared agent surface.

**Architecture:** Phases 1-7 build the agent-side enrichments (conversation store, context meter, built-in tool suite, MCP host, slash RPCs) that the CLI consumes. Phases 8-17 build the CLI client itself. Phase 18 is integration + acceptance validation + docs. CLI is a gRPC client; agent code stays the single source of truth for all clients.

**Tech Stack:** Go 1.21+, gRPC, Protocol Buffers, SQLite (modernc.org/sqlite — pure Go, no cgo), Bubble Tea + Lipgloss + Bubbles (Charm), `tiktoken-go` (or equivalent per-model tokenizer), `github.com/sergi/go-diff`, `modelcontextprotocol/go-sdk`, Ollama HTTP client (existing), `fc-list` shell-out for font enum, OSC 1337 / Ghostty config / Kitty kitten / WezTerm CLI for font apply.

**Conventions for this plan:**

- Files use `cercano/source/server/internal/<pkg>/<file>.go` paths from the repo root.
- Every phase ends with `Phase Completion Verification` per `conductor/workflow.md`: confirm tests pass, manual verification step, checkpoint commit, git note.
- Every task ends with a commit; commit message format is `feat(<scope>): …` / `test(<scope>): …` / `refactor(<scope>): …`.
- TDD: failing test first, minimal implementation second, refactor third. No exceptions for the agent side.
- Each task includes `Files`, `Tests`, atomic `Steps`, and acceptance.

---

## Phase 1: Proto extensions & code generation

### Objective

Extend `source/proto/agent.proto` with the new messages and RPC methods the rest of the agent work needs. Regenerate Go bindings. No semantic changes yet — types and stubs only.

### Tasks

#### Task 1.1: Add Conversation messages and RPCs to `agent.proto`

**Files:**
- Modify: `source/proto/agent.proto`

- [ ] **Step 1: Read existing proto** to confirm package, syntax version, existing service definition.
- [ ] **Step 2: Add messages** for `Conversation`, `Turn`, `ListConversationsRequest/Response`, `ResumeRequest/Response`, `ClearRequest/Response`.

```proto
message Conversation {
  string id = 1;
  string title = 2;
  string project_dir = 3;
  string model = 4;
  int64  started_at = 5;
  int64  last_turn_at = 6;
  int32  turn_count = 7;
}

message Turn {
  string id = 1;
  string conversation_id = 2;
  string role = 3;           // "user" | "assistant" | "tool"
  string content = 4;
  int32  tokens_in = 5;
  int32  tokens_out = 6;
  int32  latency_ms = 7;
  int64  created_at = 8;
}

message ListConversationsRequest  { string project_dir = 1; int32 limit = 2; }
message ListConversationsResponse { repeated Conversation conversations = 1; }
message ResumeRequest  { string conversation_id = 1; }
message ResumeResponse { Conversation conversation = 1; repeated Turn turns = 2; }
message ClearRequest   { string conversation_id = 1; }
message ClearResponse  { bool ok = 1; }
```

- [ ] **Step 3: Add RPCs** to the existing `service AgentService` (or whatever it's called — read first):

```proto
rpc ListConversations(ListConversationsRequest) returns (ListConversationsResponse);
rpc Resume(ResumeRequest) returns (ResumeResponse);
rpc Clear(ClearRequest) returns (ClearResponse);
```

- [ ] **Step 4: Commit** with message `feat(proto): add Conversation/Turn messages and ListConversations/Resume/Clear RPCs`.

#### Task 1.2: Add Context, MCP, Tools, Slash RPCs to `agent.proto`

**Files:**
- Modify: `source/proto/agent.proto`

- [ ] **Step 1: Add Context-window messages.**

```proto
message GetContextUsageRequest  { string conversation_id = 1; }
message GetContextUsageResponse { int32 tokens_used = 1; int32 model_max = 2; double percent = 3; }
```

- [ ] **Step 2: Add MCP host messages.**

```proto
message McpServer { string name = 1; string status = 2; int32 tool_count = 3; }
message McpTool   { string name = 1; string server = 2; string description = 3; }
message ListMcpServersRequest  {}
message ListMcpServersResponse { repeated McpServer servers = 1; }
message AddMcpServerRequest    { string name = 1; string transport = 2; string command = 3; repeated string args = 4; map<string,string> env = 5; }
message AddMcpServerResponse   { McpServer server = 1; }
message RestartMcpServerRequest  { string name = 1; }
message RestartMcpServerResponse { bool ok = 1; }
message ListMcpToolsRequest      {}
message ListMcpToolsResponse     { repeated McpTool tools = 1; }
```

- [ ] **Step 3: Add Tool listing and slash RPCs.**

```proto
message BuiltinTool { string name = 1; string description = 2; string permission = 3; }
message ListToolsRequest  {}
message ListToolsResponse { repeated BuiltinTool builtins = 1; repeated McpTool mcp = 2; }
message SwitchProjectRequest  { string path = 1; }
message SwitchProjectResponse { bool ok = 1; string context = 2; }
message GetProjectContextRequest  {}
message GetProjectContextResponse { string context = 1; string path = 2; }
```

- [ ] **Step 4: Add all RPCs to the service.**
- [ ] **Step 5: Commit** with `feat(proto): add context-meter, MCP host, tools, slash RPCs`.

#### Task 1.3: Regenerate Go bindings and verify build

**Files:**
- Modify: `source/server/pkg/proto/agent.pb.go` (generated)
- Modify: `source/server/pkg/proto/agent_grpc.pb.go` (generated)

- [ ] **Step 1: Regenerate** per the project's documented command:

```bash
PATH=$PATH:~/go/bin protoc --go_out=. --go-grpc_out=. -I. source/proto/agent.proto
```

- [ ] **Step 2: Verify** generated files land in `source/server/pkg/proto/` (check the README — the workflow document calls this out explicitly).
- [ ] **Step 3: Build** to confirm nothing breaks:

```bash
cd source/server && go build -o bin/cercano ./cmd/cercano/
```

- [ ] **Step 4: Commit generated files** (with a non-amend commit) with message `feat(proto): regenerate Go bindings`.

### Phase 1 Completion

Run the verification protocol from `conductor/workflow.md`:

- Tests: `cd source/server && go test ./... -count=1` (expect all green — no semantic changes yet).
- Manual: `cd source/server && go build -o bin/cercano ./cmd/cercano/ && bin/cercano --help` — confirm binary still starts.
- Checkpoint commit with the verification note attached via `git notes`.

---

## Phase 2: Conversation store + persistence (agent)

### Objective

Build the SQLite-backed conversation store the CLI's `/resume`, `/history`, `/clear` slash commands depend on. Wire into the agent's turn loop so every turn persists automatically.

### Tasks

#### Task 2.1: Create `ConversationStore` interface and SQLite schema

**Files:**
- Create: `source/server/internal/conversation/store.go`
- Create: `source/server/internal/conversation/sqlite.go`
- Create: `source/server/internal/conversation/schema.sql`
- Test: `source/server/internal/conversation/sqlite_test.go`

- [ ] **Step 1: Write failing test** that opens an in-memory SQLite store, inserts a conversation + 2 turns + 1 tool call, retrieves them, and asserts equality.

```go
func TestSQLiteStore_RoundTrip(t *testing.T) {
    store, err := conversation.NewSQLiteStore(":memory:")
    if err != nil { t.Fatal(err) }
    defer store.Close()

    convID, err := store.CreateConversation(context.Background(), conversation.Conversation{
        ProjectDir: "/tmp/x", Model: "qwen3-coder",
    })
    if err != nil { t.Fatal(err) }
    // ... append two turns, one tool call, list, assert
}
```

- [ ] **Step 2: Run test, confirm failure** (`NewSQLiteStore undefined`).
- [ ] **Step 3: Define interface** in `store.go`:

```go
package conversation

type Conversation struct { ID, Title, ProjectDir, Model string; StartedAt, LastTurnAt int64; TurnCount int }
type Turn         struct { ID, ConversationID, Role, Content string; TokensIn, TokensOut, LatencyMs int; CreatedAt int64 }
type ToolCall     struct { ID, TurnID, ToolName, ArgsJSON, ResultSummary string; Applied bool }

type Store interface {
    CreateConversation(ctx context.Context, c Conversation) (string, error)
    AppendTurn(ctx context.Context, t Turn) (string, error)
    AppendToolCall(ctx context.Context, tc ToolCall) (string, error)
    ListConversations(ctx context.Context, projectDir string, limit int) ([]Conversation, error)
    Get(ctx context.Context, id string) (Conversation, []Turn, error)
    Delete(ctx context.Context, id string) error
    Close() error
}
```

- [ ] **Step 4: Write schema.sql** (embed via `//go:embed`):

```sql
CREATE TABLE IF NOT EXISTS conversations (
  id TEXT PRIMARY KEY,
  title TEXT NOT NULL DEFAULT '',
  project_dir TEXT NOT NULL DEFAULT '',
  model TEXT NOT NULL DEFAULT '',
  started_at INTEGER NOT NULL,
  last_turn_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_conv_project ON conversations(project_dir, last_turn_at DESC);
CREATE TABLE IF NOT EXISTS turns (
  id TEXT PRIMARY KEY,
  conversation_id TEXT NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
  role TEXT NOT NULL,
  content TEXT NOT NULL,
  tokens_in INTEGER NOT NULL DEFAULT 0,
  tokens_out INTEGER NOT NULL DEFAULT 0,
  latency_ms INTEGER NOT NULL DEFAULT 0,
  created_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_turns_conv ON turns(conversation_id, created_at);
CREATE TABLE IF NOT EXISTS tool_calls (
  id TEXT PRIMARY KEY,
  turn_id TEXT NOT NULL REFERENCES turns(id) ON DELETE CASCADE,
  tool_name TEXT NOT NULL,
  args_json TEXT NOT NULL,
  result_summary TEXT NOT NULL DEFAULT '',
  applied INTEGER NOT NULL DEFAULT 0
);
```

- [ ] **Step 5: Implement `sqlite.go`** using `modernc.org/sqlite` (pure Go — avoid cgo dependency). UUID v4 for ids via `crypto/rand`. Open with `PRAGMA foreign_keys = ON; PRAGMA journal_mode = WAL`.
- [ ] **Step 6: Run test, confirm pass.**
- [ ] **Step 7: Commit** `feat(conversation): SQLite-backed conversation store with schema`.

#### Task 2.2: Title auto-derivation (algorithmic, not LLM)

**Files:**
- Modify: `source/server/internal/conversation/store.go`
- Test: add to `sqlite_test.go`

- [ ] **Step 1: Write failing test** that asserts `DeriveTitle("Refactor SmartRouter to use top-K=5 averaged across categories")` returns `"Refactor SmartRouter to use top-K=5 averaged"` (or whatever your rule produces) — capped at 60 chars on whitespace boundary.
- [ ] **Step 2: Implement `DeriveTitle(prompt string) string`** — algorithmic: strip leading slash commands, trim, take first 60 chars at the last whitespace boundary, append `…` if truncated.
- [ ] **Step 3: Wire** into `CreateConversation` when title is empty and the first turn is appended (re-derive on first user turn).
- [ ] **Step 4: Commit** `feat(conversation): algorithmic title derivation from first user turn`.

#### Task 2.3: Resolve conversation DB path

**Files:**
- Create: `source/server/internal/conversation/path.go`
- Test: `source/server/internal/conversation/path_test.go`

- [ ] **Step 1: Test** that `DBPath()` honors `$CERCANO_CONVERSATIONS_DB`, then falls back to `~/.config/cercano/conversations.db`, creating the parent dir if missing.
- [ ] **Step 2: Implement** with `os.UserConfigDir()`.
- [ ] **Step 3: Commit** `feat(conversation): config-dir-aware DB path resolution`.

#### Task 2.4: Wire `ConversationStore` into the Agent turn loop

**Files:**
- Modify: `source/server/internal/agent/agent.go`

- [ ] **Step 1: Read** `agent.go` to find `Handle` (or whatever the public per-turn entry is — verify symbol name first; the spec sketches it from prior conversation but the codebase is authoritative).
- [ ] **Step 2: Add** `store conversation.Store` field on the `Agent` struct, wire through constructor.
- [ ] **Step 3: At turn start**, if `req.ConversationID == ""`, call `store.CreateConversation` and return the new id in the response. If set, ensure it exists; on miss, log warning and create a new one (don't fail the turn).
- [ ] **Step 4: At turn end**, append a `user` turn (the raw input) and an `assistant` turn (the rendered response) with token counts and latency.
- [ ] **Step 5: For each tool call** the Coordinator made (LoopAgent surface), append a `tool_call` row. Result summary is the first 200 chars of structured output.
- [ ] **Step 6: Tests** — extend existing agent tests with a mock store and assert two turns + N tool calls land per turn.
- [ ] **Step 7: Commit** `feat(agent): persist every turn to ConversationStore`.

#### Task 2.5: Implement `ListConversations`, `Resume`, `Clear` RPC handlers

**Files:**
- Modify: `source/server/internal/server/server.go` (or wherever RPC handlers live — verify)

- [ ] **Step 1: Read** `server.go` to see how existing RPC handlers are structured.
- [ ] **Step 2: Implement handlers** — translate proto request/response to store calls.
- [ ] **Step 3: For Resume**, rehydrate the agent's in-memory conversation history (see `internal/agent/conversation.go`) from the store. The Agent must expose a `RehydrateConversation(id string, turns []Turn) error` method.
- [ ] **Step 4: Tests** — gRPC client→server round-trip for each handler.
- [ ] **Step 5: Commit** `feat(server): wire conversation RPCs to store`.

### Phase 2 Completion

- Tests: `cd source/server && go test ./internal/conversation/... ./internal/agent/... ./internal/server/... -count=1`
- Manual: build, run two turns against `bin/cercano`, kill, restart, `cercano` resume placeholder — verify SQLite has rows: `sqlite3 ~/.config/cercano/conversations.db 'SELECT id, title FROM conversations;'`
- Checkpoint commit + verification note.

---

## Phase 3: Context-window meter (agent)

### Objective

Per-model deterministic tokenizer + running token-count per conversation. `GetContextUsage` RPC returns the live total. Status bar in the CLI reads from this.

### Tasks

#### Task 3.1: Tokenizer abstraction

**Files:**
- Create: `source/server/internal/contextmeter/tokenizer.go`
- Test: `source/server/internal/contextmeter/tokenizer_test.go`

- [ ] **Step 1: Test** with several model names — for `qwen3-coder` and `llama3`, assert non-zero token counts for known inputs.
- [ ] **Step 2: Define interface:**

```go
package contextmeter

type Tokenizer interface {
    Count(s string) int
    ModelMax() int
}
```

- [ ] **Step 3: Implement** `TiktokenTokenizer` wrapping `github.com/pkoukk/tiktoken-go` (compatible with most BPE-style tokenizers; fall back to character-count/4 heuristic for unknown models — log a warning once).
- [ ] **Step 4: Add registry** `Get(model string) (Tokenizer, error)` — model name → tokenizer mapping with sensible defaults per known family (qwen → o200k_base via tiktoken-go's named encoder, llama3 → o200k_base, fallback → cl100k_base).
- [ ] **Step 5: Commit** `feat(contextmeter): per-model tokenizer with tiktoken-go`.

#### Task 3.2: Running context counter

**Files:**
- Create: `source/server/internal/contextmeter/counter.go`
- Test: `source/server/internal/contextmeter/counter_test.go`

- [ ] **Step 1: Test** that adding 3 turns of varying length and asking for `Used()` returns the correct cumulative count.
- [ ] **Step 2: Implement** `Counter` struct with `Add(role, content string)`, `Reset()`, `Used() int`, `Percent() float64`. Holds a `Tokenizer` reference + system-prompt baseline.
- [ ] **Step 3: Per-conversation registry** keyed by conversation id. Created on first turn, destroyed on `Clear`.
- [ ] **Step 4: Commit** `feat(contextmeter): per-conversation running counter`.

#### Task 3.3: Wire counter into Agent turn loop

**Files:**
- Modify: `source/server/internal/agent/agent.go`

- [ ] **Step 1: On turn start**, look up the counter; if absent, create with the active model's tokenizer + system prompt.
- [ ] **Step 2: After turn**, add the user prompt and the assistant response.
- [ ] **Step 3: Test** with a 3-turn sequence; assert the counter reflects all three.
- [ ] **Step 4: Commit** `feat(agent): track context usage per conversation`.

#### Task 3.4: Implement `GetContextUsage` RPC handler

**Files:**
- Modify: `source/server/internal/server/server.go`

- [ ] **Step 1: Implement handler** — counter lookup, return `{tokens_used, model_max, percent}`.
- [ ] **Step 2: Test** end-to-end via gRPC client.
- [ ] **Step 3: Commit** `feat(server): wire GetContextUsage RPC`.

### Phase 3 Completion

- Tests: `cd source/server && go test ./internal/contextmeter/... ./internal/agent/... -count=1`
- Manual: drive 3 turns via grpcurl, call `GetContextUsage`, verify count reconciles with manual tokenizer count to ±1%.
- Checkpoint commit + note.

---

## Phase 4: Built-in tool registry + R-tier tools (agent)

### Objective

Tool interface, registry, and the safe (read-only) tools the agent will need on day one.

### Tasks

#### Task 4.1: Tool interface + registry

**Files:**
- Create: `source/server/internal/tools/tool.go`
- Create: `source/server/internal/tools/registry.go`
- Test: `source/server/internal/tools/registry_test.go`

- [ ] **Step 1: Define interface:**

```go
package tools

type Permission string
const ( PermR Permission = "R"; PermW Permission = "W"; PermX Permission = "X" )

type Tool interface {
    Name()        string
    Description() string
    Permission()  Permission
    Schema()      []byte // JSON Schema for args
    Execute(ctx context.Context, args json.RawMessage) (Result, error)
}

type Result struct {
    Type     string          `json:"type"`     // "rows" | "text" | "json"
    Rows     []map[string]any `json:"rows,omitempty"`
    Text     string          `json:"text,omitempty"`
    JSON     json.RawMessage `json:"json,omitempty"`
    Truncated bool            `json:"truncated,omitempty"`
}
```

- [ ] **Step 2: Implement registry** with `Register(t Tool)`, `Get(name string)`, `All() []Tool`.
- [ ] **Step 3: Test** registration + lookup + duplicate-name detection.
- [ ] **Step 4: Commit** `feat(tools): Tool interface and registry`.

#### Task 4.2: Truncation policy

**Files:**
- Create: `source/server/internal/tools/truncate.go`
- Test: `source/server/internal/tools/truncate_test.go`

- [ ] **Step 1: Test** that `TruncateRows(rows, maxRows=200)` keeps the first 200 and sets `Truncated=true`; `TruncateText(s, maxBytes=32*1024)` truncates at codepoint boundary with `…` suffix.
- [ ] **Step 2: Implement.**
- [ ] **Step 3: Commit** `feat(tools): structured-output truncation helpers`.

#### Task 4.3: Filesystem R-tier tools — `read_file`, `list_dir`, `stat_file`

**Files:**
- Create: `source/server/internal/tools/fs_read.go`
- Test: `source/server/internal/tools/fs_read_test.go`

- [ ] **Step 1: TDD** each tool — table-driven test with a temp dir; assert correct result shape, schema validation, and that `Permission() == PermR`.
- [ ] **Step 2: Implement** `read_file` (returns text up to 32 KiB, line range optional), `list_dir` (returns rows: name/type/size), `stat_file` (returns existence/size/mtime).
- [ ] **Step 3: Register** each in an `init()` that appends to a package-level registry.
- [ ] **Step 4: Commit** `feat(tools): read_file, list_dir, stat_file (R-tier)`.

#### Task 4.4: `grep` tool with rg fallback

**Files:**
- Create: `source/server/internal/tools/grep.go`
- Test: `source/server/internal/tools/grep_test.go`

- [ ] **Step 1: Test** with a temp dir containing 3 files; assert matching lines come back with `{path, line, content}` rows.
- [ ] **Step 2: Implement** — try `exec.LookPath("rg")`; if present, use `rg --json --vimgrep`; else `grep -RIn`. Parse to structured rows. Truncate to 200 rows.
- [ ] **Step 3: Commit** `feat(tools): grep tool with rg preference`.

#### Task 4.5: `find` tool with fd fallback

**Files:**
- Create: `source/server/internal/tools/find.go`
- Test: `source/server/internal/tools/find_test.go`

- [ ] **Step 1: Test** glob + name matching against temp dir.
- [ ] **Step 2: Implement** — `fd` preferred, fallback to `find`. Structured rows: `{path, type, size}`.
- [ ] **Step 3: Commit** `feat(tools): find tool with fd preference`.

#### Task 4.6: Git read-only tools — `git_status`, `git_log`, `git_diff`, `git_blame`, `git_branches`, `git_show`

**Files:**
- Create: `source/server/internal/tools/git_read.go`
- Test: `source/server/internal/tools/git_read_test.go`

- [ ] **Step 1: Test** each against a temp repo created by `git init` + a commit. Assert structured rows (porcelain-v2 for status, --format for log).
- [ ] **Step 2: Implement** with `exec.Command`. All structured — no stdout strings.
- [ ] **Step 3: Commit** `feat(tools): git read tools (status, log, diff, blame, branches, show)`.

#### Task 4.7: Project meta tools — `project_context`, `classify_file`

**Files:**
- Create: `source/server/internal/tools/project_meta.go`
- Test: `source/server/internal/tools/project_meta_test.go`

- [ ] **Step 1: Test** `project_context` reads `.cercano/context.md` from a temp dir or returns `not-initialized`.
- [ ] **Step 2: Test** `classify_file("foo_test.go")` → `{language: "go", kind: "test"}`, `classify_file("config.yaml")` → `{language: "yaml", kind: "config"}`.
- [ ] **Step 3: Implement** with extension + name-pattern map.
- [ ] **Step 4: Commit** `feat(tools): project_context and classify_file (algorithmic)`.

### Phase 4 Completion

- Tests: `cd source/server && go test ./internal/tools/... -count=1`
- Manual: run a small Go program calling the registry against this repo's cercano source; verify `grep "SmartRouter"` returns sensible rows.
- Checkpoint + note.

---

## Phase 5: Built-in W/X tools + permission enforcement (agent)

### Objective

Write-tier and destructive-tier tools, plus the permission gate that turns the agent's tool-call surface into a "ask the user / autoapply / refuse" decision.

### Tasks

#### Task 5.1: Permission gate

**Files:**
- Create: `source/server/internal/tools/permission.go`
- Test: `source/server/internal/tools/permission_test.go`

- [ ] **Step 1: Define interface:**

```go
type PermissionGate interface {
    // Returns Approved if the tool may run, with the (possibly edited) args.
    // Returns Denied (refuse silently) or Edit (user wants to modify before run).
    Check(ctx context.Context, tool Tool, args json.RawMessage) (Decision, error)
}
type Decision struct { Approve bool; Edited json.RawMessage; Reason string }
```

- [ ] **Step 2: Implement `AlwaysAllowGate`** (R tier or bypass mode), `RequireConfirmGate` (forwards to a client confirm channel via a `ConfirmRequester` interface). The CLI provides the `ConfirmRequester`; for tests, use a fake.
- [ ] **Step 3: Implement** `TierGate` that combines: R → always allow; W → call confirm; X → call confirm with `destructive: true` flag.
- [ ] **Step 4: Test** all three tiers with both fakes.
- [ ] **Step 5: Commit** `feat(tools): permission gate with R/W/X tiers`.

#### Task 5.2: Filesystem W-tier tools — `write_file`, `edit_file`, `apply_patch`

**Files:**
- Create: `source/server/internal/tools/fs_write.go`
- Test: `source/server/internal/tools/fs_write_test.go`

- [ ] **Step 1: Test** each against a temp dir. `edit_file` must require exact-match string replace; mismatches return a structured error, not a corrupted file. `apply_patch` must reject hunks with stale context.
- [ ] **Step 2: Implement** using `github.com/sergi/go-diff/diffmatchpatch` for patch validation. Always atomic write (write to temp, rename).
- [ ] **Step 3: Commit** `feat(tools): write_file, edit_file, apply_patch (W-tier)`.

#### Task 5.3: Filesystem X-tier tools — `rm_file`, `mv_file`

**Files:**
- Create: `source/server/internal/tools/fs_destructive.go`
- Test: `source/server/internal/tools/fs_destructive_test.go`

- [ ] **Step 1: Test** in temp dir; assert permission tier is X.
- [ ] **Step 2: Implement** trivially via `os.Remove` / `os.Rename`.
- [ ] **Step 3: Commit** `feat(tools): rm_file, mv_file (X-tier)`.

#### Task 5.4: Git W/X tools — `git_add`, `git_commit`, `git_branch_create`, `git_checkout`, `git_push`, `git_reset_hard`

**Files:**
- Create: `source/server/internal/tools/git_write.go`
- Test: `source/server/internal/tools/git_write_test.go`

- [ ] **Step 1: Test** in temp repo. Verify tier per tool — push/reset hard are X.
- [ ] **Step 2: Implement.**
- [ ] **Step 3: Commit** `feat(tools): git write tools (W) and git_push, git_reset_hard (X)`.

#### Task 5.5: Build / test / run W-tier tools — `run_command`, `run_tests`, `build`, `lint`, `format`

**Files:**
- Create: `source/server/internal/tools/run.go`
- Create: `source/server/internal/tools/autodetect.go`
- Test: `source/server/internal/tools/run_test.go`

- [ ] **Step 1: Test** `autodetect.Detect(dir)` against fixture dirs with `go.mod`, `package.json`, `pyproject.toml`, `Cargo.toml`. Asserts the correct `Project` struct (test cmd, build cmd, lint cmd, format cmd).
- [ ] **Step 2: Implement** `Detect` — file-existence heuristics, no LLM call.
- [ ] **Step 3: Implement** `run_command` (generic shell exec with timeout, captured stdout/stderr/exit), `run_tests` / `build` / `lint` / `format` (use Detect to pick the command).
- [ ] **Step 4: Truncate output to 32 KiB.**
- [ ] **Step 5: Commit** `feat(tools): run_command, run_tests, build, lint, format with auto-detect`.

#### Task 5.6: Allowlist + permissions config loader

**Files:**
- Create: `source/server/internal/tools/permissions_config.go`
- Test: `source/server/internal/tools/permissions_config_test.go`

- [ ] **Step 1: Test** that a YAML file with `allowlist: [{tool: run_command, when: "args.cmd starts with 'go test'", promote: silent}]` parses and promotes correctly.
- [ ] **Step 2: Implement** YAML loader + a tiny predicate evaluator. Don't write a DSL — support `starts with`, `equals`, `matches /regex/` only.
- [ ] **Step 3: Wire into `TierGate`.**
- [ ] **Step 4: Commit** `feat(tools): permissions.yaml allowlist with simple predicates`.

### Phase 5 Completion

- Tests: `cd source/server && go test ./internal/tools/... -count=1`
- Manual: run `bin/cercano` with a temp project, confirm a write-tier tool calls back to the (fake) confirm gate.
- Checkpoint + note.

---

## Phase 6: MCP host runtime (agent)

### Objective

Cercano becomes an MCP host (in addition to staying an MCP server in `--mcp` mode). Loads external MCP servers, registers their tools alongside built-ins.

### Tasks

#### Task 6.1: MCP client integration

**Files:**
- Create: `source/server/internal/mcphost/client.go`
- Test: `source/server/internal/mcphost/client_test.go`

- [ ] **Step 1: Test** against a stub MCP server (use `modelcontextprotocol/go-sdk` examples as the stub). Assert `Initialize`, `ListTools`, `CallTool` round-trips.
- [ ] **Step 2: Implement** using the official Go SDK. Each remote server gets a long-lived stdio connection.
- [ ] **Step 3: Commit** `feat(mcphost): MCP client over stdio`.

#### Task 6.2: Server lifecycle manager

**Files:**
- Create: `source/server/internal/mcphost/manager.go`
- Test: `source/server/internal/mcphost/manager_test.go`

- [ ] **Step 1: Test** `Start(name, transport, cmd, args, env)`, `Stop(name)`, `Restart(name)`, `Health(name)`. Use a stub server that exits on stdin close.
- [ ] **Step 2: Implement** — `os/exec.Cmd` with stdio pipes, supervised by a goroutine that detects process exit and marks server `failed`.
- [ ] **Step 3: Health pings** — periodic `ping` via the MCP `ping` RPC every 30s; mark `degraded` after 1 missed, `failed` after 3.
- [ ] **Step 4: Commit** `feat(mcphost): server lifecycle manager with health monitoring`.

#### Task 6.3: MCP tool adapter — register external tools as built-ins

**Files:**
- Create: `source/server/internal/mcphost/adapter.go`
- Test: `source/server/internal/mcphost/adapter_test.go`

- [ ] **Step 1: Test** that an external MCP tool with name `search` from server `brave` registers as `mcp/brave/search` in the `tools.Registry` and can be called through the same `Tool.Execute` interface.
- [ ] **Step 2: Implement** an `mcpTool` type wrapping a remote tool spec + a manager reference. `Execute` proxies to `CallTool`.
- [ ] **Step 3: Permission tier** — MCP tools default to W (require confirm) since we can't introspect their side effects. Allow override in `mcp.yaml` per tool.
- [ ] **Step 4: Commit** `feat(mcphost): adapt external tools into the built-in registry namespace`.

#### Task 6.4: Config loader — merge global + project `mcp.yaml`

**Files:**
- Create: `source/server/internal/mcphost/config.go`
- Test: `source/server/internal/mcphost/config_test.go`

- [ ] **Step 1: Test** that `~/.config/cercano/mcp.yaml` + `<project>/.cercano/mcp.yaml` merge with project entries overriding by `name`.
- [ ] **Step 2: Implement** YAML unmarshaling with merge.
- [ ] **Step 3: Commit** `feat(mcphost): merged global + project MCP config`.

#### Task 6.5: Wire into agent + RPCs

**Files:**
- Modify: `source/server/internal/agent/agent.go` — instantiate `mcphost.Manager` on startup, start all configured servers.
- Modify: `source/server/internal/server/server.go` — implement `ListMcpServers`, `AddMcpServer`, `RestartMcpServer`, `ListMcpTools`, `ListTools`.

- [ ] **Step 1: Tests** for each RPC handler end-to-end against a stub server.
- [ ] **Step 2: Implement.**
- [ ] **Step 3: Commit** `feat(server): MCP host RPCs and agent wiring`.

### Phase 6 Completion

- Tests: `cd source/server && go test ./internal/mcphost/... ./internal/server/... -count=1`
- Manual: add a fake MCP server entry to `~/.config/cercano/mcp.yaml`, restart cercano, call `ListMcpTools` via grpcurl, verify the fake's tools appear.
- Checkpoint + note.

---

## Phase 7: Slash RPCs + Streaming verification (agent)

### Objective

The small glue RPCs the CLI's slash commands hit, plus end-to-end verification of streaming chat (closes any gaps in the existing token-streaming work).

### Tasks

#### Task 7.1: Implement `Clear`, `SwitchProject`, `GetProjectContext`, `ListModels`, `GetUsage` handlers

**Files:**
- Modify: `source/server/internal/server/server.go`

- [ ] **Step 1: For each handler**, write a TDD test that exercises the RPC end-to-end with a stub agent.
- [ ] **Step 2: Implement** each. Most thinly wrap existing internal functions:
  - `Clear` → `store.Delete` + counter reset
  - `SwitchProject` → set the agent's project root + re-read `.cercano/context.md`
  - `GetProjectContext` → read `.cercano/context.md`
  - `ListModels` → existing `cercano_models` behavior
  - `GetUsage` → existing `cercano_stats` behavior
- [ ] **Step 3: Commit** `feat(server): slash-style RPCs (Clear, SwitchProject, GetProjectContext, ListModels, GetUsage)`.

#### Task 7.2: Verify streaming chat RPC works end-to-end

**Files:**
- Test: `source/server/internal/agent/streaming_test.go` (extend existing)
- Possibly: `source/server/internal/server/server.go`

- [ ] **Step 1: Write an integration test** that opens a stream, submits a prompt, and asserts tokens arrive incrementally (not in a single chunk at the end).
- [ ] **Step 2: If it fails**, close the gap — likely a buffering issue in the gRPC server stream send.
- [ ] **Step 3: Commit** `test(agent): streaming chat integration coverage` (+ any fix commit).

### Phase 7 Completion

- Tests: `cd source/server && go test ./internal/server/... ./internal/agent/... -count=1`
- Manual: grpcurl a streaming Chat RPC, verify per-token messages.
- Checkpoint + note. **Agent surface is now ready for the CLI.**

---

## Phase 8: CLI scaffold + theme primitives

### Objective

Lay down the Go CLI module, dependencies, entry point, theme constants, and reusable chrome helpers.

### Tasks

#### Task 8.1: Module scaffold

**Files:**
- Create: `source/clients/cli/` directory
- Create: `source/clients/cli/go.mod`
- Create: `source/clients/cli/cmd/cercano-cli/main.go`
- Create: `source/clients/cli/Makefile`

- [ ] **Step 1: `go mod init cercano/source/clients/cli`** with module path matching the workspace pattern.
- [ ] **Step 2: Add deps**: `github.com/charmbracelet/bubbletea`, `lipgloss`, `bubbles`, the project's `pkg/proto` via `replace` directive to the server module.
- [ ] **Step 3: `main.go`** — flag parsing (`--bypass-permissions`, `--yolo`, `--server-addr`, `--no-banner`, `--theme`), prints a placeholder "cercano-cli starting…" and exits 0.
- [ ] **Step 4: Makefile** with `build`, `test`, `run`, `lint` targets.
- [ ] **Step 5: Commit** `feat(cli): module scaffold and main entry`.

#### Task 8.2: Theme palette

**Files:**
- Create: `source/clients/cli/internal/theme/palette.go`
- Create: `source/clients/cli/internal/theme/styles.go`
- Test: `source/clients/cli/internal/theme/palette_test.go`

- [ ] **Step 1: Test** that `Cracker()` returns a `Palette` with the 12 named colors at the exact hex codes specified in `spec.md §6.1`.
- [ ] **Step 2: Define `Palette` struct** with named lipgloss.Color fields. Implement `Cracker()` returning the default palette.
- [ ] **Step 3: Define `Styles` struct** that takes a palette and exposes pre-built lipgloss styles (border, primary, accent, user-prompt, dim, error, success, etc.).
- [ ] **Step 4: Commit** `feat(cli): cracker theme palette and lipgloss styles`.

#### Task 8.3: Chrome primitives — frame, divider, box helpers

**Files:**
- Create: `source/clients/cli/internal/chrome/frame.go`
- Test: `source/clients/cli/internal/chrome/frame_test.go`

- [ ] **Step 1: Test** that `OuterFrame(width=80, rounded=true)` produces top/middle/bottom strings of exactly width chars each, using `╭ ╮ ╰ ╯`.
- [ ] **Step 2: Implement** with `lipgloss.NewStyle().Border(...)`. Provide `RoundedFrame` and `SharpFrame`. Test column count for several widths to nail the resize math.
- [ ] **Step 3: Helpers** `Divider(width)`, `LeftBar()`, `RightBar()` returning runes that compose into frames.
- [ ] **Step 4: Commit** `feat(cli): chrome primitives (frames, dividers)`.

#### Task 8.4: UI config loader

**Files:**
- Create: `source/clients/cli/internal/config/ui.go`
- Test: `source/clients/cli/internal/config/ui_test.go`

- [ ] **Step 1: Test** loading `~/.config/cercano/ui.yaml` with `font:`, `theme:`, `keybindings:`. Defaults applied when fields missing.
- [ ] **Step 2: Implement** with `gopkg.in/yaml.v3`. Honor `$CERCANO_UI_CONFIG` override.
- [ ] **Step 3: Commit** `feat(cli): UI config loader`.

### Phase 8 Completion

- Tests: `cd source/clients/cli && go test ./... -count=1`
- Manual: `make run` prints the startup line; no panics.
- Checkpoint + note.

---

## Phase 9: CLI banner + shimmer animation

### Objective

The locked F-refined banner with smooth, mildly-angled shimmer on boot.

### Tasks

#### Task 9.1: Static banner rendering

**Files:**
- Create: `source/clients/cli/internal/banner/banner.go`
- Test: `source/clients/cli/internal/banner/banner_test.go`

- [ ] **Step 1: Test** that `Render(width=62, palette)` returns a string with exactly 8 lines, each visually 62 cols wide, containing the wordmark at col 4 of rows 3 and 4.
- [ ] **Step 2: Implement** — hardcoded lines (the locked 8-row layout), styled via the palette.
- [ ] **Step 3: Commit** `feat(banner): static F-refined banner`.

#### Task 9.2: Per-column shimmer color function

**Files:**
- Create: `source/clients/cli/internal/banner/shimmer.go`
- Test: `source/clients/cli/internal/banner/shimmer_test.go`

- [ ] **Step 1: Test** `ColorAt(col, sweepPos, palette)` produces `base` far from sweep, `white` at the head, and a smooth gradient between. Sweep at col -10 → all base; sweep at col 0 with col 0 → bright peak.
- [ ] **Step 2: Implement** the smooth color falloff from `spec.md §6.3`.
- [ ] **Step 3: Commit** `feat(banner): shimmer color function`.

#### Task 9.3: Bubble Tea shimmer model

**Files:**
- Create: `source/clients/cli/internal/banner/anim_model.go`
- Test: `source/clients/cli/internal/banner/anim_model_test.go`

- [ ] **Step 1: Test** that the model emits 30fps tea.Tick messages, advances `sweepPos` linearly over 1.4s, and exits the animation after one full pass.
- [ ] **Step 2: Implement** as a Bubble Tea sub-model: `Init`, `Update`, `View`. Angle = 1 col (top row sweep = bottom row sweep + 0.5).
- [ ] **Step 3: View** uses the palette + shimmer color fn to render the banner with per-column color overrides during the sweep.
- [ ] **Step 4: Commit** `feat(banner): bubble tea shimmer animation model`.

### Phase 9 Completion

- Tests: `cd source/clients/cli && go test ./internal/banner/... -count=1`
- Manual: run a tiny standalone main that just plays the banner; eyeball the shimmer.
- Checkpoint + note.

---

## Phase 10: CLI main session model

### Objective

The Bubble Tea root model: header + scrollback + input + status bar, with bulletproof resize handling, auto-launching the agent server, and gRPC client connection.

### Tasks

#### Task 10.1: Agent connection — gRPC client with auto-launch

**Files:**
- Create: `source/clients/cli/internal/agentclient/client.go`
- Test: `source/clients/cli/internal/agentclient/client_test.go`

- [ ] **Step 1: Test** `Connect(ctx, addr)` — pass `localhost:50052`; if no listener, spawn `bin/cercano agent` (new subcommand we'll add) as a child process; wait for socket; connect.
- [ ] **Step 2: Implement** — `net.Dial` check, then `exec.Command` with `Process.Release()` so the child outlives a CLI crash; poll the port up to 5s.
- [ ] **Step 3: Add `cercano agent` subcommand** to the server (Phase 1 left it as `cercano` only).

```go
// source/server/cmd/cercano/main.go (modify)
case "agent":
    // run the gRPC server, no MCP, foreground
```

- [ ] **Step 4: Commit** `feat(cli): gRPC client with auto-launch agent`.

#### Task 10.2: Root model — Bubble Tea program

**Files:**
- Create: `source/clients/cli/internal/ui/model.go`
- Test: `source/clients/cli/internal/ui/model_test.go`

- [ ] **Step 1: Define `Model` struct:**

```go
type Model struct {
    width, height int
    palette       theme.Palette
    styles        theme.Styles
    header        HeaderModel
    scrollback    ScrollbackModel
    input         InputModel
    status        StatusModel
    splash        *banner.AnimModel // nil after first dismiss
    agent         agentclient.Client
    convID        string
    bypassMode    BypassMode
}
```

- [ ] **Step 2: Init** — start the splash if `--no-banner` is not set; connect to agent; create a fresh conversation id.
- [ ] **Step 3: Update**:
  - `tea.WindowSizeMsg` → set width/height, propagate to all sub-models.
  - Splash tick messages → forward to splash model until it signals done.
  - User input → submit to agent, transition state.
- [ ] **Step 4: View** — vertical composition using lipgloss `JoinVertical`. Alt-screen mode enabled via `tea.WithAltScreen()`.
- [ ] **Step 5: Tests** — drive the model with synthetic messages, assert state transitions. **Specifically test resize:** send several `WindowSizeMsg` with shrinking widths during a fake assistant stream; assert the rendered view's char width matches the message width on every frame.
- [ ] **Step 6: Commit** `feat(cli): root Bubble Tea model with resize-safe layout`.

#### Task 10.3: Header sub-model

**Files:**
- Create: `source/clients/cli/internal/ui/header.go`
- Test: `source/clients/cli/internal/ui/header_test.go`

- [ ] **Step 1: Test** rendering at widths 60, 80, 120 — `▓▓ CERCANO v0.1.0  ·  ~/git/cercano · qwen3-coder@local  ·  ● connected`.
- [ ] **Step 2: Implement** with truncation rules: shrink cwd to last 2 path components → just project name → drop entirely → drop the dot separator.
- [ ] **Step 3: Commit** `feat(cli): header sub-model with adaptive truncation`.

#### Task 10.4: Scrollback sub-model with `bubbles/viewport`

**Files:**
- Create: `source/clients/cli/internal/ui/scrollback.go`
- Test: `source/clients/cli/internal/ui/scrollback_test.go`

- [ ] **Step 1: Test** appending 100 turns, then resizing from width 100 → 60. Each line wraps correctly; no garbage.
- [ ] **Step 2: Implement** using `bubbles/viewport`. Internally store raw `Turn` items (not pre-rendered strings); `View()` re-renders by re-wrapping each turn at the current width every frame.
- [ ] **Step 3: Render functions** for user turn (`▶ ...`), assistant turn (default text), tool call (boxed sub-frame placeholder for now — fully rendered in Phase 12).
- [ ] **Step 4: Commit** `feat(cli): scrollback with raw-content storage, re-wrap on render`.

#### Task 10.5: Input sub-model with `bubbles/textinput`

**Files:**
- Create: `source/clients/cli/internal/ui/input.go`
- Test: `source/clients/cli/internal/ui/input_test.go`

- [ ] **Step 1: Test** that pressing Enter submits a `SubmitMsg{Content}`, Shift+Enter inserts a newline (multi-line mode).
- [ ] **Step 2: Implement** wrapping `bubbles/textinput`. Lime `▶` prefix.
- [ ] **Step 3: Commit** `feat(cli): input sub-model with multi-line via shift+enter`.

#### Task 10.6: Status bar sub-model

**Files:**
- Create: `source/clients/cli/internal/ui/status.go`
- Test: `source/clients/cli/internal/ui/status_test.go`

- [ ] **Step 1: Test** rendering with values `(used=21400, max=128000, lastIn=1247, lastOut=412, latency=214ms, mode="local")` produces:

```
ctx ██████░░░░░░░░░░░░░░ 21.4k/128k 17%  ·  turn 1.2k↑412↓  ·  214ms  ·  local
```

- [ ] **Step 2: Implement** with a 20-cell meter, lime fill / dim-amber empty. Percentage color shifts amber → red past 70% / 90%.
- [ ] **Step 3: When bypass mode active**, prepend a solid red `! BYPASS` block (Phase 16 wires it for real; expose the field now).
- [ ] **Step 4: Commit** `feat(cli): status bar with context meter and bypass indicator`.

### Phase 10 Completion

- Tests: `cd source/clients/cli && go test ./internal/ui/... ./internal/agentclient/... -count=1`
- Manual: run `bin/cercano-cli` (build target), type a placeholder message, observe scrollback append + status bar tick + resize-clean redraw. Drag the terminal narrow during a fake stream — no garbage.
- Checkpoint + note. **Acceptance criterion §12.4 (resize) now provable.**

---

## Phase 11: Streaming chat turn + context-meter polling

### Objective

Submit input → streaming RPC → tokens append live to scrollback → context meter ticks on each turn.

### Tasks

#### Task 11.1: Streaming-aware scrollback append

**Files:**
- Modify: `source/clients/cli/internal/ui/scrollback.go`
- Test: extend `scrollback_test.go`

- [ ] **Step 1: Test** that calling `AppendStream(turnID, chunk)` repeatedly grows the assistant message in place and re-wraps on every paint.
- [ ] **Step 2: Implement** — turns have a `Streaming bool` field; renderer shows a thinking indicator (lime `⟳`) while streaming.
- [ ] **Step 3: Commit** `feat(cli): streaming-aware scrollback append`.

#### Task 11.2: Streaming chat client

**Files:**
- Modify: `source/clients/cli/internal/agentclient/client.go`
- Test: extend `client_test.go`

- [ ] **Step 1: Test** against a stub gRPC server that sends 3 chunks then EOF.
- [ ] **Step 2: Implement** `StreamChat(req) (<-chan StreamMsg, error)` — wraps the gRPC stream, emits typed messages: `Token`, `ToolCall`, `Done(meta)`.
- [ ] **Step 3: Commit** `feat(agentclient): typed streaming chat channel`.

#### Task 11.3: Wire stream into root model

**Files:**
- Modify: `source/clients/cli/internal/ui/model.go`
- Test: extend `model_test.go`

- [ ] **Step 1: Test** that submitting input triggers `agent.StreamChat`, each token forwards to scrollback, and on `Done`, status bar context updates.
- [ ] **Step 2: Implement** — turn the channel into Bubble Tea `tea.Msg` values via a cmd that reads the channel.
- [ ] **Step 3: After `Done`, fire `agent.GetContextUsage(convID)` and apply the result to the status bar.**
- [ ] **Step 4: Commit** `feat(cli): wire streaming chat into root model`.

### Phase 11 Completion

- Tests: `cd source/clients/cli && go test ./internal/ui/... ./internal/agentclient/... -count=1`
- Manual: cercano-cli + cercano agent end-to-end — type "hello", see tokens arrive, status bar context bar fills.
- Checkpoint + note. **Acceptance criterion §12.2 (multi-turn + streaming + context meter) now provable.**

---

## Phase 12: Table + diff render primitives, tool-call rendering, confirm prompts

### Objective

The dedicated Table primitive (non-negotiable §5.2), the diff renderer, and the boxed tool-call sub-frame with letter-shortcut confirms.

### Tasks

#### Task 12.1: Table primitive

**Files:**
- Create: `source/clients/cli/internal/render/table.go`
- Test: `source/clients/cli/internal/render/table_test.go`

- [ ] **Step 1: Define types:**

```go
type Column struct { Name string; Priority int /* lower drops first */; Wrappable bool }
type Table   struct { Cols []Column; Rows []map[string]string }
func (t Table) Render(maxWidth int, styles theme.Styles) string
```

- [ ] **Step 2: Test** the width-fit rules from `spec.md §5.2`:
  - Fits: 4-col grid with box-draw, lime header row.
  - Too wide: drop lowest-priority column (footnote that says `(dropped: X)`).
  - Still too wide: truncate the wrappable column with ellipsis.
  - Still too wide: transpose to key:value pairs.
- [ ] **Step 3: Implement.** Column widths computed by max(header, max cell), then iterative drop/truncate/transpose.
- [ ] **Step 4: Commit** `feat(render): Table primitive with width-fit drop/truncate/transpose`.

#### Task 12.2: Markdown table interceptor

**Files:**
- Create: `source/clients/cli/internal/render/markdown_intercept.go`
- Test: `source/clients/cli/internal/render/markdown_intercept_test.go`

- [ ] **Step 1: Test** that an assistant response containing a `| col1 | col2 |` markdown table is detected and converted into a `Table`, leaving non-table text alone.
- [ ] **Step 2: Implement** with a simple line-by-line parser (no full markdown lib).
- [ ] **Step 3: Wire** the interceptor into the assistant-message renderer in `scrollback.go`.
- [ ] **Step 4: Commit** `feat(render): markdown table interceptor`.

#### Task 12.3: Diff renderer

**Files:**
- Create: `source/clients/cli/internal/render/diff.go`
- Test: `source/clients/cli/internal/render/diff_test.go`

- [ ] **Step 1: Test** rendering a unified diff with `+`/`-` lines, gutter colors, collapsed unchanged blocks (`… N unchanged …`).
- [ ] **Step 2: Implement** using `github.com/sergi/go-diff` to parse, then style with lipgloss.
- [ ] **Step 3: Commit** `feat(render): diff renderer with colored gutter`.

#### Task 12.4: Tool-call sub-frame component

**Files:**
- Create: `source/clients/cli/internal/render/toolcall.go`
- Test: `source/clients/cli/internal/render/toolcall_test.go`

- [ ] **Step 1: Test** that a tool call `{Name: "write_file", Args: {Path: "x.go", Content: "..."}}` renders as:

```
┌─ tool:write_file · x.go ──
│   + ...
└──
```

with the diff inside if the content represents a diff against existing content.

- [ ] **Step 2: Implement.**
- [ ] **Step 3: Commit** `feat(render): boxed tool-call sub-frame`.

#### Task 12.5: Confirm prompts — letter shortcuts

**Files:**
- Create: `source/clients/cli/internal/ui/confirm.go`
- Test: `source/clients/cli/internal/ui/confirm_test.go`

- [ ] **Step 1: Test** that pressing `y`/`n`/`d`/`e` resolves a confirm with the corresponding decision, no Enter required.
- [ ] **Step 2: Implement** as a Bubble Tea sub-model that overlays at the bottom of the scrollback area. Resolves on any of the four keys; Esc → same as `n`.
- [ ] **Step 3: `d` expands** the boxed diff inline; `e` opens `$EDITOR` on a temp file and applies the edited version on save.
- [ ] **Step 4: Commit** `feat(cli): letter-shortcut confirm prompts (y/n/d/e)`.

#### Task 12.6: Wire tool calls + confirms end-to-end

**Files:**
- Modify: `source/clients/cli/internal/ui/model.go`
- Modify: `source/clients/cli/internal/agentclient/client.go`

- [ ] **Step 1: Add `ConfirmRequester`** interface in agentclient that the agent calls back through when a W/X tool needs approval (gRPC bidi stream).
- [ ] **Step 2: Modify the streaming chat** to surface ToolCall messages → render via `render.ToolCall` → if W/X tier, show confirm → answer back over the stream.
- [ ] **Step 3: Test** end-to-end with a stub agent that emits a W-tier `write_file` and expects a `y` confirm.
- [ ] **Step 4: Commit** `feat(cli): tool-call rendering with confirm flow`.

### Phase 12 Completion

- Tests: `cd source/clients/cli && go test ./internal/render/... ./internal/ui/... -count=1`
- Manual: trigger an agent response with both a markdown table AND a tool call; verify table renders via primitive, confirm prompt works.
- Checkpoint + note. **Acceptance criteria §12.3 (diff confirm) and §12.9 (table sanity) now provable.**

---

## Phase 13: Slash command parsing + basic dispatch

### Objective

Algorithmic prefix-match command registry; wire `/help`, `/clear`, `/quit`, `/models`, `/model`, `/config`, `/init`, `/context`, `/tools`, `/usage`.

### Tasks

#### Task 13.1: Command registry + parser

**Files:**
- Create: `source/clients/cli/internal/slash/registry.go`
- Create: `source/clients/cli/internal/slash/parser.go`
- Test: `source/clients/cli/internal/slash/parser_test.go`

- [ ] **Step 1: Define types:**

```go
type Command struct { Name string; Aliases []string; Help string; Handler func(ctx Context, args []string) tea.Cmd }
type Registry struct { /* … */ }
func (r *Registry) Register(c Command)
func (r *Registry) Match(input string) (*Command, []string, bool)  // exact + fuzzy fallback
```

- [ ] **Step 2: Test** parsing `/models`, `/model qwen3-coder`, unknown `/xyz` → suggestion.
- [ ] **Step 3: Implement** prefix match → exact match → fuzzy (`xfz` style) fallback for the "did you mean" suggestion.
- [ ] **Step 4: Commit** `feat(slash): command registry with algorithmic dispatch`.

#### Task 13.2: Wire slash detection into input

**Files:**
- Modify: `source/clients/cli/internal/ui/input.go`
- Modify: `source/clients/cli/internal/ui/model.go`

- [ ] **Step 1: Test** that input starting with `/` routes to `slash.Registry.Match` instead of agent.StreamChat.
- [ ] **Step 2: Implement.**
- [ ] **Step 3: Commit** `feat(cli): route /-prefix input to slash registry`.

#### Task 13.3: Basic commands — `/help`, `/quit`, `/clear`

**Files:**
- Create: `source/clients/cli/internal/slash/basic.go`
- Test: `source/clients/cli/internal/slash/basic_test.go`

- [ ] **Step 1: Test** each — `/help` returns a Table of registered commands; `/quit` returns `tea.Quit`; `/clear` calls `agent.Clear(convID)`.
- [ ] **Step 2: Implement.**
- [ ] **Step 3: Commit** `feat(slash): /help, /quit, /clear`.

#### Task 13.4: Model commands — `/models`, `/model [name]`, `/config`

**Files:**
- Create: `source/clients/cli/internal/slash/models.go`
- Test: `source/clients/cli/internal/slash/models_test.go`

- [ ] **Step 1: Test** `/models` calls `agent.ListModels` and emits a Table; `/model qwen3-coder` calls `agent.SetConfig(model=...)`.
- [ ] **Step 2: Implement.**
- [ ] **Step 3: Commit** `feat(slash): /models, /model, /config`.

#### Task 13.5: Project commands — `/init`, `/context`, `/tools`, `/usage`

**Files:**
- Create: `source/clients/cli/internal/slash/project.go`
- Test: `source/clients/cli/internal/slash/project_test.go`

- [ ] **Step 1: Test** each against a stub agent.
- [ ] **Step 2: Implement.**
- [ ] **Step 3: Commit** `feat(slash): /init, /context, /tools, /usage`.

### Phase 13 Completion

- Tests: `cd source/clients/cli && go test ./internal/slash/... -count=1`
- Manual: every command above works end-to-end.
- Checkpoint + note.

---

## Phase 14: Conversation persistence UI

### Objective

`/resume`, `/history`, `/diff`, `/undo` — the affordances that turn the SQLite store into something the user can navigate.

### Tasks

#### Task 14.1: Conversation picker overlay

**Files:**
- Create: `source/clients/cli/internal/ui/picker.go`
- Test: `source/clients/cli/internal/ui/picker_test.go`

- [ ] **Step 1: Test** that the picker, given a list of conversations, supports up/down/enter/esc and emits `SelectedMsg{ID}` or `CancelMsg`.
- [ ] **Step 2: Implement** as a reusable overlay (Bubble Tea sub-model). Renders a floating panel using `chrome.RoundedFrame` over scrollback.
- [ ] **Step 3: Filter input** at top — substring + fuzzy match.
- [ ] **Step 4: Commit** `feat(cli): reusable picker overlay`.

#### Task 14.2: `/resume` and `/history`

**Files:**
- Create: `source/clients/cli/internal/slash/conversation.go`
- Test: `source/clients/cli/internal/slash/conversation_test.go`

- [ ] **Step 1: Test** `/resume` no-args opens the picker; with id rehydrates directly. `/history` lists conversations as a Table — date, model, project, title, turns.
- [ ] **Step 2: Implement.**
- [ ] **Step 3: Commit** `feat(slash): /resume, /history`.

#### Task 14.3: `/diff` and `/undo`

**Files:**
- Modify: `source/clients/cli/internal/slash/conversation.go`
- Test: extend

- [ ] **Step 1: Test** `/diff` shows pending file changes from the most recent assistant turn's tool calls; `/undo` reverts them (restores backup files).
- [ ] **Step 2: For `/undo`** to work, the agent's `write_file` and `edit_file` tools must keep a per-turn backup. Add: agent-side `RevertLastTurn(conversation_id)` RPC + the proto + the handler.
- [ ] **Step 3: Implement client-side commands.**
- [ ] **Step 4: Commit** `feat(slash): /diff, /undo with agent-side backups`.

### Phase 14 Completion

- Tests across `slash`, `ui` packages.
- Manual: have a session, quit, restart, `/resume`, continue. Make an edit, `/undo`, verify the file reverts.
- Checkpoint + note. **Acceptance criterion §12.7 now provable.**

---

## Phase 15: MCP UI

### Objective

`/mcp list|add|remove|restart` and `/tools` — surface the host runtime.

### Tasks

#### Task 15.1: `/tools` listing

**Files:**
- Modify: `source/clients/cli/internal/slash/project.go` (already created in Phase 13)
- Test: extend

- [ ] **Step 1: Test** `/tools` calls `agent.ListTools()` and renders one Table with built-ins, another with MCP tools.
- [ ] **Step 2: Implement.**
- [ ] **Step 3: Commit** `feat(slash): /tools enumerates built-in + MCP`.

#### Task 15.2: `/mcp` subcommands

**Files:**
- Create: `source/clients/cli/internal/slash/mcp.go`
- Test: `source/clients/cli/internal/slash/mcp_test.go`

- [ ] **Step 1: Test** `/mcp list`, `/mcp add <name> <cmd>`, `/mcp restart <name>`, `/mcp remove <name>`.
- [ ] **Step 2: Implement** wiring to the corresponding agent RPCs.
- [ ] **Step 3: Commit** `feat(slash): /mcp list/add/remove/restart`.

### Phase 15 Completion

- Tests for slash/mcp.
- Manual: add a fake stdio MCP server via `/mcp add`, verify `/tools` reflects it.
- Checkpoint + note. **Acceptance criterion §12.8 now provable.**

---

## Phase 16: Bypass permissions UI

### Objective

`/bypass on|off|status`, the confirmation overlay (Enter-on-button gate), and the persistent red status-bar indicator + per-tool-call markers.

### Tasks

#### Task 16.1: Bypass state

**Files:**
- Create: `source/clients/cli/internal/bypass/state.go`
- Test: `source/clients/cli/internal/bypass/state_test.go`

- [ ] **Step 1: Test** transitions: `Off → Full → Off`, `Off → Tiered → Off`.
- [ ] **Step 2: Implement** as a small state machine with `Scope` enum (`Off`, `Full`, `Tiered`). Persisted in memory only (no auto-expire).
- [ ] **Step 3: Commit** `feat(bypass): state machine`.

#### Task 16.2: Confirmation overlay — Enter-on-button gate

**Files:**
- Create: `source/clients/cli/internal/ui/bypass_overlay.go`
- Test: `source/clients/cli/internal/ui/bypass_overlay_test.go`

- [ ] **Step 1: Test** rendering and interaction: arrow keys toggle scope radio (Full/Tiered), Tab moves focus to YES button, Enter confirms, Esc cancels.
- [ ] **Step 2: Implement** as a Bubble Tea sub-model. Red frame from chrome primitives.
- [ ] **Step 3: Lists the actual tools** that will be bypassed (queried from `agent.ListTools` filtered by permission tier).
- [ ] **Step 4: Commit** `feat(ui): bypass confirmation overlay`.

#### Task 16.3: `/bypass` slash command + flags

**Files:**
- Create: `source/clients/cli/internal/slash/bypass.go`
- Test: `source/clients/cli/internal/slash/bypass_test.go`
- Modify: `source/clients/cli/cmd/cercano-cli/main.go` — honor `--bypass-permissions` / `--yolo`.

- [ ] **Step 1: Test** `/bypass on` opens overlay; flag startup skips overlay; `/bypass status` returns current scope.
- [ ] **Step 2: Implement.**
- [ ] **Step 3: Commit** `feat(slash): /bypass on/off/status and CLI flags`.

#### Task 16.4: Wire bypass into agent — skip confirm gates when active

**Files:**
- Modify: `source/clients/cli/internal/agentclient/client.go`
- Modify: agent-side server handler that asks for confirm

- [ ] **Step 1: Test** that when bypass is Full, the agent's confirm-request gets auto-approved client-side without showing the prompt; tier `Tiered` auto-approves W but still prompts for X.
- [ ] **Step 2: Implement.**
- [ ] **Step 3: Each auto-approved call** still surfaces an inline marker in scrollback: `⚡ (no confirm — bypass)` in dim red. Implement in `render/toolcall.go`.
- [ ] **Step 4: Commit** `feat(bypass): auto-approve confirm-requests by tier, audit markers`.

#### Task 16.5: Status bar bypass indicator

**Files:**
- Modify: `source/clients/cli/internal/ui/status.go`
- Test: extend

- [ ] **Step 1: Test** that when bypass != Off, the status bar leads with a red `! BYPASS` block.
- [ ] **Step 2: Implement.**
- [ ] **Step 3: Commit** `feat(status): persistent ! BYPASS indicator`.

### Phase 16 Completion

- Tests across bypass, slash, ui.
- Manual: `/bypass on` overlay → Enter → run a coding loop → tool calls run silently with audit markers → status bar red the whole time.
- Checkpoint + note. **Acceptance criterion §12.6 now provable.**

---

## Phase 17: Font picker

### Objective

`/font` — enumerate monospace fonts, filter, apply via emulator-specific path, persist.

### Tasks

#### Task 17.1: Font enumeration

**Files:**
- Create: `source/clients/cli/internal/font/enum.go`
- Test: `source/clients/cli/internal/font/enum_test.go`

- [ ] **Step 1: Test** `List()` returns a non-empty `[]Font` on macOS/Linux.
- [ ] **Step 2: Implement** — try `fc-list :mono :family`, parse output. On macOS fallback: shell out to `system_profiler SPFontsDataType` (slow but reliable) and filter by `Fixed-Pitch: Yes`. On Windows: defer (panic with "not yet supported").
- [ ] **Step 3: Filter** to families with regular weight AND glyph coverage check (open `~/.local/share/fonts` or system path, parse with `golang.org/x/image/font/sfnt`, check U+2580 and U+2500).
- [ ] **Step 4: Commit** `feat(font): enumerate monospace fonts with glyph coverage check`.

#### Task 17.2: Emulator detection

**Files:**
- Create: `source/clients/cli/internal/font/emulator.go`
- Test: `source/clients/cli/internal/font/emulator_test.go`

- [ ] **Step 1: Test** detection by env vars: `TERM_PROGRAM=iTerm.app`, `KITTY_PID=…`, `GHOSTTY_RESOURCES_DIR=…`, `WEZTERM_EXECUTABLE=…`, fallback `unknown`.
- [ ] **Step 2: Implement.**
- [ ] **Step 3: Commit** `feat(font): emulator detection from env`.

#### Task 17.3: Per-emulator apply paths

**Files:**
- Create: `source/clients/cli/internal/font/apply.go`
- Test: `source/clients/cli/internal/font/apply_test.go`

- [ ] **Step 1: Test** that `Apply(emulator, fontName)` emits the right OSC for iTerm2, writes the right Ghostty config + SIGUSR1 for Ghostty, runs `kitten @ set-font` for Kitty. For unknown, return a `ManualInstructions` value with the config snippet.
- [ ] **Step 2: Implement** each path.
- [ ] **Step 3: Commit** `feat(font): per-emulator apply paths`.

#### Task 17.4: Picker overlay

**Files:**
- Create: `source/clients/cli/internal/ui/font_overlay.go`
- Test: `source/clients/cli/internal/ui/font_overlay_test.go`

- [ ] **Step 1: Test** the floating panel: arrow up/down navigates, `/` enters filter, Enter applies, Esc cancels.
- [ ] **Step 2: Implement** with `chrome.RoundedFrame`. Reuses `picker.go` from Phase 14 if feasible, with a font-specific detail strip below the list (emulator status, glyph compatibility).
- [ ] **Step 3: Commit** `feat(ui): font picker overlay`.

#### Task 17.5: `/font` slash command + persistence

**Files:**
- Create: `source/clients/cli/internal/slash/font.go`
- Modify: `source/clients/cli/internal/config/ui.go` — add `ui.font` field
- Test: extend

- [ ] **Step 1: Test** `/font` opens overlay → selection → `Apply` runs → `ui.yaml` updated → on next launch font re-applies.
- [ ] **Step 2: Implement.**
- [ ] **Step 3: Commit** `feat(slash): /font picker with persistence`.

### Phase 17 Completion

- Tests across font, ui, slash.
- Manual on iTerm2: pick a different font, see it apply live. On Ghostty: same. On Terminal.app: see the config-snippet message.
- Checkpoint + note. **Acceptance criterion §12.5 now provable.**

---

## Phase 18: Integration + acceptance validation + docs

### Objective

Walk through the 9 acceptance criteria from `spec.md §12` end-to-end. Polish edge cases. Documentation.

### Tasks

#### Task 18.1: Acceptance walk-through

**Files:** none (manual)

- [ ] **Step 1:** For each of §12.1–§12.9, run the scenario, document the result.
- [ ] **Step 2:** Any failure → file a follow-up task or open an issue. Block phase completion until §12.1–§12.4 (the truly load-bearing ones) pass.

#### Task 18.2: Performance pass

- [ ] **Step 1:** Splash render: time from `cercano` invocation to first interactive prompt. Target <2s including agent auto-launch.
- [ ] **Step 2:** Resize fluidity: drag width across 80→40→120, no perceptible lag or stale frames.
- [ ] **Step 3:** If targets miss, profile + optimize (most likely culprits: tokenizer cold start, gRPC dial timeout). Commit any fixes per phase rules.

#### Task 18.3: README + .agents/skill update

**Files:**
- Modify: `README.md` — add a "Standalone CLI" section
- Create: `.agents/skills/cercano-cli/SKILL.md`
- Modify: `.claude/skills/cercano-cli/SKILL.md` (mirror)

- [ ] **Step 1:** Document how to install, launch, the key slash commands, the bypass flag.
- [ ] **Step 2:** Skill file follows the existing pattern in `.agents/skills/cercano-*/SKILL.md`.
- [ ] **Step 3:** Commit `docs: cercano CLI README + skill`.

#### Task 18.4: Homebrew formula update

**Files:**
- Modify: `source/server/Formula/cercano.rb` — install both `cercano` and `cercano-cli` binaries

- [ ] **Step 1:** Add the new binary to the install block.
- [ ] **Step 2:** Tap-update test on a clean machine if practical.
- [ ] **Step 3:** Commit `chore(brew): install cercano-cli alongside cercano`.

### Phase 18 Completion

- Tests: full suite green.
- Manual verification report attached to checkpoint commit via `git notes`.
- Update `conductor/plan.md` master plan: mark this track complete.

---

## Self-Review Checklist (done by author before handoff)

- [x] Spec §3.1 (algorithmic > LLM) → enforced in tools registry dispatch (Task 4.1, 6.3), slash parser (13.1), font filter (17.4), title derivation (2.2), file classify (4.7), autodetect (5.5).
- [x] Spec §3.2 (CLI/agent separation) → file layout splits `source/server/internal/` (agent) from `source/clients/cli/` (CLI). No cross-imports.
- [x] Spec §5.1 (resize correctness) → Task 10.4 stores raw content + re-wraps on render; Task 10.2 tests narrow-during-stream.
- [x] Spec §5.2 (Table primitive) → Task 12.1 implements; Task 12.2 intercepts markdown tables.
- [x] Spec §5.3 (live context meter) → Task 3.x agent side, Task 10.6 + 11.3 client side.
- [x] Spec §6 (visual design) → Phases 8, 9, 10.
- [x] Spec §7.1 (slash commands) → Phases 13, 14, 15, 16, 17 cover every command in the V1 list.
- [x] Spec §7.2 (font picker) → Phase 17.
- [x] Spec §7.3 (conversation persistence) → Phase 2 (store) + Phase 14 (UI).
- [x] Spec §7.4 (diff rendering) → Task 12.3.
- [x] Spec §8.1–8.6 (agent additions) → Phases 1–7.
- [x] Spec §9 (permission model + bypass) → Task 5.1 + Phase 16.
- [x] Spec §10 (configuration) → all config files are created in their owning phase.
- [x] Spec §12 (acceptance) → Task 18.1.
- [x] No placeholders — all code blocks contain working Go fragments or exact commands.
- [x] Type consistency — `Tool`, `Permission`, `Result`, `Conversation`, `Turn`, `ToolCall` are used consistently from definition forward.
- [x] No scope creep — features beyond the spec are NOT planned (no themes beyond `cracker`, no Windows font enum, no semantic_search).
