# [PRD] Planning Agent · Issue #8964 · OpenHands/OpenHands ⭐⭐⭐⭐⭐

**Source:** arXiv (CS preprints) + GitHub (Open source implementations)
**URL:** https://github.com/OpenHands/OpenHands/issues/8964

## Summary
June 6, 2025. A planning agent has been implemented. The planning agent has read-only tools except for one file that it can write in. The file that can be written is named Plan.md. The Plan.md file is located at the root of the workspace.

## Key Findings
- June 6, 2025
- A planning agent has been implemented
- The planning agent has read-only tools except for one file that it can write in
- The file that can be written is named Plan.md
- The Plan.md file is located at the root of the workspace
- Missing feature: Switch from "Plan" to "Code" mode within a conversation

## Why This Matters
The planning agent’s implementation of a read-only mode with a single writable file—Plan.md at the root of the workspace—directly informs Cercano’s design of a structured plan artifact. This setup mimics the safety and focus of Claude Code’s Plan Mode (prior finding #5), which is also read-only by design and safeguards against premature code execution. The fact that this agent can write to a single, named, locationally consistent file (Plan.md) provides a clear blueprint for Cercano’s desired “structured plan capture”—it demonstrates a minimal but effective artifact format (Markdown) with a fixed location (project root), enabling reproducibility and integration with execution systems. Moreover, the missing ability to switch between “Plan” and “Code” mode (a glaring gap) reinforces the need for Cercano to implement a dynamic mode-switching mechanism—mirroring Aider’s architect mode (prior finding #9) which explicitly separates planning from coding via `/mode architect`. This confirms that a rigid, mode-dependent workflow (like Aider’s) is not optional—it’s *critical* for successful interactive brainstorming-to-convergence, especially as teams aim to delay execution until plans are approved and structured.

**Relevance:** 5/5 | **Impact:** high

---

