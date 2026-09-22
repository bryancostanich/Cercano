# Client/Server Architecture

## What it is

An independent agent server owns execution, providers, permissions, and
conversation persistence. Thin clients connect over gRPC: the interactive
terminal UI and headless commands use the same agent infrastructure.
Multiple conversations have their own execution state, including working
directory and turn state, instead of sharing one mutable chat session.

## Why it matters

Keep separate projects and investigations in separate sessions while using one
agent service. The terminal interface can evolve independently of the agent,
and the same execution capabilities can serve automation and other clients
without duplicating the agent itself.

## Try it

Launch `cercano` in two terminals and start separate conversations for different
tasks. The client auto-launches the agent server when needed. For a headless task:

```bash
cercano run "Explain the repository structure without editing files"
```

You can also start the server explicitly with `cercano agent`.

## Controls and limitations

- Session-state isolation is **not filesystem sandboxing**. Sessions with tool
  access can affect the same files; use separate worktrees for conflicting edits.
- Permissions remain important regardless of which client submits a task.
- An independent process does not promise that every in-flight task survives a
  client disconnect or server restart. Persisted conversations are a separate feature.
- Server and CLI are separate Go modules; interface changes may require updating both.

See the [architecture reference](../agent-isolation/architecture.md),
[developer build guide](../self-dev.md), and
[session retention](automatic-session-retention.md).

Back to the [feature index](README.md).
