# Cercano Plugin Packaging Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Package Cercano as plugins for Claude Code, Gemini CLI, and Codex CLI with auto-routing skills, progress feedback, and marketplace-ready packaging.

**Architecture:** Canonical skills live in `cercano/plugins/skills/`. Three thin plugin repos (`cercano-claude`, `cercano-gemini`, `cercano-codex`) contain platform-specific manifests and config, with skills synced from the main repo via GitHub Action. MCP progress notifications are added server-side for long-running tools.

**Tech Stack:** Go (MCP server progress), YAML/JSON/TOML (plugin manifests), GitHub Actions (sync), Bash (scripts)

**Spec:** `docs/superpowers/specs/2026-04-08-plugin-packaging-design.md`

---

### Task 1: Create Canonical Skills Directory

Migrate existing skills from `.agents/skills/` to `plugins/skills/` with refined trigger descriptions for aggressive auto-routing.

**Files:**
- Create: `plugins/skills/cercano-research/SKILL.md`
- Create: `plugins/skills/cercano-deep-research/SKILL.md`
- Create: `plugins/skills/cercano-fetch/SKILL.md`
- Create: `plugins/skills/cercano-summarize/SKILL.md`
- Create: `plugins/skills/cercano-explain/SKILL.md`
- Create: `plugins/skills/cercano-extract/SKILL.md`
- Create: `plugins/skills/cercano-classify/SKILL.md`
- Create: `plugins/skills/cercano-local/SKILL.md`
- Create: `plugins/skills/cercano-document/SKILL.md`
- Create: `plugins/skills/cercano-config/SKILL.md`
- Create: `plugins/skills/cercano-models/SKILL.md`
- Create: `plugins/skills/cercano-stats/SKILL.md`
- Create: `plugins/skills/cercano-init/SKILL.md`
- Create: `plugins/skills/cercano-submit-usage/SKILL.md`

- [ ] **Step 1: Create plugins/skills/ directory and copy existing skills**

```bash
mkdir -p plugins/skills
cp -r .agents/skills/* plugins/skills/
```

- [ ] **Step 2: Refine trigger descriptions for auto-routing**

Update the YAML frontmatter `description` field in each SKILL.md to be more aggressive about when to trigger. The key change: explicitly tell the AI to use Cercano INSTEAD of cloud equivalents.

For `plugins/skills/cercano-research/SKILL.md`, update the frontmatter to:

```yaml
---
name: cercano-research
description: >
  Use when the user asks to research, look up, investigate, find information,
  or learn about any topic. Use this INSTEAD of WebSearch or WebFetch for
  general research questions. ALWAYS prefer this tool for web research.
  DO NOT TRIGGER when: user provides a specific URL to read (use cercano-fetch instead).
compatibility: Requires Cercano server running and Python venv (run 'cercano setup').
---
```

For `plugins/skills/cercano-deep-research/SKILL.md`:

```yaml
---
name: cercano-deep-research
description: >
  Use when the user needs thorough, multi-source research with ranked findings,
  citations, and synthesis. Use this for literature reviews, competitive analysis,
  technical deep-dives, or any research that needs more than a quick answer.
  Prefer this over cercano-research when depth and comprehensiveness matter.
compatibility: Requires Cercano server running, Ollama instance, and Python venv (run 'cercano setup').
---
```

For `plugins/skills/cercano-fetch/SKILL.md`:

```yaml
---
name: cercano-fetch
description: >
  Use when the user asks to fetch, read, or open a specific URL. Use this INSTEAD
  of WebFetch to read web pages locally without sending content to the cloud.
  DO NOT TRIGGER when: user asks a research question without a specific URL (use cercano-research instead).
compatibility: Requires Cercano server running.
---
```

For `plugins/skills/cercano-summarize/SKILL.md`:

```yaml
---
name: cercano-summarize
description: >
  Use when the user needs to summarize large text, files, logs, or diffs.
  ALWAYS prefer this over reading large files directly into cloud context.
  Processes content locally and returns a concise summary.
compatibility: Requires Cercano server running.
---
```

For `plugins/skills/cercano-explain/SKILL.md`:

```yaml
---
name: cercano-explain
description: >
  Use when the user asks to explain unfamiliar code, complex algorithms,
  or dense documentation. Processes the explanation locally before deciding
  what context to send to the cloud. Prefer this for initial code understanding.
compatibility: Requires Cercano server running.
---
```

