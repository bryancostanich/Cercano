# Competitive Audit — AI Coding Agent Features Landscape

## Overview & Scope

Before designing Cercano's tool surface and Agent Skills integration, we need a clear
picture of what other coding agents offer — open source and closed source alike. This
document is the consolidated reference: a feature matrix mapping capabilities across
agents, per-agent notes, identified gaps, and takeaways that inform Cercano's roadmap.

The audit covers two dimensions:

1. **Tool / capability surface** — what tools agents expose (code gen, search,
   summarize, refactor, review, etc.).
2. **Integration model** — how agents integrate with local inference and external
   tools (MCP, skills, plugins, extensions).

This is a research-only reference. No Cercano code changes are tied to it.

### Agents in scope

**Open source**

- Codex (OpenAI)
- Aider
- Continue
- Cody (Sourcegraph)
- OpenHands
- SWE-Agent
- OpenCode
- Gemini CLI (Google)

**Closed source / commercial**

- Claude Code (Anthropic)
- Cursor
- Windsurf
- GitHub Copilot (incl. Copilot Chat, Copilot Workspace)
- JetBrains AI Assistant
- Amazon Q Developer

### Audit dimensions

For each agent, capture:

**Tool / capability surface**
- Built-in tools (file read/write/edit, search, terminal, browser, etc.)
- Code generation, review, refactoring, summarization, explanation support
- Agentic loops (plan → execute → validate → fix)
- Multi-turn conversation with context

**Local inference support**
- Can it run models locally? How? (Ollama, llama.cpp, ONNX, built-in)
- Can it offload specific tasks to local models while using cloud for others?
- Is there a "co-processing" concept (local handles grunt work, cloud handles hard work)?

**Extensibility / plugin model**
- MCP support — client, server, or both?
- Agent Skills (agentskills.io) support?
- Own plugin/extension system?
- Custom user-defined tools?

**Privacy & offline**
- Can it operate fully offline?
- What data leaves the machine?
- Is there a local-only mode?

**Unique / notable features**
- Anything distinctive Cercano should consider adopting.

### Out of scope

- Building anything (research only).
- Deep reverse-engineering of closed-source agents.
- Pricing comparisons or business-model analysis.

## Findings

> **Status:** The audit track was scoped (this document captures that scope in full)
> but the per-agent investigation was not executed — the source track contained no
> completed findings, only the dimensions and agent roster reproduced above. The
> matrices below are the intended structure, ready to be populated against current
> public docs. Each cell should be spot-checked against the agent's current
> documentation at fill-in time.

### Feature matrix — tool / capability surface

| Agent | Built-in tools | Agentic loop |
|---|---|---|
| Codex (OpenAI) | _TBD_ | _TBD_ |
| Aider | _TBD_ | _TBD_ |
| Continue | _TBD_ | _TBD_ |
| Cody (Sourcegraph) | _TBD_ | _TBD_ |
| OpenHands | _TBD_ | _TBD_ |
| SWE-Agent | _TBD_ | _TBD_ |
| OpenCode | _TBD_ | _TBD_ |
| Gemini CLI | _TBD_ | _TBD_ |
| Claude Code | _TBD_ | _TBD_ |
| Cursor | _TBD_ | _TBD_ |
| Windsurf | _TBD_ | _TBD_ |
| GitHub Copilot | _TBD_ | _TBD_ |
| JetBrains AI | _TBD_ | _TBD_ |
| Amazon Q Developer | _TBD_ | _TBD_ |

### Feature matrix — local inference & co-processing

| Agent | Local models | Local/cloud offload |
|---|---|---|
| Codex (OpenAI) | _TBD_ | _TBD_ |
| Aider | _TBD_ | _TBD_ |
| Continue | _TBD_ | _TBD_ |
| Cody (Sourcegraph) | _TBD_ | _TBD_ |
| OpenHands | _TBD_ | _TBD_ |
| SWE-Agent | _TBD_ | _TBD_ |
| OpenCode | _TBD_ | _TBD_ |
| Gemini CLI | _TBD_ | _TBD_ |
| Claude Code | _TBD_ | _TBD_ |
| Cursor | _TBD_ | _TBD_ |
| Windsurf | _TBD_ | _TBD_ |
| GitHub Copilot | _TBD_ | _TBD_ |
| JetBrains AI | _TBD_ | _TBD_ |
| Amazon Q Developer | _TBD_ | _TBD_ |

### Feature matrix — extensibility (MCP / Skills / plugins)

| Agent | MCP | Agent Skills | Custom tools |
|---|---|---|---|
| Codex (OpenAI) | _TBD_ | _TBD_ | _TBD_ |
| Aider | _TBD_ | _TBD_ | _TBD_ |
| Continue | _TBD_ | _TBD_ | _TBD_ |
| Cody (Sourcegraph) | _TBD_ | _TBD_ | _TBD_ |
| OpenHands | _TBD_ | _TBD_ | _TBD_ |
| SWE-Agent | _TBD_ | _TBD_ | _TBD_ |
| OpenCode | _TBD_ | _TBD_ | _TBD_ |
| Gemini CLI | _TBD_ | _TBD_ | _TBD_ |
| Claude Code | _TBD_ | _TBD_ | _TBD_ |
| Cursor | _TBD_ | _TBD_ | _TBD_ |
| Windsurf | _TBD_ | _TBD_ | _TBD_ |
| GitHub Copilot | _TBD_ | _TBD_ | _TBD_ |
| JetBrains AI | _TBD_ | _TBD_ | _TBD_ |
| Amazon Q Developer | _TBD_ | _TBD_ | _TBD_ |

### Per-agent notes

Detailed observations (strengths, gaps, notable features) per agent go here, one
subsection per agent, covering the five audit dimensions above.

_Open source:_ Codex, Aider, Continue, Cody, OpenHands, SWE-Agent, OpenCode, Gemini CLI.

_Closed source:_ Claude Code, Cursor, Windsurf, GitHub Copilot, JetBrains AI, Amazon Q Developer.

## Takeaways for Cercano

The audit exists to answer two design questions, both feeding active tracks:

1. **Tool surface** — which built-in tools and agentic patterns are table stakes vs.
   differentiating, to shape Cercano's exposed MCP tools.
2. **Local co-processing** — how (and whether) other agents split work between local
   and cloud models, validating Cercano's "local handles grunt work, cloud handles the
   hard stuff" thesis.

Recommendations should explicitly connect findings to:

- **Local Co-Processor Tools track** — what local-offload patterns to adopt or avoid.
- **Agent Skills track** — how Skills / MCP extensibility is converging across the
  landscape and where Cercano should align with agentskills.io conventions.

Acceptance for the underlying research: all listed agents covered, matrices
spot-checked against current public docs, and recommendations tied directly to
Cercano's tool design.
