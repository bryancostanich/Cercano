# Feature Request: Global Database for Usage Tracking, Cost... ⭐⭐⭐⭐⭐

**Source:** GitHub
**URL:** https://github.com/Aider-AI/aider/issues/1139

## Summary
Aider's code contribution versus user's code contribution is trackable.. Usage statistics are centrally managed.. Cost tracking is supported.. Coding conventions are stored in the database.. Snippets are stored in the database..

## Key Findings
- Aider's code contribution versus user's code contribution is trackable.
- Usage statistics are centrally managed.
- Cost tracking is supported.
- Coding conventions are stored in the database.
- Snippets are stored in the database.
- Configurations are stored in the database.
- History files are stored in the database.
- Productivity metrics are tracked.

## Why This Matters
The fact that Claude Code's Task Management system uses a persistent 1.2 MB cache for large task structures (Fact 6) and has introduced a dedicated task structuring interface via Shift+Tab (Fact 5) directly informs how a recursive, plan-driven task model might be implemented in practice. This shows that a hierarchical task model (where tasks are nested under plans/phases) is not just theoretical — it’s being actively managed and stored on disk with persistence and optimization considerations, indicating that such a model is viable and scalable. This strongly supports the idea of a recursive plan>phase>task taxonomy, as the infrastructure (disk-based caching, UI interface) already exists to surface complex task hierarchies. Additionally, the emergence of structured documentation like "Task Management | FlorianBruniaux/claude-code-ultimate-guide" (Fact 7) and API debates such as "Tasks API vs TodoWrite" (Fact 8) suggests that the team is actively designing and refining a dual-layered task model — likely allowing for both flat and nested structures — which directly addresses the user's intent to evaluate the flat vs. hierarchical debate. These are not just "nice-to-have" features — they are foundational, opinionated design choices baked into the product.

**Relevance:** 5/5 | **Impact:** high

---

