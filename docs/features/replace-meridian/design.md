# Replacing Meridian with native Anthropic subscription OAuth

## Goal

Replace the external **Meridian** Node/TypeScript proxy with a native Go path
that calls `api.anthropic.com/v1/messages` **directly**, authenticated by a
Claude Max/Pro **subscription OAuth token**. No proxy, no Agent SDK, no Node.

Because the Messages API is **stateless**, every request is independent and
carries full history — eliminating Meridian's session-lineage / fingerprinting
/ turn-cap / concurrency-ceiling machinery, which is the root of the
multi-threaded-correctness problems that motivate this work. Concurrency
becomes correct by construction.

## Verified live (2026-07-13, on b's machine)

A direct `curl` to `api.anthropic.com/v1/messages` with the subscription token
returned **HTTP 200** with a valid completion. Confirmed:

- **Auth:** `authorization: Bearer sk-ant-oat01-…` (OAuth token; no `x-api-key`).
- **`anthropic-version: 2023-06-01`** — accepted.
- **`anthropic-beta: oauth-2025-04-20`** — accepted.
- **System first block** must claim Claude Code identity:
  `You are Claude Code, Anthropic's official CLI for Claude.` (spoof retained —
  see Decisions). Necessity not separately tested; we keep it regardless.
- **Model:** `claude-haiku-4-5-20251001` served directly by the subscription.

From the pasted `claude login` authorize URL:

- **authorize endpoint:** `https://claude.ai/oauth/authorize`
- **client_id:** `9d1c250a-e61b-44d9-88ed-5944d1962f5e`
- **params:** `response_type=code`, `code=true` (enables on-screen code as a
  manual-paste fallback), `code_challenge_method=S256`.
- **redirect_uri:** `http://localhost:<ephemeral-port>/callback` — a **loopback
  redirect**: Claude Code runs a throwaway local HTTP server and catches the
  code there (RFC 8252 style), not a manual console paste.
- **scope:** `org:create_api_key user:profile user:inference
  user:sessions:claude_code user:mcp_servers user:file_upload`. The
  load-bearing scope is `user:inference`; we mirror the full set. (`user:` set
  is what is actually granted; `org:create_api_key` is not needed by us.)

Verified live via our own PKCE flow (the `oauth-spike`, since deleted):

- **token / refresh endpoint:** `https://console.anthropic.com/v1/oauth/token`
  — our loopback flow exchanged a real authorization code here and the minted
  bearer returned HTTP 200 from `/v1/messages`. Refresh reuses the same
  endpoint + standard grant (first exercised at the ~8h token expiry).

Reference only (Flow A stores its OWN copy, see Decisions): Claude Code keeps
its token in keychain service `Claude Code-credentials` as JSON —
`claudeAiOauth.{accessToken, scopes, expiresAt(ms epoch), subscriptionType}`.

## Decisions (locked with b, 2026-07-13)

- **Flow A** — we run our OWN PKCE authorize via a **loopback server on an
  ephemeral port**, and hold our OWN token lineage. NOT reading Claude Code's
  keychain (Flow B): B couples us to the `claude` binary + its keychain schema,
  and — because Anthropic's refresh tokens rotate (single-use) — sharing B's
  credential would cause a refresh war between Claude Code and Cercano. Keep
  `code=true` manual paste as a headless/SSH fallback.
- **Identity spoof retained**, documented as ToS-gray (same posture as the
  ChatGPT-subscription path). Not dropping it for now.
- **Route value renamed** `meridian` → `subscription`; migrate existing
  profiles on config load.

## Architecture

Mirror the existing ChatGPT design: `internal/chatgptauth` +
`internal/llm/responses/client.go` consume a `TokenSource` and set
`Authorization: Bearer` in `authorize()`. We copy that shape.

### `internal/anthropicauth/` (new)

- PKCE loopback OAuth flow (authorize URL builder, ephemeral loopback callback
  server, code→token exchange, refresh).
- Token store in the OS keychain under a Cercano-owned service (NOT Claude
  Code's), holding `{access, refresh, expires}`.
- A `Source` type implementing a `TokenSource { Token(ctx) (access string, err) }`
  interface, with **single-flight** refresh so N concurrent 401s trigger one
  refresh, not N.

### `internal/llm/anthropic/` (changed)

- Add `route = "subscription"`. On that route the client:
  - pulls a bearer from the `TokenSource` instead of an API key,
  - sets `anthropic-beta: oauth-2025-04-20` (+ any feature betas we want),
  - prepends the Claude Code system block as the first system text block,
  - targets `api.anthropic.com` directly.
- **Delete** the OpenCode header spoofing (`x-opencode-*`), the `anon-…`
  session minting, and the `x-meridian-source` logic.

### Concurrency

The provider is a plain stateless HTTP client; each `Chat`/`StreamChat` is
independent. The only shared mutable state is the token source's cached
access token, guarded by one mutex + single-flight refresh. No per-conversation
caches, no lineage, no locks.

## Phases

1. **Auth package.** `internal/anthropicauth/`: authorize URL + PKCE, loopback
   callback server, token exchange, refresh, keychain store, `Source`
   TokenSource with single-flight. Unit-test the pure bits (PKCE, URL build,
   expiry math); integration-test refresh against a stub token endpoint.
2. **Provider route.** `subscription` route in `internal/llm/anthropic/`:
   bearer + beta header + system-block injection + direct base URL. Adapter
   tests for header/system-block shaping.
3. **Config + UX.** Rename route to `subscription`; migrate `meridian`
   profiles on load; wizard "Sign in with Claude (subscription)" drives the
   Flow A loopback; proto comment/enum updates; cloud catalog + presets.
4. **Delete Meridian.** Remove `internal/meridian/`, `MeridianStatus` proto +
   broadcast + CLI chip + event subscription, `internal/llm/session.go`
   independent-session machinery, Node/npx prereqs, and the docs referencing
   the proxy.

## To delete once shipped

`internal/meridian/` · `MeridianStatus` proto messages + `broadcastMeridianStatus`
+ `SetupMeridian`/`StopMeridian`/`SyncMeridianForProfile` · CLI `renderMeridianChip`
+ `meridianStatusChangedMsg` + status subscription · `internal/llm/session.go`
`WithIndependentSession`/`IsIndependentSession` (+ `WithSessionID` if unused
elsewhere) · the `routeMeridian` header block in `anthropic/client.go` · Node
prereq detection + `npx` spawn.

## Progress

- **Phase 1 done** (commit on `replace-meridian`): `internal/anthropicauth/`
  — PKCE loopback Flow (Start/Wait/Redeem), skew-aware TokenSet, single-flight
  Source; 13 tests green under `-race`. Verified end-to-end live: our own flow
  minted a token that returned HTTP 200 from `/v1/messages`.

## Open items

- Exercise refresh at the ~8h token expiry (same endpoint, standard grant).
- Meridian open-issues survey still owed (as corroborating "what we're
  escaping"); `dispatch` sub-agent needs local Ollama up, or pull via `fetch`.
