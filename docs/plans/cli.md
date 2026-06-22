# Cercano CLI

## Overview / Goal

Stand-alone Cercano CLI — a Go-based terminal agent harness with retro 80s cracker/hacker chrome (amber + lime on charcoal, shadowed block-letter banner, themed chrome throughout), comparable in scope to Claude Code or Codex CLI. It consumes the shared Cercano agent over gRPC.

Cercano today is consumed via MCP (Claude Code, Cursor) and IDE extensions (VS Code, Zed) — no first-party terminal experience. This track delivers one, and to make it useful, substantially enriches the shared agent surface (conversation persistence, MCP host runtime, context-window accounting, built-in CLI/shell tool suite, slash-style RPCs). The CLI is a thin client; agent logic stays in the shared core so VS Code, Zed, and future clients consume it equally.

**What changes:**
- New CLI client under `source/clients/cli/` (Go, Bubble Tea).
- Agent surface additions under `source/server/internal/` (conversation persistence, MCP host runtime, context meter, built-in tool suite, slash RPCs).
- New gRPC service methods.
- New config files: `~/.config/cercano/config.yaml` (server), `ui.yaml` (CLI), `permissions.yaml`, `mcp.yaml`, per-project `.cercano/mcp.yaml`, `conversations.db`.

**What does NOT change:** existing MCP server mode (`cercano --mcp`), VS Code / Zed extensions, existing SmartRouter / Coordinator / engine layer / Ollama (enriched, not replaced).

## Design / Approach

### Design principles (load-bearing)

1. **Algorithmic over LLM whenever possible.** Default to deterministic algorithms; only call a model when no algorithmic path exists. New algorithmic call sites: slash command parsing (prefix match), context-window accounting (tokenizer + sum), MCP tool dispatch (name lookup + arg validation), diff rendering, conversation truncation/compaction triggers, file-type classification, font picker filter, table layout. LLM stays the right tool for code generation, refactor, explain, prose summarization, and tool selection — handled by native tool calling, where the model emits structured tool_use blocks with parameters via the provider's tool-calling channel. See `docs/plans/native_tool_calling.md`.
2. **Clean CLI / agent separation.** CLI owns: TUI rendering (Bubble Tea/Lipgloss/Bubbles), slash parsing, table/diff render primitives, font picker, theme palette, animations, local UI config. Agent owns: conversation state + persistence, SmartRouter, Coordinator/LoopAgent, MCP host runtime, context-window accounting, project context, engine layer, cloud providers, telemetry, built-in tool suite. Adding a feature either calls an existing agent RPC or adds a new one — no CLI logic in the agent, no agent logic in the CLI shell.

### Transport — hybrid

CLI checks `localhost:50052` for an existing `cercano` gRPC server. If absent, auto-launches one in the background (`cercano agent` subcommand) and connects as a client. Single user-facing binary; multiple clients (CLI + VS Code) share the same agent and conversation store.

### Non-negotiable behaviors (acceptance-gated)

- **Resize correctness.** Every `WindowSizeMsg` triggers full re-render. Alt-screen mode. Scrollback stores raw content; widths re-computed and bodies re-wrapped at paint time. No pre-baked widths.
- **Dedicated Table render primitive.** Typed `Table` (columns + priority hints, rows of cells) renders all tabular data. Width-fit: fits → gridded box-draw, lime header; too wide → drop lowest-priority column; still too wide → truncate wrap-OK column with ellipsis; still too wide → transpose to `key: value`. Markdown tables from the agent are intercepted at the render layer, never reach the terminal raw. Max 4 columns at full grid.
- **Live context-window meter.** Status bar leads with a visual meter; updates every turn; counts system prompt + history + last response deterministically via per-model tokenizer (agent-owned, served via RPC); color amber → red past 70% / 90%.

### Visual design

