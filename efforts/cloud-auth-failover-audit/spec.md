# Cloud Authentication Recovery and Failover Audit

## Problem and motivation

Expired cloud login currently behaves like an ordinary provider failure. Authentication errors can be retried incorrectly, silently switch cloud providers or inference tiers, or fail without a usable login prompt. The existing prompt depends on failover narration, assumes a canonical Claude profile, and has no continuation handshake with the failed request.

The audit traced credential storage and refresh, provider error normalization, host and worker provider construction, credential transport, both fallback layers, and CLI login and confirmation handling. It also identified adjacent refresh coordination, login-attempt lifecycle, listener cleanup, and error-sanitization issues. Those are in scope because they can undermine recovery itself.

The existing confirmation reconnect behavior is the recovery baseline. The CLI retains pending tool confirmations through disconnection, marks them stale after reconnect, and lets the user explicitly resubmit the captured request rather than approving a dead waiter. Authentication must extend that lifecycle rather than introduce separate persistence or discard the user's pending decision.

## Goals

When subscription login credentials require user action, pause the affected inference request before either cloud-provider failover or cloud/local tier fallback. Present the correct provider and named profile, with Log in, Use fallback when configured and permitted, and Cancel. Dismissal cancels; it never implicitly authorizes fallback.

After successful login, resume the interrupted inference call if its owning request is still live. Do not restart the entire tool loop merely to recover a live inference call, replay already completed tools, or duplicate streamed output. Recovery must be bounded: rejected replacement credentials must not create an automatic login/retry loop.

If reconnect has made the confirmation stale under the existing confirmation lifecycle, preserve the captured retry request and explain that proceeding starts a fresh turn. Explicit login-and-retry authenticates first, then resubmits that request. Explicit fallback may start a fresh turn with the selected request-scoped authorization. No action may signal the old waiter or resurrect canceled work.

Reauthentication must update credentials for the affected profile without changing its active status, model selection, endpoint configuration, provider identity, or unrelated configuration. Successful automatic refresh remains silent.

Complete the full audited scope, including refresh coordination, stale login events, listener cleanup, and error sanitization. Inspection-only findings must be reproduced or otherwise validated before fixing them; rejected hypotheses must be recorded honestly rather than converted into speculative changes.

## Scope and evidence

Credential sources and error normalization must distinguish actionable login expiry or rejection from transient refresh-network failures, credential-store failures, malformed configuration, API-key authentication failures, and authorization denial. Do not treat all HTTP 403 responses as expired login. Preserve actionable authentication identity and reason through SDK wrapping and worker transport without exposing secrets.

The provider resilience layer and the outer turn runner both permit fallback today. Both must respect the same authentication pause and explicit fallback outcome. Cover primary and backup providers, configurations with no backup, direct host execution, worker execution, and streaming and non-streaming inference. Authentication recovery must not depend on an emitted prose notice.

The current worker credential protocol stringifies errors; the recovery distinction must survive that boundary explicitly. Missing credentials must also be handled consistently rather than disappearing behind an absent-provider sentinel or silent removal of a configured backup. Unused backup providers must not prompt merely because they are configured.

The CLI must support both Claude and ChatGPT subscription login and named profiles. Pending authentication decisions must not be silently discarded when another confirmation is open. Integrate their ownership, captured retry text, stale status, and cancellation with the shared confirmation lifecycle. Retain existing confirmation behavior for tool permissions, details, and chat redirection; authentication-specific choices must not be mislabeled as tool approval.

Concurrent requests for the same credential identity must coordinate refresh and login without racing rotating refresh tokens. Coordination must cover host and worker consumers, provider rebuilds, and replacement credentials saved by interactive login. Shared authentication does not imply shared permission to resume or fall back: each request retains its own cancellation and fallback outcome. Unrelated profiles must remain independent.

Login frames must be associated with the correct attempt. Late frames cannot complete a newer modal or restart canceled work. Login resources, including Claude's loopback listener, must be released on early failure, cancellation, and normal completion. Error reporting and logs must retain useful diagnostic information without including tokens, authorization codes, credential payloads, or unsanitized token-endpoint responses.

Audit evidence includes existing passing package tests and temporary probes demonstrating immediate backup invocation, missing login notices for no-backup and backup-authentication errors, Claude expiry being classified as a retryable network error through SDK wrapping, ChatGPT refresh-network failure being classified as authentication failure, and CLI provider mismatch and confirmation suppression. Temporary probes were removed; durable regression tests are part of implementation. No live-provider authentication was exercised during the audit.

Relevant areas include source/server/internal/anthropicauth, chatgptauth, llm/anthropic, llm/responses, inference/resilience, hostsvc/providers, hostsvc/config, worker, runner, and server login handlers, plus source/clients/cli/internal/ui. Existing reconnect behavior is documented by ui/confirm_stale_test.go and implemented in model.go's connection-state and confirmation handlers.

## Constraints and invariants

Ordinary network, overload, and quota failures retain their existing retry and fallback policies. API-key failures must not launch subscription login. Cancellation is terminal for the affected continuation. No client, detached client, or canceled request may cause an unbounded authentication wait or implicit provider switch; noninteractive callers must receive an actionable structured failure when no supported interactive decision channel exists.

Fallback authorization applies only to the interrupted request, or its explicitly authorized fresh retry after reconnect. It does not modify active profiles, default routing, permission settings, or authorization for subsequent unrelated requests. Only configured, policy-permitted fallback is offered. An authentication failure on the selected fallback is not permission to silently move to yet another provider.

