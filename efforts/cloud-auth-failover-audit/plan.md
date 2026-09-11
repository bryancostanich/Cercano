# Cloud Authentication Recovery and Failover Audit

Implement the approved spec.md without reopening its decisions: full audit scope; pause before both fallback layers; explicit Log in / Use fallback / Cancel; preserve profile configuration; extend the existing stale-confirmation reconnect lifecycle. Implementation is not authorized until this plan is approved.

All server paths below are relative to source/server unless explicitly qualified. CLI paths are relative to source/clients/cli. Run commands within their respective Go modules, not the untracked root module. Phases are ordered dependencies; no partial phase constitutes a shippable recovery flow. Record evidence and test results as execution proceeds, and checkpoint solved units with explicit paths. Do not push, install, or restart the running agent as part of routine verification.

## Phase 1 — Establish regression evidence and lifecycle boundaries

Objective: preserve the audit findings as reproducible tests and locate the exact ownership boundaries before implementing fixes. Files: internal/anthropicauth and internal/chatgptauth tests; internal/llm/anthropic/normalize_test.go and client tests; internal/llm/responses tests; internal/inference/resilience/resilience_test.go; internal/runner/core_test.go; internal/worker tests; CLI internal/ui/confirm_test.go and confirm_stale_test.go. Tests: expiration through SDK wrapping, refresh-network classification, missing prompt paths, wrong-profile login, concurrent refresh, late login frames, and early listener failure. Use fake endpoints, synthetic credentials, controllable streams, and synchronization barriers rather than live accounts or timing sleeps.

- [x] Inspect current branch and user changes through delegated git operations; create an isolated feature worktree from the intended base and copy only the approved effort artifacts as needed.
- [ ] Confirm the approved spec still matches the current checkout; inspect intervening changes without overwriting user work.
- [ ] Map direct host, worker, primary, backup, streaming, non-streaming, and outer-tier fallback call paths and their request cancellation owners.
- [ ] Turn the audit probes into durable regressions that first demonstrate the existing failures.
  - [ ] Capture SDK-wrapped Claude expiration and ChatGPT transient refresh-network errors with their actual current classes.
  - [ ] Count backup and alternate-tier calls to show silent fallback, including no-backup and backup-authentication cases.
  - [ ] Demonstrate provider/profile misrouting, discarded auth confirmations, and missing live continuation.
- [ ] Reproduce the inspection-only refresh race, stale login frames, listener cleanup failure, and unsafe error-body logging before implementing each fix; record any disproved premise.
- [ ] Document live-call versus fresh-turn retry ownership and the point after which streamed output or tool effects make automatic replay unsafe.
- [ ] Record the baseline package results and expected failing regressions; do not suppress failures to obtain a green baseline.

## Phase 2 — Make authentication reasons and identity reliable

Objective: represent actionable login failure independently of prose and ordinary provider errors. Files: internal/llm/error.go; internal/llm/anthropic/normalize.go and subscription authorization; internal/llm/responses/client.go and normalization; internal/anthropicauth/source.go and token exchange; internal/chatgptauth/source.go and token exchange; corresponding tests. Tests: source-to-adapter errors through wrapping, missing and expired credentials, rejected refresh, malformed stores, endpoint failures, API-key 401, subscription 401, and permission-denied 403.

- [ ] Introduce a shared typed authentication failure contract carrying non-secret provider, named profile or credential identity, authentication method, and actionable reason; preserve the wrapped cause for safe diagnostics.
- [ ] Classify known token-endpoint rejection separately from transient transport or service failure using structured endpoint responses where supported.
- [ ] Preserve actionable expiry/rejection through Anthropic SDK wrapping instead of letting a URL wrapper turn it into a retryable network failure.
- [ ] Stop converting every ChatGPT token-source error into authentication failure.
- [ ] Distinguish subscription login from API-key errors and authorization denial; avoid blanket conversion of 403 into login-required.
- [ ] Bound diagnostic payloads and redact token endpoint responses and sensitive URLs before they reach logs or UI.
- [ ] Keep ordinary network, overload, and quota retry behavior unchanged and prove it with existing and new tests.
- [ ] Verify all new classification regressions independently of CLI behavior.

## Phase 3 — Coordinate credentials and preserve login configuration

Objective: ensure a completed login or refresh produces usable, current credentials for every execution path without races or profile mutation. Files: internal/hostsvc/providers/providers.go; internal/hostsvc/config/config.go; internal/worker/host.go; internal/anthropicauth and internal/chatgptauth sources and stores; internal/server/claude_login.go and chatgpt_login.go; related login flow and loopback code. Tests: concurrent host/worker refresh, rebuilt providers, simultaneous login replacement, unrelated profiles, cancellation, store failure, and exact configuration preservation.

