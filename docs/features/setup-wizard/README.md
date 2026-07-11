# Setup Wizard

Launch-time interactive setup for Cercano: gets a new user from zero to a
working configuration in one guided pass. The organizing question is **how you
want to use Cercano** (the locus), and everything else follows from it — cloud
profiles when cloud is in the mix, a recommended set of open-weight models when
open is in the mix. Open models are served by the **bundled `llama-server`
runtime**; there is no Ollama install step.

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
the wizard configures, and open models are acquired as **GGUF downloads from
the llama-server catalog** (direct HuggingFace `resolve/main/...gguf` URLs,
downloaded into `llama_server.model_dirs`). The wizard never checks for a
running Ollama daemon and never pulls from the Ollama library.

Pointing Cercano at an **external server** — an Ollama instance running
locally or remotely, or a hosted OpenAI-compatible endpoint (DeepInfra,
Mistral, Groq, …) — is a deliberate, post-setup configuration action on the
`/m` (models) surface, not a first-run branch. Keeping it out of setup is what
lets the wizard present a single, honest "llama-server is the local engine"
story.

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
one per open capability tier, drawn from the **llama-server catalog**:

| Tier | Role | Catalog pick (GGUF) |
|---|---|---|
| `everyday` | Main chat / agent workhorse | Coder model, ~7B class |
| `fast_light` | Small background helpers | Small coder, ~1.5B class |
| `fast_light_text` | Prose judgment (recaps, verdicts, summaries) | Small text/instruct model |
| `embedding` | Local embeddings | Embedding GGUF |

- The default set is shown as an accept-and-move-on list. Accepting fills the
  **`.Open` slots** of `Models.Tiers.{everyday,fast_light,fast_light_text,embedding}`.
- The set is **editable**: each row opens the same model picker `/m` uses, so a
  user can swap any tier's pick before accepting. The picker here is backed by
  the llama-server catalog, **not** the Ollama registry.
- Populating `fast_light_text.Open` here is also what removes the compaction
  summarizer warning at its source — the tier has a real model instead of
  falling through to an empty slot.

**No download UI in the wizard.** Selecting the set does not block on transfer.
The catalog today ships only two Qwen coder GGUFs; expanding it with per-tier
entries (a ~7B everyday coder, a ~1.5B fast-light coder, a small prose model
for `fast_light_text`, and an embedding GGUF) is part of this work — the set
above is only real once those catalog entries exist.

### 4. Finish — background downloads, `/m` progress, cloud covers the gap

On finish the wizard:

1. Writes locus + cloud profiles + open tier picks through `UpdateConfig` /
   `UpsertCloudProfile` / `ApplyModelTierPatch`.
2. **Kicks off the open-model downloads in the background** via the existing
   local-runtime download manager (`DownloadModel` / `EnrollDownload`; states
   `not_downloaded → downloading → downloaded`). It does not wait.
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

## Routing contract (dependency)

The finish-message promise — *cloud covers the gap while open models
download* — is **not** automatic. `dispatch/select.go`'s `Select` crosses to
the cloud tier only when the preferred (open) provider registers as **absent**
(`nil` / `"NONE"`) and the mode permits crossing; it does **not** inspect
download state. So this contract must hold:

> An open tier slot whose GGUF is not yet on disk must cause the open provider
> to register as **absent** for that tier, not as present-but-failing. A
> provider that is always constructed and only errors at call time would break
> the fallback (the request would error instead of crossing to cloud).

Verify (and, if needed, implement) that llama-server provider registration is
gated on GGUF presence (`Discover` returning the model as `downloaded`) before
relying on the closing message. Under `open_only` there is no cloud tier to
cross to, which is why the gate warns that work waits on the first download.

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

### 2026-07-10 — locus-first rewrite

- **Locus is the organizing first question.** The old "open or cloud?"
  `StepPrimary` is removed; the four locus modes carry the whole "how do you
  want to use Cercano" decision, with `cloud_primary` and `open_only`
  recommended, `open_primary` as cost-saver, `cloud_only` discouraged.
- **llama-server catalog only.** Open models are GGUF downloads from the
  llama-server catalog. Ollama is dropped from setup entirely and becomes a
  post-setup external-server option.
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
  llama-server catalog for the recommended set to be real.

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
