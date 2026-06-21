# Track Specification: Cercano CLI

## 1. Job Title

Stand-alone Cercano CLI — a Go-based terminal agent harness with retro cracker/hacker chrome, consuming the shared Cercano agent over gRPC.

## 2. Overview

Cercano today is consumed via MCP (Claude Code, Cursor) and IDE extensions (VS Code, Zed). It has no first-party terminal experience. This track delivers one: a daily-driver agent harness comparable in scope to Claude Code or Codex CLI, but with a deliberate retro 80s cracker/hacker aesthetic (amber + lime on charcoal, shadowed block-letter banner, themed chrome throughout).

The CLI is a **client**. Agent logic stays in the shared core, where VS Code, Zed, and any future client consume it equally. To make the CLI useful, the agent surface itself must be enriched substantially — this track owns both halves.

**What changes:**

- New CLI client under `source/clients/cli/` (Go, Bubble Tea).
- Substantial agent surface additions under `source/server/internal/` (conversation persistence, MCP host runtime, context-window accounting, built-in CLI/shell tool suite, slash-style RPCs).
- New gRPC service methods to expose the additions.
- New configuration files: `~/.config/cercano/config.yaml` (UI), `~/.config/cercano/permissions.yaml`, `~/.config/cercano/mcp.yaml`, plus per-project `.cercano/mcp.yaml`.

**What does NOT change:**

- Existing MCP server mode (`cercano --mcp`). Other agents continue to consume Cercano as today.
- VS Code / Zed extensions. They benefit from the agent surface additions but require no direct changes within this track.
- Existing SmartRouter, Coordinator/LoopAgent, engine layer, Ollama integration. They're enriched, not replaced.

## 3. Design Principles

These are load-bearing. Any design decision that conflicts with them needs an explicit override note in this spec.

### 3.1 Algorithmic over LLM whenever possible

Default to deterministic algorithms. Only call a model when no algorithmic path exists.

Already algorithmic (preserve): intent classification (embeddings + cosine), engine routing, project context file scan.

New algorithmic call sites this track adds:
- Slash command parsing (prefix match → dispatch)
- Context-window accounting (per-model tokenizer + running sum)
- MCP tool dispatch (name lookup + arg validation)
- Tool selection for ambiguous intent (embedding similarity over tool descriptions; LLM fallback only when no clear winner)
- Diff rendering (standard diff algorithm + box drawing)
- Conversation truncation/compaction triggers (token-count + recency rules; LLM-summarize only as last resort)
- File-type classification (extension + name patterns)
- Font picker filter (substring + fuzzy)
- Table layout (width math)

LLM is the correct tool for: code generation, refactor, explain, summarization of arbitrary prose. The principle is "don't burn model tokens on decisions math can answer."

### 3.2 Clean CLI / agent separation

The agent is a shared library + gRPC surface consumed by all clients equally.

- **CLI owns:** TUI rendering (Bubble Tea/Lipgloss/Bubbles), slash command parsing, table render primitive, diff render primitive, font picker, theme palette, animations, local UI config.
- **Agent owns:** conversation state + persistence, SmartRouter, Coordinator/LoopAgent, MCP host runtime, context-window accounting, project context, engine layer, cloud providers, telemetry, built-in tool suite.
- **Boundary rule:** Adding a feature to the CLI either calls an existing agent RPC or adds a new one. No CLI-specific logic leaks into the agent. No agent logic embeds in the CLI shell.

## 4. Architecture

```
┌──────────────────────────────────────────────────────────────────────────┐
│  CLIENTS  (each one is a thin shell)                                     │
│    cercano CLI (Go/TUI)    VS Code ext (TS)    Zed ext (Rust)    future │
└────────────────┬─────────────────┬──────────────────┬───────────────────┘
                 │ gRPC            │ gRPC             │ gRPC
┌────────────────┴─────────────────┴──────────────────┴───────────────────┐
│  AGENT SURFACE  (single binary — cercano)                                │
│                                                                          │
│  gRPC service:                                                           │
│    Chat · Stream · Tools · Models · Conversation · Context · Init · MCP │
│                                                                          │
│  Subsystems:                                                             │
│    Agent (turn loop)    Coordinator/LoopAgent    SmartRouter (embeds)    │
│    MCP host (external)  Conversation Store      Context Meter           │
│    Project Context      Telemetry (SQLite)      Engine Layer            │
│                                                  │                       │
│                                            Ollama · ONNX · vLLM …       │
└──────────────────────────────────────────────────────────────────────────┘
```

### 4.1 Transport — hybrid

