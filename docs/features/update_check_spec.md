# Update Check & Upgrade Prompt

## Overview
Users previously had no way to know a new Cercano version existed without checking GitHub manually. This feature adds a lightweight, non-blocking version check that queries the GitHub Releases API, compares against the running version, and surfaces an upgrade prompt across the CLI and MCP surfaces — install-method-aware so it gives the right upgrade command. Design principles: never delay startup or tool responses; prompt at most once per session (low noise); cache results so GitHub isn't hit on every startup.

## Design / Architecture
Core logic lives in the `internal/update/` package. It queries `GET https://api.github.com/repos/bryancostanich/Cercano/releases/latest` (public, no auth), parses `tag_name`, strips the `v` prefix, and does a semantic-version comparison against the compiled-in `version` variable. Pre-release tags are skipped.

- `CheckForUpdate(currentVersion) (*UpdateInfo, error)` — fetches and compares; HTTP timeout 3s; returns nil (never errors) on network failure. `UpdateInfo` carries LatestVersion, CurrentVersion, UpdateAvailable (bool), and ReleaseURL.
- `CheckCached(currentVersion) (*UpdateInfo, error)` — reads/writes `~/.config/cercano/update_check.json` with a 24h TTL; returns the cached result if fresh, otherwise fetches and caches. On stale cache + network failure, returns the stale cache (better than nothing).
- `DetectInstallMethod() string` — returns "homebrew" (via `exec.LookPath("brew")` + `brew list cercano`) or "manual". Cached in `update_check.json` to avoid shelling out on every check. Homebrew installs get `brew upgrade cercano`; manual installs get the release download URL.

Cache file shape:
```json
{
  "latest_version": "0.8.0",
  "checked_at": "2026-03-25T17:00:00Z",
  "download_url": "https://github.com/bryancostanich/Cercano/releases/tag/v0.8.0"
}
```

## Key behaviors / capabilities
- **`cercano version`** — always performs a fresh check (bypasses cache). Prints the version plus, if newer is available, the upgrade command and release URL; otherwise "(up to date)"; if the check fails, version only with no error shown.
- **`cercano setup`** — checks at the start (cached, non-blocking, best-effort) and prints a note before "Checking prerequisites..." if a newer version exists.
- **MCP startup (stderr)** — uses the cached check; if an update is available, prints a `[UPDATE]` line to stderr only, so the host agent never sees it but it appears in logs/debug output.
- **First MCP tool-response nudge** — appends a one-time note to the first tool response in a session ("Cercano vX is available..."). Implemented via `updateVersion`, `updateCommand`, and `updateNudgeSent` fields on the MCP Server struct, a `SetUpdateInfo` method for main.go, and a `maybeUpdateNudge` chained into the existing `maybeNudge` so all tool handlers benefit. Verified once-per-session (second response had no nudge).

## Notable decisions / constraints
- Non-goals: auto-updating (always prompt, never download/install automatically); checking pre-release versions; update notifications in non-interactive contexts (CI, scripts); Homebrew tap management (the formula already lives in the repo).
- Network failures are handled silently — the check never errors or blocks; 3-second HTTP timeout.