For `plugins/skills/cercano-extract/SKILL.md`:

```yaml
---
name: cercano-extract
description: >
  Use when the user needs to pull specific information from large text —
  function signatures, error messages, config values, API endpoints.
  Extracts locally instead of reading entire files into cloud context.
compatibility: Requires Cercano server running.
---
```

For `plugins/skills/cercano-classify/SKILL.md`:

```yaml
---
name: cercano-classify
description: >
  Use when the user needs to categorize, triage, or classify text —
  error severity, code quality, bug reports, log entries. Quick local
  classification without cloud round-trip.
compatibility: Requires Cercano server running.
---
```

For `plugins/skills/cercano-local/SKILL.md`:

```yaml
---
name: cercano-local
description: >
  Use when the user wants to run a prompt against a local AI model via Ollama.
  Handles both chat-style queries and agentic code generation with validation.
  Use this to offload work to local inference — faster, private, zero cost.
compatibility: Requires Cercano server running.
---
```

For `plugins/skills/cercano-document/SKILL.md`:

```yaml
---
name: cercano-document
description: >
  Use when the user wants to generate or update doc comments for Go code.
  Handles the entire read-think-write cycle locally — the host never sees
  the file contents. Supports dry_run mode to preview.
compatibility: Requires Cercano server running.
---
```

For `plugins/skills/cercano-config/SKILL.md`:

```yaml
---
name: cercano-config
description: >
  Use when the user wants to check or change Cercano's runtime configuration —
  switch the local model, change the Ollama endpoint URL, or change the cloud
  provider and model. No server restart needed.
compatibility: Requires Cercano server running.
---
```

For `plugins/skills/cercano-models/SKILL.md`:

```yaml
---
name: cercano-models
description: >
  Use when the user wants to see what AI models are available on their
  Ollama instance. Returns model names, sizes, and modification dates.
compatibility: Requires Cercano server running.
---
```

For `plugins/skills/cercano-stats/SKILL.md`:

```yaml
---
name: cercano-stats
description: >
  Use when the user asks about Cercano usage, token savings, or local vs
  cloud inference stats. Shows total requests, tokens processed locally,
  and breakdowns by tool, model, and day.
compatibility: Requires Cercano server running.
---
```

For `plugins/skills/cercano-init/SKILL.md`:

```yaml
---
name: cercano-init
description: >
  Use when setting up Cercano for a new project. Scans the repo to build
  a project context file that makes all Cercano tools project-aware.
  Run this once per project for better local AI responses.
compatibility: Requires Cercano server running.
---
```

For `plugins/skills/cercano-submit-usage/SKILL.md`:

```yaml
---
name: cercano-submit-usage
description: >
  Use when the user wants to submit cloud token usage data to Cercano
  for tracking. This sends data, not a report — use cercano_stats to
  view usage. Opt-in telemetry for local-vs-cloud comparison.
compatibility: Requires Cercano server running.
---
```

Keep the body of each SKILL.md unchanged from the existing `.agents/skills/` version — only the frontmatter description changes.

- [ ] **Step 3: Verify all 14 skills are present**

```bash
ls plugins/skills/ | wc -l
```

Expected: 14

- [ ] **Step 4: Commit**

```bash
git add plugins/skills/
git commit -m "feat: create canonical plugin skills with auto-routing triggers"
```

---

### Task 2: Create Claude Code Plugin Repository

Create the `cercano-claude` repo with manifest, MCP config, hooks, and skills.

**Files:**
- Create: `cercano-claude/.claude-plugin/plugin.json`
- Create: `cercano-claude/.mcp.json`
- Create: `cercano-claude/hooks/hooks.json`
- Create: `cercano-claude/skills/` (copied from `plugins/skills/`)
- Create: `cercano-claude/README.md`

- [ ] **Step 1: Create the GitHub repo**

```bash
gh repo create bryancostanich/cercano-claude --public --description "Cercano plugin for Claude Code — local-first AI co-processor" --clone
```

- [ ] **Step 2: Create the plugin manifest**

Create `cercano-claude/.claude-plugin/plugin.json`:

