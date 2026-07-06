# Setup Wizard

Launch-time interactive setup for Cercano: gets a new user from zero to a
working primary model (and sensible secondaries) in one guided pass.

## Triggers

- First run: no `~/.config/cercano/config.yaml` (or config present but no
  usable provider) → wizard offers to run before the first turn.
- Explicit: `cercano -s` / `cercano -setup` re-enters the wizard any time;
  re-running edits the existing config rather than starting blank.

## Steps

### 1. Primary model: open or cloud

First question: "Where should your main model run?"

- **Open (local)** → embedded inference path: pick a model from the curated
  list, wizard configures `local_runtime` (managed `llama-server` or existing
  Ollama if detected) and queues the download.
- **Cloud** → provider picker (step 2).

### 2. Cloud provider + auth

Provider list with per-provider auth methods. Decision (2026-07-05): we ship
subscription sign-in flows where they exist, with honest labeling for the
unofficial one — see `docs/research/cloud-subscription-auth/verified-findings.md`.

| Provider | Auth methods (in offered order) |
|---|---|
| Anthropic | Meridian proxy (subscription) · API key |
| OpenAI | ChatGPT sign-in (unofficial) · API key |
| GitHub Copilot | Device-code sign-in (subscription) |
| Google Gemini | AI Studio API key (free tier) |
| Mistral / Groq / xAI | API key |
| OpenAI-compatible endpoint | Base URL + API key |

Auth flow details:

- **Meridian proxy (Anthropic):** existing flow, unchanged.
- **ChatGPT sign-in (OpenAI):** authorization-code + PKCE against
  `auth.openai.com`, localhost callback on port 1455; headless fallback via
  the device-code variant (`/api/accounts/deviceauth/*`). Requests route to
  the ChatGPT Codex backend with the `ChatGPT-Account-Id` header.
  **Labeling requirement:** the wizard entry reads
  "ChatGPT Plus/Pro sign-in — unofficial; uses the Codex CLI's client and may
  stop working. Model list is restricted." API-key entry is the adjacent
  option, one keystroke away. If the flow fails at runtime, the error message
  offers the API-key switch directly.
- **GitHub Copilot:** standard device-code flow
  (`github.com/login/device/code` → `login/oauth/access_token` →
  `api.githubcopilot.com`). Model catalog fetched live; serves OpenAI,
  Anthropic, and Google models under one subscription. Enterprise domain
  supported (`copilot-api.<domain>`).
- **API keys:** masked prompt, validated with a cheap live call before saving.

### 3. Locus mode

"How should Cercano split work between cloud and open models?"
Maps directly onto the four `locus.Mode` values:

- Cloud only → `cloud_only`
- Cloud primary, open co-processor → `cloud_primary`
  (recommended when a cloud provider was configured in step 2)
- Open primary, cloud fallback → `open_primary` (Cercano's default)
- Open only → `open_only`

### 4. Model tier profile

Fill the four tiers with a pre-populated profile derived from steps 1–3.
The wizard shows each tier with a plain-English line about what it's used
for (wording tracks the definitions in `pkg/config/models.go`):

- `most_capable` — frontier reasoning for the hardest tasks
- `everyday` — the default workhorse for main chat and agent turns
- `fast_light` — small, low-latency background helpers
- `fast_light_text` — fast prose-judgment work (watchdog verdicts,
  summaries, recaps); separate from `fast_light` because small coder
  models are poor text judges and small text models are poor code helpers

Every recommendation is editable in place: each tier row is focusable, and
selecting it opens the model picker (same list the `/m` page uses) to
override the suggestion before accepting. The screen states explicitly
that these choices are easy to change later — via `/m` or the config
file — so nobody agonizes here. Writes through `ApplyModelTierPatch`
(`default_provider`, `<tier>.<provider>` keys).

Autofill comes from a **curated recommendations file** shipped with the
binary (embedded, same pattern as the search script): provider → tier →
ordered candidate models. For static providers the first candidate wins;
for Copilot the candidates are intersected with the live catalog (which
varies by subscription plan) and the first available one wins. Updating
recommendations is a data edit, not a code change.

### 5. Secondary / open-weight models

If any open-weight models were selected: wizard finishes immediately,
downloads run in the background, `/m` shows download state, and switchover
happens live when a model becomes ready. Until then the configured cloud
model serves as fallback (existing behavior).

## Non-goals (V1)

- No enterprise SSO / Vertex AI / Azure Entra flows — API-key style entries
  only beyond the three sign-in flows above.
- No credential migration from other tools' keychains.
- Wizard does not manage MCP servers or permissions; those keep their own
  config surfaces.

## Decisions (2026-07-05)

- ChatGPT sign-in ships in the default provider list; the "unofficial" label
  carries the warning. No config flag gate.
- Tier autofill for all providers comes from the shipped recommendations
  file (see step 4); Copilot additionally filters against its live catalog.
- Quitting mid-wizard resumes where the user left off on the next entry
  (wizard state persisted alongside config; a completed run clears it).

## Decisions (2026-07-05, later)

- **Abandon trapdoor.** `q` (pressed twice — the first press asks for
  confirmation in the status line) abandons the run without keeping anything:
  eager commits are rolled back and the resume file is cleared. `esc` keeps
  its meaning (back / pause-and-resume-later).
- **Baseline snapshot powers the rollback.** A fresh run snapshots the cloud
  profiles + active profile into the wizard state file before any eager
  commits. Abandon restores it: profiles the run created are removed (their
  keychain keys with them), profiles it modified are restored, the previously
  active profile is re-activated. Because the baseline is persisted, a
  crashed or broken run can be relaunched and abandoned back to the
  pre-wizard configuration. Known limit: a key overwritten on a pre-existing
  profile can't be restored — keys are write-only.
- **Meridian profiles carry the proxy base URL.** The wizard's meridian
  commit sends `base_url` (Meridian's default `http://127.0.0.1:3456`) and
  `route: meridian`; the server only activates a keyless profile when a proxy
  base URL says who handles auth. `route` now travels through the
  `UpsertCloudProfile` RPC, and an empty route on update preserves the
  existing profile's route so route-unaware clients can't demote meridian
  to direct.
