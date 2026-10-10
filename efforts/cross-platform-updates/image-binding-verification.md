# Staged executable classification — native verification

Native matrix: https://github.com/cercano-ai/Cercano/actions/runs/38027840346
Source fec0d9d321653a1239de632f93bbc3071437f2db. Windows/Linux/macOS pass.
The user authorized pushing the feature branch for this verification.

The classifier checks supported Mach-O/ELF/PE executable shapes, architecture,
header/table/section bounds and parser allocation counts without executing code.
macOS arm64 and Linux/Windows amd64/arm64 fixtures pass, including actual owned
cross-compiled Go binaries inspected on the host. Static Linux PIE without an
interpreter remains conservatively refused. Race tests, vet and approximately
1.64 million fuzz inputs completed locally.

CheckStaged binds required manifest member lengths and SHA-256 values to bounded,
immutable bytes before classification. Reads are confined under an explicit
staging root and reject symbolic links, nonregular files, changed bindings and
unexpected lengths. Unix opens are nonblocking to avoid FIFO replacement hangs.
This is a read-only observation, not durable authority for a later launch.
Callers still authenticate the manifest and protect its directory; checks do not
claim publisher identity, embedded version identity or runtime health.

Initial native run38027550354 passed image checks but failed an older safe-stop
concurrency fixture. A deterministic probe showed the existing contract permits
Aborted while admission is sealed but shutdown has not committed. Tests now allow
only that retryable race outcome, require a winner, require every post-commit
repeat to succeed, and retain the exactly-once callback/sealed-gate assertions.
Both focused tests passed100 race-enabled repetitions. No production stop
semantics changed.

Still pending: activation integration, platform signing/version checks, health
and recovery execution, package/backend completion and Update UI. No live agent,
installation, production trust material or release feed was modified.