```json
{
  "name": "cercano",
  "version": "0.9.0",
  "description": "Local-first AI co-processor — offload research, summarization, extraction, and more to local models via Ollama",
  "author": {
    "name": "Bryan Costanich",
    "url": "https://github.com/bryancostanich"
  },
  "repository": "https://github.com/bryancostanich/cercano-claude",
  "license": "MIT",
  "keywords": ["local-inference", "ollama", "research", "mcp"],
  "mcpServers": "./.mcp.json"
}
```

- [ ] **Step 3: Create the MCP config**

Create `cercano-claude/.mcp.json`:

```json
{
  "cercano": {
    "command": "cercano",
    "args": ["--mcp"]
  }
}
```

- [ ] **Step 4: Create hooks for progress feedback**

Create `cercano-claude/hooks/hooks.json`:

```json
{
  "hooks": {
    "PreToolUse": [
      {
        "matcher": "mcp__cercano__cercano_deep_research|mcp__cercano__cercano_research",
        "hooks": [
          {
            "type": "command",
            "command": "echo 'Cercano: researching locally...' >&2",
            "timeout": 5
          }
        ]
      },
      {
        "matcher": "mcp__cercano__cercano_summarize",
        "hooks": [
          {
            "type": "command",
            "command": "echo 'Cercano: summarizing locally...' >&2",
            "timeout": 5
          }
        ]
      }
    ],
    "PostToolUse": [
      {
        "matcher": "mcp__cercano__cercano_deep_research|mcp__cercano__cercano_research",
        "hooks": [
          {
            "type": "command",
            "command": "echo 'Cercano: research complete' >&2",
            "timeout": 5
          }
        ]
      },
      {
        "matcher": "mcp__cercano__cercano_summarize",
        "hooks": [
          {
            "type": "command",
            "command": "echo 'Cercano: summarization complete' >&2",
            "timeout": 5
          }
        ]
      }
    ]
  }
}
```

- [ ] **Step 5: Copy skills from canonical source**

```bash
cp -r ../Cercano/plugins/skills/* cercano-claude/skills/
```

- [ ] **Step 6: Create README.md**

Create `cercano-claude/README.md`:

```markdown
# Cercano — Claude Code Plugin

Local-first AI co-processor for Claude Code. Offload research, summarization, extraction, and more to local models via Ollama.

## Prerequisites

- [Cercano](https://github.com/bryancostanich/Cercano) installed and on your PATH (`brew install bryancostanich/cercano/cercano`)
- [Ollama](https://ollama.com/) running with at least one model pulled

## Install

```bash
claude plugin install cercano
```

Or install from this repo directly:

```bash
claude plugin install --plugin-dir /path/to/cercano-claude
```

## What It Does

Cercano runs inference locally via Ollama, keeping your data private and saving cloud tokens. This plugin auto-routes appropriate tasks to local inference:

| Tool | Replaces | Purpose |
|---|---|---|
| `cercano_research` | WebSearch | Web research via DuckDuckGo + local AI |
| `cercano_deep_research` | WebSearch | Multi-source deep research with ranked findings |
| `cercano_fetch` | WebFetch | Fetch and extract text from URLs |
| `cercano_summarize` | Reading large files | Summarize text/files locally |
| `cercano_explain` | Inline analysis | Explain code locally |
| `cercano_extract` | Reading full files | Extract specific info from text |
| `cercano_classify` | — | Categorize/triage text locally |
| `cercano_local` | — | General local inference |
| `cercano_document` | — | Generate Go doc comments locally |
| `cercano_config` | — | Change Cercano settings at runtime |
| `cercano_models` | — | List available Ollama models |
| `cercano_stats` | — | View usage and token savings |
| `cercano_init` | — | Initialize project context |
| `cercano_submit_usage` | — | Submit cloud usage data |

## License

MIT
```

- [ ] **Step 7: Commit and push**

```bash
cd cercano-claude
git add -A
git commit -m "feat: initial Claude Code plugin — skills, hooks, MCP config"
git push -u origin main
```

---

### Task 3: Create Gemini CLI Extension Repository

Create the `cercano-gemini` repo with manifest, context file, commands, hooks, and skills.

**Files:**
- Create: `cercano-gemini/gemini-extension.json`
- Create: `cercano-gemini/GEMINI.md`
- Create: `cercano-gemini/skills/` (copied from `plugins/skills/`)
- Create: `cercano-gemini/commands/research.toml`
- Create: `cercano-gemini/commands/fetch.toml`
- Create: `cercano-gemini/hooks/hooks.json`
- Create: `cercano-gemini/README.md`

