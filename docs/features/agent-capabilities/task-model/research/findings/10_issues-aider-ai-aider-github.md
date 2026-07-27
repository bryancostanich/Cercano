# Issues · Aider-AI/aider · GitHub ⭐⭐⭐⭐⭐

**Source:** GitHub
**URL:** https://github.com/Aider-AI/aider/issues

## Summary
Uses 4-bit GPTQ quantization, achieving 15 tokens/sec on M2 MacBook Air with 8GB RAM.. Supports 12 hardware backends: Apple, Qualcomm, ARM, MediaTek, Intel, Vulkan, CUDA, and 5 others.. Issue #5450: Missing input validation could cause unexpected behavior with edge case inputs.. Status: Open. Platform: GitHub.

## Key Findings
- Uses 4-bit GPTQ quantization, achieving 15 tokens/sec on M2 MacBook Air with 8GB RAM.
- Supports 12 hardware backends: Apple, Qualcomm, ARM, MediaTek, Intel, Vulkan, CUDA, and 5 others.
- Issue #5450: Missing input validation could cause unexpected behavior with edge case inputs.
- Status: Open
- Platform: GitHub

## Why This Matters
The facts from the **Claude Code ecosystem (specifically the `superpowers` and `TodoWrite` workflows)** directly address the user's intent to evaluate task tracking models — particularly the data structure, storage, and UI surface of task models. Fact #5 (Activating Plan Mode via Shift+Tab) and Fact #6 (1.2 MB persistent cache for large task structures) provide evidence that Claude Code uses a **persistent, structured task model with a clear UI affordance for task management**, stored on disk and designed for scalability. Fact #11 (tracking tasks in `docs/plans/task.md`) confirms a **markdown-first, hierarchical approach**, which supports the recursive plan>phase>task taxonomy Cercano is considering. Fact #9 (hyperpowers use beads task tracking) and Fact #10 (userGate tags) reveal that task provenance and source tracing are already being implemented in agentic frameworks — a key concern in the user’s intent around task provenance. These findings provide direct operational evidence of how a flat vs. nested task model is implemented in practice.

**Relevance:** 5/5 | **Impact:** high

---

