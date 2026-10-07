# Filesystem evidence and live-control approval boundary

Latest green native matrix:
https://github.com/bryancostanich/Cercano/actions/runs/37551347138
Commit 6e7b49f754ad. Native tests/vet pass on Windows, Linux and macOS; Unix
race tests and all-platform TUF/site fixtures pass. A Windows-only concurrency
failure was reproduced locally with an exclusive legacy-database lock and fixed
by bounded full-open retries on typed SQLITE_BUSY. Foreign/invalid schemas are
not retried as empty state. A separate Windows test-only provider-applicability
assumption was corrected; unavailable applicable providers still fail closed.

The probe package observes actual filesystem paths, identities and metadata,
rejects stale facts and competing same-manager source claims, and corroborates
receipts from injected providers. Manager receipt authenticity/command parsing
is NOT implemented by this layer: real Homebrew/APT/Chocolatey adapters remain
Phase7 work. No production code calls the new probe or storage modules yet.
Phase3's value, persistence and filesystem foundation is checkpointed, but live
provenance integration is not claimed complete.

## Next explicit security gate: local update-control authentication

Read-only inspection found the agent's main TCP gRPC listener constructed in
source/server/cmd/cercano/main.go with recovery interceptors but no per-user
transport authentication. The existing ShutdownAgent handler accepts the request
and schedules shutdown. This is not a global agent-security audit; it establishes
that the existing connection alone cannot be treated as authorization for new
update-control actions.

The approved Phase4 plan requires an authenticated local control boundary and
operator approval before adding a transport/security boundary. Two viable paths:

1. Recommended: private local OS IPC for update control (Unix-domain sockets on
   macOS/Linux, named pipes on Windows), restrictive ownership/access controls,
   and peer user-identity checks. Do not use a port being on localhost as identity.
   All paths into update/drain control must enforce the boundary; the ordinary
   agent RPC must not become an unauthenticated proxy/confused deputy.
2. Separate loopback gRPC control endpoint using protected per-user credentials
   and authenticated server identity. Reuses existing RPC tooling but requires
   secure credential/endpoint discovery, rotation and endpoint-spoof resistance.
   A bare bearer token sent to an unverified local port is not sufficient.

Both apply only to new update controls. Do not introduce a version-compatibility
handshake or block existing normal client/agent connections. The helper runs
only as needed for an explicit operation; no always-privileged background service
is installed. Package-manager elevation remains separately authorized through
normal OS mechanisms. TUF signing keys are never reused as IPC credentials.

Windows ACL and peer-token validation, Unix permissions/peer checks, lifecycle
coordination and native adversarial tests are required whichever path is chosen.
Unsupported old agents receive actionable restart guidance, not a forced update
shutdown or a blanket version connection gate. No live endpoints or credentials
have been introduced while this choice is pending.
