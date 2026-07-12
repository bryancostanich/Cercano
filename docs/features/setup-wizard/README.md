# Setup Wizard

Launch-time interactive setup for Cercano: gets a new user from zero to a
working configuration in one guided pass. The organizing question is **how you
want to use Cercano** (the locus), and everything else follows from it — cloud
profiles when cloud is in the mix, a recommended set of open-weight models when
open is in the mix. Open models are served by the **bundled `llama-server`
runtime** and drawn from a **curated compatibility catalog** we author and
test; there is no Ollama install step and no Ollama catalog dependency.

## Triggers

- First run: no `~/.config/cercano/config.yaml` (or config present but no
  usable provider) → wizard offers to run before the first turn.
- Explicit: `cercano -s` / `cercano --setup` re-enters the wizard any time;
  re-running edits the existing config rather than starting blank.
- The legacy `cercano setup` Ollama flow (`runSetup` in
  `cmd/cercano/main.go`) is **retired** — `setup` enters this wizard. No path
  detects, installs, or pulls from Ollama during setup.

## Engine posture: llama-server is the open runtime

Cercano ships and supervises `llama-server`. It is the one open-weight runtime
the wizard configures, and open models are acquired as **GGUF downloads**
(direct HuggingFace `resolve/main/...gguf` URLs, downloaded into
`llama_server.model_dirs`). The wizard never checks for a running Ollama daemon
and never pulls from the Ollama library.

Pointing Cercano at an **external server** — an Ollama instance running
locally or remotely, or a hosted OpenAI-compatible endpoint (DeepInfra,
Mistral, Groq, …) — is a deliberate, post-setup configuration action on the
`/m` (models) surface, not a first-run branch. Keeping it out of setup is what
lets the wizard present a single, honest "llama-server is the local engine"
story.

## Model catalog & the compatibility gate

**The problem this closes.** llama-server (llama.cpp) is not a catalog — it
runs whatever GGUF you hand it, *if its architecture is compiled into the
build*. A GGUF's first four bytes being `GGUF` only proves it's a GGUF
container; the `general.architecture` metadata field inside names the model
architecture, and llama.cpp only loads architectures it supports. Ollama has
built its **own** inference engine, so its library now includes architectures
only Ollama can run (e.g. qwen3-next). "In the Ollama library" therefore means
"works in Ollama," **not** "works in llama-server" — the two have diverged, and
pulling an Ollama-library model into llama-server can fail with "unknown
architecture." The old `ollamacatalog`-as-discovery assumption is retired.

**Two layers, one gate:**

| Layer | Source | Guarantee |
|---|---|---|
| Recommended set (setup + tiers) | Curated list we author & test | Verified on our build |
| Browse / advanced (`/m`) | HuggingFace GGUF index + gate | Gate blocks incompatible |

- **Curated compatibility catalog.** A hand-maintained list of GGUFs we have
  verified load-and-run on Cercano's pinned llama.cpp build, one per open tier,
  with tool-calling flagged. This is the only source setup ever touches, so the
  guided path is structurally foolproof. It supersedes the two-entry
  `defaultCatalog` stub in `llamaserver/provider.go`.
- **HuggingFace browse.** For power users on `/m`, discovery switches from the
  Ollama library to the HuggingFace GGUF index (`hf.co/models?library=gguf`) —
  API-backed, the real home of GGUFs, no Ollama dependency. Because that index
  is large and noisy, browse filters to known-good uploaders
  (bartowski / unsloth / ggml-org) and sorts by popularity, *on top of* the
  gate.
- **The architecture gate** is the shared primitive: before committing a
  multi-gigabyte download, read `general.architecture` from the GGUF header via
  an HTTP Range request (the header holds the metadata KV block; no full
  download needed) and refuse/warn if our bundled llama.cpp doesn't support
  that architecture. The in-tree `headerIdentity` parser in `provider.go`
  already extracts architecture from on-disk files; it needs to also accept a
  remote Range reader. This gate is what would have caught qwen3-next *before*
  the failed pull.

