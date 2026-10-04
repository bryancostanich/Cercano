# Cercano Enterprise implementation status

The target is a reproducible local V1 demonstration: an administrator manages
members, teams, model policies and shared skills, and the running Cercano host
applies those settings to a developer's work. The implementation is not complete.

## Implemented foundations

The enterprise service has Google sign-in, manual membership and team APIs,
customer isolation in PostgreSQL, versioned policies and text-only skills,
signed policy leases, publication and rollback, audit events, and native client
login with PKCE and rotating credentials. Each developer has at most one team
per organization. Enterprise service PRs 1 through 5 are merged.

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
standalone execution. The implementation still needs managed defaults, skill discovery, and the full
administration-to-developer demonstration below.

## Remaining work, in order

1. **Apply managed defaults and shared skills.** Pin a complete bundle for each
   turn, use its approved defaults and ordered fallbacks, and expose namespaced
   skill IDs with source and version. Apply the latest restrictions before each
   physical call. Keep administrator settings separate from personal settings.
2. **Finish the administration interface.** Provide People & teams, Model policy,
   Shared skills, Devices & sync, and Audit. Make publication, affected teams,
   version rollback, and synchronization status understandable. Add any missing
   service endpoints needed for those flows.
3. **Demonstrate the complete workflow locally.** Use two disposable customer
   organizations. Demonstrate allowed and blocked calls, forbidden fallback,
   policy and skill updates, rollback, outage and expiry, reconnect, membership
   deactivation, and customer separation. Capture administration screenshots.
4. **Finish operational documentation and acceptance checks.** Document setup,
   migrations, backup and restore, signing-key rotation, health checks, request
   limits, audit retention and log privacy. Run the relevant tests and CI and
   distinguish local evidence from checks needing live Google or Keychain access.

## Repository and deployment rules

Test and merge enterprise service changes after CI passes. Leave all Cercano
PRs open for Bryan; never merge them or enable auto-merge. Dependent branches can
continue implementation while he reviews.

Keep membership and teams manual for V1. LDAP, directory synchronization and
SCIM are deferred. Production deployment, paid infrastructure and changes to
real customer organizations require separate authorization. The two proposed
live pilot customers are separate Google Workspace organizations:
`qeetbarecords.com` and `impossiblecomputing.com`.
