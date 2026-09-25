# deepinfra_infer capability

Status: implemented (partial-recovery effort; no commit yet)

## What it is

`deepinfra_infer` is a built-in capability that runs ONE inference request
against DeepInfra's **native** inference endpoint:

    POST https://api.deepinfra.com/v1/inference/{model}

It is NOT the OpenAI-compatible chat-completions surface: no message
wrapping, no chat roles. The body is the raw JSON object the caller passes
in `input`, exactly as the model's API page documents — arbitrarily nested
JSON is fine; only attachment bindings must be top-level `input` fields.
The model id is arbitrary (`vendor/model`, no whitespace), so any hosted
DeepInfra model — embeddings, rerankers, image models, chat — is reachable
with the same tool.

API shape verified against DeepInfra's official Python SDK source (read,
not a live paid call):

- Root endpoint constant `ROOT_URL = "https://api.deepinfra.com/v1/inference/"`:
  https://github.com/deepinfra/deepinfra-python/blob/main/deepinfra/constants/client.py
- Models POST the raw JSON body:
  https://github.com/deepinfra/deepinfra-python/blob/main/deepinfra/models/base/base.py
- Bearer-token auth header:
  https://github.com/deepinfra/deepinfra-python/blob/main/deepinfra/clients/deepinfra.py

## Surfaces and tier

- Tier: **W** (spends paid quota; confirm-gated like other write-tier tools).
- Surfaces: `SurfaceAgent | SurfaceMCP` — one capability implementation,
  exposed on the standalone agent loop (via `agentadapter`) and the MCP
  plugin catalog (via `capabilities.RegisterMCPCatalogSource`, picked up
  automatically from the surface mask in `builtins.init`).

## Arguments