Default theme `cracker` (12 named colors; bg `#1A1A1A`, primary amber `#EA8212`, accent lime `#BDF000`, info cyan `#00C8E8`, etc.). 62-col splash banner with 2-row half-block wordmark + lime rail + status meta. One-pass left-to-right shimmer on boot (1.4 s, amber→white peak→amber, ~30 fps via `tea.Tick`). Main layout: header bar, scrollback, input row, status bar — rounded outer frame. Tool calls render in boxed sub-frame with cyan header + green/red diff gutter. Confirm prompts use single-keypress letter shortcuts `[y]es / [n]o / [d]iff / [e]dit`.

### Permission model

Every tool carries a tier — R (read, runs silently), W (write, confirm with preview), X (destructive, always confirms). Bypass permissions mode (`--bypass-permissions` / `--yolo`, `/bypass on|off|status [full|tiered]`, or pinned in `permissions.yaml`) lets power users skip per-call confirms; first engagement shows a red confirmation overlay; status bar leads with red `! BYPASS`; each skipped call shows `⚡ (no confirm — bypass)` in scrollback. Allowlist in `permissions.yaml` can promote specific commands to silent.

### Agent surface additions (CLI V1 cannot ship without these)

Streaming chat RPC (verify token streaming), conversation store + resume (SQLite, `ListConversations`/`Resume`/`Clear`), context-window meter RPC (`GetContextUsage`), MCP host runtime (`internal/mcp_host/`, external server lifecycle + tool registration under `mcp/<server>/<tool>`), built-in CLI/shell tool suite (`internal/tools/`, R/W/X tiers, structured output, algorithmic dispatch), slash-style RPCs (`Clear`, `SwitchProject`, `GetProjectContext`, `ListModels`, `GetUsage`).

**Tech stack:** Go 1.21+, gRPC, protobuf, SQLite (`modernc.org/sqlite`, pure Go), Bubble Tea + Lipgloss + Bubbles, `tiktoken-go`, `github.com/sergi/go-diff`, `modelcontextprotocol/go-sdk`, Ollama HTTP client (existing), `fc-list` for font enum, OSC 1337 / Ghostty config / Kitty kitten / WezTerm CLI for font apply.

## Status

Plan drafted; no implementation tasks checked off yet. Phases 1–7 build agent-side enrichments; phases 8–17 build the CLI client; phase 18 is integration + acceptance + docs.

### Agent surface (phases 1–7)

**Phase 1 — Proto extensions & code generation**
- [ ] Task 1.1: Add Conversation/Turn messages and `ListConversations`/`Resume`/`Clear` RPCs to `agent.proto`
- [ ] Task 1.2: Add Context-meter, MCP host, Tools, slash RPCs to `agent.proto`
- [ ] Task 1.3: Regenerate Go bindings and verify build

**Phase 2 — Conversation store + persistence (agent)**
- [ ] Task 2.1: `ConversationStore` interface + SQLite schema (`conversations`, `turns`, `tool_calls`)
- [ ] Task 2.2: Algorithmic title auto-derivation from first user turn
- [ ] Task 2.3: Resolve conversation DB path (`$CERCANO_CONVERSATIONS_DB` → `~/.config/cercano/`)
- [ ] Task 2.4: Wire `ConversationStore` into the Agent turn loop (persist every turn + tool calls)
- [ ] Task 2.5: Implement `ListConversations`, `Resume`, `Clear` RPC handlers (Resume rehydrates in-memory history)

**Phase 3 — Context-window meter (agent)**
- [ ] Task 3.1: Tokenizer abstraction + registry (`tiktoken-go`, per-model defaults, char/4 fallback)
- [ ] Task 3.2: Per-conversation running counter (`Add`/`Reset`/`Used`/`Percent`)
- [ ] Task 3.3: Wire counter into Agent turn loop
- [ ] Task 3.4: Implement `GetContextUsage` RPC handler

