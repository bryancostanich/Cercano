# Journal-to-selection switch transaction — native verification

Native run https://github.com/cercano-ai/Cercano/actions/runs/38011123744
Source f71744857fd5ab2682a12dc791981fb8afb7267b. Windows/Linux/macOS pass.

A single installation lease guard covers journal re-read, switch intent,
publication, observed target readback and Selected acknowledgement. Store and
publication directory must be associated; a regression reproduced and fixed
cross-installation publication through an otherwise valid foreign lease.
Callback-scoped guard capabilities replace the comment-only guarded publisher
bypass. Zero/expired/foreign/concurrently used capabilities refuse.

Owned subprocess fixtures exit after durable intent and after publication before
acknowledgement. Fresh store/lease recovery respectively publishes-and-acks or
acks-without-rewrite; repeated recovery is idempotent. This proves process-exit
handling, not physical power-loss durability. No health/completion is inferred.

Windows fixture directories must be provisioned privately before SQLite opens
them. Opening the database first correctly caused subsequent ACL verification to
refuse; fixtures now provision only newly owned temporary paths in the right
order and register Close immediately. Production provisioning remains to be
integrated; unsafe existing ACLs are never silently rewritten. Linux's close
waiter test now observes the mutex-protected close-pending state, not a sleep or
goroutine launch. It passed 100 race-enabled repetitions.

Next work: staged executable OS/architecture verification and integration. The
classifier delegation failed (incorrect repo context/prose, then three tool errors).
Its only partial file contained incorrect Mach-O constants/offsets and no tests;
it was removed, not checkpointed or counted as implemented. No production image
verification exists from that attempt. Delegation/runtime settings were not
changed. The completed switch transaction remains checkpointed and native-tested.

No live installation, real agent, default user directory or selected production
version was changed. Full health/recovery/backend/UI integration remains pending.
