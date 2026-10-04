# Cercano Enterprise implementation status

The target is a reproducible local V1 demonstration: an administrator manages
members, teams, model policies and shared skills, and the running Cercano host
applies those settings to a developer's work. The implementation is not complete.

## Implemented foundations

The enterprise service has Google sign-in, manual membership and team APIs,
customer isolation in PostgreSQL, versioned policies and text-only skills,
signed policy leases, publication and rollback, audit events, and native client
login with PKCE and rotating credentials. Each developer has at most one team
per organization. Enterprise service PRs 1 through 12 are merged, including the
five-area administration console, reproducible workflow, recovery checks, and
shared organization request limits.

The Cercano connection preview stores enterprise credentials separately in
Keychain, verifies signed policies and complete skill bundles, and handles
revocation, lease expiry, restart and logout. Contract PR 38 and connection
preview PR 39 remain open for Bryan.

[PR 40](https://github.com/bryancostanich/Cercano/pull/40) adds physical-request
authorization to model adapters and direct engines, plus a managed worker protocol
that asks the host before every attempt. See [runtime enforcement](enterprise-runtime-enforcement.md).

The host integration now installs that boundary at startup, owns background
synchronization and the credential lock, and exposes login, status, sync, logout
and explicit standalone selection through local RPC. A durable profile blocks
work after credential loss or logout. Each user inference request pins a verified
bundle. Warm workers are replaced when the profile changes between managed and
standalone execution. Shared skills now appear in the agent and MCP catalogs with namespaced IDs,
source and version. Main and delegated agents retrieve the pinned text through a
read-only tool. Settings-aware workers reject missing or incomplete bundles and
never receive enterprise credentials. Signed update/removal/rollback tests and
real worker tool-loop tests cover these paths. See [shared skills](enterprise-shared-skills.md).

Managed routing now applies administrator task defaults to the main conversation,
delegation, local co-processor tools, watchdog calls, and compaction. It uses the
existing provider adapters and retry engine, binds only matching provider
credentials, and follows the explicit administrator fallback order. Locked
settings ignore personal defaults; unlocked choices must still be approved.
Nested work keeps the turn's selected settings, while physical requests always
check current authorization. See [managed routing](enterprise-managed-routing.md).

The cross-repository workflow harness now exercises administrator publication,
native sign-in, the real host and streamed agent loop, shared-skill retrieval,
physical fallback, model and skill updates, rollback, temporary outage, reconnect,
host recreation and membership revocation for two disposable companies. Its
optional long run waits for the actual signed lease to expire. Run it with the
enterprise repository's `scripts/test-client-workflow.py`; see
[the workflow guide](https://github.com/keithballinger/cercano-enterprise/blob/main/docs/client-workflow-demo.md).

The integrated workflow exposed two defects corrected in this branch: an
explicit streaming-RPC model choice was not forwarded to the runner, and the
initiator could send its final response before the broker's lossless queue had
finished forwarding tokens. Worker capability negotiation now also rejects an
older worker that cannot preserve explicit managed model choices.

The macOS CLI now exposes `enterprise policy`, `enterprise skills`, and
`enterprise skills --skill ID`, with readable output and optional JSON. They
read verified host settings and assigned text without exposing credentials or
starting inference. Blocked connections cannot inspect cached assignments.

The developer's status now includes the organization name and assigned team from
its last applied policy response. `cercano enterprise status` prints a readable
summary; `--json` preserves machine-readable output. Missing metadata is distinct
from having no team. Expired or revoked access remains blocked regardless of the
displayed membership. These display fields are authenticated by HTTPS, checked
against the verified scope, and never used to grant inference permission.

The enterprise [deployment guide](https://github.com/keithballinger/cercano-enterprise/blob/main/docs/deployment.md)
covers configuration, migrations, proxy requirements, request limits, health,
retention, rollback and privacy. Local operational tests restore a real PostgreSQL
backup into a fresh database, recheck isolation, invalidate restored credentials,
and load overlapping signing-key rotation stages. The two-company workflow has
also passed with a real 15-minute authorization expiry and subsequent renewal.

Client synchronization failures now send a bounded code and version through a
separate best-effort endpoint. They never acknowledge an incomplete bundle. The
console retains the previous applied revision and skills, shows recovery guidance,
and clears the error after successful application. The two-company workflow also
interrupts skill downloads and checks failure reporting and recovery.

## Remaining work, in order

1. **Complete live acceptance.** The reproducible fixture substitutes Google
   identity, memory-backed credential storage and deterministic model generation.
   Live Google consent, macOS Keychain prompts and a packaged-client process
   restart still need a configured machine and user interaction. Administration
   screenshots are saved in the enterprise repository's console demo guide.
2. **Finish the V1 acceptance audit.** Check each agreed requirement against
   current code, tests and artifacts, run final CI, and keep local evidence
   distinct from checks needing live Google or Keychain access.

## Repository and deployment rules

Test and merge enterprise service changes after CI passes. Leave all Cercano
PRs open for Bryan; never merge them or enable auto-merge. Dependent branches can
continue implementation while he reviews.

Keep membership and teams manual for V1. LDAP, directory synchronization and
SCIM are deferred. Production deployment, paid infrastructure and changes to
real customer organizations require separate authorization. The two proposed
live pilot customers are separate Google Workspace organizations:
`qeetbarecords.com` and `impossiblecomputing.com`.
