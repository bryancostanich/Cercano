# Multi-Cloud Provider Profiles — Design

**Status:** Foundation implemented 2026-06-27.

The foundation for non-Anthropic cloud support: let Cercano store **multiple named
cloud provider configurations** ("profiles"), switch the active one at runtime,
and keep secrets in the **OS keychain**. This is sub-project 1 of the multi-cloud
effort; it adds the config + secrets + factory plumbing so later sub-projects each
just register a new wire-format "flavor."

## Context & decomposition

The native tool-loop talks to `llm.Provider` (`Chat`/`StreamChat` with tools).
Each backend is a hand-rolled package under `internal/llm/<name>/`
(`client.go` + `adapter.go` + `stream.go`). Today only `anthropic` (cloud) and
`ollama` (local) exist, and cloud is wired Anthropic-only.

The full effort is four sequential sub-projects, in priority order:

1. **This doc — multi-profile config + keychain secrets + provider factory** (no new provider).
2. OpenAI **Chat Completions** provider (`base_url`-parameterized; also covers Gemini-compat, Groq, Together, OpenRouter, DeepSeek, local OpenAI-compatible servers).
3. OpenAI **Responses API** provider.
4. **Bedrock Converse** provider (+ AWS SigV4).

Each later sub-project adds one package + one `case` in the factory. They are NOT
part of this design.

## 1. Config model

Profiles hold non-secret metadata only; the API key lives in the keychain.

```yaml
cloud_profiles:
  - { name: claude, flavor: messages,         base_url: "",           model: claude-... }
  - { name: openai, flavor: chat_completions, base_url: "",           model: gpt-5 }
  - { name: groq,   flavor: chat_completions, base_url: groq.com/..., model: llama-... }
active_cloud_profile: claude
```

- `CloudProfile{ Name, Flavor, BaseURL, Model string }` — **no key field.**
- `Config.CloudProfiles []CloudProfile`, `Config.ActiveCloudProfile string`.
- `Flavor` is an enum. Only `messages` resolves today; `chat_completions`,
  `responses`, `bedrock` are reserved and error as "not yet supported."

## 2. Secrets — OS keychain

A new `internal/secrets` package wraps `99designs/keyring` (macOS Keychain,
Windows Credential Manager, Linux Secret Service — chosen over `zalando/go-keyring`
for its pluggable fallback backends, which the deferred headless case will reuse).
Interface:

```go
Get(profile string) (string, error)
Set(profile, key string) error
Delete(profile string) error
```

Stored under `service = "cercano"`, `account = <profile-name>`. **Keychain-only
for now** — if the keychain is unavailable (headless Linux / Docker), return a
clear error naming the deferred fallback. No secret is ever written to
`config.yaml`.

## 3. Provider factory

```go
func BuildCloudProvider(p CloudProfile, apiKey string) (llm.Provider, error)
```

`switch p.Flavor`: `messages` → `anthropic.NewClient(anthropic.Config{BaseURL,
APIKey, Model})`. Any other flavor returns `fmt.Errorf("flavor %q not yet
supported", p.Flavor)`. **This is the single extension point** for sub-projects
2–4.

## 4. Wiring — one cloud system (untangle the dual paths)

Cercano has two cloud client systems today:
- **Native tool-loop cloud** — `s.cloudLLMProvider` (`llm.Provider`); the modern
  path the main agent loop uses. Anthropic-only.
- **Legacy langchaingo cloud** — `legacymodels.CloudModelProvider`
  (`agent.ModelProvider`), registered in the router as `"CloudModel"`. Hardwired
  to `anthropic` + `googleai` (the only two langchaingo imports). **The
  co-processor cloud tier uses this one** (`processCoproc` grabs
  `providers["CloudModel"]`).

Leaving these split means a new profile (e.g. OpenAI) would serve the main
tool-loop but **not** the co-proc tier under Cloud Only (langchaingo can't serve
it) — a silent gap exactly for the providers we're adding. So this sub-project
**unifies onto the native factory**:

1. Add an adapter `llmProviderModelProvider` that wraps any `llm.Provider` as an
   `agent.ModelProvider` — its `Process(req)` does a one-shot `Chat` (no tools)
   and returns the text as a `*agent.Response`.
2. Build the active profile's `llm.Provider` via `BuildCloudProvider`, feed it to
   the native tool-loop (`srv.SetCloudLLMProvider`) **and**, wrapped in the
   adapter, register it as the router's `"CloudModel"` — replacing the langchaingo
   provider.
3. Resolve/rebuild both at startup and whenever the active profile (or its key)
   changes.

Result: one cloud system. Every profile works for the tool-loop **and** the
co-proc tier. This also retires the langchaingo cloud path (progress toward the
deferred SmartRouter-shelving cleanup).

