# Bug brief: user message torn mid-stream; tail persisted as an assistant turn

**Status:** ROOT-CAUSED + defended 2026-07-05 — see **Resolution** at the bottom.
Cercano-side guard shipped in `internal/agent/toolloop.go` (collectStream framing
guard); upstream Meridian report drafted in
`2026-07-04-meridian-resume-replay-report.md`. Observed in the wild 2026-07-04
~00:18 UTC (2026-07-03 evening local) in conversation `bc2a46f4c1642455`
("CERCANO - AGENT/UX").
**Severity:** critical correctness. The agent executed instructions the user never
sent, and the conversation store attributes the user's words to the assistant.

## Symptom

A multi-line user message composed in the CLI (bare-CR line endings, `0x0D` only)
was torn apart on its way through the stack:

1. Only the **head** (first line) arrived as the request (`req.Input`).
2. The **tail** (922 bytes) was persisted 17 s later as the **assistant's**
   response turn — plain `content`, not `content_json`.
3. The **real** assistant turn is missing from the store entirely, even though its
   two tool calls (`get_protocol`, `Bash` ls) demonstrably executed — their results
   are persisted.
4. The **model-bound context diverged from the record**: the provider request
   carried the FULL message as user text (the model acted on the tail as an
   instruction), while the server log and the persisted user turn contain the head
   only.

## Hard evidence

All in `~/.config/cercano/conversations.db` (WAL mode — query in place), conversation
`bc2a46f4c1642455`. Timestamps below are UTC (`datetime(created_at,'unixepoch')`).

### User turn `2ecbb5c1ce69a8e69bfdcc01` — 00:18:31, 35 bytes

```
* settings/config should get tabs<0x0D>.
```

Note the period is AFTER the carriage return. The original sentence was
"...should get tabs." — the byte got **reordered** across the CR at the tear.

### Phantom assistant turn `3127ee8868271eb4e4382b2a` — 00:18:48, 922 bytes

Plain `content` column (no tool_use blocks). Verbatim (blank lines were bare CRs
in the original; reproduced here as line breaks):

```
.go, the settings UI, whatever else it's called is getting long. maybe they shoudl be split out into their own UI page files and maybe controls shoudl be split out into their own files or a file called controls if they're small. i actually don't know what the file layout is, but 123KB for a UI file is huge. we shoudl explore breaking it apart. also, i wonder if there are other cleanup wins in this stuff, e.g. is there a bunch of legacy stuff hanging around from the various movements and implementations?

let's brainstorm on this hold on, i wonder if this should actually be tabs in the main UI, hold on. before you achitect this let's chat about the tabs

before we brainstorm, can you clean and inventory first, that's probably step 1 regardless, and i wonder how big the cleanup wins are. maybe do some cleaning first if it's low hanging fruit, then inventory and give me the lay of the land, then we can chat tabs
```

This is the tail of the same composed message as the user turn. Stitched together
they read "...should get tabs." / "model.go, the settings UI..." — the tear is
**mid-word inside "model.go"**, and the word `model` is missing entirely at the
seam. So the seam both **lost** bytes (`model`) and **reordered** bytes (the
period). This is chunk-boundary/framing corruption, not a clean line split.

### Tool-result user turn `5cb7786977122e8901fb69de` — 00:18:48, 19,335 bytes

`content_json` carrying `tool_result` blocks for `get_protocol` (design-decisions)
and `Bash` (`ls internal/ui && wc -c`). Proves the model's real first response was
text + two `tool_use` blocks — and **no assistant turn carrying those blocks
exists in the store**. The phantom sits in its place.

### Server log (`$TMPDIR/cercano-server.log`)

```
Received request (Stream): * settings/config should get tabs.
```

`internal/server/server.go:2023` prints raw `req.Input` via `%s`, so the server
genuinely received head-only as the request input.

### Concurrent anomalies in the same window

- `[tool-loop] EnsureConversation(bc2a46f4c1642455) failed: context canceled` on a
  subsequent user message (it was dropped and auto re-sent — appears twice in the
  log).
- A background compaction pass on a DIFFERENT live conversation
  (`80109e871fba4e18`, ~305k tokens) did a hard-override truncation of 304
  messages, then FAILED after 2 minutes with
  `Post "http://localhost:11434/api/generate": context deadline exceeded`.
  Possible load/concurrency factor — or a red herring. Isolate it.

## The three anomalies a root cause must explain

1. **Seam corruption** — mid-word split with lost + reordered bytes at a chunk
   boundary.
2. **Role misattribution** — the tail persisted as assistant content; the real
   assistant turn (with tool_use blocks) never persisted.
3. **Context/persistence divergence** — the provider request contained the full
   message as user text; the server log and store contain head-only.

