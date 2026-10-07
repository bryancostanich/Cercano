# Simplified one-shot foundation — current verification

Approved direction: a short-lived update utility, no new listening service,
transport credentials or updater daemon. Normal client/agent connections stay
unchanged. The former service/authentication proposal is superseded.

Latest native run:
https://github.com/bryancostanich/Cercano/actions/runs/37564868940
Commit 5d3c5f0ea2de. Native updatecoord, pkg/update and TUF/site fixtures pass on
Windows, Linux and macOS. Unix race tests pass. Windows-specific agentclient
launch-lock tests and vet also pass in separate steps so a later successful
PowerShell command cannot hide a failed test.

## Implemented

- Installation-scoped shared/exclusive file-lock primitive with context
  cancellation, stable never-unlinked pathname, idempotent release and owned
  subprocess death/reacquisition tests. No guessed age-based takeover.
- One-shot execution core bound to the trusted store's directory and exact
  installation/operation IDs. Re-read after lock acquisition; reject stale or
  terminal jobs; compiled backend callback only, never metadata commands.
- Success requires persisted completion. Backend failure recording is bounded
  and independent of UI cancellation; recording errors are surfaced. A reproduced
  late-failure race could alter a replacement operation; current checks refuse it.
- Optional progress reports omit raw errors/paths and do not change state on
  errors/panics. Reporters must return promptly; actual output/process wiring
  remains work and must not make UI disconnection kill an operation.
- The existing Windows agentclient auto-launch path now takes a real LockFileEx
  lock instead of the prior nil/no-op. Its exported coordination API matches the
  Unix shape; Unix implementation/namespace is unchanged. Temporary-directory
  tests cover cancellation, contention, release, parallel starts and child death.

## Explicit remaining work

This is not yet an executable installed updater or complete launch integration.
There is no helper entrypoint/process-copy/bootstrap wiring, real backend
activation, safe idle/admission barrier, installed-process restart or Update UI.
The per-installation update lock and legacy auto-launch lock still need coherent
ordering/integration; a parent must not hold a lock while waiting on its child
for the same lock. Old clients that do not participate cannot be assumed excluded.

Windows ACL/ownership validation is not supplied by Unix-style 0600 bits.
Preflight pathname checks are not a sandbox against a hostile same-user owner
replacing directories. Real backend receipts and process identity remain separate
checks. No current installation, running agent, package registration or production
signing/feed settings were changed during these tests.

Delegation failed partway through the Windows-lock slice; partial edits were
preserved, reviewed and tested directly. A handle attribute check alone was not
claimed to detect followed symlinks: pathname identity and cancellation are
rechecked. Tests use owned subprocesses/temporary directories only.
