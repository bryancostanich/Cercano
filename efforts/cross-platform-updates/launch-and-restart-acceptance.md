# Launch and Homebrew restart integration — native fixture acceptance

Native matrix: https://github.com/bryancostanich/Cercano/actions/runs/37708931367
Commit 66567cf77dff. Windows/Linux/macOS matrix passes. Verbose Windows evidence
was read, not inferred from aggregate success: parent-exit, parent-hard-kill,
permitted-job-close survival, bound exited-process observation, output handling,
and restrictive/default-context refusal tests all PASS rather than SKIP.

Production launch primitive starts only an explicit trusted executable with fixed
caller-built argv. It opens regular output logs/private new files or discards
output, supplies null stdin, uses independent session/console creation settings,
and asynchronously reaps its own child without cancelling/supervising it.
Windows requires OS-permitted breakaway; no fallback silently puts the updater
back under a restrictive parent job. CI's default job denies breakaway. Positive
proof uses fixture-owned permissive jobs, not changed host/runner policy. Actual
product launch eligibility and actionable refusal remain necessary integration.

The existing macOS Homebrew restart hook now calls the idle-only RPC, using its
verified process identity. Old agents return Unimplemented with guidance; no
legacy force-stop fallback. Deadline/transport/cancellation means uncertain,
not proof of busy/alive. A bounded local reinspection can continue only on
positive exit evidence; different/unknown identity refuses. Later unknown
samples cannot be overridden by an earlier alive sample. Normal client
connections and user-initiated legacy shutdown behavior were not version-gated.

Fixture corrections did not weaken production behavior: fd churn retries require
positive ownership proof; Windows liveness uses bound process handles, not PID
existence; completion uses one reaper with broadcast notification; fixture protocol
files are immutable create-if-absent publications. Full/absent data and conflicting
writer checks pass. POSIX mode assertions are not claimed as Windows ACL proof.

No user installation, running agent, package registry, production signing keys,
release feed or main branch was changed. There is still no installed updater
entrypoint, self-managed activation transaction, Windows/Linux package backend
integration, Update UI or complete installed-system acceptance.

Next explicit review gate: stable launcher/helper bootstrap and self-upgrade
contract. The process primitive intentionally accepts an already verified image;
it does not choose whether that image is an internal mode of the existing agent
or a separately shipped helper, nor does it copy/replace installed files.