- [ ] **Step 1: Create the GitHub repo**

```bash
gh repo create bryancostanich/cercano-gemini --public --description "Cercano extension for Gemini CLI — local-first AI co-processor" --clone
```

- [ ] **Step 2: Create the extension manifest**

Create `cercano-gemini/gemini-extension.json`:

```json
{
  "name": "cercano",
  "version": "0.9.0",
  "description": "Local-first AI co-processor — offload research, summarization, extraction, and more to local models via Ollama",
  "mcpServers": {
    "cercano": {
      "command": "cercano",
      "args": ["--mcp"]
    }
  },
  "contextFileName": "GEMINI.md",
  "settings": [
    {
      "name": "Ollama URL",
      "description": "URL of the Ollama instance (default: http://localhost:11434)",
      "envVar": "OLLAMA_URL"
    }
  ]
}
```

- [ ] **Step 3: Create the context file**

Create `cercano-gemini/GEMINI.md`:

```markdown
# Cercano — Local AI Co-Processor

This project has a local AI co-processor (Cercano) running via MCP. **Prefer Cercano tools over cloud-native equivalents** to save cloud context tokens and keep work local:

- **Web research**: Use `cercano_research` instead of web search when investigating a question. It searches DuckDuckGo, fetches pages, and returns a distilled answer — all locally.
- **URL fetching**: Use `cercano_fetch` instead of web fetch for reading web pages. Returns extracted text without stuffing raw HTML into the cloud context.
- **Summarization**: Use `cercano_summarize` for large files, logs, or diffs before processing them yourself.
- **Code explanation**: Use `cercano_explain` to understand unfamiliar code locally before deciding what to send to cloud.
- **Information extraction**: Use `cercano_extract` to pull specific info from large text instead of reading it all into context.
- **Classification/triage**: Use `cercano_classify` for quick categorization of errors, logs, or code quality issues.

**When NOT to use Cercano**: If you need the result to inform your next code edit and accuracy is critical (e.g., exact API signatures), use your own tools. Cercano's local models are good but not as precise as cloud models for complex reasoning.
```

- [ ] **Step 4: Create custom commands**

Create `cercano-gemini/commands/research.toml`:

```toml
description = "Research a topic using Cercano's local AI pipeline"
prompt = "Use cercano_research to investigate: {{args}}"
```

Create `cercano-gemini/commands/fetch.toml`:

```toml
description = "Fetch and extract text from a URL locally"
prompt = "Use cercano_fetch to get the content of: {{args}}"
```

- [ ] **Step 5: Create hooks for progress feedback**

Create `cercano-gemini/hooks/hooks.json`:

```json
{
  "hooks": {
    "BeforeTool": [
      {
        "matcher": "mcp_cercano_cercano_deep_research|mcp_cercano_cercano_research",
        "hooks": [
          {
            "type": "command",
            "command": "echo 'Cercano: researching locally...' >&2",
            "timeout": 5
          }
        ]
      },
      {
        "matcher": "mcp_cercano_cercano_summarize",
        "hooks": [
          {
            "type": "command",
            "command": "echo 'Cercano: summarizing locally...' >&2",
            "timeout": 5
          }
        ]
      }
    ],
    "AfterTool": [
      {
        "matcher": "mcp_cercano_cercano_deep_research|mcp_cercano_cercano_research",
        "hooks": [
          {
            "type": "command",
            "command": "echo 'Cercano: research complete' >&2",
            "timeout": 5
          }
        ]
      },
      {
        "matcher": "mcp_cercano_cercano_summarize",
        "hooks": [
          {
            "type": "command",
            "command": "echo 'Cercano: summarization complete' >&2",
            "timeout": 5
          }
        ]
      }
    ]
  }
}
```

Note: Gemini CLI uses `BeforeTool`/`AfterTool` instead of Claude's `PreToolUse`/`PostToolUse`, and tool names use single underscores (`mcp_cercano_cercano_research`) instead of double underscores.

- [ ] **Step 6: Copy skills from canonical source**

```bash
cp -r ../Cercano/plugins/skills/* cercano-gemini/skills/
```

- [ ] **Step 7: Create README.md**

Create `cercano-gemini/README.md`:

