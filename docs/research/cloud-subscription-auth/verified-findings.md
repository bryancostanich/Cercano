# Verified findings: subscription auth paths for the setup wizard

**Status:** authoritative — supersedes `synthesis.md` for design decisions.

The auto-generated `synthesis.md` in this directory is unreliable: its retrieval
landed on tangential pages (two "findings" are a generic Google Cloud service
list) and the gap-filling invented claims (a `davinci` OAuth scope; Gemini API
keys gated by VPC Service Controls). The findings below were verified against
primary sources instead: the opencode checkout at `../../../../opencode/`
(a shipping third-party open-source agent with working subscription sign-in),
and the Codex CLI / Gemini CLI repositories.

## Per-provider picture

### OpenAI — ChatGPT subscription sign-in EXISTS and works for third parties, but is ToS-gray

Evidence: `opencode/packages/opencode/src/plugin/openai/codex.ts` (shipping code).

- Two flows, both against issuer `https://auth.openai.com`:
  - **Browser:** OAuth 2.0 authorization-code + PKCE (S256), localhost callback on
    port **1455** (`/auth/callback`), scope `openid profile email offline_access`,
    plus non-standard params `id_token_add_organizations=true` and
    `codex_cli_simplified_flow=true`.
  - **Headless:** device-code-style flow via
    `POST /api/accounts/deviceauth/usercode` → user enters code at
    `{issuer}/codex/device` → poll `POST /api/accounts/deviceauth/token` →
    returns an `authorization_code` + `code_verifier` for a standard token
    exchange.
- Token refresh: standard `grant_type=refresh_token` at `{issuer}/oauth/token`.
- Requests are rerouted from `api.openai.com` paths to
  `https://chatgpt.com/backend-api/codex/responses`, with
  `Authorization: Bearer <access>` and a `ChatGPT-Account-Id` header extracted
  from JWT claims (`chatgpt_account_id` or `https://api.openai.com/auth`).
- **The catch:** the flow uses the Codex CLI's own client ID
  (`app_EMoamEEZ73f0CkXaXp7hrann`) — a third-party agent borrows another
  product's OAuth client. OpenAI has tolerated opencode doing this publicly
  (opencode even sends `originator: opencode`), but there is no sanctioned
  third-party registration path, and it can be revoked at any time.
- Subscription-auth'd model list is restricted (opencode maintains an
  allowlist: gpt-5.5, gpt-5.2, gpt-5.3-codex[-spark], gpt-5.4[-mini], and newer).
- API keys (`platform.openai.com`) remain the fully sanctioned path.

### GitHub Copilot — the most legitimate "Meridian equivalent" for non-Anthropic models

Evidence: `opencode/packages/opencode/src/plugin/github-copilot/copilot.ts`.

- Classic GitHub **device-code flow**: `https://github.com/login/device/code` →
  `https://github.com/login/oauth/access_token`, client ID `Ov23li8tweQw6odWQebz`.
- The GitHub OAuth token is then used against
  `https://api.githubcopilot.com` (enterprise: `copilot-api.<domain>`), which
  serves models from multiple vendors (OpenAI, Anthropic, Google) under one
  Copilot subscription; model catalog is fetched live from the API.
- Device flow is GitHub's first-class public-client pattern; the ecosystem has
  used it for years. Copilot ToS nominally scopes usage to official clients, but
  enforcement posture has been tolerant and the integration surface is stable.

### Google — no practical consumer-subscription path for third parties

- Gemini CLI's "Login with Google" rides the **Code Assist** backend with
  Google's own OAuth client; there is no third-party registration story for it.
- Telling: opencode ships **no** Google consumer plugin at all — only
  `google-vertex` (enterprise Application Default Credentials / service
  accounts). If a viable consumer flow existed, the most aggressive
  multi-provider OSS agent would carry it.
- Practical wizard path: **Gemini API key from AI Studio** (has a meaningful
  free tier). Vertex AI (ADC) is enterprise-only territory, not a wizard default.

### Anthropic — already handled

Meridian proxy (subscription OAuth) + API key, both first-class in Cercano.
Anthropic locked third-party use of Claude-subscription OAuth to first-party
clients; the Meridian proxy is the sanctioned route. (Consistent with opencode's
core anthropic plugin being plain SDK wiring with no OAuth flow.)

### Everyone else — API key (or key + base URL)

Mistral, Groq, xAI, DigitalOcean, Azure OpenAI, and generic OpenAI-compatible
endpoints: plain API-key auth. opencode's plugins for these are key-based.
Azure adds resource endpoint + deployment name; a generic
"OpenAI-compatible endpoint (base URL + key)" wizard entry covers the long tail.

## Design implication for the wizard's cloud step

Three tiers of auth legitimacy, which the wizard should surface honestly:

1. **Sanctioned subscription OAuth:** Anthropic via Meridian. GitHub Copilot
   device flow is near-sanctioned (first-class protocol, tolerated for years).
2. **Gray subscription OAuth:** OpenAI ChatGPT sign-in (borrowed client ID,
   revocable, restricted model list). If shipped, label it clearly and keep an
   API-key fallback one keystroke away.
3. **API key only:** Google (AI Studio key), Mistral/Groq/xAI, Azure, generic
   OpenAI-compatible endpoints.
