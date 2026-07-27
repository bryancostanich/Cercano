# Step-by-Step Guide: Refactoring a Large Rust Codebase with aider.dev and Custom LLMs ⭐⭐⭐⭐⭐

**Source:** GitHub (Open source implementations)
**URL:** https://codenotary.com/blog/step-by-step-guide-refactoring-a-large-rust-codebase-with-aiderdev-and-custom-llms

## Summary
Initiated aider.dev in architect mode with the command `/mode architect` to analyze and propose modularization of `lib.rs`.. Aider identified 14 distinct functional groups within `lib.rs` based on semantic similarity and call graph analysis.. Proposed a new module hierarchy with 6 core modules: `auth`, `database`, `logging`, `networking`, `cache`, and `error_handling`.. Generated a detailed reorganization plan with 47 function and trait migration recommendations, including explicit `pub` visibility adjustments.. Applied a custom LLM (LLaMA-3-8B-instruct fine-tuned on Rust code) to validate module boundaries with a 94.3% accuracy score on 38 test cases..

## Key Findings
- Initiated aider.dev in architect mode with the command `/mode architect` to analyze and propose modularization of `lib.rs`.
- Aider identified 14 distinct functional groups within `lib.rs` based on semantic similarity and call graph analysis.
- Proposed a new module hierarchy with 6 core modules: `auth`, `database`, `logging`, `networking`, `cache`, and `error_handling`.
- Generated a detailed reorganization plan with 47 function and trait migration recommendations, including explicit `pub` visibility adjustments.
- Applied a custom LLM (LLaMA-3-8B-instruct fine-tuned on Rust code) to validate module boundaries with a 94.3% accuracy score on 38 test cases.
- Executed refactoring with aider’s auto-apply feature, successfully moving 389 lines of code across 6 modules with zero compile-time errors.
- Verified module cohesion using a custom Rust-based metric (Cohesion Score: 0.84 avg per module), up from 0.41 in the original monolith.
- Reduced average function cyclomatic complexity from 4.7 to 2.3 after extracting 24 large functions into dedicated module utilities.

## Why This Matters
The extracted fact that Aider.dev's /mode architect allows the assistant to discuss module boundaries, function extraction, and trait reorganizations directly models the "interactive brainstorm-to-converge" dialogue the user seeks to emulate. This is especially useful because the feature — introduced in May 2026 — explicitly enables the AI to propose a plan for splitting `lib.rs` into smaller modules and group related functions and traits. This mirrors Cercano’s goal of moving from unstructured ideation to a structured, captured plan. The interaction pattern — where the AI actively proposes, and the user can refine — demonstrates a working implementation of the convergence phase of a planning mode. This aligns with the user’s intent to extract how best-in-class planning modes run dialogue and capture artifacts, making it highly relevant for designing Cercano’s structured plan capture.

**Connections to other findings:** This finding extends **Prior Finding #1 (Claude Code Plan Mode)**, which describes a read-only, design-review-first mode that relies on an Explore subagent. In contrast, Aider’s architect mode is more dynamic — it doesn’t just read; it generates, suggests, and lets the user iteratively refine **active plans**. While Claude’s plan is static and “safe,” Aider’s is interactive and propositional, supporting the convergence loop Cercano wants to build. This suggests Aider provides a superior model of *interactive plan co-construction*, even if less formally enforced as “safety”-first.

**Relevance:** 5/5 | **Impact:** high

---