```markdown
# Cercano — Gemini CLI Extension

Local-first AI co-processor for Gemini CLI. Offload research, summarization, extraction, and more to local models via Ollama.

## Prerequisites

- [Cercano](https://github.com/bryancostanich/Cercano) installed and on your PATH (`brew install bryancostanich/cercano/cercano`)
- [Ollama](https://ollama.com/) running with at least one model pulled

## Install

```bash
gemini extensions install https://github.com/bryancostanich/cercano-gemini
```

## Custom Commands

- `/research <topic>` — Research a topic using Cercano's local AI pipeline
- `/fetch <url>` — Fetch and extract text from a URL locally

## Configuration

Set a custom Ollama URL:

```bash
gemini extensions config cercano "Ollama URL" --value "http://my-server:11434"
```

## What It Does

Cercano runs inference locally via Ollama, keeping your data private and saving cloud tokens. See the [main Cercano repo](https://github.com/bryancostanich/Cercano) for full documentation.

## License

MIT
```

- [ ] **Step 8: Add the gemini-cli-extension topic for marketplace auto-listing**

```bash
gh repo edit bryancostanich/cercano-gemini --add-topic gemini-cli-extension
```

- [ ] **Step 9: Commit and push**

```bash
cd cercano-gemini
git add -A
git commit -m "feat: initial Gemini CLI extension — skills, hooks, commands, MCP config"
git push -u origin main
```

---

### Task 4: Create Codex Plugin Repository

Create the `cercano-codex` repo with manifest, MCP config, and skills.

**Files:**
- Create: `cercano-codex/.codex-plugin/plugin.json`
- Create: `cercano-codex/.mcp.json`
- Create: `cercano-codex/skills/` (copied from `plugins/skills/`)
- Create: `cercano-codex/README.md`

- [ ] **Step 1: Create the GitHub repo**

```bash
gh repo create bryancostanich/cercano-codex --public --description "Cercano plugin for Codex CLI — local-first AI co-processor" --clone
```

- [ ] **Step 2: Create the plugin manifest**

Create `cercano-codex/.codex-plugin/plugin.json`:

```json
{
  "name": "cercano",
  "version": "0.9.0",
  "description": "Local-first AI co-processor — offload research, summarization, extraction, and more to local models via Ollama",
  "author": {
    "name": "Bryan Costanich",
    "url": "https://github.com/bryancostanich"
  },
  "repository": "https://github.com/bryancostanich/cercano-codex",
  "license": "MIT",
  "keywords": ["local-inference", "ollama", "research", "mcp"],
  "skills": "./skills/",
  "mcpServers": "./.mcp.json"
}
```

- [ ] **Step 3: Create the MCP config**

Create `cercano-codex/.mcp.json`:

```json
{
  "cercano": {
    "command": "cercano",
    "args": ["--mcp"]
  }
}
```

- [ ] **Step 4: Copy skills from canonical source**

```bash
cp -r ../Cercano/plugins/skills/* cercano-codex/skills/
```

- [ ] **Step 5: Create README.md**

Create `cercano-codex/README.md`:

```markdown
# Cercano — Codex Plugin

Local-first AI co-processor for OpenAI Codex CLI. Offload research, summarization, extraction, and more to local models via Ollama.

## Prerequisites

- [Cercano](https://github.com/bryancostanich/Cercano) installed and on your PATH (`brew install bryancostanich/cercano/cercano`)
- [Ollama](https://ollama.com/) running with at least one model pulled

## Install

Copy this plugin to your Codex plugins directory:

```bash
mkdir -p ~/.codex/plugins
cp -r /path/to/cercano-codex ~/.codex/plugins/cercano
```

Then add to your local marketplace (`~/.agents/plugins/marketplace.json`):

```json
{
  "name": "local-plugins",
  "plugins": [
    {
      "name": "cercano",
      "source": {
        "source": "local",
        "path": "~/.codex/plugins/cercano"
      },
      "policy": {
        "installation": "INSTALLED_BY_DEFAULT"
      },
      "category": "Productivity"
    }
  ]
}
```

## What It Does

Cercano runs inference locally via Ollama, keeping your data private and saving cloud tokens. See the [main Cercano repo](https://github.com/bryancostanich/Cercano) for full documentation.

## Known Limitations

- No hooks support yet — progress feedback relies on MCP progress notifications
- Official Codex plugin marketplace is not yet available — manual installation required

## License

MIT
```