- [ ] Unify refresh coordination for the actual credential identity across host provider construction, worker credential requests, and provider rebuilds; reuse existing ownership facilities rather than adding a competing cache.
- [ ] Ensure a refresh started before interactive login cannot overwrite newer credentials; reread or invalidate cached credentials at the coordinated boundary.
- [ ] Release refresh locks before waiting for human interaction; allow individual canceled waiters to leave without breaking unrelated consumers.
- [ ] Route primary and backup credential resolution through the consistent path, resolving unused backup credentials lazily.
- [ ] Preserve actionable missing-credential failures rather than hiding a configured profile behind an absent provider or silently removing its backup.
- [ ] Separate reauthentication of an existing profile from first-time profile setup; preserve model, endpoints, active profile, and unrelated settings on reauthentication.
- [ ] Close Claude loopback resources on every exit, including stream-send failure before waiting for the callback; verify equivalent device-login cancellation cleanup.
- [ ] Run race-enabled credential and login ownership tests with deterministic synchronization.

## Phase 4 — Carry recovery decisions across server and worker interfaces

Objective: connect an owning request to a structured authentication decision and its outcome, preserving identity across process boundaries. Files: source/proto/agent.proto and generated bindings via source/proto/generate.sh; internal/worker/host.go, worker.go, proxies.go, and event conversion; internal/server request/confirmation handling; pkg/agentclient; internal/runner event and request plumbing. Tests: real in-process client/server and host/worker transport round trips, cancellation, duplicate responses, wrong owners, and unsupported peers.

- [ ] Extend the existing event/confirmation plumbing with authentication-specific payloads and correlated outcomes, rather than encoding login requests in notice text or disguising them as permission approval.
- [ ] Carry typed credential failures across CredentialResponse so workers do not reconstruct every failure as an untyped string.
- [ ] Bind recovery decisions to their conversation, owning request, provider/profile, and login attempt; reject stale, mismatched, and duplicate responses.
- [ ] Represent Login, Use fallback, and Cancel distinctly; carry fallback availability from actual routing policy, not from CLI guesses.
- [ ] Keep completion waiters owned by the live request and clean them up on cancellation, shutdown, or lost ownership.
- [ ] Make unsupported interaction and incompatible peers return actionable failure rather than hanging or silently falling back; cover missing/unknown protocol fields and unsupported operations.
- [ ] Regenerate bindings with the repository script and update client and worker adapters together.
- [ ] Verify transport preserves reasons and identity but does not introduce credential copies into confirmation payloads or recovery state.

## Phase 5 — Pause and resume at the inference boundary

Objective: enforce the agreed behavior before either fallback layer while resuming only the live interrupted inference call. Files: internal/inference/resilience/resilience.go; internal/llm/error.go fallback policy; internal/runner/core.go; internal/agent inference/tool-loop integration; internal/hostsvc/providers/providers.go; internal/worker execution wiring. Tests: request-level integration with fake primary, backup, and local providers; streamed partial output; already-completed tool calls; repeated rejection; no interactive client.

- [ ] Route actionable login failures into the request-owned recovery gate before ordinary retry or cloud failover; wire it for streaming and non-streaming calls.
- [ ] Prevent the outer runner's alternate-tier fallback from bypassing an unresolved, canceled, or unsupported authentication gate.
- [ ] On successful login, obtain the current credentials and retry only the interrupted inference call when replay is safe.
  - [ ] Prove completed tools are not re-executed for live recovery.
  - [ ] Prevent duplicate visible content or tool fragments when output has already escaped; surface an explicit terminal recovery result if safe continuation is impossible.
  - [ ] Bound automatic retries after login so repeated rejection cannot create an automatic prompt/retry loop.
- [ ] On explicit fallback, validate the currently permitted destination and scope authorization to this request; do not mutate default routing or active profiles.
- [ ] Treat authentication failure on the selected backup as a new actionable failure, not permission to silently try further providers.
- [ ] On Cancel, dismissal, or unavailable interaction, stop the affected request without backup invocation and release its waiters.
- [ ] Deduplicate same-profile login work while preserving separate request cancellation, continuation, and fallback outcomes.
- [ ] Remove the runner's hard-coded Claude reauthentication marker and dependence on failover narration once structured recovery is connected.
- [ ] Verify counters show zero fallback calls before explicit permission, and no inherited authorization on a subsequent request.

## Phase 6 — Integrate login with shared confirmations and reconnect

