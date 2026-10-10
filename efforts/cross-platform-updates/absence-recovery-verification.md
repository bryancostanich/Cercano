# First-activation absence recovery — model and primitive verification

The user approved restoring explicit prior absence under exact-selection,
installation-exclusion and owned-candidate-completion safeguards. No version
directory or user-data deletion is authorized by this behavior.

Native matrix: https://github.com/cercano-ai/Cercano/actions/runs/38030212014
Source 9288a9efb7861d949807c4d2990d49287a759990. Windows/Linux/macOS pass.

The journal now has distinct absence-intent and terminal absence-restored
checkpoints. They are valid only for explicit prior-none, only before health
success, and cannot bypass intent or leave terminal state. Existing prior-version
rollback remains separate. Record fields/database schema are unchanged; older
readers reject the unknown checkpoint rather than silently interpreting it.

The read-only reconciler distinguishes removal needed, positive absence awaiting
confirmation, and terminal consistency. Unknown, foreign or contradictory
observations refuse. These recommendations are not proof a candidate stopped
and do not authorize a filesystem deletion.

A package-private, currently uncalled production primitive removes only the
fixed selection entry. It requires a live guard, protected existing directory,
exact parsed descriptor/raw digest, pinned file and parent identity, and a final
cancellation/content/path check. It preserves replacements, including identical
bytes, and unrelated files. Windows keeps a delete-sharing identity handle until
removal and closes it before confirming absence. Any failure after removal is
reported as committed-but-durability-uncertain, never a fictional precommit failure
or silent rollback. The model assumes protected directories and cooperating
writers, not a filesystem compare-and-swap against hostile same-user writes.

Owned temporary fixtures cover exact removal, stale expectations, cancellation,
identical replacement preservation, missing guards, invalid absence expectations
and post-removal uncertainty. Race/vet and native tests pass. This is not a
power-loss durability guarantee.

Remaining before enabling recovery: integrate an actual owned-candidate completion
proof, keep journal intent/acknowledgement under one lease, and add subprocess
interruption/reconciliation tests for removal. The primitive is deliberately
unexported, has no production callers and cannot yet be reached by the updater
entrypoint. No installed selection, real candidate process, version directory or
user data was changed.
