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

The next client increment adds physical-request authorization to model adapters
and direct engines, plus a managed worker protocol that asks the host before
every attempt. See [runtime enforcement](enterprise-runtime-enforcement.md) for
its coverage and limits. This boundary is not yet installed by the host's
enterprise lifecycle, so login still reports enforcement as inactive.

## Remaining work, in order

1. **Connect enterprise sign-in to the running host.** The host must own the
   connection, credential lock and background refresh for its lifetime. Route
   login, status, synchronization and logout through that owner. A saved managed
   profile must remain blocked when verification fails or credentials disappear;
   it must never silently become standalone.
2. **Apply managed defaults and shared skills.** Pin a complete bundle for each
   turn, use its approved defaults and ordered fallbacks, and expose namespaced
   skill IDs with source and version. Apply the latest restrictions before each
   physical call. Keep administrator settings separate from personal settings.
3. **Finish the administration interface.** Provide People & teams, Model policy,
   Shared skills, Devices & sync, and Audit. Make publication, affected teams,
   version rollback, and synchronization status understandable. Add any missing
   service endpoints needed for those flows.
4. **Demonstrate the complete workflow locally.** Use two disposable customer
   organizations. Demonstrate allowed and blocked calls, forbidden fallback,
   policy and skill updates, rollback, outage and expiry, reconnect, membership
   deactivation, and customer separation. Capture administration screenshots.
5. **Finish operational documentation and acceptance checks.** Document setup,
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
