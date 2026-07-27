# GitHub - coctostan/pi-superpowers · GitHub ⭐⭐⭐⭐⭐

**Source:** GitHub
**URL:** https://github.com/coctostan/pi-superpowers

## Summary
Uses TDD tasks in the Plan skill (/skill:writing-plans). Executes plans using /skill:executing-plans or /skill:subagent-driven-development. Verifies completion with /skill:verification-before-completion. Requests code reviews using /skill:requesting-code-review. Finishes development with /skill:finishing-a-development-branch.

## Key Findings
- Uses TDD tasks in the Plan skill (/skill:writing-plans)
- Executes plans using /skill:executing-plans or /skill:subagent-driven-development
- Verifies completion with /skill:verification-before-completion
- Requests code reviews using /skill:requesting-code-review
- Finishes development with /skill:finishing-a-development-branch
- Each skill cross-references related skills for next steps
- Contains a test directory named tests/
- Includes extension/ subdirectory with plan-tracker.test.ts
- Contains a skills/ subdirectory with skill-validation.test.ts
- plan-tracker.test.ts is a unit test for plan-tracker core logic
- skill-validation.test.ts validates all skills including frontmatter, cross-refs, and file refs

## Why This Matters
The fact that "coctostan/pi-superpowers" uses a skill-based, TDD-driven workflow with structured plan execution and verification reflects a proven implementation of task tracking through recursive, goal-oriented skill coordination. This directly addresses the user's intent by providing a live, code-level example of a hierarchical task model (plan → phase → task) where tasks are not just flat items but are part of a structured, skilled workflow with clear state transitions. The existence of `plan-tracker.test.ts` and tests validating cross-references between skills demonstrates that the project actively tracks and verifies task progression at a granular, testable level—making it a concrete prototype of a recursive tracking system. This supports the hypothesis that plan-driven, hierarchical task modeling (Plan > Phase > Task) is technically feasible and actively implemented in real-world agentic systems.

**Relevance:** 5/5 | **Impact:** high

---