CLI checks `localhost:50052` for an existing `cercano` gRPC server. If absent, it auto-launches one in the background and connects as a gRPC client. Single user-facing binary; multiple clients (CLI + VS Code) can share the same agent and conversation store while the CLI is running.

### 4.2 Subsystem ownership

| Subsystem | Side | Status |
|---|---|---|
| TUI rendering, banner, shimmer, theme | CLI | NEW |
| Slash command parsing | CLI | NEW |
| Table render primitive | CLI | NEW |
| Diff render primitive | CLI | NEW |
| Font picker (OS + emulator) | CLI | NEW |
| Local UI config | CLI | NEW |
| Conversation store + resume | Agent | NEW |
| Context-window meter (tokenizer + accounting) | Agent | NEW |
| MCP host runtime | Agent | NEW |
| Built-in CLI/shell tool suite | Agent | NEW |
| Slash-style RPCs (Clear, ListMcpTools, SwitchProject, ...) | Agent | NEW |
| SmartRouter | Agent | EXISTS — enriched |
| Coordinator / LoopAgent | Agent | EXISTS — enriched (new tool table) |
| Project context (`.cercano/context.md`) | Agent | EXISTS |
| Engine layer + Ollama | Agent | EXISTS |
| Telemetry / stats | Agent | EXISTS |

## 5. Non-negotiable Behavior

These are spec-level requirements with acceptance criteria. They are not optional and cannot be hand-waved during implementation.

### 5.1 Resize correctness

Every `WindowSizeMsg` triggers a full re-render. Alt-screen mode. Scrollback stores raw content; widths are re-computed and message bodies re-wrapped at paint time. No pre-baked widths anywhere — message frames, tool boxes, diffs, tables all re-flow on resize.

**Acceptance:** Drag the terminal window narrower mid-stream during an agent response. Scrollback re-flows without garbage. Cursor remains on the input row. No stale ANSI escapes survive.

### 5.2 Dedicated Table render primitive

A typed `Table` (columns with priority hints, rows of cells) renders all tabular data. Width-fit behavior:

1. If table fits inner width: render gridded box-draw, header row in lime.
2. If too wide: drop the lowest-priority column first.
3. Still too wide: truncate the wrap-OK column with ellipsis.
4. Still too wide: transpose to `key: value` pairs, one cell per line. Always readable.

Markdown tables emitted by the agent are intercepted at the render layer and routed through this primitive — they never reach the terminal raw. Max 4 columns at full grid (per project rule).

**Acceptance:** Receive an agent reply with a 6-column markdown table on an 80-column terminal. Render does not scramble — drops columns by priority and indicates dropped columns in a footnote line.

### 5.3 Live context-window meter

Status bar leads with a visual meter (`██████░░░░░░░░░░░░░░ 21.4k/128k 17%`). Updates every turn. Counts system prompt + conversation history + last response, deterministically via the per-model tokenizer (lives in the agent, served via RPC). Color shifts amber → red as % climbs past 70% / 90%.

**Acceptance:** Submit turns and watch the bar fill. Numbers reconcile with raw tokenizer count to ±1%.

## 6. Visual Design

### 6.1 Palette (default theme: `cracker`)

| Role | Hex | Use |
|---|---|---|
| bg-deep | `#1A1A1A` | terminal background fallback |
| surface | `#252525` | overlay panels (e.g., font picker) |
| border | `#434343` / `#6F6F6F` | frame chrome, gridlines |
| primary | `#EA8212` | wordmark, prompts, default text |
| bright amber | `#FFB84D` | active state, focus, highlight |
| dim amber | `#5A3308` | meter empty cells, ghost text |
| accent (lime) | `#BDF000` | user prompt sigil, success peak, accent rail, current selection |
| info (cyan) | `#00C8E8` | metadata, version, file paths |
| muted | `#888888` | secondary text |
| success | `#6FCF6F` | confirmations, build pass, R-tier confirmations |
| warn | `#FFD24D` | meter mid-range, advisory tags |
| error | `#E84D4D` | failures, bypass indicator, X-tier confirms |

Additional themes ship later; `cracker` is the only V1 theme.

### 6.2 Splash banner

Left-aligned 62-column frame (`╔═...═╗`). Wordmark uses 2-row half-block letterforms:

```
█▀▀ █▀▀ █▀█ █▀▀ █▀█ █▄ █ █▀█
█▄▄ ██▄ █▀▄ █▄▄ █▀█ █ ▀█ █▄█
```

Frame width 62 cols (1 left wall + 60 inner + 1 right wall). Wordmark at column 4 (2-col left padding). Lime rail (`━` × 56) spans inner width below wordmark. Status meta line below rail: `▶ local-first ai coprocessor     · v0.1.0 · qwen3-coder`.

