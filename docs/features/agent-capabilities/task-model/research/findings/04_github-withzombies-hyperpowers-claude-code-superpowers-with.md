# GitHub - withzombies/hyperpowers: Claude Code superpowers with beads task tracking and refinement · GitHub ⭐⭐⭐⭐⭐

**Source:** GitHub
**URL:** https://github.com/withzombies/hyperpowers

## Summary
Uses markdown-first workflow. Tracks active work in local markdown task directories. Stores tasks under the directory path: plans/active/.

## Key Findings
- Uses markdown-first workflow
- Tracks active work in local markdown task directories
- Stores tasks under the directory path: plans/active/

## Why This Matters
The fact that "GitHub - withzombies/hyperpowers: Claude Code superpowers with beads task tracking and refinement" tracks active work in local markdown task directories under `plans/active/` directly addresses the user’s intent to understand how task tracking is implemented in terms of data structure, storage location, and UI surface. This reveals a plain-file, markdown-first approach with a clear, hierarchical directory structure that aligns with a plan-centric model—specifically, tasks are stored in a nested path (`plans/active/...`), implying that the system naturally supports a recursive hierarchy where subtasks (or phases) can be organized under a plan. This is directly relevant to evaluating whether a recursive `plan > phase > task` structure is viable and already practiced in industry-adjacent tools.

**Connections to other findings:** This finding corroborates and extends finding #5: "Activated Plan Mode in Claude via Shift+Tab..." and #8: "Tasks API vs TodoWrite | FlorianBruniaux/claude-code-ultimate-guide". While #5 confirms the existence of a Plan Mode UI, this fact reveals *how it is implemented under the hood*: with a structured, hierarchical file system (plans/active/), not just a memory-based state. This shows that the UI is backed by persistent, intentional data organization. Finding #8 (on Tasks API vs TodoWrite) further suggests that multiple task forms exist—this fact supports the idea that hyperpowers' `active/` directory may contain multiple task types (e.g., plan-level tasks vs. phase-level tasks), which is evidence for multi-source origins. The persistence of these files on disk (as per the 1.2 MB cache mentioned in #6) reinforces that provenance is maintained.

**Relevance:** 5/5 | **Impact:** high

---

