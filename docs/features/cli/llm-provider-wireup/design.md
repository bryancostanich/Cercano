# LLM Provider Wireup — Design

**Status:** Designed 2026-06-29.

A better experience for wiring up cloud LLM backends from the CLI settings page.
Replaces the legacy flat "Cloud" section with a **Cloud Providers** section: a
vertical list of your configured profiles plus the known-provider templates, each
showing status, with an inline detail editor to create / edit / activate / delete
a profile. API keys go to the OS keychain, never to `config.yaml`.

This is a **server + CLI** feature. The profile data model exists already
(see [cloud-profiles.md](../../../agent/cloud-profiles.md)); what's missing is the
ability to create, edit, or delete a profile over the wire — only switch-active
and set-key exist today.

## Context — what already exists

- `config.CloudProfile{ Name, Flavor, Backend, BaseURL, Model }` — the API key is
  **not** here; it lives in the OS keychain keyed by profile name
  (`internal/secrets`, `99designs/keyring`).
- `Flavor` is the wire format: `messages` (Anthropic, implemented),
  `chat_completions` (OpenAI + all compat endpoints, implemented),
  `responses` / `bedrock` (reserved — the factory returns "not yet supported").
- `Backend` selects per-backend quirks for `chat_completions` only
  (`internal/llm/openai/quirks.go`): `openai` (passthrough), `gemini`, `groq`
  (base64 + error-normalize + retry), `""` → defensive default (all on).
- `cloudfactory.BuildCloudProvider(profile, key)` is the single extension point;
  switching the active profile live-rebuilds the cloud provider with no restart.
- Existing RPCs (`agentclient`): `GetCloudProfiles()` → `[]CloudProfileInfo{Name,
  Flavor, BaseURL, Model, HasKey}` + active name; `SetActiveCloudProfile(name)`;
  `SetCloudProfileKey(name, key)`.
- The current settings "Cloud" section (`internal/ui/settings_build.go`) drives
  the **legacy** flat fields (`cloud_provider` anthropic/google, `cloud_model`,
  `cloud_base_url`, `cloud_api_key`) — not profiles.

## 1. Server side — profile CRUD (new RPCs)

Add to `agent.proto` and regenerate `*.pb.go`:

| RPC | Behavior |
|---|---|
| `UpsertCloudProfile` | create or update a profile's metadata (name, flavor, backend, base_url, model) |
| `RemoveCloudProfile` | delete the profile and its keychain key |

Reused unchanged: `GetCloudProfiles`, `SetActiveCloudProfile`,
`SetCloudProfileKey`.

**Upsert semantics:**
- Match by `Name`. If the name exists, update its metadata in place; otherwise
  append a new profile.
- Validate: non-empty unique-on-create name; `base_url` required when
  `flavor == chat_completions`; `flavor` must be one of the known enum values.
- Persist `currentConfig.CloudProfiles` to `config.yaml`.
- If the upserted profile is the active one, rebuild the live cloud provider
  (same path as `SetActiveCloudProfile`).

**Remove semantics:**
- Drop the profile from `currentConfig.CloudProfiles`, save config.
- Delete the keychain entry for that name (best-effort; missing key is not an
  error).