- [ ] **Step 6: Commit and push**

```bash
cd cercano-codex
git add -A
git commit -m "feat: initial Codex plugin — skills, MCP config"
git push -u origin main
```

---

### Task 5: Add MCP Progress Notifications to Server

Add progress notification emission to the Cercano MCP server for long-running tools. The Go MCP SDK (`gomcp` v0.7.0) supports `req.Session.NotifyProgress()`.

**Files:**
- Modify: `source/server/internal/mcp/server.go` (add progress helper, modify research/deep_research handlers)
- Test: manual — run `cercano --mcp` and invoke `cercano_research`, verify progress events on stderr

- [ ] **Step 1: Add a progress notification helper to the MCP server**

Add this helper method to `source/server/internal/mcp/server.go` after the `maybeUpdateNudge` method (around line 126):

```go
// notifyProgress sends an MCP progress notification if the request has a progress token.
// Errors are silently ignored — progress is best-effort.
func notifyProgress(ctx context.Context, req *gomcp.CallToolRequest, message string, progress, total float64) {
	token := req.Params.GetProgressToken()
	if token == nil {
		return
	}
	req.Session.NotifyProgress(ctx, &gomcp.ProgressNotificationParams{
		ProgressToken: token,
		Message:       message,
		Progress:      progress,
		Total:         total,
	})
}
```

- [ ] **Step 2: Add progress notifications to handleResearch**

In `source/server/internal/mcp/server.go`, find the `handleResearch` method. Add progress notifications at key stages. The handler starts around line 971.

Before the gRPC call to ProcessRequest (around line 997), add:

```go
notifyProgress(ctx, request, "Crafting search queries...", 0, 3)
```

After the gRPC call succeeds, before returning the result (around line 1020), add:

```go
notifyProgress(ctx, request, "Research complete", 3, 3)
```

- [ ] **Step 3: Add progress notifications to handleDeepResearch**

In the `handleDeepResearch` method, add progress at each phase boundary. The handler needs to pass the request through to enable progress. Add notifications before each major gRPC call:

```go
notifyProgress(ctx, request, "Planning research sources...", 0, 4)
// ... after plan phase:
notifyProgress(ctx, request, "Searching sources...", 1, 4)
// ... after search phase:
notifyProgress(ctx, request, "Analyzing findings...", 2, 4)
// ... after analyze phase:
notifyProgress(ctx, request, "Synthesizing report...", 3, 4)
// ... after synthesis:
notifyProgress(ctx, request, "Deep research complete", 4, 4)
```

The exact insertion points depend on the handler's structure — look for phase transitions or sequential gRPC calls.

- [ ] **Step 4: Add progress notifications to handleSummarize**

In the `handleSummarize` method, add a single progress notification before the gRPC call:

```go
notifyProgress(ctx, request, "Summarizing locally...", 0, 1)
```

And after completion:

```go
notifyProgress(ctx, request, "Summarization complete", 1, 1)
```

- [ ] **Step 5: Build and verify**

```bash
cd source/server && go build -o bin/cercano ./cmd/cercano/
```

Expected: clean build, no errors.

- [ ] **Step 6: Run tests**

```bash
cd source/server && go test ./internal/mcp/... -count=1
```

Expected: all tests pass.

- [ ] **Step 7: Commit**

```bash
git add source/server/internal/mcp/server.go
git commit -m "feat: add MCP progress notifications for long-running tools"
```

---

### Task 6: Create GitHub Action for Skill Sync

Create the GitHub Action workflow that syncs canonical skills to all three plugin repos when they change.

**Files:**
- Create: `.github/workflows/sync-plugins.yml`

- [ ] **Step 1: Create the workflow file**

Create `.github/workflows/sync-plugins.yml`:

