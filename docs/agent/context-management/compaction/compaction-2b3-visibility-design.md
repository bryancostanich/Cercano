# Compaction 2b-3 — Visibility (Footer + `/c` + Export) — Design

**Status:** Design approved. Implementation starting.

Compaction went live in 2b-1 but is **invisible** — long sessions silently
compact with no signal. 2b-3 surfaces it: an always-visible footer indicator
(with a live "compacting" animation), a `/c` drill-down showing the summary and
the compacted-vs-live split, a toggle to the full original, and a JSON export of
the complete uncompacted context.

## Why

The agent now sends a *compacted* context (consolidated summary + live tail), but
the footer meter still reports a raw running counter, `/c` lists raw turns with
no compaction cues, and there's no way to pull the full original. Users can't see
that their context is being kept lean, can't read what the summary captured, and
can't recover the raw history. 2b-3 closes all three gaps.

## Layering — agent-owned, client-agnostic

The server computes everything (sent/raw sizes, the compacting flag, the
consolidated summary, the export). The CLI only renders. New/extended RPCs are
the entire client contract, so VS Code / Zed get the same data.

## The three surfaces

### 1. Footer meter (always visible — the primary indicator)

`renderStatus` (`model.go`) composes the footer; its `ctx` section today shows
`context 18k / 200k · 9%` from a running counter.

- **Honest size.** The meter now reports the **sent (compacted)** size against
  the model max — the true "what the model is working with." A small savings
  badge shows the original: `context 18k / 200k · 9% · ▣ 95%↓`.
- **Live compacting animation.** While a background compaction pass is in flight
  for the current conversation, the `ctx` section swaps the bar for an
  **animated sprite** (`animateSpinnerGlyph`) + a **`compacting…`** overlay swept
  with `animateLimeSweep` (both already exist). It snaps back to the settled
  meter when the pass finishes. The existing spinner tick loop drives the frames;
  the existing `GetContextUsage` poll carries the `compacting` flag.

### 2. The `/c` page (drill-down)

`/c` (`context_view.go`) already loads every raw turn (`GetConversationTurns`).
2b-3 adds, above the turn list:

- A **status line** (sent · raw · savings · frozen/live counts).
- The **consolidated summary** block (the preamble actually sent), rendered from
  `GetCompactionState`.
- The turn list, now **split**: frozen turns collapse behind the summary; live
  turns render verbatim as today.
- A **toggle to "full original"** — pure client-side rendering over the
  already-loaded turns (no new fetch): show all turns, hide the summary. A
  keybind flips between *sent view* and *original*.
- An **export** keybind that calls `ExportContext` and writes the full uncapped
  raw context to a JSON file (path shown to the user).

(The exact footer-badge and `/c` header wording are cosmetic; sensible defaults,
tuned on first build.)

### 3. Server data surface (RPCs)

- **`GetContextUsage` extended.** `tokens_used` becomes the **sent (compacted)**
  size; add `raw_tokens` (uncompacted) and `compacting` (bool). `sent` is
  computed as `TotalTokens(BuildSendView(turns, state))`; `raw` as
  `TotalTokens(BuildLLMHistory(turns))`. Cheap (sent view is small).
- **`GetCompactionState` (new).** Returns `frozen_through`, `frozen_turns`,
  `live_turns`, `compacted_segments`, `raw_tokens`, `sent_tokens`,
  `consolidated_summary` (rendered text), and `compacting`. Drives the `/c`
  status + summary block + the frozen/live split.
- **`ExportContext` (new).** Returns the complete, **uncapped** raw history as
  JSON — the real `[]llm.Message` from `BuildLLMHistory(all turns)`, marshalled.
  The CLI writes it to disk. (Plain-text transcript export is a later nicety.)

### The "compacting" flag (in-flight state)

The generator (`compactiongen`) tracks an in-flight set: mark a conversation
compacting when `runCompaction` starts, clear it when it finishes or fails
(under the existing mutex). Expose `IsCompacting(convID) bool`; the agent
surfaces it (`a.IsCompacting`), and both usage RPCs read it. The footer poll
(~1–2s) catches it; passes take seconds, so it's reliably visible.

## Data flow

```
footer poll ─▶ GetContextUsage(convID)
                 └▶ sent=TotalTokens(BuildSendView), raw=TotalTokens(full),
                    compacting=generator.IsCompacting → animate or settle

/c open/refresh ─▶ GetConversationTurns (all raw, already) + GetCompactionState
                     └▶ summary block + frozen/live split + status line

/c export key ─▶ ExportContext(convID) ─▶ JSON ─▶ CLI writes file
```

## Decomposition (build order — footer first)

- **2b-3a — server data surface:** the `compacting` flag (generator + agent),
  the extended `GetContextUsage`, `GetCompactionState`, `ExportContext`
  (proto regen + server handlers + agentclient). Deterministic, unit-testable.
- **2b-3b — footer:** honest sent/raw meter + savings badge + compacting
  animation/overlay in `renderStatus`.
- **2b-3c — `/c` page:** status line + summary block + frozen/live split +
  original toggle + export keybind.

## Error / edge

| Case | Behavior |
|---|---|
| No compaction state | `GetCompactionState` returns empty; footer shows the plain meter (sent==raw); `/c` shows all turns as today |
| `GetContextUsage` mid-compaction | `compacting=true`; footer animates; numbers are last-settled until the pass commits |
| Export of a huge raw context | Returns the full JSON; the CLI streams it to the file (size is the user's concern — it's an explicit dump) |
| Compaction disabled | `compacting` always false; meter is plain (sent==raw); `/c` shows raw turns |
| Adding RPCs | New RPCs touch the AgentClient interface → update the mcp `mockAgentClient` (proactively, per prior regressions) |

## Testing

- **Server:** `GetContextUsage` reports sent < raw when compacted (and sent==raw
  when not); `compacting` reflects the generator flag; `GetCompactionState`
  returns the summary + frozen/live counts; `ExportContext` returns valid JSON
  that round-trips to `[]llm.Message`. The generator's `IsCompacting` is true
  during a pass, false after (deterministic with a blocking fake summarizer).
- **CLI:** `renderStatus` shows the savings badge when sent<raw and the animated
  `compacting…` overlay when the flag is set (assert via `stripAnsiCSI`); `/c`
  renders the summary block + frozen/live split; the original toggle flips the
  rendered set; export writes a file.

## Out of scope (later)

- Plain-text transcript export (JSON now).
- Explicit "compact now" user trigger (background handles it; the engine's
  `CompactNow` already exists if we want a keybind later).
- 2b-2 retention enforcement.
- The 2b-1b minors (per-request tokenization pre-check, `ModelMax` substring
  robustness, per-conversation compaction serialization).

## Key file references

| Concern | Location |
|---|---|
| Footer compose + animations | `source/clients/cli/internal/ui/model.go` (`renderStatus`, `animateSpinnerGlyph`, `animateLimeSweep`, `ctxUsageMsg`) |
| `/c` page | `source/clients/cli/internal/ui/context_view.go` (`renderHeader`, `turnsLines`, `renderMeterBar`) |
| Usage/state/export RPCs | `source/proto/agent.proto`; `source/server/internal/server/{server.go,context_turns.go}`; `source/server/pkg/agentclient/client.go` |
| Compacting flag + send-view sizing | `source/server/internal/compactiongen/`, `internal/compactor/` (`BuildSendView`), `agent.BuildLLMHistory` |
| mcp mock to update on new RPCs | `source/server/internal/mcp/server_test.go` (`mockAgentClient`) |
