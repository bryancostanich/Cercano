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
