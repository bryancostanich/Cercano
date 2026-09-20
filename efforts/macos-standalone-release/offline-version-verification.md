# Offline version command verification

## Requirement

Version-only invocations must work without network access so that release and formula smoke tests do not require a provider, model download, credentials, or GitHub availability.

## Reproduction before fix

Added `TestReleaseVersionOffline` to both command packages. The test invokes the real `main` entrypoint with isolated arguments and flag state, captures stdout, and replaces `http.DefaultTransport` with a probe that counts requests and immediately rejects them. Tests are deliberately nonparallel and restore global state. No HTTP reaches the network and no agent is launched.

Commands and observed results:

- In source/server: `go test ./cmd/cercano -run '^TestReleaseVersionOffline$' -count=1` — FAIL; both `version` and `--version` attempted one HTTP request each.
- In source/clients/cli: `go test . -run '^TestReleaseVersionOffline$' -count=1` — FAIL; `--version` attempted one HTTP request.

Root cause: all three version-only branches directly called `update.CheckForUpdate`, which performs a live GitHub request rather than a cached-only lookup.

## Fix and verification

Removed update-check blocks only from version-only branches in source/server/cmd/cercano/main.go and source/clients/cli/main.go. Removed the now-unused CLI update import. Version output is exactly `<binary> v<version>\n`; normal non-version update notifications are unchanged.

- In source/server: `go test ./cmd/cercano ./pkg/update -run 'TestReleaseVersionOffline|TestCheckForUpdate|TestUpgradeCommand' -count=1` — PASS for both packages.
- In source/clients/cli: `go test . -run '^TestReleaseVersionOffline$' -count=1` — PASS after removing the unused import identified by the first post-fix compile.

Regression tests: source/server/cmd/cercano/release_version_test.go and source/clients/cli/release_version_test.go.

The broad implementation delegation exhausted its budget and left three invalid root-level scratch probes, but did not change tracked production files. Those probes were removed; they are not verification evidence. This fix was completed using direct, bounded inspection and execution after delegation failed.

## Limits

No full end-to-end suite, Homebrew installation, signed binary execution, notarization, or clean-machine upgrade test was run. The source-level entrypoint tests prove the targeted network regression is fixed, not general release readiness.