This whole subsystem is a re-architecture of the model-catalog track; the
`ollamacatalog` package and `docs/features/cli/model-catalog-online/design.md`
need a follow-up rewrite to match (Ollama-library discovery → HuggingFace +
gate). This doc records the decision; that track carries the implementation.

## Steps

The step machine is **locus-first**. `wizard.State.Step` sequences:

```
StepLocus → StepCloud (if locus uses cloud) → StepOpen (if locus uses open) → StepDone
```

Both middle steps are conditional on the locus answer. Cloud-only skips
`StepOpen`; open-only skips `StepCloud`; the two "primary" modes visit both.
(This replaces the old `StepPrimary → StepCloud → StepLocus → StepTiers`
ordering, where an "open or cloud?" question preceded locus — locus now
subsumes that question.)

### 1. Locus — how you want to use Cercano

First and organizing question: **"How should Cercano run your work?"** Maps
directly onto the four `locus.Mode` values, presented with a recommendation
framing rather than as neutral peers:

| Choice | Mode | Framing |
|---|---|---|
| Cloud primary, open co-processor | `cloud_primary` | **Recommended** — highest-quality, frontier experience |
| Open only | `open_only` | **Recommended** — fast, fully private, local |
| Open primary, cloud fallback | `open_primary` | Cost saver |
| Cloud only | `cloud_only` | Not recommended — skips the point of Cercano |

The two recommended options lead. `open_primary` is offered as the cost-saver
middle path. `cloud_only` is selectable but explicitly discouraged: it never
engages the local co-processor, so it forgoes the privacy, latency, and cost
benefits Cercano exists to provide. The screen states that this is easy to
change later (via `/m` or the config file), so nobody agonizes.

The answer sets `LocusMode` and determines which of the next two steps run.

### 2. Cloud profiles — one or more (only when locus uses cloud)

Runs for `cloud_only` and `cloud_primary`. The user configures **one or more**
cloud profiles:

- **One** profile is enough to finish.
- **Multiple** profiles are supported: one is the active primary, others are
  available as a backup/fallback and as targets the user can switch to later
  from `/m`. Each added profile is a full `UpsertCloudProfile` commit.

Provider list with per-provider auth methods (order preserved from prior
design; the auth mechanics are unchanged by the reordering):

| Provider | Auth methods (in offered order) |
|---|---|
| Anthropic | Meridian proxy (subscription) · API key |
| OpenAI | ChatGPT sign-in (unofficial) · API key |
| GitHub Copilot | Device-code sign-in (subscription) |
| Google Gemini | AI Studio API key (free tier) |
| Mistral / Groq / xAI | API key |
| OpenAI-compatible endpoint | Base URL + API key |

Auth flow details:

