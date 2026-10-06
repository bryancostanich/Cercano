# Update and entrypoint audit

Source verified directly: `source/server/pkg/update/update.go` and the main entrypoint in `source/server/cmd/cercano/main.go`. Findings below distinguish version behavior before and after the offline-version fix.

## Update checks

`CheckForUpdate` calls the GitHub latest-release API using an HTTP client with a three-second timeout. It returns nil on network errors, non-200 responses, invalid responses, and prereleases. It does not consult the cache.

`CheckCached` is a separate API with a 24-hour freshness interval and fallback to a stale cached result on network failure. It can perform synchronous network work; describing every update check as cached or non-blocking is incorrect.

`DetectInstallMethod` checks for `brew` on PATH, then runs `brew list cercano`. It does not establish that the currently executing binary belongs to that formula; its scope is installation advice. `UpgradeCommand` returns `brew upgrade cercano` for a Homebrew installation, or a release download URL otherwise. This source file provides advice and caching, not a self-installing updater. Other update paths remain to be audited.

## Version commands

Originally CLI `--version`, server `--version`, and server `version` all invoked `CheckForUpdate`. Regression probes demonstrated one intercepted HTTP request per invocation without making real network requests. The offline-version fix removes those three checks, leaving single-line version output and preserving non-version update behavior. See offline-version-verification.md.

## Server entrypoint

The server main function explicitly runs server mode for bare `cercano`, `cercano agent`, and `cercano --server`. It does not launch `cercano-cli`. The terminal entrypoint is `cercano-cli`, which connects to or auto-launches the agent. Installation documentation and formula smoke tests must reflect this distinction. This establishes intended entrypoint behavior from source; a complete installed-process test remains outstanding.
