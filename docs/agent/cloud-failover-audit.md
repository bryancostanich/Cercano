# Cloud Failover Path — Audit

**Principle under audit:** failover should be experience-preserving — the same
*kind* of work continues on a different vendor. A summarize call running on the
primary vendor's economy model should land on the backup vendor's economy
model, not the backup's premium default.

**Trigger incident (2026-07-15):** during a `/compact` run, Anthropic returned
`529 overloaded_error`; the fallback composite re-served the compaction
summarize call on the backup profile (`openai-responses`) — but rewrote the
model from `claude-haiku-4-5` (economy, tier-resolved) to `gpt-5.5` (the backup
profile's default). The call then failed on a parameter dialect difference
(`Unsupported parameter: temperature`), surfacing the whole chain.

## The path, mapped

```
                       ┌────────────────────────────────────────┐
                       │  providers.Rebuild (hostsvc/providers) │
                       │  primary = cloudfactory.Build(active)  │
                       │  prov    = wrapBackup(primary, cfg)    │──── fallback.Provider
                       └───────────────┬────────────────────────┘     (primary, backup,
                                       │                               backupModel = bp.Model)
              ┌────────────────────────┼─────────────────────────┐
              ▼                        ▼                          ▼
   SetCloudLLMProvider(prov)   NewLLMModelProvider(prov,   dispatch engine
   → tool loop StreamChat        prof.Model) → "CloudModel"  modelFor(isCloud, tier)
   (main chat; model =           slot (one-shot Process:     → ResolveCloudModelForTier
   active profile default)       compaction summarizer         (ACTIVE profile) → pinned
                                 fallback, escalation,          model id rides ToolLoopInput
                                 "use cloud")
```

Failover classification (`fallback.ShouldFailover`): 401/403/429/5xx and
unknown errors fail over; 400s do **not** (correct — a request the primary
rejects as invalid must be fixed, not re-served; parameter-dialect rejections
are handled per-adapter, see finding 5).

## Findings

### 1. Tier intent does not survive failover (the incident) — HIGH
`fallback.backupRequest` rewrites `req.Model` to the backup profile's default
model, unconditionally. Rewriting is *necessary* (model ids are vendor
namespaces; `claude-haiku-4-5` means nothing to OpenAI) — but the rewrite has
no idea what the pinned model *meant*. Every tier-resolved call degrades to
the backup's default on failover:

- compaction summarizer cloud fallback (fast_light_text → economy): economy →
  premium. The incident.
- dispatch sub-agent escalation: `modelFor(isCloud, tier)` resolves the ACTIVE
  vendor's model for the requested tier (`light`/`standard`/`deep`), pins it
  into `ToolLoopInput.Model` → same loss on failover.
- Main chat is the accidental exception: its model IS the active profile's
  default, so default→default preserves the experience.

The vendor cost table (`ModelProfiles.ResolveCloudModelForTier`) already knows
the answer for every configured vendor — the request just doesn't carry the
question.

### 2. Served-model attribution is wrong on failover — MEDIUM
`llmModelProvider.Process` reports `RoutingMetadata.ModelName` = the requested
model; the runner reports `Providers.MainModel(isCloud)`. On a failed-over
call, logs/telemetry claim the primary's model served the request when the
backup's did. Diagnosis of the incident required the composite's own failover
log line; per-request records lie. `llm.ChatResponse` carries no served-model
field for adapters to fill.

### 3. Capabilities report the primary only — LOW
`fallback.Provider.Capabilities()` returns the primary's capabilities. A
backup vendor with different support (caching, vision, tool limits) is
mis-planned against for the duration of a failed-over call. Low priority: the
current pairs (anthropic ↔ openai-responses) are close enough, and per-call
capability switching has its own costs. Document, don't fix yet.

### 4. Duplicate composite builders — LOW (hygiene)
`providers.wrapBackup` (live path) and `Server.wrapBackupLocked`
(`internal/server/cloud_backup.go`) are near-identical; the latter has **no
callers** — dead code that will drift from the live builder. Delete it.

### 5. Parameter dialects — RESOLVED
Handled per-adapter, where wire knowledge lives:
- `max_tokens`: floored at the anthropic adapter (never 0 on the wire);
  dropped on the ChatGPT codex route (rejects it).
- `temperature`: pointer-typed end-to-end (nil = provider default, &0 =
  greedy); models that reject the parameter outright (`deprecated` /
  `Unsupported parameter`) get one retry without it and are remembered
  per-client (anthropic + responses adapters).

## Design: tier intent travels with the request

**Rule:** a request carries its capability tier as metadata; whichever vendor
serves the call resolves the tier in its own namespace at serve time.

- `llm.ChatRequest` gains a `Tier` metadata field carrying the capability-tier
  name (wire adapters ignore it; it exists for the composite and any future
  router).
- `fallback.New` takes `backupModelFor func(tier string) string` instead of a
  bare `backupModel` string. The closure (built in `wrapBackup` and the
  worker's mirror, over the backup profile + `ModelProfiles`) resolves the
  backup vendor's model for the tier; empty tier or empty slot falls back to
  the backup profile's default — today's behavior.
- Setters of the tier:
  - `agent.Request` gains the field (like `Temperature`);
    `llmModelProvider.Process` maps it through. The compaction summarizer sets
    fast_light_text.
  - `ToolLoopInput` gains the field; dispatch normalizes the resolved tier
    onto `Spec.Tier` for both the agentic runner and the one-shot request (an
    explicit `ModelOverride` clears it — the caller pinned a model, not a
    tier). Main chat leaves it empty (default → default is already correct).
- Attribution: `llm.ChatResponse` gains `Model` (the model that actually
  served, from each adapter's non-streaming response envelope); the composite
  passes it through untouched, so failed-over one-shot calls report the
  backup's model. `llmModelProvider` prefers it when non-empty. Streaming
  attribution (the tool loop's engine badge) still reports the requested
  model — a follow-up if it bites.
- `wrapBackupLocked` deleted (dead twin of the live builder).

**Status: implemented** (all of the above; capabilities merging remains
deferred).

Out of scope (documented, deliberate): capabilities merging (finding 3);
failover for the local/open tier (locus owns that); retrying mid-stream after
content has flowed (correctly off the table in the composite).

### 6. Hidden retry layers below the composite starve it of errors — HIGH

**Trigger incident (2026-07-16):** a credits-exhausted Claude subscription
answered `429` with a quota-scale `Retry-After`. `anthropic-sdk-go` honors
that header **verbatim, uncapped** (`requestconfig.go: retryDelay`) and sleeps
*inside the request*, up to its default two retries — so the turn hung 8–11
minutes with no output, no error, and therefore no failover: the composite
only reacts to errors, and the error was being swallowed one layer below it.
The user killed the turn repeatedly (context-cancel: correctly not a failover
trigger) and finally flipped profiles by hand. Log fingerprint: turns with a
`serving on unix:…` line and nothing else; user asking "did you get stuck?".

Two structural problems, beyond the incident itself:

- **Retry policy existed at three layers** — SDK-internal (anthropic, hidden,
  unbounded), transport (`httpx.RetryTransport` on the OpenAI clients,
  bounded but invisible), and the composite (failover). Whichever fired first
  decided the user experience, and only one of the three was designed.
- **The classifier keyed off HTTP statuses** it dug out of vendor SDK error
  types, so it could not tell a quota-dead 429 (retry pointless) from a
  transient rate-limit 429 (retry sensible).

## Design: one resilience engine, normalized error classes, narrated actions

**Status: implemented (2026-07-16, feat/llm-resilience).**

- Every cloud adapter normalizes its wire errors into `llm.Error` with a
  provider-agnostic class: `quota`, `busy`, `auth`, `invalid_request`,
  `network`, `unknown`. Wire knowledge stays in the adapter (per finding 5):
  anthropic maps quota off a ≥30s `Retry-After` or the credit-balance 400;
  openai/responses map `insufficient_quota` / usage-limit phrasing; the
  vendor error stays reachable via `Unwrap`.
- `internal/inference/resilience` replaces `internal/llm/fallback` and owns
  the ENTIRE retry/failover policy: `busy` → one narrated same-provider retry
  (Retry-After capped at 2s, default 500ms), then failover; `quota`/`auth`/
  `network`/`unknown` → immediate failover; `invalid_request` → surface;
  context-cancel → surface. The engine wraps every cloud primary **even with
  no backup configured** (retry + narration still apply).
- All lower retry layers are gone: `option.WithMaxRetries(0)` on the
  anthropic SDK client; `httpx.RetryTransport` deleted from both OpenAI-side
  clients.
- Every engine decision is narrated: in-band `llm.EventNotice` on streams
  ("anthropic quota reached — switching to openai", "openai server busy —
  trying once more", "openai still busy — switching to anthropic"), forwarded
  by the tool loop to the client's progress channel and excluded from
  persisted history; the `OnEvent` hook logs the same decision server-side,
  including on non-streaming (background) calls.
- Regression pin: `internal/inference/resilience/quota_incident_test.go`
  replays the incident wire shape through the real anthropic adapter and
  asserts immediate narrated failover, exactly one primary request, and a
  wall-clock bound.

Still deliberately out of scope: capabilities merging (finding 3); a
circuit breaker that remembers a quota-dead primary across calls (today every
call knocks on the primary once and fails over in one round-trip).