### 6.3 Splash shimmer

Single left-to-right sweep across the wordmark on boot:

- Duration: 1.4 s
- Color falloff: amber → bright amber → white peak → amber (smooth)
- Tail width: ~5 cols
- Angle: top row leads bottom row by 1 column (mild `/` lean)
- One pass only; locks to base amber after

Implementation: Bubble Tea `tea.Tick` at ~30 fps. Per frame compute sweep position in column space; per cell pick a Lipgloss style by distance from sweep head.

### 6.4 Main layout

Single full-width screen. Rounded outer frame (`╭ ╮ ╰ ╯`). Top to bottom:

1. **Header bar (1 line):** compressed wordmark + version, cwd, model@endpoint, connection dot
2. **Divider**
3. **Scrollback (fills):** user turns lime with `▶`, agent prose default amber, tool calls in boxed sub-frame with cyan header
4. **Divider**
5. **Input row (1 line):** lime `▶` + cursor; shift-enter for multi-line
6. **Divider**
7. **Status bar (1 line):** context meter (lead) · per-turn IO · latency · mode

### 6.5 Tool call sub-frame

```
┌─ tool:write_file · internal/server/health.go ────────────
│   + package server
│   + ...
└──────────────────────────────────────────────────────────
```

Header cyan with tool name + target. Body in dim default amber. Diff gutter uses green `+` / red `-`. Below the sub-frame: build/test result line, then confirm prompt.

### 6.6 Confirm prompts

Letter shortcuts: `[y]es / [n]o / [d]iff / [e]dit`. Single keypress (no Enter required). `d` expands the boxed diff if collapsed. `e` opens `$EDITOR` on the proposed change before applying.

## 7. CLI Surface

### 7.1 Slash commands (V1)

| Command | Action |
|---|---|
| `/help` | List commands with one-line descriptions |
| `/clear` | Clear conversation state (server-side); banner re-renders |
| `/resume [id]` | Resume a prior conversation; no arg → picker |
| `/history` | Open conversation picker (filter by date, project, model) |
| `/model [name]` | Switch active local model; no arg → picker |
| `/models` | List models on the active Ollama (renders via Table primitive) |
| `/config` | Open server-side runtime config view (model, ollama url, providers) |
| `/init` | Run project context init (writes `.cercano/context.md`) |
| `/context` | Show current project context content |
| `/tools` | List built-in + MCP tools available to the agent (Table) |
| `/mcp` | List configured MCP servers + status; subcommands `add`, `remove`, `restart` |
| `/usage` | Show token usage + cloud savings (calls existing `cercano stats`) |
| `/font` | Open font picker overlay |
| `/theme [name]` | Switch theme; no arg → list |
| `/bypass [on\|off\|status] [full\|tiered]` | Bypass permissions mode (see §9) |
| `/diff` | Show pending changes from the most recent tool calls |
| `/undo` | Revert the last applied file change |
| `/quit`, `/exit`, Ctrl-D | Leave REPL |

Parsing is algorithmic — prefix match against the static command table, dispatch to handler. Unknown commands print a "did you mean…" suggestion from fuzzy match.

### 7.2 Font picker (`/font`)

Floating panel over the live session. Banner and status bar remain visible beneath the overlay frame.

- **Filter input** at top (substring + fuzzy, algorithmic).
- **List** of detected monospace fonts, scrollable, current row highlighted on amber background. Each row: family name, weight count, features (ligatures / nerd icons / system / narrow).
- **Detail strip** at bottom: selected font name, detected emulator, glyph compatibility (`✓ half-blocks  ✓ box-drawing  ✓ unicode > 0xFFFF`).
- **Key hints** in bottom border: `↑↓ navigate · / filter · enter apply · esc cancel`.

**Font enumeration:**

- macOS / Linux: `fc-list :mono :family`
- macOS Core Text fallback (no `fc-list`): shell out to `system_profiler SPFontsDataType` or cgo Core Text
- Windows: DirectWrite

Filter to families that have a regular weight and contain U+2580–U+259F (block) + U+2500–U+257F (box-draw).

**Emulator detection:** read `$TERM_PROGRAM`, `$TERM_PROGRAM_VERSION`, `$TERM`, `$KITTY_PID`, `$GHOSTTY_RESOURCES_DIR`, `$WEZTERM_EXECUTABLE`.

**Apply at runtime:**

- iTerm2 → OSC 1337 `SetProfile` (cercano maintains one profile per font, created on first use)
- Ghostty / Alacritty / WezTerm → write to config file, SIGUSR1 to reload
- Kitty → `kitten @ set-font` if remote control enabled
- Apple Terminal / unknown → save selection in config, print the config snippet, ask user to relaunch

