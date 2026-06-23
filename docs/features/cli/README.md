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

1. **Algorithmic over LLM whenever possible.** Default to deterministic algorithms; only call a model when no algorithmic path exists. New algorithmic call sites: slash command parsing (prefix match), context-window accounting (tokenizer + sum), MCP tool dispatch (name lookup + arg validation), diff rendering, conversation truncation/compaction triggers, file-type classification, font picker filter, table layout. LLM stays the right tool for code generation, refactor, explain, prose summarization, and tool selection — handled by native tool calling, where the model emits structured tool_use blocks with parameters via the provider's tool-calling channel. See `docs/features/cli/native-tool-calling/design.md`.
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

**Substantially built.** The agent-surface enrichments (phases 1–7) and the core CLI client (phases 8–14, 16) are largely in place and the binary runs an interactive streaming REPL today. Remaining gaps: MCP host runtime (phase 6 + its UI in 15), diff renderer / `/diff` / `/undo` (12.3, 14.3), font picker (phase 17), and the formal acceptance pass + Homebrew CLI formula (phase 18). The build also diverged from the original plan in several load-bearing ways — see **Deviations from the original plan** below.

### Deviations from the original plan

These are intentional changes discovered/decided during implementation; the task lists below are annotated to match.

- **CLI location.** The client lives in `source/server/internal/cli/` (in-tree with the agent), **not** the planned standalone `source/clients/cli/` module. Single binary; subpackages: `ui/`, `render/`, `slash/`, `theme/`, `banner/`, `overlay/`, `agentclient/`.
- **LLM provider layer rewritten.** A new `internal/llm` Provider abstraction (`Provider`, `Capabilities`, `StreamEvent`, internal `Block`/`Message`/`Tool` types) with first-party **anthropic-sdk-go** and **native Ollama** adapters replaced the old langchaingo cloud path. Legacy providers moved to `internal/legacymodels`. Charm libraries ported to **v2** (`charm.land/*`).
- **Native tool calling supersedes embedding-based dispatch.** The SmartRouter no longer picks tools via embeddings; the model emits structured `tool_use` blocks through the provider's tool-calling channel (`internal/agent/toolloop.go`, `internal/agenttools`). The old `dispatch` plan is marked superseded.
- **Permission model is mode-based, not a bypass toggle.** Phase 16's "bypass UI" shipped as three first-class modes — **Strict / Permissive (default) / Bypass** — set once via `/strict` `/permissive` `/bypass` `/mode`, surfaced as a status-bar mode chip, enforced agent-side via `PermissionStore` + `permissions.yaml`. Per-tool R/W/X tiers still exist underneath (`agenttools` fs_read/fs_write/fs_destructive, git_read/git_write).
- **Built-in tools renamed to Claude Code conventions** (`glob` instead of `find`, etc.).
- **New work not in the original plan:** living-recap (debounced local one-line work summary, persisted + shown in resume banner / history picker), streaming **glamour** markdown rendering with per-width renderer cache and a streaming block splitter, mouse text-selection + clipboard copy (pbcopy), grabbable scrollbar with drag-scroll, native multi-line prompt via textarea (bracketed paste, shell-style history recall, esc-to-cancel), and a `--mdtest` markdown render-testing harness.

### Legend

`[x]` done · `[~]` partial / in progress · `[ ]` not started · `[—]` superseded or dropped

### Agent surface (phases 1–7)

### Agent surface (phases 1–7)

**Phase 1 — Proto extensions & code generation** ✅
- [x] Task 1.1: Add Conversation/Turn messages and conversation RPCs to `agent.proto` (shipped as `ListConversations`/`ResumeConversation`/`DeleteConversation`/`RenameConversation`/`GetConversation`)
- [x] Task 1.2: Add Context-meter, Tools, slash, permission RPCs (`GetContextUsage`, `ListTools`/`InvokeTool`, `ListModels`, `ListSkills`/`GetSkill`, `SetPermissionMode`/`GetPermissionMode`, `AllowToolCall`/`DenyToolCall`, `GetProviderCapabilities`) — **MCP-host RPCs not added (phase 6 deferred)**
- [x] Task 1.3: Regenerate Go bindings and verify build

