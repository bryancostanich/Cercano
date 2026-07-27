# Spec-Driven Development with Gemini CLI | by Giovanni Galloro | Google Cloud - Community | Medium ⭐⭐⭐⭐⭐

**Source:** GitHub (Open source implementations)
**URL:** https://medium.com/google-cloud/spec-driven-development-with-gemini-cli-dfb4b88d4880

## Summary
April 9, 2026. create-plan command. Defined in .gemini/commands/create-plan.toml. Read the Technical Spec. Breaks down the project into atomic, sequential phases.

## Key Findings
- April 9, 2026
- create-plan command
- Defined in .gemini/commands/create-plan.toml
- Read the Technical Spec
- Breaks down the project into atomic, sequential phases
- Includes: Git Setup, Init, Backend, API, Frontend, Completion
- Writes the plan based on the spec

## Why This Matters
The Gemini CLI’s `create-plan` command, defined in `.gemini/commands/create-plan.toml` and executed via a dedicated CLI interface, provides a concrete, structured blueprint for how to instantiate a plan generation process from a technical spec — directly aligning with Cercano’s goal of building a holistic planning mode. The fact that it “breaks down the project into atomic, sequential phases” (Git Setup, Init, Backend, API, Frontend, Completion) demonstrates a clear, hierarchical, phase-based plan structure that can be captured as a granular artifact. This supports Cercano’s need to extract best practices in plan artifact design: format (TOML-based config), granularity (atomic tasks), and hierarchy (sequential progression). Further, the command “writes the plan based on the spec” shows a pipeline where planning is triggered by a formal spec, which models a valid “approval gate” before execution — a key component in Cercano’s required workflow. This is especially relevant because it shows a tool that “reads the technical spec” and derives a plan structure from it, mirroring the kind of structured, spec-driven input Cercano could require. The inclusion of the command in a well-defined configuration file (.gemini/commands/create-plan.toml) also suggests a standardized, reproducible way to capture and version the plan, which can later be handed to execution agents — supporting Cercano’s intent on plan delivery and mid-run re-entry.

**Relevance:** 5/5 | **Impact:** high

---

