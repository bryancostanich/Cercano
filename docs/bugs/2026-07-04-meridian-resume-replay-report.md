# Upstream report draft — Meridian: session resume can replay stale/non-assistant stream content as response deltas

For filing against github.com/rynfar/meridian (OpenCode adapter path).
Companion to `2026-07-04-user-message-tear.md` (full forensics).

## Summary

After a Meridian restart coinciding with a client (Cercano agent) restart, the
first request on a resumed session lineage received, at the head of its
response stream, ~922 bytes of a **previous, severed request's user-message
content** as `text_delta` events — starting at a mid-word byte offset matching
where the severed stream had stopped being consumed. The client accumulated
these deltas as assistant output and persisted them, attributing
user-composed text to the assistant.

## Environment

- Meridian on 127.0.0.1:3456, adapter=opencode, model=opus, streaming, Claude
  Max OAuth.
- Client: Cercano agent, one `x-opencode-session` per conversation id.
- 2026-07-04 ~00:18 UTC: Cercano agent killed/relaunched by its dev launcher;
  Meridian also restarted in the same window (its log: "Cache hit rate 4% on
  resume — normal after proxy restart").

## Observed sequence

1. A large multi-line user message (bare-CR line endings, ~957 bytes) was
   submitted as a streaming request just as the client process was killed; the
   stream was severed mid-flight inside the session at byte offset
   `…should get tabs.\r\rmodel|` (mid-word in "model.go").
2. Client restarted, reconnected; the user re-sent a short 35-byte message on
   the same conversation/session lineage.
3. Meridian's log for this window shows `Stale session UUID, evicting and
   retrying as fresh session` and suffix-overlap resume logic active
   (`Compaction detected (key=…): suffix overlap N/M. Allowing resume.`) —
   including, later, on this exact conversation key (`bc2a46f4…`).
4. The response stream for the short re-sent message contained, before/among
   the genuine assistant content (text + `tool_use` blocks with `toolu_…`
   IDs), `text_delta` events carrying the severed message's **tail from the
   exact mid-word offset** — byte-identical to the user's composed text,
   typos included, which the model cannot have regenerated.

## Suspected mechanism

On resume of a matched session lineage, the adapter replays unconsumed
buffered session events onto the new request's response stream, without
filtering by (a) part role — user-message echo/parts are replayed as if they
were assistant deltas — or (b) request identity — content belonging to the
severed request is emitted under the new request's message framing.

## Ask

- Scope resume replay strictly to content generated for the **current**
  request id; drop (or re-frame with explicit message identity) anything
  buffered from a prior severed request.
- Never emit non-assistant session parts as response `content_block_delta`s.
- Consider an explicit `message_start`-bounded replay contract so clients can
  detect and discard stale prefixes (Cercano now enforces this defensively:
  content outside `message_start`/`message_stop` framing is dropped).

## Client-side impact (for severity)

The replayed bytes were persisted as an assistant turn and re-entered the
model's context on subsequent turns as (mis-attributed) instructions — i.e.,
session-resume replay can make an agent execute text the user never sent.
