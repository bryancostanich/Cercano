# Locus Mode

**Status:** Design approved 2026-06-23. Not yet implemented.

Locus Mode is a single setting that controls how much of Cercano's work runs on
the cloud versus on local models. It is the **single source of truth** for the
local-vs-cloud decision — replacing the prototype embedding-based SmartRouter,
which is shelved (see Engineering design).

---

## Overview

Cercano does two kinds of work:

- **Main LLM** — the agent's primary reasoning and generation: the native
  tool-calling loop that drives the conversation, writes code, and runs tools.
- **Co-processor tasks** — the cheap, offloadable work: summarize, extract,
  classify, explain, document, research.

Locus Mode sets which tier — cloud or local — serves each kind of work. It
applies in **both** deployment contexts: the standalone Cercano agent/CLI, and
the MCP co-processor tools called by a host agent (e.g. Claude Code).

## The four modes

| Mode | Main LLM | Co-processor |
|------|----------|--------------|
| Cloud Only | cloud | cloud |
| Cloud Primary | cloud | local |
| Local Primary | local | local |
| Local Only | local | local |

- **Cloud Only** — everything runs on the configured cloud provider, including
  the co-processor tasks. For users who want maximum quality, or who have no
  local GPU.
- **Cloud Primary** — frontier brain, local grunt work. The main reasoning runs
  on cloud, but summarize/extract/classify/explain run locally to keep cloud
  cost and latency down. The "best of both" mode.
- **Local Primary** — local-first. The main LLM runs locally; cloud is reached
  only as a fallback (see below). Co-processor work stays local. **This is the
  default.**
- **Local Only** — nothing ever leaves the machine. Local for both tiers, no
  cloud under any circumstance.

## Fallback & visibility

| Mode kind | When preferred tier can't serve |
|-----------|---------------------------------|
| Primary (Cloud/Local) | Fall back to the other tier |
| Only (Cloud/Local) | Hard fail with a clear error |

- **Primary modes fall back bidirectionally.** Cloud Primary drops to local if
  the cloud is down or unconfigured; Local Primary reaches cloud as needed
  (e.g. the local model errors or can't complete the task).
- **Only modes never cross tiers.** If the required tier can't serve, the
  request fails with a clear message — e.g. "Local Only: Ollama unreachable" or
  "Cloud Only: no cloud provider configured" — rather than silently switching.

**No silent degradation — ever.** Any time the tier actually serving a request
is not the mode's preferred tier (a Primary mode falling back, or a fallback
endpoint serving), Cercano surfaces it: a scrollback notice in the CLI plus the
existing `RouteSelected` engine badge and response routing metadata reflect the
tier that actually ran.

## Setting Locus Mode

- **Config file** — `locus_mode` in `~/.config/cercano/config.yaml`. Enum:
  `cloud_only` | `cloud_primary` | `local_primary` | `local_only`. Default
  `local_primary`.
- **Runtime (no restart)** — `cercano_config(action: "set", locus_mode: "…")`,
  persisted to disk like model/endpoint changes today.
- **CLI** — a `/locus` slash command to view and switch the mode.
- **VS Code** — `cercano.locusMode` setting.

Switching mid-conversation applies to the next turn.

---

## Engineering design

### locus.Policy — the single source of truth

A new unit, `locus.Policy` (package `internal/locus`), is the only thing that
decides local vs cloud. It is constructed from the current mode plus handles to
the available providers, and exposes resolution for each tier:

- `ResolveMain() → (provider, fallback, crossAllowed)`
- `ResolveCoproc() → (provider, fallback, crossAllowed)`

`crossAllowed` is false for the `*Only` modes (callers must hard-fail instead of
using `fallback`). The policy is held centrally (on the server / agent wiring)
and is updated atomically when the mode changes at runtime.

### Shelving the SmartRouter

`SmartRouter` / `LazyRouter` `SelectProvider` and `ClassifyIntent` are **unwired**
from the request path. The code stays in `internal/agent/` (it's a useful
prototype that may return later — e.g. as an auto mode) but nothing calls it for
provider selection. This realizes the repo's existing "Simplify Provider
Routing" TODO. Locus Mode is now the sole local-vs-cloud authority.

### Main-LLM tier

The native tool-loop (`streamProcessRequestWithToolLoop` in
`internal/server/server.go`) currently pins its provider to `s.cloudLLMProvider`
(Anthropic). It changes to take the provider from `locus.ResolveMain()`:

- The server gains `SetLocalLLMProvider(p llm.Provider)` alongside the existing
  `SetCloudLLMProvider`. The local tool-capable provider already exists —
  `internal/llm/ollama.Client` implements `llm.Provider` with
  `Capabilities().SupportsTools == true` and `StreamChat` — so this is wiring,
  not new tool-calling work.
- On a Primary-mode fallback, the loop re-resolves to the fallback provider and
  emits an updated `RouteSelected` frame + scrollback notice.
- On an Only-mode unavailability, it returns a clear error without crossing.

The legacy `ProcessRequest`/coordinator path (the pre-tool-loop flow) also routes
its provider through `locus.Policy` rather than the SmartRouter, so behavior is
consistent if that path is still reachable.

### Co-processor tier

The co-processor handlers — the `cercano_summarize/extract/classify/explain/
document/research` MCP tools and any in-agent use — select their model via
`locus.ResolveCoproc()`. Local for every mode except Cloud Only, where they run
on the configured cloud model. Same fallback/visibility rules apply.

### Edge cases

- **Cloud Only / Cloud Primary, no cloud configured** — Only errors; Primary
  runs local with a loud notice.
- **Local Only / Local Primary, Ollama down** — Only errors; Primary reaches
  cloud (if configured) with a notice.
- **V1 cloud provider** — the native tool-loop cloud side is Anthropic-only
  today; Cloud modes require an Anthropic cloud config until other providers are
  wired into the tool-loop.
- **Mode change mid-conversation** — applies to the next turn; in-flight turns
  finish on their current tier.

### Testing

- Policy resolution table: mode × tier × availability → expected provider and
  whether crossing is allowed.
- Primary fallback emits a notice and updates `RouteSelected`.
- Only modes hard-fail with the right message when their tier is unavailable.
- Co-processor tier picks the correct provider per mode.
- Config round-trips (`locus_mode` load/save) and the runtime switch takes effect
  on the next turn.

## Open items

- Confirm the `/locus` CLI affordance shape (slash command vs config-editor row)
  during implementation.
- Non-Anthropic cloud providers in the native tool-loop are out of scope for V1;
  Cloud modes assume Anthropic until then.
