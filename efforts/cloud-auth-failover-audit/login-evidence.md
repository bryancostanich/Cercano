# Configuration-preserving login and attempt ownership

## Implemented

Interactive login now obtains a single-use LoginAttempt from the shared credential service. Starting a newer attempt for the same profile cancels the old attempt; only the current, uncanceled attempt can commit. Closing an obsolete attempt cannot cancel its replacement. Direct credential replacement, deletion, and backend replacement invalidate pending attempts. Refresh commits do not invalidate an ongoing interactive login; the eventual login may supersede refreshed credentials.

The config service owns a CloudLogin transaction. Begin snapshots the selected profile, then releases the config lock before browser/device interaction. Commit holds the config lock, verifies the selected profile still matches, commits through the credential owner, and applies only authorized setup mutations. Reauthentication performs no configuration mutation, no provider rebuild/model writeback, and no config persistence/broadcast that could change active selection. Initial setup retains its creation/activation behavior and honors explicit model pins.

A regression exposed a remove-and-recreate case: checking profile equality alone let an obsolete login commit after the identical profile was recreated. That test failed before the fix. Relevant config mutations now cancel pending login ownership, including removal/recreation and full config changes, while unrelated changes and primary selection do not invalidate another profile's login.

Both existing provider login handlers use this ownership path. Their flow contexts are canceled by supersession, and their error frames no longer forward arbitrary callback text, transport URLs, or token endpoint descriptions.

A separate, provider-neutral ReauthenticateCloud RPC accepts an explicit existing profile and client attempt ID. It cannot create a profile, convert an API-key profile, select a model, or activate a profile. Separating it from onboarding is compatibility protection: an older server returns Unimplemented rather than ignoring a preservation flag and executing setup. The client adapter never falls back to an onboarding RPC, validates returned profile/attempt identity, and cancels its stream on terminal completion or mismatch. Attempt IDs correlate frames only; they do not confer the server's commit capability.

Bindings were regenerated using source/proto/generate.sh.

## Verification

Passed full package suites for internal/hostsvc/credentials, internal/hostsvc/config, internal/hostsvc/providers, internal/server, internal/worker, and pkg/agentclient. The CLI internal/ui suite also passed after the protocol/client additions.

Passed race-enabled focused login, reauthentication, and credential tests for the shared service, config, server, and client.

Tests cover:

- Single-use commit, cancellation, superseded attempts, old-attempt cleanup, independent profiles, and backend/credential replacement.
- Preservation of complete configuration, including model pin, endpoint, provider fields, active profile, and backup; initial setup behavior; missing/mismatched profiles; concurrent profile mutation; removal and recreation.
- Complete local-endpoint flows for both providers, with correlated initial and terminal events and actual credential persistence. The browser callback fixture runs asynchronously because the loopback page waits for token exchange before responding; a synchronous fixture was corrected after its timeout exposed that ordering.
- Actual in-process RPC transport for supported responses and older servers, with no unsafe setup fallback, rejection of mismatched identity, and stream cleanup after terminal events.
- Sanitized login error frames and continued loopback cleanup on failed initial sends.

No live-provider credentials were used. The server tests use synthetic credentials, local token endpoints, and a local browser callback.

## Not complete yet

The CLI still needs to use ReauthenticateCloud through the shared confirmation queue and stale-confirmation reconnect lifecycle. Existing UI login modals do not yet have complete attempt ownership integration. The inference recovery gate, request-scoped fallback decisions, worker error metadata transport, and request continuation remain pending. This checkpoint implements their safe login primitive, not the full end-to-end expired-login behavior.