- **Meridian proxy (Anthropic):** existing flow, unchanged. The commit sends
  `base_url` (Meridian's default `http://127.0.0.1:3456`) and `route: meridian`;
  the server only activates a keyless profile when a proxy base URL says who
  handles auth.
- **ChatGPT sign-in (OpenAI):** authorization-code + PKCE against
  `auth.openai.com`, localhost callback on port 1455; headless fallback via
  the device-code variant (`/api/accounts/deviceauth/*`). Requests route to
  the ChatGPT Codex backend with the `ChatGPT-Account-Id` header.
  **Labeling requirement:** the entry reads "ChatGPT Plus/Pro sign-in —
  unofficial; uses the Codex CLI's client and may stop working. Model list is
  restricted." API-key entry is one keystroke away; a runtime failure offers
  the API-key switch directly.
- **GitHub Copilot:** standard device-code flow (`github.com/login/device/code`
  → `login/oauth/access_token` → `api.githubcopilot.com`). Model catalog
  fetched live; serves OpenAI, Anthropic, and Google models under one
  subscription. Enterprise domain supported (`copilot-api.<domain>`).
- **API keys:** masked prompt, validated with a cheap live call before saving.

Each profile is seeded with the provider's everyday-tier recommended model —
the profile's model is what serves main-chat requests, so a wizard-created
profile is **never activated modelless**. `UpsertCloudProfile` already enforces
this at the RPC (flavor required; `base_url` required for `chat_completions`),
which is why the empty-`flavor` / modelless-profile warnings cannot arise from
a wizard-built config: the wizard only ever writes complete, valid profiles.
The legacy stray-`GEMINI_API_KEY` capture is **not** carried into this flow —
setup does not opportunistically mint cloud profiles from ambient environment
variables.

### 3. Open model set — recommended, tier-mapped (only when locus uses open)

Runs for `cloud_primary`, `open_primary`, and `open_only`. Instead of picking
one "primary" open model, the wizard presents a **recommended set** of GGUFs,
one per open capability tier, drawn from the **curated compatibility catalog**
(above) — every entry verified on our llama.cpp build.

The set is **sized to the machine**: the server picks it with
`CuratedCatalog.ProfileForRAM(totalRAM)` (profiles keyed by RAM threshold —
24 / 48 / 96 / 128 GB; a machine below the smallest threshold still gets the
24 GB profile) and returns it over `ListRuntimeModels` as
`recommended_open_models` (tier → the stable inventory id
`llama_server:catalog:<id>`). The wizard autofills the `.Open` slots of the
four capability tiers from it:

| Tier | Role | 24 GB | 128 GB |
|---|---|---|---|
| `most_capable` | Hardest agentic work | Qwen3-14B | GLM-4.5-Air |
| `everyday` | Main chat / workhorse | Qwen3-14B | Qwen3-30B-A3B Instruct |
| `fast_light` | Small background helpers | Phi-4-mini | Phi-4-mini |
| `fast_light_text` | Prose judgment (recaps, verdicts) | Phi-4-mini | Phi-4-mini |

Picks are stored as the model's **display name** (the open-slot convention;
taxonomy resolution and the finish-time download resolve it back to the
catalog id). The `embedding` tier lives in the catalog profile but is not part
of the wizard's autofilled capability set.

- The default set is shown as an accept-and-move-on list. Accepting fills the
  **`.Open` slots** of `Models.Tiers.{most_capable,everyday,fast_light,fast_light_text}`.
- The set is **editable**: each row opens the same model picker `/m` uses. In
  setup the picker offers the curated catalog; the broader HuggingFace browse
  (with the architecture gate) is available on `/m` afterward, not inline.
- Populating `fast_light_text.Open` here is also what removes the compaction
  summarizer warning at its source — the tier has a real model instead of
  falling through to an empty slot.

**No download UI in the wizard.** Selecting the set does not block on transfer.
The curated catalog is populated (six GGUFs across the 24 / 48 / 96 / 128 GB
profiles in `llamaserver/catalog.json`), superseding the old two-entry
`defaultCatalog` stub; every entry passes the architecture gate.

### 4. Finish — background downloads, `/m` progress, cloud covers the gap

On finish the wizard:

1. Writes locus + cloud profiles + open tier picks through `UpdateConfig` /
   `UpsertCloudProfile` / `ApplyModelTierPatch`.
2. **Kicks off the open-model downloads in the background.** `applyConfig`'s
   `enrollOpenDownloads` resolves each distinct open pick's display name back to
   its catalog id and fires `DownloadRuntimeModel` (states
   `not_downloaded → downloading → downloaded`), skipping any already on disk.
   Best-effort and non-fatal — a failure surfaces in the status line but never
   blocks finish. It does not wait.
3. Prints a closing message: *"Your open models are downloading in the
   background. View progress any time with `/m`. Until they finish, Cercano
   uses your cloud model so you can start working now."*

