# Meridian / OpenCode-impersonation cloud route — diagnosis

**Status:** diagnosis captured 2026-07-06; cross-session incident root-caused
and Cercano-side fixes shipped 2026-07-07 (see below). Remaining defect
(within-conversation turn lag) needs the Meridian-side fix: fork + PR.
Long-term = native Cercano adapter, possibly re-implement Meridian.

## What this documents

When Cercano runs against a cloud profile whose `Route == "meridian"`, the
request path to the model is:

```
Cercano tool loop
  → Cercano anthropic client   (internal/llm/anthropic/client.go, route=meridian)
    → local Meridian OAuth bridge   (spawned via internal/meridian, SetupMeridian)
      → Meridian's OpenCode adapter
        → Anthropic API → the model
```

The pivot is in `anthropic/client.go`'s `headerRoundTripper`: on
`route == "meridian"` it sets

- `x-opencode-session: <session id>`
- `x-opencode-request: msg-<random hex>`   (fresh per HTTP request)
- `x-opencode-agent-mode: primary`

so the Meridian bridge routes the traffic through its **OpenCode adapter**.
This is a deliberate **borrowed identity**: Cercano claims to be OpenCode to get
a 4-turn SDK cap instead of the default 3-turn cap (the 3-turn cap would break
Cercano's ~10-round tool loop). It is flagged in-code as
`TODO(cercano-native-bridge-adapter)`.

## Why this matters

The OpenCode adapter is **not** a passthrough. It rewrites the request in
OpenCode's shape and keeps its own per-session state. Two user-visible symptoms
were traced to it.

### Symptom 1 — "tool results framed as Human" — NOT A BUG

Cercano's own serialization is correct end to end: tool results are
`llm.BlockToolResult` blocks carrying `ToolUseRef`, in a `RoleUser` message,
and every provider adapter (anthropic/ollama/openai/responses/bedrock) emits a
native `tool_result`/`tool`-role block. That is the correct Anthropic Messages
API convention — tool results *ride inside a user-role turn*.

The proxy log confirms the shape reaching the model is correct:

```
msgs=user[text] → assistant[text,tool_use] → user[tool_result] → ...
```

So "results look like the user said them" was a **misread of a correct
representation**, not a defect. Nothing to fix here.

### Symptom 2 — one-turn call/result lag — REAL

Observed: issue tool call X, receive the result of call X−1; consistently one
behind. Root cause is the OpenCode adapter's **session-state churn**, driven by
imperfect impersonation. Evidence from `~/.config/cercano/meridian.log`:

- `adapter=opencode` on every request.
- `deferred=28/34 tools (core: read,write,edit,bash,glob,grep)` — the adapter
  hides 28 of Cercano's 34 tools and reveals the rest **one per turn** via a
  `discovered=1 (<tool>) session_total=N` mechanism keyed to session state.
- `[PROXY] Stale session UUID, evicting and retrying as fresh session` —
  repeated frequently. The adapter keeps deciding the session is stale, evicts
  it, and re-drives the turn as a fresh session. This resets discovery state
  (the same tool, e.g. `checkpoint`, is re-`discovered` many times in a row).
- `TOKEN WARN: Input tokens grew 100% in one turn (1 -> 2). Possible context
  leak or full replay.` — the adapter's own replay heuristic firing.

Mechanism: Cercano mints a **fresh `x-opencode-request` id on every HTTP
request** and does not replicate OpenCode's real session/request lifecycle, so
Meridian's adapter cannot stably correlate turns. It evicts "stale" sessions and
retries/replays, and the deferred-tool discovery churns on each reset. That
turn-level desync surfaces to the model as the one-turn result lag.

### Collateral — 28/34 tools hidden

Independent of the lag, hiding 28 of 34 tools behind progressive discovery means
the model only sees 6 core tools (read/write/edit/bash/glob/grep) until the rest
are "discovered." Session eviction repeatedly resets that discovery, so tools
like `git_status`, `checkpoint`, `dispatch` are re-discovered over and over.

### Symptom 3 — tool results cross-delivered between concurrent sessions (2026-07-07) — ROOT-CAUSED, CERCANO SIDE FIXED

Observed across several concurrent dev threads: tool calls returning output
belonging to a *different* command — often another thread's; the same stale
file content replayed for many consecutive calls regardless of what was asked;
an assistant turn "rewritten" into a different command than the one issued
(another lineage's tool_use adopted and executed); ~50% intermittent; one-turn
result lag.

**How Meridian routes a request to a Claude SDK session** (read from the
shipped v1.45.0 bundle, `src/proxy/` region):

1. `x-opencode-session` header present → `sessionCache[header]`.
2. Header absent → `fingerprintCache[sha256(cwd + first 2000 chars of the
   FIRST user message)]` — a pure content key.
3. The matched entry is lineage-verified by message-hash prefix/suffix
   overlap. **A mismatch DELETES the cache entry** (`verifyLineage` →
   `cache.delete`) and evicts; a fuzzy match "allows resume" of the cached SDK
   session; the resume path can replay another request's unconsumed buffer
   (the 2026-07-04 bug below).
4. Sessions persist across Meridian restarts in
   **`~/.cache/meridian/sessions.json`** (`storeSharedSession` /
   `lookupSharedSession`), keyed the same way.
5. Escape hatch: a `requestSource` of `subagent-*` / `fork-*` skips lineage
   lookup entirely (`isIndependentSession`).

**The Cercano-side hole:** the session id was stamped only at the top of
`StreamProcess` (the conversation id). Everything else either inherited the
parent's id with a disjoint history — dispatch subagents, dispatch one-shots,
`context_edit` — which *evicted the parent conversation's lineage* on every
call, or went out headerless and collapsed onto the content-fingerprint key,
which collides across concurrent conversations in the same repo with
templated prompts. Live evidence: one adapter session's `msgCount` running
1187→1197 then dropping to 1170 and climbing again (two history views
alternating on one key); 114 replay-heuristic warnings in a single log
window.

**Shipped fixes (commit `204fb3cc`):**

- `internal/llm/session.go` — provider-neutral `WithSessionID` /
  `SessionIDFromContext`; the anthropic helper delegates to it.
- The anthropic adapter never sends a meridian-route request without a
  session id: unstamped calls get a random `anon-<hex>` id per request,
  closing the fingerprint fallback permanently.
- Dispatch one-shots override the inherited ctx with a fresh `oneshot-<hex>`
  id; agentic dispatch scopes the subagent loop to its sub-conversation id.

Companion CLI fix (commit `53654a8c`, not Meridian-specific): stream events
are fenced by turn generation, so a canceled turn's late error/close events
can no longer tear down the next prompt.

**Recovery after a corruption episode:** poisoned lineage survives Meridian
restarts via `sessions.json`. Full clean: kill Meridian, delete
`~/.cache/meridian/sessions.json` (safe — it is only the resume cache;
conversation history lives in Cercano's SQLite), restart. Continue affected
work in *fresh* conversations; resuming a corrupted conversation re-adopts
its tainted lineage key.

**Still open:** Symptom 2's within-conversation one-turn lag — driven by the
unstable `x-opencode-request` lifecycle — is untouched by the above and
still reproduces in long resumed threads. That is the Meridian-side work.

## Fix directions

- **Short term (forking + PR to Meridian):** either make Cercano's impersonation
  faithful enough that the adapter stops evicting sessions (stable
  `x-opencode-request`/session lifecycle), or fix the adapter's stale-session
  handling and disable deferred-tool discovery for this client. The PR should
  also cover the Symptom-3 sharp edges: `verifyLineage` must not *delete* a
  session entry on divergence (bypass it instead), and replay must be scoped
  strictly to the current request id. Also worth adopting Cercano-side:
  Meridian already honors a `requestSource` of `subagent-*`/`fork-*` to skip
  lineage matching — find the carrying header and stamp dispatched work with
  it as a second layer of isolation.
- **Long term:** a **native Cercano adapter** in Meridian — handle Cercano's
  real anthropic `tool_result` format directly, swap `x-opencode-*` for
  `x-cercano-*`, drop the impersonation (the `TODO(cercano-native-bridge-adapter)`
  path). Given the recurring friction, re-implementing Meridian is on the table.
- **Not viable alone:** switching `Route` to `direct` removes the OpenCode
  adapter (and the lag) but reinstates the 3-turn cap that breaks the tool loop.

## Code pointers

- `source/server/internal/llm/anthropic/client.go` — `route=meridian`,
  `headerRoundTripper`, the `x-opencode-*` headers, the `anon-<hex>` fallback
  for unstamped calls, `TODO(cercano-native-bridge-adapter)`.
- `source/server/internal/llm/session.go` — provider-neutral session-identity
  ctx key; every out-of-conversation call site (dispatch one-shot, agentic
  subagent) must stamp through it.
- `~/.cache/meridian/sessions.json` — Meridian's persistent session store
  (delete to purge poisoned lineage; conversation history is unaffected).
- `source/server/internal/server/agentic_dispatch.go` — `resolveGrantName`
  strips a leading `mcp__<server>__` prefix (recovers `mcp__oc__Read` → `Read`).
- `source/server/internal/meridian/` — the local Meridian proxy manager;
  `SetupMeridian` tees to `~/.config/cercano/meridian.log`.
- `source/server/internal/server/cloud_models.go` — `Route == "meridian"` handling.

## Related prior work

This route has already produced a filed-worthy corruption bug in the same
OpenCode adapter; the "lag" here is very likely another face of the same
session-churn/replay fragility, not an independent root cause.

- `docs/bugs/2026-07-04-meridian-resume-replay-report.md` — drafted upstream
  report for github.com/rynfar/meridian: on session resume (suffix-overlap
  match), the adapter replays a severed request's unconsumed buffer as
  `text_delta` events on the *next* request's stream, unfiltered by part role
  or request id.
- `docs/bugs/2026-07-04-user-message-tear.md` — full forensics of the incident
  the report is drawn from.
- Shipped Cercano-side defense: the `collectStream` framing guard in
  `internal/agent/toolloop.go` (accept content only between `message_start` and
  `message_stop`), with `internal/agent/toolloop_stream_guard_test.go`.

The `Stale session UUID, evicting and retrying as fresh session` churn and the
`deferred=…/discovered=…` tool cycling documented above are the same adapter's
session-state handling seen from the tool-loop side rather than the
resume-corruption side. A PR against Meridian should address both: (a) stable
session/request correlation so sessions stop being evicted as stale, and
(b) scoping any replay strictly to the current request id and assistant parts.
