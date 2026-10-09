# Selection publication — native verification

Native matrix:
https://github.com/cercano-ai/Cercano/actions/runs/37989222484
Source commit 9a93dd6df5b6f4dd1af21bf2f58586bde195ef51.
Windows, Linux and macOS passed. A clean committed-tree archive export also
passed the selection/exclusion/privdir focused race and vet checks before CI.

The publisher uses a fixed selection filename in an explicit existing protected
directory. The permission guard verifies existing state only: it does not create
missing directories or rewrite unsafe permissions. The caller's Update lease
must be live and bound to that directory. Guarded uses serialize on a handle;
Close waits for them, and changed lock/directory bindings refuse before writes.
This is cooperative exclusion, not a filesystem CAS against a hostile same-user
writer or an authorization proof inferred from a PID.

Publication validates the new descriptor and expected prior state, stages a
private file, flushes it, rechecks cancellation/state/binding before the commit,
and uses platform-specific publication. Post-commit failures report uncertainty
rather than pretending no change occurred or silently rolling back. Cleanup is
limited to proven owned staging objects; uncertain leftovers are reported.

Native Windows fixtures exposed two test errors: Stat on a closed handle reports
ERROR_INVALID_HANDLE, and path-derived FileInfo can resolve identity lazily after
a replacement. Fixtures now capture identity from open handles, accept only the
specific platform closed-handle error, and retain positive no-write/cleanup and
byte-identical replacement-preservation assertions. No production identity or
permission rules were relaxed for those tests.

No live selection or installation was changed. This verifies a publication
primitive in temporary fixtures, not a power-loss guarantee or a complete updater.
Journal coordination, durable recovery execution, health/restart, package/backend
integration and the user-facing update flow remain pending. The unrelated local
state/schema.go formatting edit was preserved outside the commits.