Objective: present reliable provider-aware recovery using the existing confirmation lifecycle. Files: CLI internal/ui/model.go, main_agent_driver.go, confirm_test.go, confirm_stale_test.go, claude_login_modal.go, chatgpt_login_modal.go, and login command/message definitions; pkg/agentclient login and recovery adapters. Tests: model updates and command execution against fake clients, both login providers, named profiles, mixed confirmation queues, connection events, and reordered login frames.

- [ ] Generalize confirmation ownership, captured retry text, stale status, and cleanup beyond tool-only gates without changing tool-permission semantics.
- [ ] Queue authentication requests behind existing confirmations instead of dropping them; remove canceled queued requests and avoid orphaning hidden waiters.
- [ ] Render the actual provider and profile with Log in, permitted Use fallback, Cancel, and appropriate details; Escape or dismissal resolves as Cancel.
- [ ] Dispatch Claude and ChatGPT login correctly without canonical-profile assumptions or automatic active-profile changes.
- [ ] Associate login commands and frames with attempt identity; ignore late frames from closed or replaced attempts.
- [ ] Acknowledge successful login to the owning live recovery request, not merely the modal; represent failure and cancellation without claiming work resumed.
- [ ] Preserve the confirmation and captured retry text through connection loss and apply existing stale-gate handling after reconnect.
  - [ ] For stale Login, clearly offer authentication followed by an explicit fresh-turn retry; never send success to the dead waiter.
  - [ ] For stale fallback, bind the explicit authorization to that fresh retry without granting it to unrelated turns.
  - [ ] Preserve cancellation, details, and existing chat-redirection behavior; do not substitute stale tool approval wording for authentication choices.
  - [ ] Ensure late login success may update credentials but never resurrects canceled or superseded work.
- [ ] Run the full UI package and agentclient integration tests, including existing stream-end ordering and stale-confirmation regressions.

## Phase 7 — Verify the complete matrix and hardening

Objective: prove the integrated flow, including full-audit dependencies, rather than relying on isolated green unit tests. Files: server provider, worker, runner, login and transport integration tests; CLI confirmation/reconnect tests; narrowly scoped new integration fixtures where existing harnesses cannot express the flow. Tests use synthetic credentials and local fake endpoints, not real subscription accounts.

- [ ] Exercise both providers, named profiles, direct host and worker paths, and streaming and non-streaming calls at the boundaries appropriate to each.
- [ ] Cover primary expiry, backup expiry, absent backup, missing login, rejected refresh, transient refresh failure, permission denial, and API-key failure with expected prompt and fallback outcomes.
- [ ] Verify live login success resumes once; cancel invokes no backup; explicit fallback obeys policy and cannot leak to another request.
- [ ] Exercise reconnect before choice, during login, after login success but before acknowledgement, and after stream cleanup has cleared global prompt state.
- [ ] Exercise concurrent same-profile requests, unrelated profiles, canceled waiters, provider rebuild, and refresh versus login replacement under the Go race detector.
- [ ] Inject duplicate and out-of-order outcomes, unsupported peers, store-write failure, login cancellation, and listener setup/send failure; verify no deadlock, orphan waiter, leaked listener, or unintended profile change.
- [ ] Assert synthetic secrets and raw token endpoint bodies never appear in captured logs, confirmation details, or serialized recovery errors.
- [ ] Run targeted package tests throughout; at this cross-interface integration gate run server and CLI module suites with go test ./... -count=1 and targeted go test -race for credential, recovery, worker, and UI lifecycle packages.
- [ ] Build both modules using their supported build targets without installing or restarting the agent; distinguish build success from behavior of the currently running binary.
- [ ] Record all results and residual live-provider validation limitations; investigate failures with minimal reproductions rather than weakening assertions.

## Phase 8 — Document, checkpoint, and hand off

Objective: leave an auditable implementation with no hidden operational or worktree side effects. Files: docs/agent/README.md, docs/features/cli/README.md, and this effort's implementation evidence and task status. Tests: final diff hygiene, generated binding consistency, and a requirements-to-tests review.

- [ ] Document login pause, explicit fallback scope, live versus stale retry, unsupported interactive clients, and credential/configuration preservation.
- [ ] Map every approved acceptance criterion and audited issue to a regression test or documented disproof; do not mark inspection-only findings fixed without evidence.
- [ ] Check the patch for obsolete string markers, hard-coded login profiles, duplicate refresh ownership, missing cancellation cleanup, and leaked diagnostic data.
- [ ] Checkpoint solved changes using explicit paths and conventional commit subjects; preserve all unrelated user work.
- [ ] Present the implemented behavior, verification results, remaining limitations, branch/commit, and installation status without replaying approved decisions.
- [ ] Do not push or deploy without explicit authorization. If landing is requested and requires stashing user changes, record the exact stash and restore it after landing; do not leave the user's work stashed as the default end state.