**Persistence:** `~/.config/cercano/config.yaml` `ui.font`. Re-applied on launch.

**Preview:** none needed. Terminal renders entire screen in one font; on Enter the UI redraws in the new font.

### 7.3 Conversation persistence

SQLite at `~/.config/cercano/conversations.db`. Schema:

- `conversations`: id, started_at, last_turn_at, project_dir, model, title
- `turns`: id, conversation_id, role, content, tokens_in, tokens_out, latency_ms, created_at
- `tool_calls`: id, turn_id, tool_name, args_json, result_summary, applied (bool)

Title auto-derived from first user prompt (algorithmic — first N words, or LLM-generated lazily on first `/history` open).

`/resume <id>` rehydrates the agent's conversation state from SQLite. `/history` lists with date, model, project, title, turn count — rendered via Table primitive.

### 7.4 Diff rendering

Standard unified-diff algorithm (use `github.com/sergi/go-diff`). Render inside tool sub-frame:

- Box-drawn left gutter (`│`)
- Gutter mark: `+` green / `-` red / ` ` (unchanged context lines collapsed to `… N unchanged …`)
- File path in cyan header
- Truncate hunks longer than ~30 lines with an `... expand with [d]` footnote

## 8. Agent Surface — Required Additions

These ship as a parallel sub-track under this plan. CLI V1 cannot ship without them.

### 8.1 Streaming chat RPC

Confirm existing token-streaming infrastructure (track `token_streaming_20260223`) supports per-token streaming over gRPC. If gaps remain, close them.

### 8.2 Conversation store + resume

SQLite-backed conversation state on the agent side (NOT client side — see §7.3 for the schema). New RPCs:

- `ListConversations(project_dir, limit) → [{id, title, ...}]`
- `Resume(conversation_id) → ConversationHandle`
- `Clear(conversation_id)`

### 8.3 Context-window meter RPC

`GetContextUsage(conversation_id) → {tokens_used, model_max, percent}`. Agent owns the tokenizer (model-specific); clients display only.

### 8.4 MCP host runtime

New subsystem `internal/mcp_host/`. Owns:

- Lifecycle of external MCP servers (start, stop, health-check via JSON-RPC)
- Tool registration: server tools enter the agent's tool table with namespace `mcp/<server>/<tool>`
- Tool routing: dispatcher considers MCP tools equally with built-ins (algorithmic embedding-similarity selection)
- Config sources: `~/.config/cercano/mcp.yaml` (global), `.cercano/mcp.yaml` (project)

RPCs:

- `ListMcpServers() → [{name, status, tool_count}]`
- `AddMcpServer(name, transport, command, args, env)`
- `RestartMcpServer(name)`
- `ListMcpTools() → [{name, server, description}]`

### 8.5 Built-in CLI/shell tool suite

New subsystem `internal/tools/`. Each tool implements a common interface — `Name`, `Description`, `Schema`, `Permission` (R/W/X), `Execute(ctx, args) → StructuredResult`.

**Filesystem & search:** `grep` (prefer `rg`), `find` (prefer `fd`), `read_file`, `list_dir`, `stat_file`, `write_file`, `edit_file` (string replace, exact match), `apply_patch` (unified diff), `rm_file`, `mv_file`.

**Git:** `git_status`, `git_log`, `git_diff`, `git_blame`, `git_branches`, `git_show`, `git_add`, `git_commit`, `git_branch_create`, `git_checkout`, `git_push`, `git_reset_hard`.

**Build / test / run:** `run_command`, `run_tests` (auto-detect: go / pytest / npm / cargo), `build` (auto-detect), `lint`, `format`.

**Project meta (algorithmic, not shell):** `project_context` (reads `.cercano/context.md`), `semantic_search` (future track; embeddings over file chunks), `classify_file` (extension + name patterns).

**Output discipline:** structured JSON to the agent, not raw stdout. Built-in truncation policy: 200 rows OR 32 KiB, whichever first, with a `… truncated, refine query` footnote. Agent never sees megabytes.

**Algorithmic tool dispatch:** each tool's description is embedded at startup. User prompt → embedding → cosine vs tool descriptions → if top score > threshold AND clear winner over second-place, dispatch directly. Otherwise fall back to LLM tool-choice. Same dispatcher serves Coordinator and chat path.

### 8.6 Slash-style RPCs

`Clear`, `SwitchProject(path)`, `GetProjectContext`, `ListModels`, `GetUsage`, etc. — RPCs that back specific slash commands. Slash command parsing is CLI-side; the work is agent-side.

## 9. Permission Model

