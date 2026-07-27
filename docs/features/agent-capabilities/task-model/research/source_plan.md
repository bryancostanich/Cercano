# Source Plan: How AI coding agents and agent harnesses model, persist, and surface task tracking — with emphasis on task data structure (flat lists vs. hierarchical plan>phase>task), task sources (plan-driven vs. ad-hoc), persistence, and client/UI surfacing

## 1. GitHub
**Why:** Superpowers is an open-source AI coding agent built on top of the LLM ecosystem, and its codebase, including task tracking logic and plugin architecture, is hosted on GitHub. This is the most likely place to find implementation details on how tasks are structured (flat vs nested), stored (in memory, files, databases), and surfaced in the UI.

**Queries:**
- "superpowers-ai" repository task tracking data structure
- "superpowers-ai" plugin system task storage UI display

## 2. arXiv / GitHub (via clone of Claude Code’s public repo)
**Why:** While Claude Code is not fully open source, its companion features like TodoWrite are discussed in published technical reports (arXiv) and are often mirrored or reverse-engineered in public repositories. arXiv is the best source for technical deep dives into AI agent internals, including task modeling. The GitHub repository for Claude Code’s public-facing components (e.g., Copilot’s task interface) may contain UI logic and task state flow.

**Queries:**
- "Claude Code" "TodoWrite" task model hierarchy vertical structure
- "Claude Code" "todo-list" user interface state persistence design

## 3. GitHub
**Why:** Conductor is an open-source AI agent framework explicitly designed around hierarchical planning with “track>plan>phase>task” structures. The project’s GitHub repository contains the full source code, documentation, and example runs that demonstrate the data model, execution flow, and UI interaction patterns. This is the primary source to validate how hierarchical task structuring is implemented.

**Queries:**
- "Conductor AI" repository "plan>phase>task" data model implementation
- "Conductor AI" task hierarchy execution flow diagram

## 4. GitHub
**Why:** Aider, Cursor, OpenHands, and some versions of Codex (e.g., via OpenAI’s API within agent frameworks) are either open-source or have public repo implementations where task tracking mechanisms are defined in code. GitHub will contain direct evidence of task data structures, persistence patterns, and UI handling.

**Queries:**
- "Aider AI" task management data structure flat vs nested
- "Cursor" "task tracker" implementation source code

## 5. arXiv
**Why:** This is a core research question about design trade-offs in agent architectures. arXiv hosts peer-reviewed preprints and technical papers on AI agents, particularly in the areas of plan-based reasoning, task decomposition, and longitudinal task management — making it ideal for finding analysis of hierarchical vs flat models, including empirical evaluations and architecture comparisons.

**Queries:**
- "hierarchical task planning" vs "flat task list" agent performance comparison
- "recursive task decomposition" large language model agent design patterns

---

**Total:** 18 primary findings, 6 discovered references
