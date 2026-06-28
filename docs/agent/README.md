# Cercano Agent

The standalone Cercano agent is a daily-driver AI coding assistant with native tool calling, a terminal UI, and a headless mode for scripts and CI. It connects to local models via Ollama and to Anthropic Claude via Meridian (Claude Max OAuth) or a direct API key.

## What you get

- **Terminal CLI** with retro Team17 chrome (amber + lime + cyan on charcoal), animated splash, slash commands, live context-window meter, and folded tool-call entries
- **Headless one-shot mode** (`cercano run "prompt"`) for scripts, CI, and other agents
- **Native tool calling** through the provider's structured tool channel (Anthropic `tool_use` blocks, Ollama `tool_calls`) — no JSON-in-text parsing
- **Three permission modes** controlling when the user is prompted before destructive work
- **Conversation persistence** in SQLite with lossless tool-call history for `/resume`
- **Pluggable cloud + local providers** behind a shared interface

## Quick start

### Install the dev launcher

The launcher rebuilds-if-stale and kills stale agents on each invocation:

```bash
cp source/server/scripts/cercano-launcher.sh ~/bin/cercano
chmod +x ~/bin/cercano
```

Then `cercano` runs the CLI when stdin is a TTY, and `cercano agent` runs the gRPC agent server. The CLI auto-launches the agent if no server is on `:50052`.

### Interactive mode

```bash
cercano
```

Type a prompt. The agent decides whether to use tools (Read, Glob, Grep, LS, Bash, Edit, Write, plus git operations). For W- and X-tier tools, the CLI prompts you to confirm `[y]es / [n]o / [d]iff`.

### Headless / scripted mode

```bash
cercano run "list the markdown files in this directory"
cercano run --auto-allow "edit README.md and add a TODO line at the top"
cercano run --conv my-session "continue where we left off"
```

Stdout carries the assistant's text response. Stderr carries progress diagnostics (tool calls, exec status). Exit codes: `0` success, `2` usage error, `3` user denied a confirm gate.

## Permission modes

| Mode | R-tier (read_file, grep, list) | W-tier (write, edit, run_command) | X-tier (rm, git push, reset) |
|---|---|---|---|
| `strict` | silent | confirm | confirm |
| `permissive` *(default)* | silent | silent | confirm |
| `bypass` | silent | silent | silent |

Switch with `/strict`, `/permissive`, `/bypass`, or `/mode <name>`. Persists to `~/.config/cercano/permissions.yaml`.

## Slash commands

| Command | What it does |
|---|---|
| `/tools` | List the agent's registered tools |
| `/tool <name> <json>` | Invoke a tool directly (W/X requires `/bypass`) |
| `/strict` `/permissive` `/bypass` `/mode <m>` | Change permission mode |
| `/history` | Pick a past conversation to resume |
| `/resume <id>` | Resume by conversation id |
| `/rename <title>` | Rename the current conversation |
| `/clear` | Wipe in-memory state for this session |
| `/color <name|hex>` | Change the prompt border accent color |
| `/config` | Open the settings page (or `/config <key> <value>` to set one value directly; `/config show` to print current config) |
| `/s` `/settings` | Open the settings page (sectioned: local model, cloud, routing, permissions, UI/theme) |
| `/cloud` | Cloud provider settings shortcut |
| `/context` | Inspect the current context window usage |
| `/mcp` | List/add/remove/restart hosted MCP servers |
| `/help` | Show keymap and command list |
| `/quit` | Quit (or press Ctrl+C twice) |

## Cloud setup

See [cloud-profiles.md](cloud-profiles.md) for full profile management and the `/cloud` CLI commands.

The agent supports two cloud paths:

### Meridian (Claude Max via OAuth)

