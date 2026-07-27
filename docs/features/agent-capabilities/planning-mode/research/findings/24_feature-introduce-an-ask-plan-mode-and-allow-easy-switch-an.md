# Feature: Introduce an Ask/Plan mode and allow easy switch an agent in context between two modes · Issue #557 · OpenHands/software-agent-sdk ⭐⭐⭐⭐⭐

**Source:** arXiv (CS preprints) + GitHub (Open source implementations)
**URL:** https://github.com/OpenHands/software-agent-sdk/issues/557

## Summary
August 16, 2025. Clicking "Execute" automatically summarizes the context of the "planning" conversation. "PLAN.md" is passed to the default "Execute" agent. Read-only agent (openhands/agenthub/readonly_agent) has an action space consisting only of "READ-ONLY actions".

## Key Findings
- August 16, 2025
- Clicking "Execute" automatically summarizes the context of the "planning" conversation
- "PLAN.md" is passed to the default "Execute" agent
- Read-only agent (openhands/agenthub/readonly_agent) has an action space consisting only of "READ-ONLY actions"

## Why This Matters
The fact that clicking "Execute" automatically summarizes the context of the "planning" conversation directly informs Cercano’s design of the transition from interactive planning to execution. This automatic summarization is a critical mechanism for closing the loop between plan generation and plan execution — a core requirement in Cercano’s holistic planning mode. The automatic pass of "PLAN.md" to the default "Execute" agent reveals a clean, closure-oriented handoff pattern: plans are not just stored but actively ingested by an execution agent. The read-only nature of the planning agent (`openhands/agenthub/readonly_agent`) — which only allows "READ-ONLY actions" — reinforces a safety and integrity model where the plan is frozen at creation, preventing accidental modifications during execution. This aligns with Cercano’s intent to separate planning from execution and to prevent plan drift. The combination of an immutable plan artifact and a seamless handoff to an execution agent demonstrates a mature design for gating approval and transitioning between modes — especially relevant to how Cercano should structure its own plan-execution handoff and mid-run extensibility.

**Relevance:** 5/5 | **Impact:** high

---

