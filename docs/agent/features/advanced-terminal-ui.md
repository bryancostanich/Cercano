# Advanced Terminal UI

## What it is

Cercano's terminal client combines rich response formatting, responsive layouts,
color themes, a live context meter, and visible tool and subagent activity.
Settings bring model selection, routing, permissions, and metrics into the same
interface instead of requiring a separate configuration workflow.

## Why it matters

Long coding sessions produce dense output: code, tables, tool results, and
parallel lines of investigation. Readable presentation helps you follow what
the agent is doing, spot decisions that need attention, and stay focused on the
work rather than deciphering a transcript. Themes let you adapt the interface
to your preferences.

## Try it

Start `cercano`, then use:

- `/theme` to select or edit a theme.
- `/config` to explore settings and responsive layouts at different terminal widths.
- `/context` to inspect context usage alongside the live meter.
- `/search TODO` to find matching text in the current conversation.
- `/help` for the current keymap and command list.

Ask the agent to compare a few project components in a table and inspect the
rendering in your normal terminal size.

## Controls and limitations

- Layout and color rendering depend on terminal dimensions and capabilities.
- The interactive client and headless output are different presentations of
  the same agent; do not expect terminal interactions in a script.
- The [CLI track](../../features/cli/README.md) records remaining interface work.
  Formatting claims are not a promise that every planned control is implemented.

See the [agent command reference](../README.md).

Back to the [feature index](README.md).