If you have [Meridian](https://github.com/rynfar/meridian) running on `127.0.0.1:3456`, configure:

```yaml
# ~/.config/cercano/config.yaml
cloud_provider: anthropic
cloud_model: claude-sonnet-4-6
cloud_base_url: http://127.0.0.1:3456
cloud_api_key: dummy
```

The Anthropic provider streams through Meridian, which uses your Claude Max subscription. Cercano injects `x-opencode-session` / `x-opencode-request` / `x-opencode-agent-mode` headers per request so Meridian routes through its OpenCode adapter and bumps the SDK turn cap from 3 to 4.

### Direct Anthropic API

Set a real `cloud_api_key` and leave `cloud_base_url` empty.

### Local-only (Ollama)

If no cloud is configured, the agent runs purely on local Ollama with whatever `local_model` you've set. Tool calling works for tool-capable local models (qwen3-coder, qwen2.5-coder, llama3.1+, deepseek-r1, mistral-nemo, granite3.x). Non-tool-capable models are rejected with a clear error rather than degrading silently.

## Architecture

```
┌─────────────────┐                ┌─────────────────┐
│   cercano CLI   │                │   cercano run   │
│   (Bubble Tea)  │                │   (headless)    │
└────────┬────────┘                └────────┬────────┘
         │ gRPC                             │ gRPC
         └─────────────┬────────────────────┘
                       │
                       ▼
              ┌────────────────┐
              │ cercano agent  │
              │ (gRPC server)  │
              └────────┬───────┘
                       │
       ┌───────────────┼────────────────┐
       │               │                │
       ▼               ▼                ▼
 ┌───────────┐  ┌────────────┐   ┌──────────────┐
 │ Tool Loop │  │ Conversation│   │  Provider    │
 │ (bounded  │  │  Store      │   │  (Anthropic  │
 │ autono-   │  │  (SQLite)   │   │   / Ollama)  │
 │ mous)     │  │             │   │              │
 └───────────┘  └─────────────┘   └──────────────┘
```

### Provider layer (`internal/llm/`)

Layered abstraction. The shared `Provider` interface exposes `Chat`, `StreamChat`, and `Capabilities`. Per-provider packages own the native wire protocol:

- `internal/llm/anthropic/` — uses `github.com/anthropics/anthropic-sdk-go` v1.51. Custom User-Agent RoundTripper for Meridian fingerprint compatibility.
- `internal/llm/ollama/` — uses `github.com/ollama/ollama/api`.

The internal `Block` type carries text / `tool_use` / `tool_result` and is the lingua franca. Each adapter translates SDK types ↔ `Block`.

### Tool loop (`internal/agent/toolloop.go`)

Bounded autonomous loop. Per iteration:

1. Build the tool catalog from `agenttools.Registry` (the 15 built-in tools).
2. Call `Provider.StreamChat` with the catalog. Streaming is required: Meridian's non-streaming path has a 3-turn SDK cap that bites broad agentic prompts.
3. Accumulate text + `tool_use` blocks from the stream.
4. Partition tool calls by tier:
   - R-tier runs concurrently via goroutines
   - W/X-tier serializes through the permission gate
5. For each W/X call requiring confirmation, emit a `PermissionRequired` streaming event; wait for the client's `AllowToolCall` or `DenyToolCall` reply via the `PendingDecisions` map.
6. Execute the allowed tools. Each result becomes a `tool_result` block, fed back as a user message.
7. Track guards: 3 consecutive iterations of all-errored calls aborts; `MaxToolLoopIterations = 10` caps total round-trips; user denial of any confirm is a hard turn-end.

### Built-in tools (`internal/agenttools/`)

Names match Claude's training (Read, Write, Edit, LS, Glob, Grep, Bash) to keep Claude's internal planning concise:

| Name | Tier | Action |
|---|---|---|
| `Read` | R | Read a file |
| `LS` | R | List directory contents |
| `Glob` | R | Glob-match files (no `**` in V1) |
| `Grep` | R | Search files for a regex |
| `stat_file` | R | File metadata |
| `git_status` | R | `git status` |
| `git_log` | R | `git log` |
| `Write` | W | Atomic file write |
| `Edit` | W | Find/replace in a file (refuses ambiguous/zero/no-op matches) |
| `Bash` | W | Run a shell command (16 KiB output cap, timeout-aware) |
| `git_add` | W | `git add` |
| `git_commit` | W | `git commit` |
| `rm_file` | X | Delete a file (refuses directories) |
| `git_push` | X | `git push` (uses `--force-with-lease` when force-requested) |
| `git_reset_hard` | X | `git reset --hard` |

### Persistence (`internal/conversation/`)

SQLite via `modernc.org/sqlite` (pure Go). Each turn is a row in `turns`. Tool-calling turns carry an ordered block-array JSON in `content_json` matching the Anthropic Messages wire shape. `/resume` deserializes and drops straight into the next provider call — no translation.

### Permission gating

The decision is **agent-side**. The CLI just renders the `PermissionRequired` streaming event and forwards user input via `AllowToolCall` / `DenyToolCall` RPCs. Every client (CLI, VS Code, Zed, future) sees consistent behavior under the same mode.

## Detailed design docs

- `docs/features/cli/native-tool-calling/design.md` — the brainstorm + spec for native tool calling
- `docs/features/cli/native-tool-calling/tasks.md` — the implementation plan
- `docs/features/cli/native-tool-calling/followups.md` — V1 follow-ups (resolved and deferred)
- `docs/features/cli/README.md` — the CLI track spec
- `docs/archive/dispatch.md` — superseded raw-dispatch design (kept for context)

## Known limitations

- **`/tool` invoking W/X-tier** requires `/bypass` mode. The unary `InvokeTool` RPC can't stream a confirm prompt back to the CLI. Model-driven tool calls in normal chat flow always go through the gate correctly.
- **Inline tool-call expand/collapse keybind** isn't implemented yet. Tool entries render folded; scroll your terminal to see args/results.
