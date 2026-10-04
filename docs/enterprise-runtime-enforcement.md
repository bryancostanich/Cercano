# Enterprise runtime enforcement

The running host owns the enterprise connection and installs this authorization
boundary before creating model providers. The [enterprise CLI](enterprise-connection.md)
controls that owner. Standalone hosts retain personal settings; managed hosts
require current authorization before every model request. Shared-skill discovery and retrieval
now use a pinned turn snapshot. [Managed routing](enterprise-managed-routing.md)
selects administrator task defaults and their explicit fallback order.

## What the boundary checks

Each model adapter checks the request immediately before sending it to the
provider. The identity consists of four fields: provider, endpoint, model, and
local or external placement. A matching model name alone is insufficient.

The check runs again for retries and fallbacks, including retries inside an SDK.
Policy denials are terminal permission errors. They cannot trigger another
provider or endpoint as an automatic fallback. Managed requests also reject
redirects, which could otherwise send a prompt to a different endpoint.

OpenAI-compatible, Anthropic, Responses, Bedrock and Ollama clients use this
boundary. Direct llama-server, mistral.rs and Ollama engine calls also use it.
For supervised local runtimes, the check resolves the actual loaded catalog
model at the serving endpoint. A wire alias such as mistral.rs's `default` does
not stand in for the loaded model's identity.

`enterprise.Manager.Authorize` reads the latest usable policy on every attempt.
It does not authorize from a turn's pinned bundle. Tightening the policy therefore
blocks the next call even when the turn began with older defaults. A temporary
outage retains authorization only until the original lease deadline. Explicit
denials, failed verification, expiry, logout and an unverified restart deny use.

## How workers obtain permission

The host selects `RunManagedTurnWithSettings` for managed work. Older workers
without this RPC fail with an update-required error. The host never retries through
the older, unrestricted turn protocol.

A worker sends only the physical route's metadata to the host and waits for the
host's current decision before sending the model request. Enterprise tokens do
not enter the worker. Replies apply to one request ID and are not cached for
later calls. A missing reply, cancellation or host disconnection denies the call.

After accepting managed work, a production worker also installs a process-wide
guard. Any request that loses its turn context is denied. The worker cannot
later switch itself to standalone execution. Policy errors retain their terminal
classification when local inference is proxied back through the host.

## Why check at the transport

Checking only when a provider is constructed would miss later policy changes.
Checking in the routing layer would be simpler to maintain, but would miss a
provider SDK's internal retries and direct engine calls. Checking the physical
HTTP request covers those paths and verifies the endpoint and model actually
being used. The cost is a protocol-specific request decoder and additional
adapter integration tests. No new dependency is needed.

Routing selects from the turn's pinned administrator defaults and explicit
fallback order. The transport independently checks current restrictions, so an
old default cannot authorize work after an administrator removes that route.

## Verification

Tests use local HTTP servers and in-memory gRPC connections. They verify allowed
calls, all four route identity fields, blocked redirects, local model aliases,
revocation before a retry, forbidden fallbacks, and direct engine calls. Worker
tests verify that authorization crosses the stream, an older worker is rejected,
and late or unmatched replies cannot authorize another request. Signed-policy
tests cover expiry, revocation, restart and outages.

These tests do not contact a real model provider, Google Workspace or Keychain.
They establish the boundary's behavior. The host integration also has a local RPC
test from browser login through actual HTTP model authorization and logout. The
administration interface and reproducible two-company workflow are implemented.
The workflow covers retries, fallback, policy updates, rollback, temporary outage,
actual lease expiry and renewal, host recreation, and member revocation. Live
Google and Keychain acceptance remain separate checks.
