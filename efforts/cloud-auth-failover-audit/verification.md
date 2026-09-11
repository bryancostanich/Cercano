# Authentication recovery — implementation and verification

## Implemented flow

Expired or rejected subscription credentials produce structured login-required failures, including the named credential profile and authentication method. That identity survives SDK wrapping, in-band stream error frames, and worker credential transport. Store/configuration failures, transient refresh failures, API-key rejection, and permission denial remain distinct.

The CLI explicitly opts into recovery. A conversation-bound, one-shot gate pauses the owning inference call before cloud failover or alternate-tier fallback. The user can log in, explicitly select a configured compatible fallback, cancel, inspect details, or cancel and return to chat. A provider/profile-specific successful login notification comes from the shared credential service, not an unverified client assertion. It wakes concurrent requests for the same profile without resurrecting canceled requests or affecting other profiles. Resolved events remove active and queued prompts.

One configuration-owned credential service serves host providers, workers, rebuilt adapters, and login writes. Refreshes stage their writes against a generation; an old refresh cannot overwrite replacement credentials. Login attempts are single-use capabilities. Later attempts, cancellation, backend replacement, and relevant profile changes invalidate obsolete capabilities. Reauthentication preserves configuration and active selection; onboarding remains a separate operation.

Fallback is selected at the interrupted inference boundary. The explicit choice is scoped to the user turn, including subsequent inference iterations needed to finish it, and never stored on a shared provider or in default routing. A subsequent user turn gets its configured route again. Completed tools are not replayed. A selected fallback failure does not restart the whole tool loop. Incompatible tool/image requests are not offered a cloud fallback; the cross-tier path additionally checks known context budgets and prepares the actual local runtime only after authorization. Routing changes while a gate is pending stop the stale request rather than authorizing its old destination.

Reconnect uses the existing confirmation lifecycle: retain the decision and captured retry text, mark lost waiters stale, and require explicit fresh-turn retry. Late login frames cannot affect a newer modal or restart canceled work. A stale fallback choice is single-use and matched to the new turn, source profile, and displayed destination.

## Requirements-to-tests map

| Requirement / audited defect | Durable coverage |
|---|---|
| Claude expiration through SDK URL wrapping | llm/anthropic/auth_recovery_test.go |
| Transient refresh versus rejected grant, malformed/missing store | anthropicauth/recovery_test.go; chatgptauth/recovery_test.go; llm/credential_error_test.go; secrets/missing_test.go |
| HTTP and in-band authentication errors | llm/anthropic/auth_recovery_test.go and stream_auth_test.go; llm/responses/auth_recovery_test.go and stream_auth_test.go |
| Authentication status/code outranks misleading error prose | llm/responses/auth_diagnostics_test.go |
| API-key and permission failures do not launch subscription login | both provider auth_recovery tests; llm/credential_error_test.go |
| No implicit backup before a decision; login/fallback/cancel; bounded retry | inference/resilience/auth_recovery_test.go |
| Cloud fallback choice remains within one turn, not shared configuration | TestExplicitFallbackIsScopedToOneTurnContext |
| No incompatible image fallback | TestAuthFallbackDoesNotOfferIncompatibleBackup |
| Already completed tools run once across login, local fallback, or cancel | runner/TestRecoveryDoesNotReplayCompletedTools |
| No replay after stream events have escaped | TestAuthRecoveryAfterPartialOutputDoesNotReplay |
| Recovery-channel failure is not a retryable provider failure | llm/TestRecoveryChannelFailureDoesNotBecomeProviderRetry |
| Unified refresh, generation checks, cancellation, replacement, commit failure | hostsvc/credentials/service_test.go |
| Canceled queued login cannot supersede another attempt | hostsvc/credentials/login_cancel_test.go |
| Single-use login ownership and unrelated-profile independence | hostsvc/credentials/login_test.go |
| Host login supersedes worker refresh for both providers | worker/TestWorkerRefreshAndHostLoginShareCredentialOwner |
| Stable owner through backend/provider replacement | hostsvc/config/credentials_test.go; hostsvc/providers/credential_lazy_test.go |
| Missing-login primary/backup construction is lazy | hostsvc/providers/credential_lazy_test.go; worker/credential_lazy_test.go |
| Full config preservation; profile removal/recreation invalidation | hostsvc/config/cloud_login_test.go |
| Both real login flows against local synthetic endpoints | server/cloud_reauthentication_test.go |
| Loopback leak before Wait and device-login cancellation | server/claude_login_cleanup_test.go; TestSupersededDeviceLoginStopsBeforePollingOrSaving |
| Safe one-shot gate ownership, cancellation, shared login completion | server/authentication_gate_test.go |
| Changed routing cannot authorize stale fallback | TestAuthenticationFallbackRejectsChangedRouting |
| Typed worker error transport; duplicate/mismatched responses | worker/credential_failure_test.go |
| Worker recovery request/reply resumes its inference call | worker/TestWorkerAuthenticationRoundTripResumesSameInference |
| Unsupported workers cannot downgrade subscription requests | TestSubscriptionRequestsRequireAuthenticationAwareWorker |
| Unsupported server cannot turn reauthentication into onboarding | agentclient/TestReauthenticationUnsupportedPeerNeverFallsBackToSetup |
| Response identity and stream cleanup | agentclient/cloud_reauthentication_test.go |
| Explicit client opt-in and resolved-event transport | agentclient/authentication_test.go |
| Queue, cancellation, reconnect, stale retry, late frames, fallback scope | CLI internal/ui/authentication_test.go, plus existing confirm_stale_test.go and confirmation suites |
| Both legacy login modals reject stale same-profile frames | TestLegacySameProfileFramesAreAttemptScoped |
| Token endpoint, HTTP auth body, login-stream and serialized-cause redaction | llm/httpx/oauth_test.go; provider auth_diagnostics tests; server/TestLoginFailureNeverEmitsUnstructuredSecrets; llm/TestCredentialErrorJSONDoesNotSerializeCause |

