# Powerful Agent Workflows

## What it is

Cercano includes tools and workflow protocols for research, delegation,
planning, approved execution, debugging, and design decisions. These give the
agent a repeatable process for substantial work instead of requiring you to
write a new set of instructions for every investigation or change.

| Workflow | What it brings to the work |
|---|---|
| Research | Search, source gathering, and synthesis tools for investigating a question. |
| Subagents | Bounded tasks with separate working context and scoped tool access. |
| Planning and execution | Reviewed plans when warranted, task progress, and approval boundaries for autonomous work. |
| Debugging | Reduce to a failing case, inspect actual evidence, test a hypothesis, then fix and verify. |
| Design decisions | Compare real alternatives by cost, risk, benefit, and side effects before committing to an approach. |
| Verification | Match checks to the change rather than treating plausible output as proof. |

## Why it matters

Spend less effort supervising the process and more effort evaluating results.
Evidence-first debugging discourages speculative patches; explicit decision
matrices make trade-offs visible; planning and execution workflows keep larger
changes tied to agreed goals. Delegation lets routine investigation happen in
its own context while the main conversation stays focused on decisions.

These workflows complement each other. Research can inform a design comparison,
an approved plan can guide execution, and targeted verification can establish
whether the result actually solves the problem.

## Try it

Ask in plain language; you do not need to memorize internal protocol names:

> Research the maintained options for this dependency. Cite the sources and
> compare compatibility and maintenance risks. Do not change the project yet.

> Diagnose this failing test. First reproduce the smallest failing case and
> show evidence for the cause before applying a fix.

> Compare the viable approaches to this migration in a decision matrix. Explain
> the trade-offs and wait for my choice before implementing it.

For a consequential change that needs a written plan, explicitly request one.
For a clear, bounded fix, ask the agent to implement and verify it directly;
formal planning is not a prerequisite for every edit.

## Controls and limitations

- Protocols guide model behavior; they do not guarantee correct reasoning or
  replace tool permissions, review, tests, and human judgment.
- Formal planning and autonomous execution have their own approval boundaries.
  Asking for a prose outline does not require entering formal planning mode.
- Research tools may require additional setup and network access. Sources can
  be incomplete or wrong; check citations and consequential conclusions.
- Tools and subagents follow configured routes and permissions. A research or
  debugging workflow is not necessarily local or free of inference charges.
- Decision matrices are for genuine choices, not a prerequisite for routine tasks.

See [built-in delegation](built-in-delegation.md),
[routing](advanced-routing.md), and the [agent guide](../README.md).

Back to the [feature index](README.md).
