# Built-In Delegation

## What it is

Cercano can dispatch a bounded task to a subagent with its own working context
and an explicit set of tools. The subagent performs its read/edit/test loop and
returns a focused result, rather than filling the main conversation with every
intermediate file read and tool response.

Delegation is native to the agent. You do not need an external co-processor
integration to use it. Saved task routing selects appropriate models, optionally
including local models or hosted open-weight providers.

## Why it matters

Reserve the main context for decisions and difficult reasoning. Code searches,
extraction, and well-specified edits can run on appropriately priced models,
reducing frontier-token consumption when the task is a good match. The main
agent can work from a concise result instead of carrying all the investigation's
raw material forward.

## Try it

> Delegate a read-only search for this project's configuration loader. Have the
> subagent return the call path and file references, then explain the findings.
> Do not modify files.

The agent chooses when to delegate; this prompt explicitly requests it. Watch
the reported route and granted tools to see where the work actually runs.

## Controls and limitations

- Routing quality, execution destination, and tool permissions are separate.
- A tool grant bounds what a subagent can call. Read-only tools are the default;
  write-capable grants interact with the permission/approval policy.
- Not every task is automatically delegated, and delegation is not necessarily
  local. Saved routes and placement restrictions determine execution.
- Subagents can fail or return incorrect results. Their conclusions still need
  review; additional inference and verification can outweigh savings on small tasks.
- Separate context is not a filesystem sandbox. Granted edits affect real files.

See [subagents](../sub_agents/README.md),
[routing](advanced-routing.md), and [permissions](../README.md).

Back to the [feature index](README.md).
