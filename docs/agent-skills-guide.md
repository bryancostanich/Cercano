# Creating Agent Skills for Cercano

This guide covers how to write [Agent Skills](https://agentskills.io) (SKILL.md files) that wrap Cercano's MCP tools. Skills make your tools discoverable by any compatible agent — Claude Code, Cursor, Copilot, Gemini CLI, and 30+ others.

## What is a Skill?

A skill is a `SKILL.md` file that tells an agent:
- **What** the tool does (name, description)
- **When** to use it (triggers, use cases)
- **How** to call it (parameters, examples)

Skills are prompt instructions, not tool registrations. When an agent activates a skill, it reads the SKILL.md content into its context and follows the instructions using its existing tools (MCP calls, Bash, file I/O, etc.).

## Directory Layout

```
your-project/
  .agents/
    skills/
      your-skill-name/
        SKILL.md          # Required — the skill definition
        scripts/           # Optional — helper scripts the agent can run
        references/        # Optional — reference docs the agent can read
        assets/            # Optional — templates, configs, etc.
  .claude/
    skills/
      your-skill-name/
        SKILL.md          # Copy for Claude Code discovery (slash commands)
```

- `.agents/skills/` is the standard location — any Agent Skills-compatible agent scans here.
- `.claude/skills/` is Claude Code-specific. Skills here appear as `/your-skill-name` slash commands.
- The directory name must match the `name` field in the SKILL.md frontmatter.

## SKILL.md Format

A SKILL.md file has two parts: YAML frontmatter and a markdown body.

### Frontmatter

```yaml
---
name: your-skill-name          # Required. Lowercase, hyphens only. Must match directory name.
description: >                  # Required. What this skill does and when to use it.
  One or two sentences that help the agent decide whether this skill
  is relevant to the current task. Max 1024 chars.
compatibility: >                # Optional. Environment requirements.
  Requires Cercano server running and connected to an Ollama instance.
license: MIT                    # Optional. License name or path to LICENSE file.
metadata:                       # Optional. Arbitrary key-value pairs.
  author: your-name
  version: "1.0"
---
```

**Key rules:**
- `name` must be lowercase with hyphens (no spaces, underscores, or uppercase). Max 64 chars.
- `description` is what agents use to decide relevance — make it specific and action-oriented.
- The `name` must exactly match the parent directory name.

### Body

The markdown body contains the instructions the agent will follow. Structure it however makes sense, but typically include:

1. **A title and short summary** — what this skill does.
2. **The MCP tool name** — so the agent knows which tool to call.
3. **Parameters** — a table or list of parameters with types and descriptions.
4. **Examples** — concrete JSON examples the agent can follow.

## Example: Wrapping a Cercano MCP Tool

Here's a complete SKILL.md that wraps the `cercano_classify` tool:

```markdown
---
name: cercano-classify
description: >
  Classify or triage text using local AI via Cercano. Returns a category,
  confidence level, and brief reasoning. Use this for quick local triage of
  errors, logs, code quality issues, bug reports, or any content that needs
  categorization without sending it to the cloud.
compatibility: Requires Cercano server running and connected to an Ollama instance.
---

# Cercano Classify

Classify or triage text using local AI through Cercano's MCP interface.
Returns a category, confidence score, and reasoning.

## MCP Tool

**Tool name:** `cercano_classify`

## Parameters

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `text` | string | Yes | The text to classify. |
| `categories` | string | No | Comma-separated list of target categories. |

## Examples

**Classify an error:**
\```json
{
  "text": "panic: runtime error: nil pointer dereference",
  "categories": "bug, config issue, infra problem"
}
\```

**Triage a log entry:**
\```json
{
  "text": "WARN: connection pool exhausted, requests queuing",
  "categories": "performance, reliability, configuration"
}
\```
```

## Writing Good Descriptions

The `description` field is how agents decide whether to activate your skill. Tips:

- **Be specific** — "Classify text using local AI" is better than "AI classification tool".
- **Include trigger words** — mention the actions and domains your skill handles (e.g., "triage", "errors", "logs", "categorize").
- **State the benefit** — why use this skill? (e.g., "without sending it to the cloud", "faster than cloud round-trip").
- **Keep it under 1024 characters** — agents only load frontmatter during discovery, so this needs to be concise.

## Installing Skills

Copy your skill directory into the project:

```bash
# Standard location (all compatible agents)
cp -r your-skill-name/ .agents/skills/your-skill-name/

# Claude Code (enables /your-skill-name slash command)
cp -r your-skill-name/ .claude/skills/your-skill-name/
```

## Programmatic Access

The `cercano_skills` MCP tool provides access to Cercano's built-in skill definitions:

```
cercano_skills(action: "list")                        → all skills with descriptions
cercano_skills(action: "get", name: "cercano-local")  → full SKILL.md content
```

This is useful for agents that want to discover Cercano's capabilities at runtime without scanning the filesystem.

## Reference

- [Agent Skills Specification](https://agentskills.io/specification)
- [Cercano's published skills](../.agents/skills/)
