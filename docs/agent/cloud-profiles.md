# Multi-Cloud Provider Profiles — Design

**Status:** Design approved 2026-06-27. Not yet implemented.

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

A new `internal/secrets` package wraps a cross-platform library
(`zalando/go-keyring`: macOS Keychain, Windows Credential Manager, Linux Secret
Service). Interface:

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

## 4. Wiring

At startup and whenever the active profile (or its key) changes: resolve the
active profile → fetch its key from the keychain → `BuildCloudProvider` →
`srv.SetCloudLLMProvider(...)`.

The existing legacy langchaingo co-processor cloud (`CloudModel` via
`legacymodels.NewCloudModelProvider`) is also driven from the active profile's
fields (provider-name mapped from flavor where possible, plus model / base_url /
key), so co-proc cloud keeps working. **Full unification of the two cloud paths
(native `llm.Provider` vs legacy langchaingo) is out of scope.**

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

## 6. Migration / backward-compat

On config load, if `cloud_profiles` is empty but the legacy
`cloud_provider`/`cloud_model`/`cloud_api_key`/`cloud_base_url` fields are set:

1. Synthesize a `default` profile (flavor inferred — `anthropic` → `messages`).
2. Move the inline `cloud_api_key` into the keychain and blank the yaml field.
3. Set `active_cloud_profile = default`.

One-time and automatic; it also de-plaintexts the existing key. A `google` legacy
provider becomes a profile with no resolvable native flavor yet — it stays
metadata-only (and keeps working through the legacy co-proc path) until a
matching flavor lands.

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
- Active-switch rebuilds the native cloud provider.

## Out of scope (explicit)

- Non-`messages` flavors (sub-projects 2–4).
- Automatic routing / failover / task-based model selection (a later layer this
  foundation enables).
- The headless / Docker keychain fallback (encrypted file or env var) — deferred.