## Additional findings during implementation

The final integration audit caught several gaps beyond the initial probes, with failing tests before their fixes: an authentication-aware worker endpoint was not enabling the callback; resolved prompts were retained; an existing modal could have its attempt invalidated by reopening it; a queued canceled login could supersede another active attempt; HTTP authentication could be misclassified by context-overflow prose; in-band stream authentication errors bypassed login recovery; raw authentication response bodies and JSON-serialized causes could expose diagnostics; and routing changes could leave stale fallback authorization usable.

A race-enabled UI golden test also exposed a wall-clock dependency: the canned tool duration rendered `1ms` instead of the committed `<1ms`. The fixture now freezes that duration and its derived result summary. Production rendering and the committed golden were not changed.

## Verification runs

- A full server `go test ./... -count=1` run passed during integration; an earlier run exposed an outdated MCP mock interface, which was updated with explicit unsupported methods.
- Affected server package suites and new focused regressions passed after their respective changes.
- The full CLI UI package passed with `-race` after fixing the deterministic golden fixture.
- The final server/CLI module tests, final targeted race run, build checks, and generated-binding reproducibility check are recorded in the completion update below.

## Limitations and operational boundaries

No real account login was performed. Most tests use synthetic stores, fake providers, local HTTP endpoints, and in-process RPC transports. One initial Responses stream fixture unexpectedly used the route's production-pinned backend despite specifying BaseURL; it sent only a synthetic invalid token and received 401. The fixture was corrected to override the package-private endpoint explicitly, then reproduced and verified the intended in-band failure locally. No real credentials were used or changed.

Once stream events have escaped to the collector, recovery repairs credentials but does not automatically replay that response. The user receives a terminal recovery result and can explicitly submit a fresh request. Reconnect recovery likewise starts a fresh turn; it is not durable continuation or an exactly-once guarantee across process restarts.

Noninteractive callers fail rather than wait for a prompt they cannot answer. Hosts require an authentication-aware worker protocol for subscription profiles; older workers fail closed with an update requirement. Reauthentication has a separate RPC so an old server cannot ignore a preservation flag and perform onboarding instead.

No merge, push, installation, running-agent restart, or production credential mutation is part of this effort. The active binary remains unchanged until separately installed.