**Consequence — `google`:** langchaingo is the *only* current backend for Google.
Retiring it means a `google` profile has no resolvable flavor until the
`chat_completions` flavor lands (sub-project 2, which reaches Gemini via its
OpenAI-compatible endpoint). Practically minor — the wired/configured cloud is
Anthropic — but noted: Google cloud is unsupported in the window between this
sub-project and sub-project 2.

## 5. Runtime management (CLI + RPC)

New RPCs + agentclient methods:
- list profiles
- add / update / remove a profile (metadata)
- set a profile's key (→ keychain)
- set the active profile

A CLI surface (extend the existing `/cloud` command, or a new `/profiles`) plus a
config-editor view manage the list and the active selection. Switching active
rebuilds the native cloud provider with no restart, mirroring today's
`UpdateConfig` live-swap.

## Using profiles

Profiles are stored in `~/.config/cercano/config.yaml` under `cloud_profiles` (a list) and `active_cloud_profile` (a string):

```yaml
cloud_profiles:
  - { name: claude, flavor: messages, base_url: "", model: claude-sonnet-4-6 }
active_cloud_profile: claude
```

API keys are **never stored in YAML** — they live in the OS keychain (macOS Keychain, Windows Credential Manager, or Linux Secret Service via the `99designs/keyring` package).

### CLI commands

| Command | What it does |
|---|---|
| `/cloud` or `/cloud list` | List all profiles; show which is active |
| `/cloud use <name>` | Set the active profile; rebuilds the cloud provider immediately |
| `/cloud key <name> <api-key>` | Store (or update) the API key for a profile in the OS keychain |

### Settings page (cercano-cli)

The `/s` settings page has a **Cloud Providers** section: a vertical list of your
configured profiles plus known-provider templates (anthropic, openai, gemini,
groq, deepinfra, together, openrouter, deepseek) and `+ other` for any
OpenAI-compatible endpoint. Selecting a row opens an inline editor for its
base URL, model, and API key (stored in the OS keychain), with save / activate /
delete actions. Untested backends are labeled `(untested)`; flavors not yet
implemented (`bedrock`, the OpenAI Responses API) are labeled `(coming soon)`
and cannot be activated. Backed by the `UpsertCloudProfile` / `RemoveCloudProfile`
/ `SetActiveCloudProfile` / `SetCloudProfileKey` RPCs.

### Key auto-migration

If you have a legacy single-cloud configuration (the old `cloud_api_key`, `cloud_model`, `cloud_base_url` fields in your YAML), Cercano auto-migrates on first run:
- Creates a `default` profile from the legacy fields.
- Moves the inline API key to the OS keychain (under the profile name `default`).
- Blanks the YAML `cloud_api_key` field (you can delete the old fields manually).
- Sets `active_cloud_profile: default`.

This is a one-time, automatic operation. Your key is no longer stored in plaintext YAML.

## 6. Migration / backward-compat

On config load, if `cloud_profiles` is empty but the legacy
`cloud_provider`/`cloud_model`/`cloud_api_key`/`cloud_base_url` fields are set:

1. Synthesize a `default` profile (flavor inferred — `anthropic` → `messages`).
2. Move the inline `cloud_api_key` into the keychain and blank the yaml field.
3. Set `active_cloud_profile = default`.

One-time and automatic; it also de-plaintexts the existing key. A `google` legacy
config becomes a profile with no resolvable flavor yet (see §4 consequence) — it
is metadata-only until the `chat_completions` flavor lands in sub-project 2.

## 7. Error handling

- **Keychain unavailable** → clear error naming the deferred fallback; the agent
  still runs with cloud absent (same as today's absent-cloud sentinel).
- **Active profile's key missing** → absent-cloud behavior + a notice.
- **Unsupported flavor activated** → error at build time, surfaced on activation.

## 8. Testing

- Config round-trip (profiles + active) load/save.
- Migration: legacy single-cloud → `default` profile, key relocated to keychain,
  yaml field blanked.
- Factory: `messages` → anthropic client; unknown flavor → error.
- Secrets layer against a mock keyring (Get/Set/Delete).
- `llmProviderModelProvider` adapter: `Process` does a one-shot `Chat` and maps
  the result to `*agent.Response` (against a fake `llm.Provider`).
- Active-switch rebuilds both the native cloud provider and the router's
  `"CloudModel"`.

## Out of scope (explicit)

- Non-`messages` flavors (sub-projects 2–4).
- Automatic routing / failover / task-based model selection (a later layer this
  foundation enables).
- The headless / Docker keychain fallback (encrypted file or env var) — deferred.