**Phase 2 — Conversation store + persistence (agent)** ✅
- [x] Task 2.1: `ConversationStore` interface + SQLite schema (`internal/conversation/{store.go,schema.sql}`, `content_json` column for tool-call blocks)
- [x] Task 2.2: Title / recap derivation (living-recap generator supplies the conversation summary; `/rename` for manual title)
- [x] Task 2.3: Resolve conversation DB path (`$CERCANO_CONVERSATIONS_DB` → `~/.config/cercano/`)
- [x] Task 2.4: Wire `ConversationStore` into the Agent turn loop (persists every turn + tool calls, propagates `WorkDir`)
- [x] Task 2.5: Implement list/resume/delete/rename/get RPC handlers (Resume rehydrates in-memory history)

**Phase 3 — Context-window meter (agent)** ✅
- [x] Task 3.1: Tokenizer abstraction + registry (`internal/contextmeter/tokenizer.go`, `tiktoken-go`, char/4 fallback)
- [x] Task 3.2: Per-conversation running counter (`internal/contextmeter/counter.go`)
- [x] Task 3.3: Wire counter into Agent turn loop
- [x] Task 3.4: Implement `GetContextUsage` RPC handler

**Phase 4 — Built-in tool registry + R-tier tools (agent)** ✅ *(renamed to Claude Code conventions)*
- [x] Task 4.1: Tool interface + registry (`internal/agenttools/{registry.go,tool.go,catalog.go}`)
- [x] Task 4.2: Truncation policy
- [x] Task 4.3: Filesystem R-tier — `read_file`, `list_dir`, `stat_file` (`fs_read.go`)
- [x] Task 4.4: `grep` tool with `rg` fallback (`grep.go`)
- [x] Task 4.5: ~~`find`~~ → `glob` tool (Claude Code naming)
- [x] Task 4.6: Git read tools (`git_read.go` — `git_status`/`log`/etc.)
- [~] Task 4.7: Project meta — `project_context` exposed via `/context`; algorithmic `classify_file` not separately surfaced

**Phase 5 — Built-in W/X tools + permission enforcement (agent)** ✅
- [x] Task 5.1: Permission gate (R/W/X tiers, R concurrent / W,X serial; `PermissionRequester`, denial = hard turn-end)
- [x] Task 5.2: Filesystem W-tier — `write_file`, `edit_file` (`fs_write.go`); `apply_patch` not confirmed present
- [x] Task 5.3: Filesystem X-tier — `rm_file`, `mv_file` (`fs_destructive.go`)
- [x] Task 5.4: Git W/X tools — `git_add`/`commit` (W), `git_push` (X) (`git_write.go`)
- [x] Task 5.5: Build/test/run W-tier + auto-detect (`run.go`; validators in `internal/tools/` — go/node/python/rust/dotnet)
- [x] Task 5.6: Allowlist + permissions config loader (`permissions.yaml`, `PermissionStore`)

**Phase 6 — MCP host runtime (agent)** ❌ not started
- [ ] Task 6.1: MCP client over stdio (official Go SDK)
- [ ] Task 6.2: Server lifecycle manager (start/stop/restart/health)
- [ ] Task 6.3: MCP tool adapter — register external tools as `mcp/<server>/<tool>` (default W tier)
- [ ] Task 6.4: Config loader — merge global + project `mcp.yaml`
- [ ] Task 6.5: Wire into agent + MCP host RPCs

**Phase 7 — Slash RPCs + streaming verification (agent)** 🟡 partial
- [~] Task 7.1: `Clear` ✓ and `ListModels` ✓ shipped; `SwitchProject`/`GetProjectContext`/`GetUsage` not added as RPCs
- [x] Task 7.2: Streaming chat RPC verified end-to-end (`StreamProcessRequest`; tool loop uses `Provider.StreamChat`)

