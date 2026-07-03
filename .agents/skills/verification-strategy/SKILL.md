---
name: verification-strategy
description: Match the test tier to the size of the change.
---

# Verification Strategy Protocol

You are now operating under Dave's verification protocol. Before running tests, think about WHICH tests to run. Running the full suite when you only changed one function wastes time. Skipping integration tests when you changed an interface misses bugs.

## Verification Tiers

### Tier 0: Unit / Isolated

Test the component in isolation. Mock or stub its dependencies. Fastest.

**Use when:** You're changing internal logic that doesn't cross boundaries. Algorithm changes, data transformations, pure functions, state machine logic.

**Don't use when:** You've touched an interface, changed data formats, or modified how components communicate.

### Tier 1: Integration / Smoke

Test the component in context with its immediate neighbors. Real dependencies, controlled inputs.

**Use when:** You've changed an interface, modified data flow between components, or touched boundary code. Also use as a sanity check after significant Tier 0 changes.

**Don't use when:** You need to validate end-to-end behavior at real operating conditions.

### Tier 2: System / End-to-End

Full system test at real operating conditions. Slowest. This is the sign-off test.

**Use when:** Pre-merge validation, release sign-off, or investigating bugs that only appear under realistic conditions.

**Don't use when:** You're iterating on a single component — use Tier 0 or 1 and save Tier 2 for when you're ready to validate.

## Rules

- **Match the tier to the change.** Don't run Tier 2 when Tier 0 covers what you changed.
- **Don't skip Tier 1 when you've touched an interface.** Unit tests won't catch integration bugs.
- **Run only affected tests during iteration.** Full suite for sign-off, targeted tests for development.
- **If a Tier 0 test passes but Tier 1 fails**, the bug is at the boundary — focus there.
- **If Tier 1 passes but Tier 2 fails**, the bug is in system-level interactions — timing, ordering, resource contention.

