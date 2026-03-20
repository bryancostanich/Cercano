# Agent Skills Research & Design

## 1. Agent Skills Specification (agentskills.io)

### SKILL.md Format

Each skill is a directory containing a `SKILL.md` file plus optional supporting files.

```
skill-name/
├── SKILL.md          # Required: YAML frontmatter + markdown instructions
├── scripts/          # Optional: executable code
├── references/       # Optional: documentation
└── assets/           # Optional: templates, resources
```

### Frontmatter (Required)

| Field | Required | Constraints |
|-------|----------|-------------|
| `name` | Yes | Max 64 chars. Lowercase `a-z`, numbers, hyphens. **Must match parent directory name.** |
| `description` | Yes | Max 1024 chars. Primary signal for agent matching — keywords matter. |
| `license` | No | License name or reference to bundled file. |
| `compatibility` | No | Max 500 chars. Environment requirements. |
| `metadata` | No | Arbitrary key-value map (string→string). |
| `allowed-tools` | No | Space-delimited pre-approved tools. Experimental. |

### Body

Free-form Markdown after frontmatter. Recommended <500 lines / <5000 tokens. Larger reference material goes in supporting files.

### Discovery (Three-Tier Progressive Disclosure)

1. **Catalog** — Session start: agents load only `name` + `description` (~50-100 tokens/skill)
2. **Instructions** — Task matches: full SKILL.md body loaded (<5000 tokens)
3. **Resources** — Only when instructions reference supporting files

### Standard Scan Paths

| Scope | Path |
|-------|------|
| Project (cross-client) | `<project>/.agents/skills/<skill-name>/SKILL.md` |
| User (cross-client) | `~/.agents/skills/<skill-name>/SKILL.md` |
| Project (Claude Code) | `<project>/.claude/skills/<skill-name>/SKILL.md` |
| User (Claude Code) | `~/.claude/skills/<skill-name>/SKILL.md` |

### Claude Code Extensions (Not Portable)

| Field | Description |
|-------|-------------|
| `argument-hint` | Autocomplete hint, e.g. `[issue-number]` |
| `disable-model-invocation` | `true` = user-only via `/name` |
| `user-invocable` | `false` = hidden from `/` menu |
| `model` | Override model for this skill |
| `effort` | Override effort level |
| `context` | `fork` = run in isolated subagent |
| `agent` | Subagent type when `context: fork` |

String substitutions: `$ARGUMENTS`, `$ARGUMENTS[N]`, `${CLAUDE_SESSION_ID}`, `${CLAUDE_SKILL_DIR}`

### Supported Agents (30+)

Claude Code, Cursor, GitHub Copilot, VS Code, OpenAI Codex, Gemini CLI, Kiro (AWS), Roo Code, Goose, Amp, JetBrains Junie, and many more.

### Gotchas

1. `name` field **must match** parent directory name
2. Description quality is critical — it's how agents decide when to activate
3. Claude Code extensions are not portable to other agents
4. Keep SKILL.md under 500 lines; offload to supporting files
5. Unquoted colons in YAML descriptions can break parsing
6. Project-level skills from untrusted repos can inject instructions

---

## 2. Cercano MCP Tools Inventory

7 tools defined in `source/server/internal/mcp/server.go`:

### cercano_local
- **Purpose**: Run prompts against local AI (Ollama). Supports both chat and agentic code generation with validate loop.
- **Params**: `prompt` (required), `file_path`, `work_dir`, `context`, `conversation_id`

### cercano_models
- **Purpose**: List available Ollama models with sizes and dates.
- **Params**: None

### cercano_config
- **Purpose**: Query or update runtime config (model, endpoint, provider).
- **Params**: `action` (required: "get"/"set"), `local_model`, `cloud_provider`, `cloud_model`, `ollama_url`

### cercano_summarize
- **Purpose**: Summarize text/files locally. Distill large content without cloud.
- **Params**: `text` or `file_path` (one required), `max_length` (brief/medium/detailed)

