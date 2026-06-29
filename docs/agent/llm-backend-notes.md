# LLM Backend Notes & Conformance

The `chat_completions` provider (`internal/llm/openai/`, wrapping `sashabaranov/go-openai`)
serves many backends through one client, selected by `base_url`. Those backends
diverge in real ways. This doc records what's been verified per backend and the
known quirks, so "good experience on each backend" is tracked, not assumed.

## Conformance matrix

Verified by the live integration tests (`internal/llm/openai/client_integration_test.go`,
`INTEGRATION_TEST=1`). ✅ verified · ⚠️ works with a caveat · ❌ broken · — untested.

| Backend (flavor) | Chat | Stream + usage | Tools | Vision (base64) | Vision (URL) |
|---|---|---|---|---|---|
| OpenAI (chat_completions) | — | — | — | — | — |
| Gemini-compat (chat_completions) | ✅ | ✅ | ✅ | ✅ | ❌ |
| Groq (chat_completions) | — | — | — | — | — |
| DeepInfra (chat_completions) | — | — | — | — | — |
| Anthropic (messages) | ✅ (prod) | ✅ | ✅ | — | — |
| Ollama (local) | ✅ (prod) | ✅ | ✅ | — | — |

(Gemini verified 2026-06-28 on `gemini-2.5-flash` via `https://generativelanguage.googleapis.com/v1beta/openai`.)

The cercano-cli settings page wires these up (`/s` → Cloud Providers); untested
rows there carry an `(untested)` label that mirrors the `—` cells above.

## Findings (2026-06-28, live Gemini run)

### 1. Image URLs are not portable — base64 is

OpenAI-native **fetches** `image_url` server-side; Gemini's OpenAI-compat endpoint
**refuses** (`"Cannot fetch content from the provided URL"`, HTTP 400). Inline
base64 (`data:` URI) works on both. So an image-by-URL "works" on OpenAI but
silently fails on a compat backend — exactly the kind of per-backend divergence
to design away.

**Implication:** the safe, portable representation for the `chat_completions`
provider is base64. `ImageURL` should be resolved to bytes before sending (we
already have `llm.ResolveImageBytes`), at least for non-OpenAI endpoints.

### 2. Error bodies aren't uniform → go-openai mangles them

Gemini returns error payloads as a JSON **array** (`[{ "error": {...} }]`); OpenAI
returns an **object** (`{ "error": {...} }`). `go-openai` unmarshals into its
`openai.ErrorResponse` (object), so on Gemini you get
`json: cannot unmarshal array into Go value of type openai.ErrorResponse` instead
of the real message (e.g. the 400 "cannot fetch URL" or a 503 "high demand"). The
HTTP status survives; the human-readable cause is lost or garbled.

**Implication:** error normalization is a backend-experience problem. Options
range from a tolerant wrapper around go-openai errors to a hand-rolled client
(the deliberately-preserved fallback) for backends go-openai serves poorly.

### 3. Transient 503s

Gemini free tier returns `503 UNAVAILABLE "high demand"` intermittently — not a
client bug. Retry/backoff is a backend-experience concern (currently none).

## Robustness strategy — per-backend quirks layer

**Decided.** See [per-backend-quirks.md](./per-backend-quirks.md).

Canonical format stays OpenAI Chat Completions (go-openai as the engine); each
backend's known deviations live in a small `Quirks` descriptor selected by an
explicit `backend` field on the profile, applied at two seams — a request-side
transform (URL images → base64) and a transport-side `HTTPDoer` wrapper (error
normalization + retry). The findings above map directly onto it: Finding 1 →
`ImagesAsBase64`, Finding 2 → `NormalizeErrors`, Finding 3 → `Retry`. The
conformance matrix here remains the measurement layer that gates per-backend
claims. Hand-rolling the client stays the reserved escape hatch, untriggered.
