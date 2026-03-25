---
name: cercano-init
description: Initialize Cercano for a project by scanning the repo and building a context file. Makes all Cercano tools project-aware with domain-specific knowledge. Run this once per project for dramatically better local AI responses.
compatibility: Requires Cercano server running with Ollama available.
---

# Cercano Init

Initialize Cercano for the current project. Scans the repo, feeds key files through a local model, and writes `.cercano/context.md` — a concise reference document that gets automatically prepended to all future Cercano tool calls in this project.

## MCP Tool

**Tool name:** `cercano_init`

## Parameters

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `project_dir` | string | Yes | The project root directory (where `.git/` lives). |
| `context` | string | No | Domain knowledge you already have about this project. **IMPORTANT: Only provide knowledge you already have. Do NOT read files or research the project to populate this field.** Cercano will scan the repo itself. If you don't have context yet, omit this parameter entirely. |

## What It Does

1. Scans `project_dir` for key files: README, CLAUDE.md, `.claude/memory/*`, `.proto`, `.h`, config files, manifests
2. Feeds discovered files through a local model to extract domain knowledge
3. Merges with any host-provided context
4. Writes `.cercano/context.md` with sections for: Overview, Architecture, Key Data Structures, APIs & Protocols, Conventions, File Layout
5. All subsequent Cercano tool calls that include `project_dir` automatically get this context prepended

## Examples

**Basic init (no prior context):**
```json
{
  "project_dir": "/Users/me/my-project"
}
```

**Init with host context:**
```json
{
  "project_dir": "/Users/me/my-project",
  "context": "This project uses a custom binary protocol over SPI between an STM32 and ESP32. The key struct is espcp_configuration_t with the message queue at offset 0x14."
}
```

## When To Use

- First time using Cercano tools in a project
- When you see the "Cercano hasn't been initialized" message
- After major project changes (re-run to rebuild context)