| Field         | Type   | Notes |
|---------------|--------|-------|
| `model`       | string | required. Plain model path (`vendor/model`); whitespace, schemes, hosts, `..`, percent-encoding, and query/fragment chars are rejected (the id is interpolated into the endpoint URL). |
| `input`       | object | optional. Arbitrary raw JSON body, arbitrarily nested. Cercano adds nothing to it. Numbers are preserved exactly (no float coercion). |
| `profile`     | string | optional. Name of a configured DeepInfra cloud profile. Required to disambiguate when several are configured (ambiguity error lists the candidates). |
| `attachments` | array  | optional. `[{path XOR attachment_id, field, encoding}]` — binds content into `input[field]` as a string. Exactly one source per entry; a field colliding with an existing input key or used twice is rejected. `encoding` is **explicit** and required: `utf8` (validated text), `base64` (standard base64 of the raw bytes), or `data_uri` (`data:<mediatype>;base64,<…>`; the media type comes from the conversation store for `attachment_id`, or `http.DetectContentType` on the local file's bytes). Content flows straight into the HTTP body, never through model context. |
| `settings`    | object | optional. `{timeout_seconds: 1..600}` (default 120; out-of-range values are rejected, not clamped), `{output_dir}` (workdir-relative artifact dir; default `.cercano/deepinfra`). |

## Credentials

The profile list is scanned for configured DeepInfra profiles — eligible
ONLY when the base URL's host is EXACTLY `api.deepinfra.com`
(`config.DeepinfraHost`); the `deepinfra` provider label alone is never
authority, so a mislabeled profile pointing at another host must not receive
the key. The API key is read from the OS keychain, keyed by profile name,
through the `capabilities.Secrets` seam:

- Host: `Server.InstallCapabilities` wires `cfgSvc.Secrets()`
  (`source/server/internal/server/server.go`).
- Worker: `buildWorkerToolSvcWithDiagnostic` opens its own keychain handle
  via `openWorkerSecrets()` (`source/server/internal/worker/worker_dispatch.go`);
  the worker is a same-machine child process, and a headless failure yields
  nil → the capability errors clearly.

Safety properties:
- **No anonymous fallback**: a missing/unreadable key is a hard error.
- **Ambiguity error**: 0 configured profiles → configuration guidance;
  >1 without a selector → ambiguity error listing profile names.
- **No paid retries**: exactly one HTTP attempt, ever. Any non-2xx status
  (>= 300 or < 200) fails status-only — no provider body text is echoed,
  and there is no retry.
- **No unsafe redirects**: `CheckRedirect` refuses any redirect; the
  non-followed 3xx is reported status-only. (Following one could leak the
  Authorization header to another host.)
- **Credential never leaks**: the exact key is redacted from the context
  copy (`[redacted:key]`) even if the provider echoes it back in the JSON
  response, and it is never part of an error message (transport errors carry
  method/URL only — the key lives only in the Authorization header).
- Transport is one https POST to `api.deepinfra.com` only.

## Size caps

- **Args cap**: oversized `call.Args` (> 32 MiB) are rejected BEFORE JSON
  decoding allocates.
- **Per-attachment cap**: each attachment (local file or `attachment_id`)
  is bounded at 8 MiB decoded, read with a Max+1 bound (overflow is an
  explicit error, never truncation).
- **Aggregate cap**: the encoded request body (input JSON + every encoded
  attachment) is capped at 32 MiB, enforced with a RUNNING budget while
  attachments are bound (peak memory is bounded by the cap plus at most one
  attachment's encoding), then re-checked exactly on the final body.
- **Response cap**: the response read is bounded at 64 MiB with a Max+1
  bound; overflow is an explicit error, never a silent truncation.

## HTTP handling order

Status and Content-Type are checked BEFORE any body read: a non-2xx status
(including a non-followed 3xx) or a `text/event-stream` response is rejected
immediately, status-only — large provider bodies are never read and never
echoed, and nothing is ever retried.

## Bounded output artifacts

The **full raw response** is written to
`<workdir>/.cercano/deepinfra/deepinfra-<random><ext>` via exclusive create
(`os.CreateTemp` — an existing file is never clobbered; dir 0755, file
0600), extension mapped from the response Content-Type, and its path + HTTP
status are reported in the result note. **The raw response is saved exactly
as received — JSON is NOT parsed/re-encoded and a base64 payload inside the
JSON is NOT auto-decoded**; callers decode it themselves if needed.

The copy returned to model context is **sanitized**: any string value
longer than 512 bytes or base64-like (64+ chars of `[A-Za-z0-9+/]` with
optional `=` padding) is replaced with `[redacted:string:<len>]` /
`[redacted:base64:<len>]`; the exact API key is replaced with
`[redacted:key]`. Non-JSON/binary responses put nothing in context — the
note carries artifact metadata only. Oversized text is cut at a rune
boundary under the 32 KiB context cap.

## Verification

- `go build ./...` (source/server): OK.
- `go vet` on capabilities/builtins: OK.
- `go test ./internal/capabilities/... ./pkg/config/... ./internal/toolstack/...`:
  all OK, including the deepinfra_infer tests (native endpoint path, body
  passthrough, Bearer key from the configured profile, ambiguity error,
  missing-key error, explicit attachment encodings, single-attempt/no-retry,
  redirect refusal, base64 redaction + artifact completeness, model-id
  validation, status-only non-2xx/3xx/SSE failures, credential redaction,
  attachment_id cap, running aggregate budget, args cap, local data_uri MIME
  detection, description wording) and the registration count.
- Endpoint URL builder is a package var (`deepinfraEndpoint`) so tests
  exercise the real request path against `httptest` servers; **no live paid
  DeepInfra calls were made and none are required** by these tests. API
  shape links above point at the official SDK source.

## Limitations

- JSON POST only: no polling, no streaming/SSE, no multipart, and no URL
  downloads (nothing is fetched; the raw response is saved locally instead).
- `attachment_id` resolves only against the ACTIVE agent conversation's
  image store (the same seam `inspect_image` reads): on the MCP surface the
  attachment lookup follows the MCP use path wiring, and in the worker it
  sees only IDs registered for the CURRENT turn — stale/unknown IDs fail
  closed.
- Attachment binding is top-level `input[field]` only (no nested paths);
  the `input` body itself may be arbitrarily nested.
- Responses are saved raw; JSON contents (including base64 fields) are NOT
  auto-decoded for the caller.
- The artifact file is not garbage-collected by this tool.
- gofmt flags some pre-existing unformatted files elsewhere in the tree
  (untouched); the new files are gofmt-clean.
