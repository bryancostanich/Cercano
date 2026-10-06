# Native fixture verification

The user explicitly authorized pushing feat/cross-platform-updates for the
fixture-only CI workflow. No main merge, signing/deployment, release publication
or production settings changes are authorized by that test run.

First run: https://github.com/bryancostanich/Cercano/actions/runs/37510143117
SQLite state tests passed on all three runners. A shared operation JSON test
failed because it compared time.Time internals after JSON removed monotonic
information and normalized UTC location identity. Reproduced locally with
TZ=UTC; corrected the assertion to compare timestamp instants and then every
other record field exactly. Production state behavior was not changed by the fix.

Successful run:
https://github.com/bryancostanich/Cercano/actions/runs/37510592756
Commit: 3aa925ee317d

- Windows: native updatecoord/pkg-update tests and vet passed.
- Linux: native tests, race tests and vet passed.
- macOS: native tests, race tests and vet passed.
- Windows race checking is intentionally not part of this workflow.
- Unix permission and symlink assertions skipped on Windows do not constitute
  proof of Windows ACL/reparse safety; those checks remain required before enrollment.

All databases and subprocesses were fixtures in temporary directories. This
proves native execution of the current packages, not a working installed updater.
The persistent state-machine adapter, real installation probes, permissions,
process coordination, UI/backend integration and installed acceptance remain
pending. Existing main, installations, package registrations and releases are
unchanged.

CI reported Node20 action deprecation warnings (checkout@v4/setup-go@v5 were
executed under Node24 by the runner). They did not fail the tests; dependency
modernization is not silently bundled into this persistence fix.