The confirmation framework already treats reconnect conservatively as a potentially lost server-side turn. This effort extends that contract; it does not promise restoration of an in-process continuation after agent restart. The CLI must clearly distinguish live-call continuation from explicit fresh-turn retry.

Any client/server or host/worker interface change must have integration coverage and safe behavior when the peer does not support recovery. Unsupported recovery must surface a clear error, not silently fail over. Do not persist new copies of credentials in recovery records or log sensitive transport data.

## Decisions

### Full audit, implemented in phases

The user selected the full audit rather than core recovery alone: the flow will not be reliable if adjacent authentication defects remain. Phase boundaries should isolate validation and integration without deferring known dependencies out of scope.

| Axis | Full audit — selected | Core recovery only — not selected |
|---|---|---|
| Coverage | Recovery plus refresh, login-event, cleanup, and sanitization hardening | Recovery flow with adjacent issues deferred |
| Cost | Broader lifecycle and concurrency verification | Smaller initial implementation |
| Risk | More change surface, managed through phases and regression tests | Remaining defects can undermine the new flow |
| Rationale | Establish consistent authentication recovery end to end | Insufficient for the agreed scope |

### Explicit login, fallback, or cancel

The user approved Log in / Use fallback / Cancel. Fallback appears only when configured and permitted. Closing the confirmation or pressing Escape cancels; it never authorizes fallback. Selection is request-scoped and does not change active configuration.

| Axis | Login, fallback, or cancel — selected | Login or cancel — not selected |
|---|---|---|
| Control | User explicitly chooses whether another provider may handle the request | User must restart separately to use another provider |
| Complexity | Three outcomes carried through both fallback layers | Two outcomes |
| Risk | Requires strict request-scoped authorization | Less routing complexity but unnecessary interruption |
| Rationale | Prevent silent provider changes while preserving a useful escape route | More restrictive than necessary |

### Reuse existing reconnect semantics

After reviewing the existing y/n/d/c confirmation implementation, the user accepted extending it. Preserve pending decisions and captured retry text, mark lost waiters stale, and require explicit fresh-turn retry rather than pretending to resume a dead continuation. The earlier proposed binary choice between dropping recovery and building durable request persistence was withdrawn because it overlooked this existing behavior.

The viable path within this effort is to generalize the shared confirmation lifecycle for authentication. A separate reconnect policy would duplicate ownership and lose established behavior; durable execution is not needed to match the existing contract. There is no new persistence decision to approve.

### One shared credential service

After reproducing duplicate refreshes across separate sources and an old refresh overwriting a newer login, the user explicitly selected a shared service: “definitely shared. this needs to be unified.”

| Axis | Shared credential service — selected | Coordination in secrets storage — not selected |
|---|---|---|
| Ownership | One host service for providers, workers, and credential replacement | Storage owns refresh/write synchronization; login still needs another owner |
| Integration | Existing config owner exposes the same service and a coordinated write facade | Expand the backend storage contract and its implementations |
| Risk | Every host credential path must use the service rather than a raw backend | Splits the authentication lifecycle across storage and login ownership |
| Rationale | Unifies lifecycle ownership while retaining provider-specific refresh logic and keychain persistence | Write protection alone does not unify recovery |

The service coordinates per stored profile, stages refresh writes against a credential generation, and rejects obsolete results after replacement. Interactive login must not wait for an old network refresh to finish. Individual request cancellation must not cancel other active waiters. Rebuilt providers and worker requests receive views of the same owner, not independent refresh caches.

## Acceptance criteria

An expired or revoked subscription credential pauses before either fallback layer, with the correct provider and profile, across host and worker execution. No-backup and backup-authentication cases remain actionable. Transient refresh failures and API-key or permission failures are not misrepresented as subscription expiry.

Approving login updates only the intended credentials and resumes exactly the live inference continuation, without replaying completed tools or already emitted content. Login rejection, repeated credential rejection, cancellation, and unsupported interaction terminate or return control explicitly rather than looping or silently falling back.

Explicit fallback uses only configured and permitted routing for the authorized request. Cancel and dismissal cause no backup invocation. New unrelated requests receive no inherited fallback permission.

An existing permission confirmation does not erase an authentication request. Concurrent same-profile failures coordinate authentication while preserving individual request outcomes. Different profiles cannot receive each other's tokens, login frames, decisions, or resumptions.

Reconnect preserves the pending decision and retry text. A stale action never calls the old waiter. The UI labels fresh-turn retry honestly; late login success cannot resurrect canceled work. Existing stale tool-confirmation, details, input, and redirection tests remain valid.

Refresh rotation is coordinated across the supported host and worker paths. Login resource cleanup is tested on cancellation and early failure. Configuration preservation and secret redaction have explicit regressions.

Verification includes focused source and provider tests, cross-boundary host/worker and runner integration tests, CLI confirmation and login lifecycle tests, and race-enabled concurrency tests where supported. Test doubles must model SDK wrapping, token endpoint rejection, refresh-network failure, and transport behavior. Any remaining live-provider validation limitations must be stated in the completion report.

## Non-goals

This effort does not add durable execution or exactly-once whole-turn restoration across process restarts, new authentication providers, new default fallback destinations, or changes to ordinary tool permission policy. It does not install, restart, or push a self-development build without the appropriate execution authorization.