This closing promise is only true if routing actually falls back to cloud
while a GGUF is still downloading — see the routing contract below.

## Completion gate

Setup completes **only when the chosen locus's required path resolves**:

- Locus uses cloud (`cloud_only` / `cloud_primary`) → at least one **complete,
  valid, active** cloud profile exists.
- Locus uses open (`open_primary` / `open_only`) → the open tier slots are
  populated (downloads may still be in flight; a *selected* set satisfies the
  gate, since cloud or a prior local model covers until they land — except
  `open_only`, which has no cloud cover and must warn that work waits on the
  first download).

If the required path does not resolve, the wizard does not report success. It
ends with an explicit **"SETUP INCOMPLETE — here's what's missing"** screen and
the `cercano setup` process exits non-zero. This replaces the legacy flow's
unconditional "[8/8] Setup complete!" — which printed success even when no
usable model was configured.

## Routing contract: cloud covers the gap

The finish-message promise — *cloud covers the gap while open models
download* — is backed by `dispatch.OpenModelReady`, checked at open-provider
registration (`hostsvc/providers/providers.go`, `worker/worker.go`). It gates on
the configured open model's GGUF being present on disk: while that file is still
downloading (or missing) `OpenModelReady` returns false, the open provider
registers **absent** (`nil` / `"NONE"`), and `dispatch/select.go`'s `Select`
crosses to the cloud tier — no special download-state logic in `Select` itself.
The gate is coarse (the configured open model, not per-tier), realizing
readiness as *on disk*:

> **An open tier's provider registers as present only when its GGUF is actually
> on disk** (`Discover` reports it `downloaded`). While the file is still
> downloading, the tier registers **absent**, so `Select` crosses to cloud with
> no new fallback logic — and the moment the file lands, the provider flips to
> present and the next request uses it.

The one thing that would break this is a provider that is always constructed and
only errors at call time; a not-yet-downloaded model must register **absent**,
not present-but-failing. Under `open_only` there is no cloud tier to cross to,
which is why the completion gate warns that work waits on the first download
rather than promising cover.

## Non-goals (V1)

- No Ollama detection, install, or pull anywhere in setup. External
  servers (local/remote Ollama, hosted OpenAI-compatible endpoints) are a
  post-setup `/m` configuration action, not a wizard branch.
- No in-wizard download progress screen — progress lives on `/m`.
- No enterprise SSO / Vertex AI / Azure Entra flows — API-key style entries
  only beyond the three sign-in flows above.
- No credential migration from other tools' keychains, and no opportunistic
  capture of ambient API-key environment variables.
- Wizard does not manage MCP servers or permissions; those keep their own
  config surfaces.

## Decisions

### 2026-07-11 — RAM-tiered open recommendations + enroll-on-finish (implemented)

Wires the previously planned open-model path end to end:

- **Open recommendations are RAM-tiered and curated.** The dead-but-tested
  `CuratedCatalog.ProfileForRAM` is now wired through a new
  `llamaserver.RecommendedOpenModels(totalRAM)` helper and surfaced on
  `ListRuntimeModels` as `recommended_open_models` (tier → stable inventory id).
  The wizard autofills its open tier slots from it, so every open recommendation
  is a gate-verified model that fits the machine — closing the gap where the
  shipped recs recommended the gate-incompatible `qwen3-coder-next`.
- **Downloads actually enroll on finish.** `applyConfig` calls
  `enrollOpenDownloads`, firing `DownloadRuntimeModel` per distinct open pick
  (deduped, skipping models already on disk, non-fatal) — the finish message's
  "downloads run in the background" promise is now backed by code.
- **Catalog populated.** `llamaserver/catalog.json` carries six curated GGUFs
  across 24 / 48 / 96 / 128 GB profiles; the two-entry `defaultCatalog` stub is
  gone.
