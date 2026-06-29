# Per-Backend Quirks Layer — Design

**Status:** Designed 2026-06-28. Follow-on robustness work for the
`chat_completions` provider ([cloud-openai.md](./cloud-openai.md)); informed by
the live Gemini findings in [llm-backend-notes.md](./llm-backend-notes.md).

One `chat_completions` client (`internal/llm/openai/`, wrapping
`sashabaranov/go-openai`) serves OpenAI **and** every OpenAI-compatible endpoint
(Gemini-compat, Groq, Together, …). Those backends diverge in real, observed
ways. The first live backend we tested (Gemini) already exposed two: it refuses
to fetch image URLs, and it returns error bodies as a JSON **array** that
go-openai cannot parse. This design isolates those divergences behind a small
per-backend "fix-up" layer so the goal — a good experience on *each* backend — is
explicit and extensible, not a pile of ad-hoc branches.

## Core decision

**Canonical format = OpenAI Chat Completions.** go-openai stays the engine. Each
backend's known deviations are captured in a small `Quirks` descriptor, applied
at two well-defined seams: a **request-side** transform (before send) and a
**transport-side** wrapper (around the HTTP round-trip). Hand-rolling the client
stays the escape hatch only for a divergence even transport rewriting can't
reach — none seen yet.

Backends are identified by an **explicit field on the profile**, not by sniffing
`base_url` (decided — unambiguous, user-controllable; a proxy/gateway URL can't
mislead it).

## 1. Backend identity (config)

`CloudProfile` (`pkg/config/config.go:13`) gains an optional `Backend string`
(yaml `backend`):

```yaml
cloud_profiles:
  - name: gemini
    flavor: chat_completions
    backend: gemini        # NEW — selects the quirks set; empty → defensive default
    base_url: https://generativelanguage.googleapis.com/v1beta/openai
    model: gemini-2.5-flash
```

Values are free strings resolved through the registry (§2). Known values today:
`openai`, `gemini`, `groq`. Empty or unrecognized → the **default** quirks.

`cloudfactory.BuildCloudProvider` passes `p.Backend` into `openai.Config.Backend`
for the `chat_completions` case. No other flavor uses it.

## 2. The Quirks descriptor + registry

A new file `internal/llm/openai/quirks.go`:

```go
// Quirks captures a backend's known deviations from OpenAI Chat Completions.
// The zero value is the strict-OpenAI baseline; the registry's "" default
// turns on the defensive options that are safe everywhere.
type Quirks struct {
    ImagesAsBase64  bool        // resolve URL images to base64 before send
    NormalizeErrors bool        // rewrite array-shaped error bodies → object shape
    Retry           RetryPolicy // transient-failure retry (see §3)
    // Extension point: future fields (DropParams []string, model aliases, …)
    // are added here as new backends require them — a struct field + a table
    // entry, never a new code path.
}

type RetryPolicy struct {
    MaxAttempts int           // total attempts incl. the first; 0/1 → no retry
    BaseDelay   time.Duration // first backoff; doubles each attempt
    OnStatus    []int         // HTTP statuses that trigger a retry (e.g. 429, 500, 502, 503)
}

func quirksFor(backend string) Quirks
```

`quirksFor` resolves a backend name to its `Quirks`:

| backend  | ImagesAsBase64 | NormalizeErrors | Retry (statuses)        | Notes |
|----------|----------------|-----------------|-------------------------|-------|
| `""`/unknown | true       | true            | 429,500,502,503         | Defensive floor: safe on any compat endpoint. |
| `openai` | false          | true            | 429,500,502,503         | Native URL fetch; object errors (norm is a no-op there). |
| `gemini` | true           | true            | 429,500,502,503         | Exactly the two findings + transient 503s. |
| `groq`   | true           | true            | 429,500,502,503         | Conservative; revise once tested live. |

`NormalizeErrors` and `Retry` are on everywhere because they are **harmless when
not needed**: normalization only triggers on a non-object error body; retry only
on the listed statuses. The one genuine per-backend axis today is image handling
(`openai` passes URLs through; everyone else base64-encodes). The struct is built
to grow without growing the call sites.

## 3. Transport-side fix-ups (a wrapping `HTTPDoer`)

go-openai's `ClientConfig.HTTPClient` is an `HTTPDoer` (`Do(*http.Request)
(*http.Response, error)`). We hand it our own, wrapping the real `*http.Client`,
in a new file `internal/llm/openai/transport.go`:

```go
type normalizingDoer struct {
    next   openai.HTTPDoer // the underlying *http.Client
    quirks Quirks
}