### CLI client (phases 8–18)

> **Location note:** the CLI client shipped in `source/server/internal/cli/` rather than the planned standalone `source/clients/cli/` module. **A move to `source/clients/cli/` is planned** (see Open Questions). Subpackages below live under `internal/cli/`.

**Phase 8 — CLI scaffold + theme primitives** ✅
- [x] Task 8.1: Module scaffold (entry in `cmd/cercano/`, `agent`/`run`/`setup` subcommands, dev launcher script)
- [x] Task 8.2: Theme palette (`cracker`) + lipgloss styles (`cli/theme/{palette.go,styles.go}`)
- [x] Task 8.3: Chrome primitives — frames, dividers, box helpers
- [~] Task 8.4: UI config loader (`ui.yaml`) — config editor present (`ui/config_editor.go`); standalone `ui.yaml` schema unverified

**Phase 9 — CLI banner + shimmer animation** ✅
- [x] Task 9.1: Static banner rendering (`cli/banner/banner.go`)
- [x] Task 9.2: Per-column shimmer color function
- [x] Task 9.3: Bubble Tea shimmer model (`cli/banner/anim.go`)

**Phase 10 — CLI main session model** ✅
- [x] Task 10.1: Agent connection — gRPC client with auto-launch (`cli/agentclient/client.go`, `cercano agent` subcommand)
- [x] Task 10.2: Root model — resize-safe layout (`cli/ui/model.go`)
- [x] Task 10.3: Header sub-model
- [x] Task 10.4: Scrollback sub-model (raw-content storage, re-wrap on render; mouse selection + scrollbar)
- [x] Task 10.5: Input sub-model (multi-line via textarea, bracketed paste, history recall)
- [x] Task 10.6: Status bar sub-model (context meter + permission-mode chip)

**Phase 11 — Streaming chat turn + context-meter polling** ✅
- [x] Task 11.1: Streaming-aware scrollback append (+ progressive/streaming markdown)
- [x] Task 11.2: Streaming chat client (Token/ToolCall/Done)
- [x] Task 11.3: Wire stream into root model (poll `GetContextUsage`)

**Phase 12 — Table + diff render primitives, tool-call rendering, confirm prompts** 🟡 partial
- [x] Task 12.1: Table primitive — wrap/transpose (revised: **never drops columns**, transposes when too narrow) (`cli/render/table.go`)
- [x] Task 12.2: Markdown table interceptor
- [ ] Task 12.3: Diff renderer (colored gutter, collapsed context) — **not built** (no `go-diff` dep yet)
- [x] Task 12.4: Tool-call sub-frame component (folded entries, expand/collapse, hang-indent wrap)
- [x] Task 12.5: Confirm prompts — letter shortcuts
- [x] Task 12.6: Wire tool calls + confirms end-to-end (`PermissionRequired` events ↔ `AllowToolCall`/`DenyToolCall`)

**Phase 13 — Slash command parsing + basic dispatch** ✅ *(mostly)*
- [x] Task 13.1: Command registry + parser, prefix → exact → fuzzy (`cli/slash/registry.go`)
- [x] Task 13.2: Wire slash detection into input
- [x] Task 13.3: `/help`, `/quit`, `/clear`
- [x] Task 13.4: `/models`, `/model [name]`, `/config`, `/cloud`, `/color`
- [~] Task 13.5: `/context`, `/tools` ✓; `/init` partial; `/usage` not surfaced as a command

**Phase 14 — Conversation persistence UI** 🟡 partial
- [x] Task 14.1: Conversation picker overlay (`cli/ui/history_picker.go`, `cli/overlay/rowlist.go`)
- [x] Task 14.2: `/resume`, `/history` (+ living-recap shown in picker & resume banner)
- [ ] Task 14.3: `/diff`, `/undo` (per-turn backups + `RevertLastTurn` RPC) — **not built**

**Phase 15 — MCP UI** ❌ not started *(blocked on phase 6)*
- [~] Task 15.1: `/tools` listing — built-ins listed; MCP tools pending phase 6
- [ ] Task 15.2: `/mcp list|add|remove|restart`

