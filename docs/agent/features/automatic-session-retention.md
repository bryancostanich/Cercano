# Automatic Session Retention

## What it is

The agent automatically persists conversation turns and tool-call history to
SQLite. Generated titles make sessions recognizable, the history picker lets
you return to prior work, and conversation search helps locate details inside
the active session. You can also name a session explicitly.

## Why it matters

Development rarely fits into one sitting. Return to an investigation without
re-explaining the project, reconstructing decisions, or manually saving a chat
transcript. Automatic naming helps distinguish sessions without making
bookkeeping part of every task.

## Try it

- `/history` — pick a previous conversation.
- `/resume <id>` — resume a known conversation.
- `/rename Refactor sprint` — give the current session a recognizable title.
- `/search configuration` — find matching text in the current conversation.

For repeatable headless work:

```bash
cercano run --conv my-session "Explain this project's configuration loader"
cercano run --conv my-session "Which tests cover it?"
```

## Controls and limitations

- Persisted history is not the same as the active model context: long sessions
  use [compacted views](context-management.md), which summarize older detail.
- Automatic naming is best-effort. Use `/rename` when a generated title is not useful.
- `/search` searches the current conversation; it should not be read as a claim
  of full-text search across every stored session.
- Resuming a transcript is not restoring the filesystem, a shell process, or a
  running command. Files may have changed since the conversation was saved.

See the [agent guide](../README.md) for persistence and session commands.

Back to the [feature index](README.md).
