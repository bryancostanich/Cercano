# Streaming Markdown Rendering in the CLI Viewport

## Problem

The standalone agent's terminal CLI shows the LLM's prose reply as raw text. Only
pipe-tables are formatted (intercepted on `TypeDone` and rendered by the responsive
`render.Table` renderer). Headings, bold/italic, inline code, fenced code blocks,
lists, blockquotes, links, and horizontal rules all render as literal markdown
syntax. The assistant frequently emits markdown (including when interpreting tool
output), so this reads as unpolished.

## Goal

Render the assistant's markdown prose nicely in the viewport, **progressively as it
streams** — formatted blocks appear as soon as they complete, not in one snap at the
end. Keep the existing responsive table rendering. No change to user/system entries
or to tool-call entries (they stay folded one-liners).

## Non-Goals

- Rendering raw tool-output bodies (tool entries remain folded summaries).
- Live formatting of partial inline markup inside the in-progress block beyond what
  falls out naturally (see Tail handling).
- Replacing the responsive table renderer with Glamour's table support.

## Context

- Target branch: `md-streaming-render` (worktree `Cercano-md-render`), off `main`.
- `main` is on the Charm **v2** stack: `charm.land/lipgloss/v2`,
  `charm.land/bubbletea/v2`, `charm.land/bubbles/v2`.
- Today: tokens stream into `Entry.Content` (raw); on `TypeDone`,
  `render.InterceptMarkdownTables` pulls tables into `Entry.Tables` and leaves
  `{{TABLE_N}}` sentinels; `renderEntry` re-renders tables every frame at the
  current width (so resize refits).

## Approach

Use **Glamour v2.0.1** for prose, keep the **existing responsive `render.Table`**
for tables, and drive both from a **streaming block splitter** so formatting lands
block-by-block as the stream arrives.

### Why Glamour for prose, our renderer for tables

- Glamour v2.0.1 (released 2026-06-12) is built against `charm.land/lipgloss/v2`
  v2.0.4 — the same lipgloss module `main` uses, so they unify on one copy. It
  bundles `chroma/v2` for syntax-highlighted code blocks.
- Glamour emits a plain ANSI string; the viewport embeds it. No lipgloss type
  interop at the boundary.
- Glamour's table support sizes columns to content and does **not** wrap cells or
  drop columns, so wide tables overflow narrow terminals. Our `render.Table` does
  priority-based column dropping + a wrappable last column. We keep it.
  - Verification during implementation: a probe rendering a 4-column table through
    Glamour v2.0.1 at width 40, confirming overflow before finalizing the split.

## Components

### 1. Dependency & theme

- Add `github.com/charmbracelet/glamour/v2 v2.0.1` (import path stays
  `github.com/charmbracelet/...`; it depends on `charm.land/lipgloss/v2`, unifying
  with `main`'s lipgloss).
- Build one custom Glamour style from `internal/cli/theme/palette.go`:

  | Element | Style |
  |---|---|
  | Heading | amber, bold |
  | Emphasis | bold / italic |
  | Inline code | cyan |
  | Fenced code | charcoal bg + chroma theme |
  | List bullet | lime |
  | Blockquote | dim left bar |
  | Link | cyan, underline |
  | Rule (`---`) | muted line |

- Word-wrap width is passed per render call (so resize reflows).
- Construct the Glamour renderer once and reuse; re-create only if width handling
  requires per-width renderers (decide in plan — Glamour takes wrap width as an
  option at construction; may build per-width or pass width per render).

### 2. Streaming block splitter (`internal/cli/render/mdstream.go`)

A pure, isolated unit. Input: the growing assistant text. Output:
`(committed []Block, tail string)`.

- `Block` has a raw markdown source and a kind: `Prose` (includes code blocks) or
  `Table`.
- Boundary rules:
  - Blank line closes a paragraph / heading / list group.
  - A fenced code block (` ``` `) is one block; never split between its open and
    close fences, even across blank lines.
  - A pipe-table block is detected via the existing `matchTable` logic and emitted
    as a `Table` block.
- The final, not-yet-terminated block is returned as `tail` (not committed) until a
  boundary closes it.
- No markdown library, no LLM call — same algorithmic spirit as the current table
  interceptor.

### 3. Entry model change (`internal/cli/ui/model.go`)

- Assistant `Entry` replaces `Content` + `Tables` with:
  - `Blocks []mdBlock` — each carries raw source, kind, and a render cache keyed by
    the width it was rendered at.
  - `Tail string` — the live, in-progress block (raw markdown).
- `applyStreamMsg`:
  - `TypeToken`: append to the accumulated buffer, run the splitter, move newly
    completed blocks into `Blocks`, set `Tail` to the remainder.
  - `TypeDone`: flush `Tail` as the final block(s); clear `Tail`.
- `renderEntry` (assistant case):
  - For each committed block: render once at the current text width and cache;
    reuse the cache on subsequent frames. `Prose` → Glamour; `Table` → `render.Table`.
  - For `Tail`: render live through Glamour by synthesizing a temporary close
    (append a closing ` ``` ` if a fence is open). So streaming code highlights as it
    grows and prose formats progressively, with no half-open-construct garbage.
  - Concatenate committed renders + tail render.
- `relayout` / width change: invalidate render caches so committed blocks and tables
  re-render at the new width. Tables refit via the responsive renderer (current
  behavior preserved).

### 4. Replaces

- The on-`TypeDone` `InterceptMarkdownTables` call: tables now flow through the
  splitter as a `Table` block kind. `render.Table` and the table-detection helpers
  (`matchTable`, `looksLikePipeRow`, etc.) are **reused**, not rewritten. The
  standalone `InterceptMarkdownTables` entry point may be retired or kept for the
  legacy path — decide in plan.

## Data Flow

```
stream tokens ─► Entry buffer ─► splitter ─► committed Blocks  ─► render once (cached)
                                   │                               Prose→Glamour
                                   │                               Table→render.Table
                                   └► tail (raw) ──────────────► render live (synth-close)
                                                                   ─► Glamour

relayout(width change) ─► invalidate caches ─► re-render committed blocks + tables at new width
```

## Error / Edge Handling

- Open code fence at stream end (model truncated): `TypeDone` flushes the tail; the
  synth-close path already renders it as a closed code block.
- Fenced block containing blank lines: fence tracking keeps it one block.
- Table immediately after prose: blank line separates them into distinct blocks;
  a table with no separating blank line is handled by `matchTable`'s own detection.
- Final block with no trailing newline: emitted on `TypeDone`.
- Glamour render error (should be rare): fall back to the raw block text styled with
  `AgentProse` so output is never lost.
- Empty assistant reply / no tokens then `TypeDone` with `Final`: existing fallback
  (`Content = sm.Final`) maps to seeding the buffer before the final split.

## Testing

- **Splitter** (`mdstream_test.go`): feed incremental chunks; assert block
  boundaries for paragraph, heading, list, open→closed fence, fence-with-blank-line,
  pipe-table mid-stream, and a no-trailing-newline tail. Assert tail contents at each
  step.
- **Render golden**: a representative markdown doc → Glamour-themed output snapshot.
- **Resize**: render an entry containing a wide table at width A then width B; assert
  the table refits (mirrors the existing table resize test).
- **Glamour table overflow probe** (one-off, may live as a test or scratch): confirm
  Glamour v2.0.1 overflows a 4-column table at width 40 — the justification for
  keeping `render.Table`.

## Open Decisions (resolve in plan)

- Whether to build one Glamour renderer and pass width per call, or cache renderers
  per width.
- Whether to retire `InterceptMarkdownTables` or keep it for the legacy/non-streaming
  path.