func (d *normalizingDoer) Do(req *http.Request) (*http.Response, error)
```

Two responsibilities, both reaching the raw response *before* go-openai parses
it (which is precisely where go-openai was failing us):

- **Retry** (`quirks.Retry`): on a listed status, drain+close the body and retry
  up to `MaxAttempts` with exponential backoff from `BaseDelay`, honoring
  `req.Context()` cancellation. Requires a replayable body — buffer the request
  body once up front (chat requests are small) so each attempt can resend it.
- **Error normalization** (`quirks.NormalizeErrors`): on a non-2xx response,
  peek the body; if it's a JSON **array** (`[{"error":{…}}]`), rewrite it to the
  object shape (`{"error":{…}}`) and replace `resp.Body` with the rewritten
  bytes, so go-openai's `ErrorResponse` unmarshal succeeds and the real message
  survives. If the body is already an object or isn't JSON, pass it through
  untouched. Always restore a readable `resp.Body` for the non-error path.

This keeps go-openai as the parser of record; we only repair the inputs it's
strict about. `NewClient` builds the `*http.Client` (with the configured
timeout), wraps it in `normalizingDoer{quirks}`, and sets it as
`ClientConfig.HTTPClient`.

## 4. Request-side fix-ups (image base64)

The only request-side quirk today. In `client.go`'s `Chat`/`StreamChat` (which
have a `ctx`), when `quirks.ImagesAsBase64` is set, pre-resolve any
`BlockImage{ImageURL:…}` to base64 **before** building the request: call the
existing `llm.ResolveImageBytes(ctx, block)` and replace the block's `ImageURL`
with `ImageData` + `MediaType` (sniffed/derived). The adapter
(`messagesToOpenAI`) then emits a `data:` URI part exactly as it does today for
base64 — no adapter change beyond receiving already-resolved blocks.

Resolution lives at the client layer (it needs `ctx` and the network), keeping
`adapter.go` pure translation. For `openai` (`ImagesAsBase64:false`) URLs pass
through to OpenAI's server-side fetch as before.

## 5. Wiring summary

- `pkg/config/config.go`: add `Backend string` (`yaml:"backend,omitempty"`) to
  `CloudProfile`.
- `cloudfactory/factory.go`: `chat_completions` case passes `p.Backend` into
  `openai.Config`.
- `internal/llm/openai/client.go`: `Config` gains `Backend string`; `NewClient`
  resolves `quirksFor(cfg.Backend)`, installs the `normalizingDoer`, and stores
  the quirks on `Client` for request-side use; `Chat`/`StreamChat` pre-resolve
  images when `ImagesAsBase64`.
- `internal/llm/openai/quirks.go`, `transport.go`: new.

Everything stays behind `llm.Provider`. The factory, tool-loop, router, and
co-processor path are untouched.

## 6. Testing

- **quirks**: `quirksFor("gemini"|"openai"|"groq"|""|"nonsense")` returns the
  expected descriptor; unknown → default.
- **transport (error norm)**: an `httptest` server returns a Gemini-style array
  error with status 400 → wrapped client surfaces a parsed, human-readable
  message (not `cannot unmarshal array`). Object-shaped error → unchanged.
  Non-JSON error body → passed through, no panic.
- **transport (retry)**: server returns 503 twice then 200 → one successful
  result, three attempts, backoff observed; request body resent intact each
  attempt; `MaxAttempts` exhausted → the last error returned; ctx cancellation
  aborts mid-backoff.
- **request (image base64)**: with `ImagesAsBase64`, a `BlockImage{ImageURL}`
  routed through `Chat` is fetched (mock server) and sent as a `data:` URI;
  with it off, the URL passes through unmodified. Assert via a capturing
  `httptest` backend.
- **factory**: a `chat_completions` profile with `backend: gemini` yields a
  client whose resolved quirks match `quirksFor("gemini")`.
- **integration (gated)**: the existing live matrix
  (`client_integration_test.go`) gains an error-path assertion — a deliberately
  bad request to a compat backend returns a clean message — to prove
  normalization end-to-end. Still `INTEGRATION_TEST=1`-gated.

## Out of scope

- **Hand-rolling** the client. Reserved; not triggered by these findings.
- **Tool-calling fidelity** workarounds beyond surfacing the backend's own error
  (unchanged from cloud-openai.md).
- **Model-name aliasing** / param-dropping — the struct reserves room
  (`DropParams`, aliases) but no backend needs them yet (YAGNI).
- **SSRF hardening** of `ResolveImageBytes` — lands with the image inbound path
  (see vision-input.md §2), not here. Note: enabling `ImagesAsBase64` means the
  *server* now fetches caller-influenced URLs for more backends, which raises the
  priority of that hardening once an inbound path exists; called out, not solved
  here.
- **Responses API** (SP3) and **Bedrock** (SP4) — separate flavors; this quirks
  layer is reusable by them but not built for them yet.
