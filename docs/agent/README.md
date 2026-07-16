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

The launcher rebuilds-if-stale and kills stale agents on each invocation.
Generate it (bakes this clone's path in, exports `CERCANO_REPO`):

```bash
cd source/server && make launcher
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
| `/d [repo-path]` (alias `/dev`) | Development mode — point the session at the Cercano repo and prime the agent to work on itself |
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
| `/theme` | Open settings to switch/edit the color theme |
| `/cloud` | Cloud provider settings shortcut |
| `/context` | Inspect the current context window usage |
| `/compact` | Compact the conversation's context incrementally (digest the backlog, keep existing summaries) |
| `/context-regen` | Rebuild the conversation's context from raw turns (clears state, re-runs compaction) |
| `/clear-compacted-context` | Drop the compacted summaries and rehydrate the context from raw turns — no re-summarization (recovery when a broken summarizer produced a bad/empty compacted layer) |
| `/elide-context` | Stub all tool outputs in the context so far — LLM-free token reclaim. In-memory and send-view only: raw turns untouched, resets on agent restart; tool results after the command stay intact |
| `/mcp` | List/add/remove/restart hosted MCP servers |
| `/help` | Show keymap and command list |
| `/quit` | Quit (or press Ctrl+C twice) |

## Cloud setup

See [cloud-profiles.md](cloud-profiles.md) for full profile management and the `/cloud` CLI commands.

The agent supports multiple cloud providers:

### Supported cloud providers

- **Anthropic** — via direct API key or Meridian (Claude Max OAuth)
- **OpenAI-compatible** — OpenAI, Gemini, Groq, and others (see [cloud-openai.md](cloud-openai.md#5-openai-compatible-endpoints) for endpoint examples and setup)
- **OpenAI (ChatGPT subscription)** — sign in with a ChatGPT Plus/Pro account instead of an API key (unofficial; see below)
- **Local** — Ollama (offline, no API key needed)

### Providers

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

### ChatGPT subscription (device-code OAuth)

Authenticate the OpenAI provider with a **ChatGPT Plus/Pro subscription**
instead of a pay-as-you-go API key. In `/config` → Cloud Providers, open the
**openai (responses)** row and choose **sign in with ChatGPT**; the setup
wizard offers the same under the OpenAI provider. A device code + URL appear
— approve them in your browser and the agent stores the OAuth tokens in your
keychain (refreshing them automatically) under a `chatgpt` profile
(`flavor: responses`, `route: chatgpt`).

**Unofficial / ToS-gray.** This borrows the Codex CLI's OAuth client id, has no
sanctioned third-party path, can break whenever OpenAI changes things, and
limits you to the ChatGPT-backend model allowlist (gpt-5.5, gpt-5.3-codex,
gpt-5.4, gpt-5.4-mini, …). An API key stays the fully-sanctioned path and is
one keystroke away in the same UI. Design + endpoint details:
[docs/features/chatgpt-subscription-auth/design.md](../features/chatgpt-subscription-auth/design.md).

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

The internal `Block` type carries text / `tool_use` / `tool_result` / `image` and is the lingua franca. Each adapter translates SDK types ↔ `Block`. Image translation is plumbed at the provider layer ([vision-input.md](vision-input.md)), but the inbound path (CLI image attach) is not yet implemented.

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
- **Image input** is not yet implemented. Vision support is plumbed at the provider layer and adapters support images, but there is no CLI/inbound path to attach images to messages.