- If it was the active profile, clear `ActiveCloudProfile` and drop to
  absent-cloud, surfaced as a notice (same as today's absent-cloud sentinel).

**agentclient additions:**
- `UpsertCloudProfile(ctx, CloudProfileInfo) error`
- `RemoveCloudProfile(ctx, name string) error`
- Add `Backend string` to `CloudProfileInfo` (returned by `GetCloudProfiles` so
  the detail editor can show it).

## 2. CLI — the Cloud Providers section

One settings section, rendered top-to-bottom:

1. **Your profiles** — one row per configured profile:
   `● <name>   ✓ key   (active)`. The active dot + `✓ key` / `— no key` come
   from `GetCloudProfiles` (`HasKey`, active name).
2. **Add provider** — the known templates + `+ other (custom endpoint)`, always
   present even with nothing configured. Each template row carries its label tier
   (see §3): verified (no label), `(untested)`, or `(coming soon)`.

Selecting any row opens its **detail block** beneath it (the form re-snapshots so
the detail fields appear inline under the selected row):

- `name` — prefilled from the template; editable **on create only**, fixed after.
  (Rename = keychain re-key, deferred.)
- `base-url` — prefilled from the preset, editable.
- `model` — suggested or blank, editable.
- `flavor` + `backend` — **read-only** for known templates (transparency about
  which quirks apply); editable selects only for **Other** (flavor defaults
  `chat_completions`, backend defaults to the defensive profile).
- `api-key` — masked; committing it calls `SetCloudProfileKey`. Shows `(stored)`
  / `(not set)`; the value is never displayed.
- Buttons: `[ save ]` (Upsert), `[ activate ]` (SetActive), `[ delete ]` (Remove;
  configured profiles only).

**Multiple accounts per provider:** pick a known template, give the new profile a
unique name, fill the key, save — repeat for a second account on the same
provider. Each account is its own named row.

## 3. Preset table & label tiers

Presets live in a static CLI table (id → display label, flavor, backend, default
base URL, label tier).

**Verified (no label):**
- `anthropic` — `messages` — default Anthropic endpoint
- `gemini` — `chat_completions` / `gemini` — `https://generativelanguage.googleapis.com/v1beta/openai`

**Untested (`chat_completions` is built; labeled `(untested)`):**
- `openai` — backend `openai` — `https://api.openai.com/v1`
- `groq` — backend `groq` — `https://api.groq.com/openai/v1`
- `deepinfra` — backend default — `https://api.deepinfra.com/v1/openai`
- `together` — backend default — `https://api.together.xyz/v1`
- `openrouter` — backend default — `https://openrouter.ai/api/v1`
- `deepseek` — backend default — `https://api.deepseek.com`

**Coming soon (flavor not built; labeled `(coming soon)`, activate blocked):**
- `bedrock` — flavor `bedrock`
- `openai (responses)` — flavor `responses` — `https://api.openai.com/v1`

**Always present:**
- `+ other (custom endpoint)` — flavor `chat_completions` (editable to
  `messages`), backend default, all fields user-supplied.

`(coming soon)` rows: the detail block opens (you can see the intended config) but
`[ activate ]` is disabled with a clear "flavor not yet supported" note. `[ save ]`
is allowed so the metadata can be parked, but it can't be made active.

## 4. Components / files

- **proto:** edit `source/server/pkg/proto/agent.proto` (two RPCs + request /
  response messages), regenerate `*.pb.go`.
- **server:** `internal/server/server.go` — `UpsertCloudProfile` /
  `RemoveCloudProfile` handlers (config save + rebuild + keychain delete on
  remove).
- **agentclient:** `pkg/agentclient/client.go` — `UpsertCloudProfile`,
  `RemoveCloudProfile`; add `Backend` to `CloudProfileInfo`.
- **cli/internal/form:** a small new **RowField** widget — a selectable labeled
  row carrying status annotations (active dot, key indicator, label tier). The
  detail block reuses existing `TextField` / `MaskedField` / `SelectField` /
  `ButtonField`.
- **cli/internal/ui:** a preset table (the providers above + defaults); a
  `cloud_providers` section builder that merges presets + profiles and tracks the
  selected row in settings-page state; `onCommit` routing for the new keys
  calling the CRUD / key / active RPCs. The legacy `config` fields stay for
  migration but leave the settings UI.

## 5. Data flow

- Open settings → `GetConfig` (existing) **and** `GetCloudProfiles` (new in the
  snapshot), cached on the settings page like `cfg` so a re-snapshot per keystroke
  doesn't round-trip.
- Select a row → set the selected profile/template in page state → re-snapshot →
  detail appears.
- Edit `name`/`base-url`/`model` + `[ save ]` → `UpsertCloudProfile` → refresh
  profiles cache.
- Commit `api-key` → `SetCloudProfileKey` → refresh (`HasKey` flips to true).
- `[ activate ]` → `SetActiveCloudProfile` → refresh; the chat tier picks up the
  rebuilt cloud provider.
- `[ delete ]` → `RemoveCloudProfile` → refresh.

The profiles cache is invalidated (set to nil) after any successful CRUD / key /
active commit so the next snapshot re-fetches.

## 6. Error handling

- Bad base URL, duplicate name, keychain unavailable, RPC failure → surfaced on
  the form's status line (same channel as existing commit errors).
- Activating a profile with no key → server falls back to absent-cloud with a
  notice; the row keeps `— no key`.
- Activating a `(coming soon)` flavor → blocked client-side (button disabled);
  the server's "not yet supported" error is the backstop.

## 7. Testing

- **Server:** Upsert add vs. update; validation (duplicate name, missing base_url
  for chat_completions, bad flavor); Remove drops profile + deletes key + clears
  active when needed; active-profile upsert rebuilds the provider; config
  round-trips.
- **agentclient:** `UpsertCloudProfile` / `RemoveCloudProfile` map to/from proto;
  `Backend` populated from `GetCloudProfiles`.
- **CLI:** section-merge (presets + profiles, correct label tiers, status
  indicators); `(coming soon)` disables activate; commit routing for each new key;
  RowField render + keyboard nav including the narrow single-column layout; cache
  invalidation after a CRUD commit.

## 8. Out of scope (explicit)

- Implementing the `responses` or `bedrock` flavors (separate sub-projects; this
  feature only labels and parks them).
- Renaming an existing profile (keychain re-key — deferred).
- Verifying the untested backends green (a conformance-matrix effort, tracked in
  [llm-backend-notes.md](../../../agent/llm-backend-notes.md)).
- Headless / Docker keychain fallback (deferred at the profiles foundation).
- Per-task / automatic model routing (a later layer the profiles enable).