A fix that explains only one of these is not the root cause.

## Suspects (verify with probes — do not assume)

- **CLI input path** — `source/clients/cli/internal/ui/prompt_input.go`:
  `InsertString` (:146), `tea.PasteMsg` handling (:259). How do bare-CR line
  endings end up stored in the textarea and in the submitted string?
- **gRPC stream framing** — the `StreamProcessRequest` path between CLI and
  server; anything that scans, splits, or re-frames the input string.
- **Server request assembly + persistence** — `internal/server/server.go` stream
  handler (log line at :2023); the assistant-turn persist path (see
  `internal/server/toolloop_persist_test.go`, `internal/agent/conversation.go`,
  grep `AppendTurn`/`persistTurn`). How could inbound user bytes land in the
  assistant-content slot?
- **Concurrency** — the compaction pass and the `EnsureConversation` cancellation
  racing turn persistence on the same store.

## Protocol

Follow the systematic-debugging protocol (`get_protocol systematic-debugging`;
same shape as the RTL debug loop): STRIP DOWN → OBSERVE → REASON → PREDICT →
PROBE → REFERENCE → FIX.

- **Reproduce FIRST.** Build a minimal scripted client that submits a ~957-byte
  multi-line message with bare-CR line endings (blank lines = `\r\r`) through
  (a) the CLI submit path and (b) the gRPC stream directly. Observe what
  `req.Input` arrives as and exactly what rows get persisted.
- Vary one factor at a time: with/without concurrent compaction load,
  with/without CR-only endings (try LF and CRLF controls), short vs long bodies.
- **Do not fix on reasoning alone.** Write the failing probe that shows the tear
  before touching any code; the probe becomes the regression test.

## Session hygiene

Until fixed, treat multi-line/pasted messages in live sessions on the affected
build as at-risk. The session that exhibited this was left running; its
transcript (both in the store and as replayed context) is corrupted and should
not be trusted as evidence beyond the rows quoted above.

## Resolution (2026-07-05)

Root cause: a four-part failure chain, not one bug.

1. The full message was submitted just as the dev launcher killed the agent
   (user's own report at 00:18:24 in a parallel session: "agent got rstart").
   No agent process logged or persisted it; it survived only in the CLI's
   `lastSubmittedPrompt`.
2. Meridian restarted in the same seconds (its log: "normal after proxy
   restart"). The in-flight turn was severed inside Meridian's OpenCode session
   at the byte offset `…tabs.\r\rmodel|`.
3. The CLI's reconnect rehydration put the full prompt back in the input box;
   the user trimmed it to the headline and re-sent — that's the 35-byte head
   with the stray `\r.` (human edit of a CR-laden buffer, not a framing bug).
4. On the next request, Meridian's suffix-overlap session resume ("Allowing
   resume", demonstrably active on this conversation) replayed the severed
   session's unconsumed remainder — the tail, from the mid-word offset — as
   `text_delta` events on the response stream. `collectStream` accumulated them
   into the same assistant message as the genuine tool_use blocks (the fused
   message is turn `3127ee8868271eb4e4382b2a`: `[text: tail, tool_use ×2]`),
   `persistTurn` wrote it, and the poisoned history propagated into every later
   context. Claude could not have produced the 922 bytes byte-identically
   (typos included); provenance is mechanical replay. Link 4's transport leg is
   inferred (both processes restarted; no timestamped proxy log survives) —
   every other link is directly evidenced.

Corrections to this brief's earlier claims: the "real assistant turn" is NOT
missing — it exists fused with the phantom text; and the seam is two mundane
causes (severed-stream consumption offset + human edit), not byte reordering in
a framing layer. Anomaly 3 dissolves: the model answered the head only; the
tail entered later contexts via the poisoned history.

Fixes shipped (Cercano side):

- **collectStream framing guard** (`internal/agent/toolloop.go`): content
  events are accepted only between `message_start` and `message_stop`;
  pre-start / post-stop deltas are dropped with a loud `[stream-guard]` log and
  never reach display or persistence; a second `message_start` discards the
  prior partial message. Regression tests:
  `internal/agent/toolloop_stream_guard_test.go` (the incident's exact shape).
- **CR normalization at CLI submit** (`model.go submit()`): CR/CRLF → LF.
- **Persistence failure is user-visible**: `EnsureConversation` failure now
  streams a ⚠ progress warning instead of only a server-side stderr line.
- Reconnect rehydration notice already existed (system entry + crash summary).

Upstream (Meridian): resume replay must never emit non-assistant / stale parts
as response deltas — report drafted in
`2026-07-04-meridian-resume-replay-report.md` for filing against
github.com/rynfar/meridian.
