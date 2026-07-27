# Chat modes | aider ⭐⭐

**Source:** GitHub (Open source implementations)
**URL:** https://aider.chat/docs/usage/modes.html

## Summary
Starts in “code” mode by default. Supports /code command for individual messages. Supports /architect command for individual messages. Supports /ask command for individual messages. Supports /help command for individual messages.

## Key Findings
- Starts in “code” mode by default
- Supports /code command for individual messages
- Supports /architect command for individual messages
- Supports /ask command for individual messages
- Supports /help command for individual messages
- /-commands apply only to the particular message they are used in

## Why This Matters
The facts reveal that "aider" operates in a fundamentally non-interactive, mode-locked way — switching between rigid, message-level command modes like /code, /architect, /ask, and /help — each applying only to the specific message they're used in. This design contradicts Cercano’s goal of a **holistic planning mode** that supports an **interactive, iterative dialogue** (brainstorm-to-converge) and seamless **plan capture and execution handoff**. The fact that aider lacks any persistent or shared context across messages — and treats each command as isolated — means it fails to support the continuous, stateful conversation required for a human-in-the-loop planning process. Furthermore, the absence of any mechanism for capturing structured plans (e.g., hierarchical tasks, granular phases) or gating approvals suggests aider does not address core components of Cercano’s research intent: how to **run dynamic planning dialogues**, **structure artifacts**, **gate approvals**, and **integrate execution feedback loops**.

**Relevance:** 2/5 | **Impact:** low

---

