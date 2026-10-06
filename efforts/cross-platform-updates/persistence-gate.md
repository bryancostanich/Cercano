# Updater-state persistence choice — pending

The pure ownership, operation and policy records are implemented and tested.
There is deliberately no durable store or runtime integration. Next is a real
choice about recovery/preferences persistence:

- Recommended: separate per-user SQLite database using the project's existing
  pure-Go `modernc.org/sqlite` dependency, not the conversation database. Use
  explicit transactional revisions and migrations; test multi-process writers,
  interruption, corrupt/future schemas and restrictive file permissions. A helper
  importing the adapter gains the driver footprint. Do not equate database
  atomicity with atomic installation/activation across external files.
- Alternative: one dedicated JSON state file, with shared OS locking, platform-
  correct atomic replacement, flush/recovery discipline and schema handling.
  Smaller storage dependency and easy inspection, but more custom cross-platform
  persistence protocol to maintain and validate. This still needs application
  update/launch exclusion and separately tested file activation recovery.

Both require the same native-platform interruption and concurrency acceptance.
Neither modifies existing conversation data. The plan leaves this storage
choice open; no durable writes or new database have been implemented pending
operator selection. In-memory concurrency tests already pass; persistence and
migration portions of the Phase3 task remain blocked by this choice.
