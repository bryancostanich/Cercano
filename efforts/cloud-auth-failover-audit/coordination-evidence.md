# Shared credential ownership evidence

## Reproductions

Temporary, deterministic local-endpoint probes against the previous implementation observed two independent sources refreshing one single-use token twice, and an in-flight refresh replacing a newer login with `stale-refresh-result`. Both probes failed their desired assertions before the implementation. No live endpoints or real credentials were used.

The user approved one shared credential service, not synchronization added to the keychain backend. The approved choice is recorded in spec.md.

Additional permanent regressions reproduced two concrete failures before their fixes:

- `TestSubscriptionBackupDoesNotFetchCredentialsDuringBuild`: both subscription providers performed one eager credential fetch and discarded the configured backup when login was missing.
- `TestClaudeLoginSendFailureClosesLoopback`: the loopback listener still accepted connections after the first stream send failed.

While testing the new service, `TestNewWaiterDoesNotInheritAbandonedFlightCancellation` exposed a new concurrency edge: a live caller joining a canceled flight received another caller's cancellation. The implementation now waits for the abandoned work to finish and resolves again with the live caller's context, with bounded attempts.

## Implemented ownership

`internal/hostsvc/credentials.Service` is the one host owner. The configuration service creates it once, exposes it through `Credentials()`, and returns its write facade from `Secrets()` when a backend exists. Backend replacement preserves the owner and invalidates existing flights. The low-level keychain and memory-store contracts are unchanged.

Host primary and backup providers and worker credential resolution obtain lightweight source views from this owner. The worker's source caches and separate secrets pointer were removed; its constructor now receives credentials through configuration rather than another store argument. All non-test raw provider-source construction is confined to the shared service.

Provider-specific refresh logic runs against a snapshot/staging store. Only the owner commits a staged write, after verifying that the profile generation and flight still match. Login writes and deletion invalidate the generation and wake waiters immediately. A transport that ignores cancellation can finish later but cannot write stale credentials. Existing provider views follow backend replacement rather than retaining retired raw storage.

Network refresh runs outside profile mutation locks with a bounded shared context. Individual cancellations remove only that waiter; the last waiter cancels the refresh. Unrelated profiles do not wait for another profile's network operation. Repeated supersession or abandoned-flight recovery is bounded, rather than creating an unbounded automatic loop.

Subscription provider construction is lazy for both host and worker backups. It neither reads the keychain nor refreshes a configured but unused subscription backup. Missing credentials remain an actionable login-required error when inference uses the provider. Static-key, proxy, and Bedrock construction behavior is retained.

Claude login now defers loopback cleanup immediately after successful flow creation, covering failures before the handler reaches Wait.

## Verification

Passed full package suites for:

- internal/hostsvc/credentials
- internal/hostsvc/config
- internal/hostsvc/providers
- internal/cloudfactory
- internal/worker
- internal/server

Passed race-enabled credentials, config, provider, and worker suites, plus focused race-enabled server/login integration coverage. Repeated the credential concurrency tests with the race detector. The cross-owner integration test exercises both Claude and ChatGPT: a worker begins refresh, the host login write supersedes it, the worker returns the new login immediately, and a rebuilt host view observes that same credential and account identity.

Permanent tests also cover multiple rebuilt source views sharing one refresh, ChatGPT account preservation, a late non-cooperative refresh being discarded, canceled versus surviving waiters, abandoned-flight recovery, independent profiles, stable owner/backend replacement, failed refresh commit, lazy missing-login routes, and listener cleanup.

## Remaining scope

This checkpoint does not complete Phase 3 or the full recovery flow. Profile-preserving reauthentication, ownership of overlapping interactive login attempts, full credential-error transport over the worker protocol, login/fallback/cancel confirmations, inference continuation, and reconnect handling remain pending. The shared service provides the common ownership boundary for those integrations; its current credential replacement behavior is not a claim that the UI authentication lifecycle is complete.

Delegated write work failed tool validation in this step and was implemented directly. An attempted automated review returned no code-based verdict and was not counted as verification.
