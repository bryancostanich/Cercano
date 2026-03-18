# Track Specification: Competitive Audit — Agent Features Landscape

## 1. Job Title
Comprehensive audit of agent features across the open-source and commercial landscape, producing a feature matrix reference document.

## 2. Overview
Before designing Cercano's tool surface and Agent Skills integration, we need a clear picture of what other coding agents offer — both open source and closed source. This track produces a structured reference document (feature matrix) that maps capabilities across agents, identifies gaps, and informs Cercano's roadmap.

The audit covers two dimensions:
1. **What tools/capabilities do agents expose?** (code gen, search, summarize, refactor, review, etc.)
2. **How do agents integrate with local inference and external tools?** (MCP, skills, plugins, extensions)

**What changes:** A new reference document is added to `docs/` (or similar). No code changes.

**What does NOT change:** Any Cercano code. This is a research-only track.

## 3. Agents to Audit

### Open Source
- Codex (OpenAI)
- Aider
- Continue
- Cody (Sourcegraph)
- OpenHands
- SWE-Agent

### Closed Source / Commercial
- Claude Code (Anthropic)
- Cursor
- Windsurf
- GitHub Copilot (including Copilot Chat, Copilot Workspace)
- JetBrains AI Assistant
- Amazon Q Developer

## 4. Audit Dimensions

For each agent, capture:

### 4.1 Tool / Capability Surface
- What built-in tools does the agent provide? (file read/write/edit, search, terminal, browser, etc.)
- Does it support code generation, review, refactoring, summarization, explanation?
- Does it have agentic loops (plan → execute → validate → fix)?
- Does it support multi-turn conversations with context?

### 4.2 Local Inference Support
- Can it run models locally? How? (Ollama, llama.cpp, ONNX, built-in)
- Can it offload specific tasks to local models while using cloud for others?
- Is there a concept of "co-processing" (local handles grunt work, cloud handles hard stuff)?

### 4.3 Extensibility / Plugin Model
- Does it support MCP? As client, server, or both?
- Does it support Agent Skills (agentskills.io)?
- Does it have its own plugin/extension system?
- Can users add custom tools?

### 4.4 Privacy & Offline
- Can it operate fully offline?
- What data leaves the machine?
- Is there a local-only mode?

### 4.5 Unique / Notable Features
- Anything distinctive that Cercano should consider adopting or learning from.

## 5. Deliverables
- A markdown document (e.g., `docs/competitive-audit.md`) containing:
  - Feature matrix table (agents as columns, capabilities as rows)
  - Detailed notes per agent
  - Summary of gaps and opportunities for Cercano
  - Recommendations for the Local Co-Processor Tools and Agent Skills tracks

## 6. Acceptance Criteria
- [ ] All agents listed in section 3 are covered.
- [ ] Feature matrix is complete and accurate (spot-checked against current public docs).
- [ ] Recommendations section explicitly connects findings to Cercano's tool design.
- [ ] Document is reviewed and accepted by the user.

## 7. Out of Scope
- Building anything. This is research only.
- Deep technical reverse-engineering of closed-source agents.
- Pricing comparisons or business model analysis.