Every tool carries a permission tier — R (read), W (write), X (destructive).

- **R** — runs silently. Output renders inline in the boxed sub-frame.
- **W** — confirm prompt with preview: `[y]es / [n]o / [d]iff / [e]dit`. Single-keypress.
- **X** — always confirms with preview. Never inferred from chat phrasing; user must say it directly or confirm through the prompt.

### 9.1 Bypass permissions mode

Power-user opt-in to run W and X without per-call confirms.

**Engagement:**

- CLI flag: `cercano --bypass-permissions` or `cercano --yolo` (no overlay; you opted in at shell)
- Slash: `/bypass on` (opens confirmation overlay), `/bypass off`, `/bypass status`
- Config: `~/.config/cercano/permissions.yaml` can pin `bypass: full` or `bypass: tiered` across launches (loud startup reminder when pinned)

**Confirmation overlay** (first-time engagement per session): red-bordered floating panel listing exactly what will be bypassed (write_file, edit_file, run_command, git_add, git_commit, plus the X tier: rm_file, mv_file, git_push, git_reset --hard). Scope radio defaults to **Full**; optional **Tiered** (W only — X still confirms). User presses Enter on the highlighted YES button to confirm. Esc cancels.

**Default scope:** Full (R+W+X). Tiered via flag arg or `/bypass on tiered`.

**Auto-expire:** none. Stays on until `/bypass off` or quit.

**Visual cues:**

- Status bar leads with a solid red `! BYPASS` block the entire time it's on. Fixed-width so it always lands in the same place.
- Each tool call that skipped a prompt shows an inline `⚡ (no confirm — bypass)` in dim red in the scrollback. Auditable history.
- No outer-frame flash; persistent indicator only.

### 9.2 Allowlist

`~/.config/cercano/permissions.yaml` supports promoting specific commands:

```yaml
allowlist:
  - tool: run_command
    when: "args.cmd starts with 'go test'"
    promote: silent  # treat as R-tier
  - tool: git_commit
    promote: silent
```

## 10. Configuration

| File | Owner | Contents |
|---|---|---|
| `~/.config/cercano/config.yaml` | server | Ollama URL, local model, cloud provider, port |
| `~/.config/cercano/ui.yaml` | CLI | font, theme, keybindings overrides |
| `~/.config/cercano/permissions.yaml` | server | tier overrides, allowlist, pinned bypass |
| `~/.config/cercano/mcp.yaml` | server | global MCP server list |
| `~/.config/cercano/conversations.db` | server | SQLite |
| `.cercano/context.md` | server | project context (per project) |
| `.cercano/mcp.yaml` | server | project-specific MCP servers (merged with global) |

## 11. Open Decisions (Deferred to Planning)

- Exact gRPC RPC signatures (proto changes) — drafted during plan phase.
- Test-runner auto-detect heuristics — start with explicit project-file presence (`go.mod`, `package.json`, `pyproject.toml`).
- Conversation title auto-derivation: algorithmic first-N-words vs LLM-summarize. Default to algorithmic; revisit if low quality.
- Semantic search backing index format (parallel track `semantic_search_20260318` may decide this).
- Future themes (V1 ships `cracker` only).
- CLI auto-launch path: spawn the same binary with a `cercano agent` subcommand, vs reuse `cercano --mcp` mode (which already starts an embedded gRPC server). Likely the former for clarity; confirm during plan phase.

## 12. Acceptance Criteria (V1)

A user can:

1. Type `cercano` in a fresh terminal, see the themed splash with shimmer, land in a working REPL within 2 seconds (auto-launching the agent server if needed).
2. Have a multi-turn chat conversation with token streaming. Context meter updates each turn.
3. Ask for code changes; see a boxed diff preview; confirm with `y`. Files write to disk.
4. Drag the terminal narrower mid-stream. Scrollback re-flows without garbage.
5. Type `/font`, pick a different monospace font, see the UI redraw in the new font (on supported emulators) or get a clear config-snippet message (on unsupported ones).
6. Type `/bypass on`, confirm the overlay, run an agentic coding loop where the agent edits multiple files and runs builds without per-call prompts. Status bar shows `! BYPASS` the entire time.
7. Quit, relaunch, type `/resume`, pick yesterday's conversation, continue where they left off.
8. Add an external MCP server via `/mcp add`, see its tools appear in `/tools`, watch the agent dispatch to one of them in response to a relevant prompt — without any LLM call burning tokens just to pick the tool.
9. Receive an agent response containing a wide markdown table; the Table primitive renders it readable (drops columns, transposes if needed) — never scrambled.

Failure to meet any of (1)–(9) blocks V1.
