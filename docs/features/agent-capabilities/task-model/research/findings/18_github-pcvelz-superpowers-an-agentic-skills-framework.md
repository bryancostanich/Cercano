# GitHub - pcvelz/superpowers: An agentic skills framework & software development methodology that works - Claude Code task management support · GitHub ⭐⭐⭐⭐

**Source:** GitHub
**URL:** https://github.com/pcvelz/superpowers

## Summary
Uses userGate / user-gate tag. Implements opt-in hooks. Forces re-validation when Claude closes a user-ordered verification task. Recommends configuration for upstream compatibility with obra/superpowers core workflow.

## Key Findings
- Uses userGate / user-gate tag
- Implements opt-in hooks
- Forces re-validation when Claude closes a user-ordered verification task
- Recommends configuration for upstream compatibility with obra/superpowers core workflow

## Why This Matters
The extracted facts provide crucial insight into the task tracking and model architecture of *Claude Code’s* Superpowers plugin — directly addressing the user’s intent to evaluate how task modeling and tracking are implemented in real systems. Specifically, the use of `userGate / user-gate` tags indicates a structured, user-defined mechanism for defining task boundaries and ensuring validation order — a direct alignment with the need to understand *how tasks are structured and validated*. The fact that the plugin *forces re-validation when Claude closes a user-ordered verification task* reveals a critical behavioral pattern: tasks are not just stored, but *executed in a sequence that requires closure verification*, pointing to a model that tracks state and progression. Additionally, the reference to *opt-in hooks* suggests a modular, extensible infrastructure that could support multiple task sources — relevant to whether Cercano should support plan-driven, ad-hoc, or mixed task provenance. Finally, the mention of *upstream compatibility with obra/superpowers core workflow* implies that the task model is not isolated but part of a larger, reusable architecture — essential for evaluating whether a recursive plan>phase>task taxonomy can be interoperable across tools.

**Relevance:** 4/5 | **Impact:** high

---