**Phase 16 — Permission-mode UI** ✅ *(redesigned — see Deviations: was "Bypass UI")*
- [—] Task 16.1: ~~Bypass state machine (Off/Full/Tiered)~~ → three modes **Strict / Permissive / Bypass**
- [~] Task 16.2: Confirmation overlay — confirm prompt present; dedicated first-engagement red-overlay gate not built
- [x] Task 16.3: `/bypass` (+ `/strict`, `/permissive`, `/mode`) slash commands
- [x] Task 16.4: Wire mode into agent — `SetPermissionMode` RPC, auto-approve by mode/tier
- [x] Task 16.5: Status bar permission-mode chip

**Phase 17 — Font picker** ❌ not started
- [ ] Task 17.1: Font enumeration (`fc-list` / `system_profiler` fallback, glyph coverage check)
- [ ] Task 17.2: Emulator detection from env
- [ ] Task 17.3: Per-emulator apply paths (iTerm2 OSC, Ghostty config+SIGUSR1, Kitty kitten, unknown→snippet)
- [ ] Task 17.4: Picker overlay
- [ ] Task 17.5: `/font` slash command + persistence

**Phase 18 — Integration + acceptance validation + docs** 🟡 partial
- [ ] Task 18.1: Acceptance walk-through (V1 criteria below)
- [ ] Task 18.2: Performance pass (<2 s to interactive; resize fluidity)
- [~] Task 18.3: README + skill docs — standalone-agent README + `--mdtest` harness added; CLI skill docs pending
- [ ] Task 18.4: Homebrew formula — install `cercano-cli` alongside `cercano`

**Plan-track features still outstanding (rollup):** MCP host runtime + `/mcp` UI (6, 15); diff renderer + `/diff` / `/undo` (12.3, 14.3); font picker (17); `SwitchProject`/`GetProjectContext`/`GetUsage` RPCs (7.1); formal acceptance pass + Homebrew CLI formula (18); **relocate CLI to `source/clients/cli/`**.

### Self-review checklist (completed by plan author before handoff)
- [x] Algorithmic > LLM enforced across dispatch / slash / font / title / classify / autodetect
- [x] CLI/agent separation via file layout (no cross-imports)
- [x] Resize correctness, Table primitive, live context meter mapped to tasks
- [x] Every V1 slash command and config file covered in its owning phase
- [x] No scope creep (cracker theme only, no Windows font enum, no semantic_search)

## Open Questions / Notes

Resolved during implementation:
- Exact gRPC RPC signatures — settled in `source/proto/agent.proto` (see phase 1).
- Test-runner auto-detect — implemented via language validators in `internal/tools/` (go/node/python/rust/dotnet + generic).
- Conversation title derivation — living-recap generator supplies the summary; `/rename` for manual override.
- CLI auto-launch path — dedicated `cercano agent` subcommand (chosen).
- Provider layer — native `internal/llm` adapters (anthropic-sdk-go + Ollama) replaced langchaingo; Charm ported to v2.

Still open:
- **Relocate the CLI from `source/server/internal/cli/` to `source/clients/cli/`** (matches the original clean-separation intent; not yet done).
- Semantic search backing index format (may be decided by parallel `semantic_search` track).
- Future themes (V1 ships `cracker` only).
- Whether to add `SwitchProject`/`GetProjectContext`/`GetUsage` as first-class RPCs or fold into existing handlers.

**V1 acceptance criteria (all 9 must pass):** fresh `cercano` to working REPL <2 s with splash+shimmer; multi-turn streaming chat with live meter; boxed diff confirm writes to disk; mid-stream resize re-flows cleanly; `/font` applies live or prints snippet; `/bypass on` runs an agentic loop without per-call prompts; quit/relaunch/`/resume` continues; `/mcp add` tools appear in `/tools` and dispatch without an LLM tool-pick call; wide markdown table renders readable, never scrambled.
