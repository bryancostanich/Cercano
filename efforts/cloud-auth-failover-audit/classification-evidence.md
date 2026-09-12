# Credential classification implementation evidence

## Reproduced before changes

Ran `go test ./internal/llm/anthropic ./internal/llm/responses -run '^TestAuthRecovery' -count=1` in source/server with new regressions against the original implementation. Observed:

- Expired Claude credential without refresh: `network`, through an SDK URL wrapper, rather than actionable login expiry.
- Rejected Claude refresh grant: generic `auth`, carrying the raw token endpoint response and relying on prose matching.
- HTTP 403: generic `auth`, without separating permission denial from subscription expiry.
- ChatGPT refresh connection reset: `auth`, rather than network failure.
- ChatGPT canceled token source: cancellation wrapped inside normalized authentication failure rather than passed through.
- ChatGPT opaque store/source error: `auth`, rather than credential infrastructure/configuration failure.

A second focused probe, `go test ./internal/llm/anthropic ./internal/llm/responses -run TestAuthRecoveryMissingSource -count=1`, demonstrated two more cases before fixing them:

- Claude subscription configured without a source: SDK-wrapped configuration error became `network`.
- ChatGPT subscription configured without a source: authorization succeeded and set an empty `Bearer ` header.

## Implemented contract

`llm.CredentialError` carries non-secret provider, named credential profile, authentication method, class, and reason. Its cause remains available through errors.Is/As but is not interpolated into printable diagnostics. Sources expose `CredentialProfile()` for adapters handling a later HTTP 401.

New classes distinguish `login_required`, `credential_error`, and `permission_denied` from API-key `auth`, transient `network`, and service `busy`. These new nontransient classes are not eligible for implicit retry or fallback. This is only a fail-closed foundation: the interactive recovery gate has not yet been implemented.

Both sources recognize absent storage separately from an unavailable store, using the portable fs.ErrNotExist sentinel from the memory and keyring adapters. Malformed stored bundles are configuration failures. Expiry without refresh and known rejected refresh grants carry login-required reasons. Refresh network and service failures remain transient; arbitrary OAuth error prose cannot request login.

Token endpoint failure parsing reads a bounded body and retains only allowlisted OAuth error codes, never error_description or arbitrary error/body text. Provider normalization prioritizes structured credential failures before SDK URL-wrapper network detection. Subscription HTTP 401 carries the actual source profile; API-key 401 remains noninteractive authentication failure, and HTTP 403 is permission denial. Missing configured token sources fail locally without submitting empty credentials.

Legacy tests that expected string matching or blanket authentication classification were updated to assert the approved behavior. Their replacements retain unknown-error and cause-propagation coverage rather than simply deleting assertions.

## Verification

Passed affected authentication, secrets, provider, llm, httpx, and resilience package suites, plus runner, worker, and hostsvc/providers compatibility tests. New tests exercise actual Claude SDK wrapping, both real credential-source implementations with synthetic stores and local token endpoints, source/profile identity, API-key versus subscription HTTP status handling, cancellation, malformed storage, missing keyring entries versus access denial, bounded endpoint parsing, and diagnostic redaction.

`TestAuthRecoveryNoImplicitBackup` verifies streaming and non-streaming resilience calls invoke the primary once, perform no retry sleeps, and invoke the backup zero times on login-required failure.

Passed race-enabled tests for internal/anthropicauth, internal/chatgptauth, internal/llm, internal/llm/httpx, internal/inference/resilience, and internal/secrets. No live account or credentials were used. Full module suites and the final cross-interface recovery matrix have not been run; they belong to later integration phases.

## Remaining work and limitations

This does NOT complete the full audit or recovery flow. Worker transport still needs structured credential metadata, and host construction still needs consistent missing-profile handling. Shared refresh/login coordination, configuration-preserving reauthentication, cleanup, and attempt ownership remain pending. The CLI and runner do not yet provide the new login/fallback/cancel continuation. Raw errors outside the credential-source/provider path still require the planned full diagnostic-sanitization audit.

The earlier baseline tooling issue did not block this direct implementation. A subsequent git-only delegated inspection also returned usable results; do not assume the original delegation failure is still universal.
