# ChatGPT Subscription Sign-In (OpenAI device-code OAuth)

## Goal

Let a user authenticate the OpenAI cloud provider with their **ChatGPT
Plus/Pro subscription** instead of a pay-as-you-go API key, via a sign-in flow
launched from the `/config` Cloud Providers section and from the first-run
setup wizard. This mirrors the Claude Max sign-in offered for Anthropic —
except Anthropic routes through the external **Meridian** proxy, whereas for
OpenAI the OAuth flow runs **in-process in the agent**.

## Status: built

Ships across six commits (`chatgptauth` device-flow core was pre-existing on
the branch; this work wired it end to end):

1. Token source with keychain-backed refresh (`internal/chatgptauth/source.go`)
2. Responses provider `route: chatgpt` + cloudfactory wiring
3. `StartChatGPTLogin` streaming RPC + agentclient wrapper
4. CLI sign-in modal + streaming plumbing
5. `/config` cloud-section wiring (button + commit + root model)
6. Wizard ChatGPT auth choice wired to the same modal

## The unofficial, ToS-gray caveat

This rides the **Codex CLI's own OAuth client ID**
(`app_EMoamEEZ73f0CkXaXp7hrann`) — there is no sanctioned third-party
registration path. It can stop working whenever OpenAI changes things, and the
usable model list is restricted to what the ChatGPT-account codex backend
accepts — the plain gpt-5.x names (gpt-5.5, gpt-5.4, gpt-5.4-mini, …). The
-codex-suffixed names (gpt-5.3-codex, gpt-5.1-codex, …) are REJECTED for
ChatGPT accounts ("model not supported when using Codex with a ChatGPT
account"), and the backend requires streaming requests. The `/config` row and the
wizard both keep an **API key** path one keystroke away as the sanctioned
fallback. Endpoint shapes are verified against a shipping third-party
implementation — see `docs/research/cloud-subscription-auth/verified-findings.md`
and opencode's `plugin/openai/codex.ts`.

## How it differs from the Anthropic (Meridian) sign-in

| | Anthropic | OpenAI (this feature) |
|---|---|---|
| Auth broker | external Meridian proxy | in-process, ours |
| Credential source | keychain from `claude login` | OAuth tokens we mint |
| Token refresh | Meridian handles it | we refresh (hourly expiry) |
| Profile shape | `flavor: messages`, `route: meridian` | `flavor: responses`, `route: chatgpt` |
| Backend | Anthropic Messages via Meridian | OpenAI Responses via chatgpt.com/backend-api/codex |

## The flow (device authorization)

OpenAI exposes two sign-in shapes; we use the **device-authorization** one
(no loopback port to bind, works headless / over SSH):

1. `POST auth.openai.com/api/accounts/deviceauth/usercode` → `{device_auth_id,
   user_code, interval}`. The agent shows the user `auth.openai.com/codex/device`
   and the `user_code`.
2. Poll `POST .../deviceauth/token` (403/404 = still pending) until it returns
   an `authorization_code` + PKCE `code_verifier`.
3. Exchange those at `oauth/token` (`grant_type=authorization_code`) →
   `{id_token, access_token, refresh_token}`.
4. Extract the ChatGPT account id from the id-token JWT claim
   `chatgpt_account_id` (three known claim locations, checked in order).

Refresh uses `grant_type=refresh_token` at the same endpoint, preserving the
old refresh token when the response omits a new one.

## Where the pieces live

- **`internal/chatgptauth/`** — device flow (`Flow.Start`/`Pending.Poll`/
  `Flow.Refresh`), `TokenSet` (keychain JSON), and `Source`: a refreshing token
  source that loads the stored set, refreshes + persists when expired, and
  hands the responses client a valid bearer + account id per request.
- **`internal/llm/responses/`** — `route: chatgpt` pins the codex backend base
  URL and sets `Authorization: Bearer`, `ChatGPT-Account-Id`, `originator`, and
  `User-Agent` per request. The direct-API-key path is byte-for-byte unchanged.
- **`internal/cloudfactory/`** — the `responses` flavor builds a
  `chatgptauth.Source` from the secrets store when the profile's route is
  chatgpt; a token source is required for that route.
- **`StartChatGPTLogin` RPC** (`internal/server/chatgpt_login.go`) — a
  server-streaming RPC: first frame carries `user_code` + `verification_url`;
  it then polls, stores the token set, creates/activates a
  responses+chatgpt profile, and sends a terminal frame.
- **CLI** — `chatgpt_login_modal.go` (device code + URL, waiting/done/failed),
  wired into `/config` (a "sign in with ChatGPT" button on responses rows) and
  the setup wizard (the chatgpt auth choice opens the same modal).

## Token storage

The token set (`{access, refresh, expires_at, account_id}`) is JSON-encoded
into the **same keychain slot** API keys use, keyed by profile name. "Has a
stored secret" is the "signed in" signal; nothing is written to a plaintext
file. Refresh happens transparently in `Source.Token`, serialized so a burst
of concurrent requests triggers at most one refresh.