### cercano_extract
- **Purpose**: Extract specific information from text locally.
- **Params**: `text` (required), `query` (required)

### cercano_classify
- **Purpose**: Classify/triage text locally. Returns category, confidence, reasoning.
- **Params**: `text` (required), `categories` (optional comma-separated list)

### cercano_explain
- **Purpose**: Explain code/text locally. Returns functionality, interfaces, data flow.
- **Params**: `text` or `file_path` (one required)

---

## 3. Design Decisions

### Directory Layout (Provider)

Target **both** directories for maximum compatibility:
- `.agents/skills/` — cross-client standard
- `.claude/skills/` — Claude Code-specific

Skills will be maintained in `.agents/skills/` as the source of truth, with `.claude/skills/` containing symlinks or copies.

### SKILL.md → Tool Mapping

| Skill Directory | MCP Tool | Primary Use Case |
|----------------|----------|------------------|
| `cercano-local` | `cercano_local` | Local AI inference + agentic code generation |
| `cercano-models` | `cercano_models` | Discover available local models |
| `cercano-config` | `cercano_config` | Change models/endpoints at runtime |
| `cercano-summarize` | `cercano_summarize` | Summarize large content locally |
| `cercano-extract` | `cercano_extract` | Extract targeted info from text |
| `cercano-classify` | `cercano_classify` | Categorize/triage content locally |
| `cercano-explain` | `cercano_explain` | Understand unfamiliar code locally |

### Skill Description Strategy

Each description will:
- Lead with what the tool does
- Include keywords matching common use cases (so agents can match tasks)
- Mention "local" / "without cloud" / "private" as differentiators
- Note the prerequisite: Cercano server must be running

### Consumer Architecture (Phase 3)

```
Cercano Server
    │
    ├── Skill Scanner
    │   ├── Scan .agents/skills/ in project root
    │   ├── Scan .claude/skills/ in project root
    │   ├── Parse SKILL.md frontmatter (name, description, compatibility)
    │   └── Three-tier loading (catalog → instructions → resources)
    │
    ├── Skill Registry
    │   ├── In-memory store of discovered skills
    │   ├── Dedup by name (project overrides user-level)
    │   └── Metadata: name, description, path, status
    │
    └── Skill Activation
        ├── Register as MCP tools dynamically
        └── Route invocations per SKILL.md instructions
```

### Dynamic Skill Discovery via gRPC + MCP

In addition to static SKILL.md files, Cercano will serve its skill catalog dynamically — following the same architecture pattern as all other Cercano tools (MCP wraps gRPC).

**gRPC layer** — Add `ListSkills` and `GetSkill` RPCs to `agent.proto`:
- `ListSkills()` → returns name + description for all available skills (catalog tier)
- `GetSkill(name)` → returns full SKILL.md content for a specific skill (instructions tier)

**MCP layer** — Add a `cercano_skills` tool wrapping the gRPC calls:
- `cercano_skills(action: "list")` → catalog of all skills
- `cercano_skills(action: "get", name: "cercano-summarize")` → full skill definition

**Why this matters for distribution:**
Agents already connected to Cercano via MCP can discover skills dynamically through the connection — no file installation step needed. The static SKILL.md files remain as a fallback for filesystem-based discovery by agents that aren't connected to Cercano.

**Two discovery paths:**
1. **Dynamic** — Agent connects to Cercano MCP → calls `cercano_skills` → gets skill catalog
2. **Static** — Agent scans `.agents/skills/` or `.claude/skills/` directories for SKILL.md files

### Distribution (Deferred to Phase 4)

Open questions tracked in plan.md:
- How do skills get from brew install to agent-discoverable directories?
- Auto-detect installed agents vs. manual `cercano skills install`?
- Symlinks vs. copies?
- Dynamic discovery via `cercano_skills` MCP tool may reduce the need for file-based distribution