```yaml
name: Sync Plugin Skills

on:
  push:
    branches: [main]
    paths: ['plugins/skills/**']

jobs:
  sync:
    runs-on: ubuntu-latest
    strategy:
      matrix:
        repo: [cercano-claude, cercano-gemini, cercano-codex]
    steps:
      - name: Checkout main repo
        uses: actions/checkout@v4

      - name: Checkout plugin repo
        uses: actions/checkout@v4
        with:
          repository: bryancostanich/${{ matrix.repo }}
          path: target
          token: ${{ secrets.PLUGIN_SYNC_TOKEN }}

      - name: Sync skills
        run: |
          rm -rf target/skills/
          cp -r plugins/skills/ target/skills/

      - name: Create PR
        uses: peter-evans/create-pull-request@v6
        with:
          path: target
          token: ${{ secrets.PLUGIN_SYNC_TOKEN }}
          branch: sync/skills-update
          title: "sync: update skills from main repo"
          body: |
            Automated skill sync from [cercano/plugins/skills/](https://github.com/bryancostanich/Cercano/tree/main/plugins/skills/).

            Triggered by commit ${{ github.sha }}.
          commit-message: "sync: update skills from main repo"
          delete-branch: true
```

- [ ] **Step 2: Commit**

```bash
git add .github/workflows/sync-plugins.yml
git commit -m "ci: add GitHub Action to sync skills to plugin repos"
```

- [ ] **Step 3: Create the PLUGIN_SYNC_TOKEN secret**

This is a manual step. Create a GitHub PAT (fine-grained) with:
- Repository access: `cercano-claude`, `cercano-gemini`, `cercano-codex`
- Permissions: Contents (read/write), Pull requests (read/write)

Then add it as a secret in the Cercano repo:

```bash
gh secret set PLUGIN_SYNC_TOKEN --repo bryancostanich/Cercano
```

(Paste the token when prompted.)

---

### Task 7: Test All Three Plugins

Install and test each plugin in its respective tool.

- [ ] **Step 1: Test Claude Code plugin**

```bash
claude plugin install --plugin-dir /path/to/cercano-claude
```

Then in a Claude Code session:
- Ask "research how MCP progress notifications work" — should auto-route to `cercano_research`
- Ask "fetch https://ollama.com/blog" — should auto-route to `cercano_fetch`
- Verify progress messages appear on stderr for research tools
- Verify all 14 tools are available via `cercano_` prefix

- [ ] **Step 2: Test Gemini CLI extension**

```bash
gemini extensions link /path/to/cercano-gemini
```

Then in a Gemini CLI session:
- Try `/research transformer architecture` — should invoke custom command
- Try `/fetch https://ollama.com/blog` — should invoke custom command
- Ask a research question naturally — should auto-route via skill triggers
- Verify GEMINI.md context is loaded (ask "what tools does Cercano provide?")

- [ ] **Step 3: Test Codex plugin**

```bash
mkdir -p ~/.codex/plugins
cp -r /path/to/cercano-codex ~/.codex/plugins/cercano
```

Then in a Codex session:
- Ask "research how MCP progress notifications work" — should auto-route to `cercano_research`
- Verify all tools are available
- Note any progress notification behavior (if Codex supports it)

- [ ] **Step 4: Fix any issues found during testing**

Address platform-specific issues:
- Tool name format differences (double underscore vs single underscore)
- Hook event name differences
- Skill trigger refinements based on actual auto-routing behavior

- [ ] **Step 5: Commit any fixes**

```bash
git add -A
git commit -m "fix: address issues found during plugin testing"
```

---

### Task 8: Marketplace Submission

Submit plugins to available marketplaces.

- [ ] **Step 1: Submit Claude Code plugin**

Go to `clau.de/plugin-directory-submission` and fill out:
- Plugin name: cercano
- Repository: https://github.com/bryancostanich/cercano-claude
- Description: Local-first AI co-processor — offload research, summarization, extraction, and more to local models via Ollama
- Category: Productivity

- [ ] **Step 2: Verify Gemini CLI auto-listing**

Check that the `gemini-cli-extension` topic was added:

```bash
gh repo view bryancostanich/cercano-gemini --json repositoryTopics
```

Expected: includes `gemini-cli-extension`. Google will auto-crawl within 24 hours.

- [ ] **Step 3: Note Codex marketplace status**

Codex official plugin directory is "coming soon." No action needed — the plugin works via local marketplace for now. Monitor https://developers.openai.com/codex/plugins for directory availability.

- [ ] **Step 4: Add conductor track**

Create a conductor track entry in `conductor/plan.md`:

```markdown
- [ ] **Track: Plugin Packaging — Claude Code, Gemini CLI, Codex CLI plugin/extension packages**
*Link: [./tracks/plugin_packaging_20260408/](./tracks/plugin_packaging_20260408/)*
```

And create the track directory with a brief status file.
