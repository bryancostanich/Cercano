# Track Plan: Update Check & Upgrade Prompt

## Phase 1: Version Check Core

### Objective
Build the version check logic: fetch latest release from GitHub, compare versions, cache results.

### Tasks
- [x] Task: Create `internal/update/` package with version check logic.
    - [x] Implement `CheckForUpdate(currentVersion string) (*UpdateInfo, error)` that queries GitHub Releases API.
    - [x] Parse `tag_name` from response, strip `v` prefix, compare with semver.
    - [x] UpdateInfo struct: LatestVersion, CurrentVersion, UpdateAvailable (bool), ReleaseURL.
    - [x] HTTP timeout: 3 seconds. Return nil on network failure (never error).
    - [x] Skip pre-release tags (`prerelease: true` in API response).
    - [x] Red/Green TDD: TestCheckForUpdate_NewerAvailable, TestCheckForUpdate_UpToDate, TestCheckForUpdate_NetworkFailure, TestSemverCompare.
- [x] Task: Implement version caching.
    - [x] Read/write `~/.config/cercano/update_check.json` with 24h TTL.
    - [x] `CheckCached(currentVersion string) (*UpdateInfo, error)` — return cached result if fresh, otherwise fetch and cache.
    - [x] On stale cache + network failure, return stale cache result.
    - [x] Red/Green TDD: TestCacheWrite, TestCacheRead_Fresh, TestCacheRead_Stale, TestCacheRead_Missing.
- [x] Task: Implement install method detection.
    - [x] `DetectInstallMethod() string` — returns "homebrew" or "manual".
    - [x] Check via `exec.LookPath("brew")` + `brew list cercano`.

## Phase 2: CLI Integration

### Objective
Surface update prompts in `cercano version` and `cercano setup`.

### Tasks
- [x] Task: Enhance `cercano version` command.
    - [x] Perform a fresh check (bypass cache).
    - [x] If newer available: print version, upgrade command, and release URL.
    - [x] If current: print version with "(up to date)".
    - [x] If check fails: print version only (no error shown).
- [x] Task: Add update check to `cercano setup`.
    - [x] Check at start of setup (using cache, non-blocking).
    - [x] If newer available: print note before "Checking prerequisites..."
- [ ] Task: Conductor - User Manual Verification 'CLI Integration' (Protocol in workflow.md)

## Phase 3: MCP Integration

### Objective
Surface update information in MCP mode — stderr on startup and optional nudge on first tool response.

### Tasks
- [x] Task: Add update check to MCP startup.
    - [x] Use cached check (non-blocking, best-effort).
    - [x] Print to stderr if update available.
- [x] Task: Add update nudge to first tool response.
    - [x] Add `updateVersion`, `updateCommand`, `updateNudgeSent` fields to MCP Server struct.
    - [x] `SetUpdateInfo` method for main.go to set update data.
    - [x] `maybeUpdateNudge` appended to first tool response only.
    - [x] Chained into existing `maybeNudge` so all tool handlers benefit.
- [ ] Task: Conductor - User Manual Verification 'MCP Integration' (Protocol in workflow.md)