**Phase 4 — Built-in tool registry + R-tier tools (agent)**
- [ ] Task 4.1: Tool interface + registry
- [ ] Task 4.2: Truncation policy (200 rows / 32 KiB)
- [ ] Task 4.3: Filesystem R-tier — `read_file`, `list_dir`, `stat_file`
- [ ] Task 4.4: `grep` tool with `rg` fallback
- [ ] Task 4.5: `find` tool with `fd` fallback
- [ ] Task 4.6: Git read tools — `git_status`/`log`/`diff`/`blame`/`branches`/`show`
- [ ] Task 4.7: Project meta — `project_context`, `classify_file` (algorithmic)

**Phase 5 — Built-in W/X tools + permission enforcement (agent)**
- [ ] Task 5.1: Permission gate (R/W/X tiers, `ConfirmRequester`)
- [ ] Task 5.2: Filesystem W-tier — `write_file`, `edit_file`, `apply_patch` (atomic, exact-match)
- [ ] Task 5.3: Filesystem X-tier — `rm_file`, `mv_file`
- [ ] Task 5.4: Git W/X tools — `git_add`/`commit`/`branch_create`/`checkout` (W), `git_push`/`reset_hard` (X)
- [ ] Task 5.5: Build/test/run W-tier — `run_command`/`run_tests`/`build`/`lint`/`format` + auto-detect
- [ ] Task 5.6: Allowlist + permissions config loader (`permissions.yaml`, simple predicates)

**Phase 6 — MCP host runtime (agent)**
- [ ] Task 6.1: MCP client over stdio (official Go SDK)
- [ ] Task 6.2: Server lifecycle manager (start/stop/restart/health)
- [ ] Task 6.3: MCP tool adapter — register external tools as `mcp/<server>/<tool>` (default W tier)
- [ ] Task 6.4: Config loader — merge global + project `mcp.yaml`
- [ ] Task 6.5: Wire into agent + MCP host RPCs

**Phase 7 — Slash RPCs + streaming verification (agent)**
- [ ] Task 7.1: Implement `Clear`, `SwitchProject`, `GetProjectContext`, `ListModels`, `GetUsage` handlers
- [ ] Task 7.2: Verify streaming chat RPC end-to-end (close any buffering gaps)

### CLI client (phases 8–18)

**Phase 8 — CLI scaffold + theme primitives**
- [ ] Task 8.1: Module scaffold (`source/clients/cli/`, main entry, flags, Makefile)
- [ ] Task 8.2: Theme palette (`cracker`) + lipgloss styles
- [ ] Task 8.3: Chrome primitives — frames, dividers, box helpers
- [ ] Task 8.4: UI config loader (`ui.yaml`)

**Phase 9 — CLI banner + shimmer animation**
- [ ] Task 9.1: Static banner rendering
- [ ] Task 9.2: Per-column shimmer color function
- [ ] Task 9.3: Bubble Tea shimmer model

**Phase 10 — CLI main session model**
- [ ] Task 10.1: Agent connection — gRPC client with auto-launch (+ `cercano agent` subcommand)
- [ ] Task 10.2: Root model — resize-safe layout
- [ ] Task 10.3: Header sub-model (adaptive truncation)
- [ ] Task 10.4: Scrollback sub-model (raw-content storage, re-wrap on render)
- [ ] Task 10.5: Input sub-model (multi-line via shift+enter)
- [ ] Task 10.6: Status bar sub-model (context meter + bypass indicator field)

**Phase 11 — Streaming chat turn + context-meter polling**
- [ ] Task 11.1: Streaming-aware scrollback append
- [ ] Task 11.2: Streaming chat client (typed channel: Token/ToolCall/Done)
- [ ] Task 11.3: Wire stream into root model (poll `GetContextUsage` on Done)

**Phase 12 — Table + diff render primitives, tool-call rendering, confirm prompts**
- [ ] Task 12.1: Table primitive (drop/truncate/transpose)
- [ ] Task 12.2: Markdown table interceptor
- [ ] Task 12.3: Diff renderer (colored gutter, collapsed context)
- [ ] Task 12.4: Tool-call sub-frame component
- [ ] Task 12.5: Confirm prompts — letter shortcuts (y/n/d/e)
- [ ] Task 12.6: Wire tool calls + confirms end-to-end (bidi confirm stream)

