# Recap Upgrade + Auto-Title — Design

**Status:** Design approved. Implementation not started.

The first, smallest rung of the "context reduction ladder." Two changes that
share the recap's existing local-model, off-request-path plumbing: widen the
living recap, and give untitled sessions a real local-model-generated title
(derived from the recap) shown in the `-r` history screen.

## Why

The reduction ladder — title → recap → rolling summary → elided stubs →
verbatim turns → raw archive — is one idea at different zoom levels, all produced
by the same local-model reduction. The two tightest rungs (title, recap) are
cheap to ship now, they make the history screen and dashboard better
immediately, and they exercise the summarization plumbing the compaction
subsystem (sub-project 2) will lean on.

Today:
- The recap (`recap/recap.go`) is a debounced, local-model, **80-char** one-liner.
  80 characters is too tight to describe a real session.
- Conversation titles come from `conversation.DeriveTitle(firstPrompt)` — a crude
  algorithmic slice of the first user prompt. Untitled sessions render
  `(untitled)` in the `-r` history screen (`history_view.go:96`).

## Layering — agent-owned, client-agnostic

Both changes live entirely in the agent (server) layer: the recap width and the
title generation are in `recap` / `conversation` under `source/server/internal/`,
running off the request path. Clients (CLI, VS Code, Zed) consume the resulting
`Title` / `Recap` through the existing `GetConversation` / `ListConversations`
RPCs and only render them. No client generates titles or recaps locally.

## 1. Recap length: 80 → 160

`recap.maxRecapChars` becomes **160**. This is the cap passed to the prompt
("at most N characters") and to `firstLine`'s truncation. The recap stays a
single line. No other behavior changes — debounce, incremental prompt
(`prior + recent maxTurns`), failure-swallowing, and the per-conversation timer
are all unchanged.

Anything that renders the recap (dashboard, `/c` header, history rows) must
already wrap or truncate to its available width; widening the source string to
160 must not break those call sites (verify each clamps to its own width).

## 2. Auto-title for untitled sessions

A session is "untitled" when it has no user-chosen title (no `Rename`) — i.e. it
still carries the algorithmic `DeriveTitle` slice or an empty title. For those,
generate a short local-model title **from the recap** (the next-tightest rung is
derived from the rung below it, not from a fresh full-history pass — that is the
ladder's whole point).

- **Source:** the conversation's current recap. If there is no recap yet, skip
  (nothing to derive from; leave the existing algorithmic title / `(untitled)`).
- **Generation:** a local-model completion (the same `CompleteFunc` the recap
  uses) with a prompt that turns the recap one-liner into a 3–6 word title.
  Capped at a small char limit (e.g. 48) via the existing `firstLine` helper.
- **When it runs:** piggyback on recap regeneration. After the generator writes a
  new recap, if the conversation is still untitled it derives and stores the
  title in the same off-request-path pass. No new trigger, no new timer.
- **Storage:** a generated title is distinct from a user `Rename`. A user title
  always wins and is never overwritten by generation. The store needs to
  distinguish "auto/derived" from "user-chosen" so generation only ever touches
  the former (a flag, or: generation only runs while the title equals the
  algorithmic `DeriveTitle` output / is empty).
- **Display:** `-r` history (`history_view.go`) shows the generated title in
  place of `(untitled)`; the header session-title slot (`model.go`
  `sessionTitle`) picks it up through the existing title plumbing.

## 3. Data flow

```
user turn persisted ─▶ recap.Schedule (debounced)
                         └▶ regenerate: recap = summarize(prior recap, recent turns)
                              └▶ if still untitled: title = makeTitle(recap)
                                   └▶ store.UpdateRecap + store auto-title write
history (-r) / header ◀── store.Get(Info{Title, Recap})
```

## 4. Error / edge

| Case | Behavior |
|---|---|
| No recap yet | Skip title generation; keep existing algorithmic title / `(untitled)` |
| Local model fails | Keep prior title (failures swallowed, like recap) |
| User has renamed | Never overwrite; generation skips user-chosen titles |
| Empty model output | Keep prior title |
| Recap shorter than a title | Use it as-is; titilize prompt tolerates short input |

## 5. Testing

- **Recap width:** the generated prompt requests ≤160; `firstLine` truncates at
  160 with the ellipsis; a long model output is cut to 160 runes.
- **Title generation (fake `CompleteFunc`):** an untitled conversation with a
  recap gets a stored generated title; a conversation with no recap is left
  untouched; a user-renamed conversation is never overwritten; model failure
  leaves the prior title.
- **Display:** a generated title renders in the `-r` history row instead of
  `(untitled)` (CLI render test against the existing history view).

## Out of scope

The compaction subsystem (sub-project 2). Re-titling already-titled sessions.
Title generation from full history (we derive from the recap by design).

## Key file references

| Concern | Location |
|---|---|
| Recap cap + generation | `source/server/internal/recap/recap.go` (`maxRecapChars`, `regenerate`, `buildPrompt`) |
| Title storage + derive | `source/server/internal/conversation/store.go` (`Title`, `DeriveTitle`, `Rename`, `UpdateRecap`) |
| History screen | `source/clients/cli/internal/ui/history_view.go` (`(untitled)` at :96) |
| Header title slot | `source/clients/cli/internal/ui/model.go` (`sessionTitle`) |
