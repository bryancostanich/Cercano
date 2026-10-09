# SQLite activation journal — native verification

The user approved extending the existing updater SQLite store rather than adding
a separate journal file. Native matrix:
https://github.com/cercano-ai/Cercano/actions/runs/37956073035
Commit 71e186bd19a2. Windows/Linux/macOS pass; Unix race checks pass.

Schema 3 adds activation_journals. Exact validated schema-1/schema-2 stores migrate
without resetting counters, operations, policy or dismissal records. Tests cover
populated legacy data, sequential migration, transactional failure rollback,
concurrent openers and refusal of foreign/future/corrupt stores. No live database
was opened or migrated.

The journal API records immutable operation-bound activation facts, explicit prior
selection or explicit none, target/stage/artifact identifiers and a revisioned
checkpoint. Writes validate the current allocated operation and target in the
same transaction. Compare-and-swap prevents stale revisions; superseded writers
and changes to immutable facts refuse.

Review caught ordinal checkpoint ordering permitting unsafe skips and changes
after completion. Explicit legal transitions now prevent success before health,
rollback after health and terminal rewrites. Prior-none records cannot claim a
rollback/restoration of a nonexistent prior selection. Stored fields must all be
explicit and non-null; malformed records are refused without rewriting bytes or
revision. Tests cover all fields and the transition table.

This is durable intent/storage only. It does not prove filesystem effects happened
and does not perform version selection, activation, cleanup, restart or recovery.
Selection-file reconciliation and executor integration still require separate
implementation and crash-boundary tests. The ordinary application's connection
policy is unchanged. No main merge, release or live installation change occurred.
