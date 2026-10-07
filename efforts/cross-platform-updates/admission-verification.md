# Work admission and actual-lifetime tracking — partial lifecycle integration

Native matrix:
https://github.com/bryancostanich/Cercano/actions/runs/37572841346
Commit bb5084d269c8. Windows/Linux/macOS focused fixtures pass; Unix race checks,
existing updater/TUF fixtures and Windows launch-lock tests pass as well.

Implemented an internal admission gate with actual lifetime counting. While
work exists, it leaves admission open so nested calls and existing work can
finish. It atomically pauses only when the count reaches zero. Cancellation of
an idle wait does not cancel work or leave a paused gate. Releases are idempotent.
There is no public pause/control RPC and no utility shutdown wiring yet.

Tracked entrypoints: unary/streaming requests, direct tool/capability invocation,
runtime start/installation, login flows, and download request preparation. The
streaming handler waits for its actual runner before returning; the gate is not
based merely on broker generation or subscriber count.

The runtime manager separately retains asynchronous download jobs until actual
cleanup. A cancelled status does not count as completed I/O. Superseded jobs
remain counted independently of their replacement. Existing jobs can be bound
at startup; failed binding rolls back acquired leases. Unsupported managers are
recorded as lacking coverage rather than assumed idle. Post-download warm-up
retains its lease before its goroutine starts, avoiding a scheduling gap.

A direct worker teardown probe found Kill called twice sequentially and twelve
times concurrently despite its idempotence contract. sync.Once now makes all
callers wait for one teardown/reap; no repeated process-group signal is sent.
Tests use seams/owned fixtures and do not kill the developer's agent or workers.

Remaining before live preparation is exposed: finish the remaining background
work/entrypoint audit, explicit user-approved cancellation, safe agent shutdown
and owned-runtime cleanup, launch-lock ordering, and one-shot process/bootstrap
integration. The gate must not be treated as a complete idle certificate until
that coverage is established. No new updater service/listener, credentials,
version gate, package action or live installation change was introduced.