- **Readiness = on-disk** (`dispatch.OpenModelReady`) is in place, so routing
  treats a still-downloading open model as absent and crosses to cloud.

### 2026-07-10 — locus-first rewrite

- **Locus is the organizing first question.** The old "open or cloud?"
  `StepPrimary` is removed; the four locus modes carry the whole "how do you
  want to use Cercano" decision, with `cloud_primary` and `open_only`
  recommended, `open_primary` as cost-saver, `cloud_only` discouraged.
- **Catalog: curated set + HuggingFace browse + architecture gate (Option C).**
  Open models come from a curated compatibility catalog we author and test —
  the only source setup touches. `/m` browse switches from the Ollama library
  to the HuggingFace GGUF index, filtered to known-good uploaders. Every
  download passes an architecture gate: read `general.architecture` from the
  GGUF header via a Range request and refuse if our llama.cpp build can't load
  it. Ollama is dropped as a catalog source entirely (still allowed as a
  post-setup external server). The `ollamacatalog` package and the
  `model-catalog-online` design need a follow-up rewrite to match.
- **Cloud covers the download gap via readiness = on-disk.** An open tier
  registers as present only when its GGUF is downloaded; while downloading it
  reads absent, so routing crosses to cloud automatically with no new fallback
  logic. `open_only` has no cover and the completion gate says so.
- **No download screen.** Open-model downloads start in the background at
  finish; `/m` shows progress; the closing message tells the user cloud covers
  the gap until they land.
- **Tier-mapped set, not a single pick.** The open step fills the `.Open`
  slots of the everyday / fast_light / fast_light_text / embedding tiers from a
  recommended, editable set. This also removes the `fast_light_text` compaction
  warning at its source.
- **Cloud step supports multiple profiles** — one required, additional ones as
  backup/switchable.
- **Hard completion gate.** Success is reported only when the locus's required
  path resolves; otherwise an explicit incomplete screen and non-zero exit.
- **Legacy `runSetup` retired**; `cercano setup` enters this wizard.
- **Catalog expansion is in scope.** Per-tier GGUF entries must be added to the
  curated compatibility catalog (superseding the two-entry `defaultCatalog`
  stub) for the recommended set to be real.

### 2026-07-05 — auth & OAuth (carried forward)

- OAuth sign-in flows (ChatGPT, Copilot device flow) run **agent-side**: the
  agent owns the OAuth dance (callback server / device polling), token storage
  (same place API keys live), and request-time refresh. The CLI renders the
  verification URL + user code and waits on flow status via RPC. Tokens never
  cross the gRPC boundary outbound; every client gets the flows for free;
  refresh works with no client attached. Profiles select the adapter via the
  Route field ("direct" | "meridian" | "chatgpt" | …).
- ChatGPT sign-in ships in the default provider list with the "unofficial"
  label; no config flag gate.
- Tier autofill for cloud providers comes from the shipped recommendations
  file; Copilot additionally filters against its live catalog.
- Quitting mid-wizard resumes where the user left off (wizard state persisted
  alongside config; a completed run clears it).

### 2026-07-05 (later) — abandon & rollback (carried forward)

- **Abandon trapdoor.** `q` (pressed twice — the first press asks for
  confirmation in the status line) abandons the run without keeping anything:
  eager commits are rolled back and the resume file is cleared. `esc` keeps its
  meaning (back / pause-and-resume-later).
- **Baseline snapshot powers the rollback.** A fresh run snapshots the cloud
  profiles + active profile into the wizard state file before any eager
  commits. Abandon restores it: created profiles are removed (their keychain
  keys with them), modified profiles are restored, the previously active
  profile is re-activated. Known limit: a key overwritten on a pre-existing
  profile can't be restored — keys are write-only.
- **Meridian profiles carry the proxy base URL** (`base_url` +
  `route: meridian`); an empty route on update preserves the existing profile's
  route so route-unaware clients can't demote meridian to direct.
