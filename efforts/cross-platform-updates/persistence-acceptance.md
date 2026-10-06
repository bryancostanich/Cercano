# Persistence foundation: native fixture acceptance

Latest successful matrix:
https://github.com/bryancostanich/Cercano/actions/runs/37547197476
Commit: 4e37628c0fe2. Windows, Linux and macOS pass native updatecoord/pkg-update
fixtures and TUF/site fixtures. Linux/macOS also pass the race detector; all pass
updatecoord vet. This is package-level acceptance, not an installed application
update rehearsal or Windows ACL certification.

The transactional adapter now makes Start/Apply/current snapshot durable under
one SQLite write transaction. Same-target requests deduplicate, conflicting
requests and stale operation callbacks refuse, and recovery survives reopening.
A real-state exploration checks 61 reachable snapshots across all16 lifecycle
states with 2806 event variants. Baseline probes found both falsely rejected
legal states and forged completion without health; those guards were corrected.

Schema2 adds version-scoped dismissals through an additive schema1 migration.
Legacy operations, counters and delegation data are retained. Migration is
validated under its lock, including the already-upgraded no-op branch. Rollback
injection, concurrent handles and concurrently launched processes cover failure
and contention. Unknown/future/altered schemas still refuse without reset.

Dismissals are installation/channel/source/version-bound. Clearing records an
inactive revision-bearing row, not deletion that would reset the revision on
recreation. Missing after a clear carries its revision token so legitimate
re-dismissal can proceed without allowing stale saves/clears. Every row, including
inactive rows, is strict-validated. Cross-install keys cannot read or clear local
records. The schema2 draft was corrected before its first native publication;
no deployed schema2 or live user database was rewritten.

Native CI found test-only UTC Location identity comparison and Linux directory
capitalization errors; both were fixed with actual failing-run evidence. A ref
update race was resolved by checking the remote SHA without force-pushing; the
next real CI coverage commit queued normally. No main merge or release occurred.

Remaining Phase3 work: corroborated real installation observations and backend
probe boundaries. Still absent: runtime/UI integration, actual package updates,
privileged handoff, process drain/restart, Windows ACL enforcement and complete
installed-system activation/recovery. No live user preferences or installation
files were migrated by these tests.