**Phase 13 — Slash command parsing + basic dispatch**
- [ ] Task 13.1: Command registry + parser (prefix → exact → fuzzy "did you mean")
- [ ] Task 13.2: Wire slash detection into input
- [ ] Task 13.3: `/help`, `/quit`, `/clear`
- [ ] Task 13.4: `/models`, `/model [name]`, `/config`
- [ ] Task 13.5: `/init`, `/context`, `/tools`, `/usage`

**Phase 14 — Conversation persistence UI**
- [ ] Task 14.1: Conversation picker overlay (reusable)
- [ ] Task 14.2: `/resume`, `/history`
- [ ] Task 14.3: `/diff`, `/undo` (agent-side per-turn backups + `RevertLastTurn` RPC)

**Phase 15 — MCP UI**
- [ ] Task 15.1: `/tools` listing (built-ins + MCP)
- [ ] Task 15.2: `/mcp list|add|remove|restart`

**Phase 16 — Bypass permissions UI**
- [ ] Task 16.1: Bypass state machine (Off/Full/Tiered)
- [ ] Task 16.2: Confirmation overlay — Enter-on-button gate
- [ ] Task 16.3: `/bypass` slash command + flags
- [ ] Task 16.4: Wire bypass into agent — auto-approve by tier, audit markers
- [ ] Task 16.5: Status bar `! BYPASS` indicator

**Phase 17 — Font picker**
- [ ] Task 17.1: Font enumeration (`fc-list` / `system_profiler` fallback, glyph coverage check)
- [ ] Task 17.2: Emulator detection from env
- [ ] Task 17.3: Per-emulator apply paths (iTerm2 OSC, Ghostty config+SIGUSR1, Kitty kitten, unknown→snippet)
- [ ] Task 17.4: Picker overlay
- [ ] Task 17.5: `/font` slash command + persistence

**Phase 18 — Integration + acceptance validation + docs**
- [ ] Task 18.1: Acceptance walk-through (§12.1–§12.9; block on §12.1–§12.4)
- [ ] Task 18.2: Performance pass (<2 s to interactive; resize fluidity)
- [ ] Task 18.3: README + `.agents/skill` / `.claude/skill` update
- [ ] Task 18.4: Homebrew formula — install `cercano-cli` alongside `cercano`

### Self-review checklist (completed by plan author before handoff)
- [x] Algorithmic > LLM enforced across dispatch / slash / font / title / classify / autodetect
- [x] CLI/agent separation via file layout (no cross-imports)
- [x] Resize correctness, Table primitive, live context meter mapped to tasks
- [x] Every V1 slash command and config file covered in its owning phase
- [x] No scope creep (cracker theme only, no Windows font enum, no semantic_search)

## Open Questions / Notes

Deferred decisions from the spec, to resolve during planning:
- Exact gRPC RPC signatures (proto changes).
- Test-runner auto-detect heuristics — start with project-file presence (`go.mod`, `package.json`, `pyproject.toml`).
- Conversation title derivation: algorithmic first-N-words (default) vs LLM-summarize.
- Semantic search backing index format (may be decided by parallel `semantic_search_20260318` track).
- Future themes (V1 ships `cracker` only).
- CLI auto-launch path: dedicated `cercano agent` subcommand (likely) vs reusing `cercano --mcp` embedded gRPC server.

**V1 acceptance criteria (all 9 must pass):** fresh `cercano` to working REPL <2 s with splash+shimmer; multi-turn streaming chat with live meter; boxed diff confirm writes to disk; mid-stream resize re-flows cleanly; `/font` applies live or prints snippet; `/bypass on` runs an agentic loop without per-call prompts; quit/relaunch/`/resume` continues; `/mcp add` tools appear in `/tools` and dispatch without an LLM tool-pick call; wide markdown table renders readable, never scrambled.
