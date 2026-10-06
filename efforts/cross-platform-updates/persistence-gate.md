# Updater-state persistence — SQLite and stable shared location approved

The pure ownership, operation and policy records are implemented and tested.
The user selected the SQLite option below. An isolated storage primitive is now
implemented and tested against temporary databases; see state-verification.md.
There is no runtime integration. The alternatives considered were:

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
Neither modifies existing conversation data. The user explicitly selected a
separate SQLite store after confirming it must be shared across versions.
Use per-user, per-installation state directories outside removable versions:
Windows LocalAppData/Cercano/updater/<installation-id>, Linux
XDG_STATE_HOME/cercano/updater/<installation-id> (fallback ~/.local/state), and
macOS ~/Library/Application Support/Cercano/updater/<installation-id>.
Installation IDs must be stable across versions and safe directory components.
No database may live inside a keg, extracted archive or version directory.

Implement against temporary test directories first. This approves storage design,
not migration of the developer's live installation or conversation data. Older
helpers must refuse unsupported schemas without changing the database. Stored
user consent is not authority for privileged package operations. Actual process
coordination and binary activation still need their own recovery protocol.
