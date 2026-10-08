# Staged capability probe — native verification

Source checkpoint 19d1a5f5970b. Native matrix:
https://github.com/cercano-ai/Cercano/actions/runs/37733663080
Windows/Linux/macOS pass. Existing lifecycle, launch, private entry, CLI and
TUF/site fixtures remain green; Unix race checks pass.

Prepared receipts retain image/directory identity. Revalidation checks binding,
length and digest before the fixed offline `version --updater-protocol` query.
It never invokes private update execution. Tests use a copied owned Go test
executable to exercise ready/not-ready/legacy/oversized/timeout responses and
refuse changed or replaced images before creating a probe process. Strict JSON
checks reject malformed, duplicate, extra, missing, null, wrong-type and future
protocol data. Child stdout/stderr memory, execution time and pipe waits are
bounded; the owned child is reaped. No sandbox/descendant-tree guarantee or
publisher authentication is inferred from these tests.

The actual agent still reports execution_ready:false; real update execution is
not connected. Next pending Phase5 work is verified TUF target acquisition and
archive validation before activation.

Delegation connectivity, not a run-wide token allowance, is currently blocking
that next slice: two fresh implementation dispatches failed with remote connection
reset. A prior local endpoint refusal briefly recovered (a fresh delegated Read
completed), then the substantive retries failed. No network, runtime, route,
billing or budget configuration was changed. No new acquisition package appeared
in the worktree during those attempts. No live agent/installation changes.
