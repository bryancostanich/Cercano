# Track Specification: Update Check & Upgrade Prompt

## 1. Job Title
Check for new Cercano releases on GitHub and prompt the user to upgrade when a newer version is available.

## 2. Overview
Users currently have no way to know a new Cercano version exists unless they check GitHub manually. This track adds a lightweight version check that queries the GitHub Releases API, compares against the running version, and surfaces an upgrade prompt when appropriate.

### Design Principles
- **Non-blocking** — never delay startup or tool responses waiting for a version check
- **Low noise** — prompt at most once per session, not on every command
- **Cached** — store the last check result locally so we don't hit GitHub on every startup
- **Install-method-aware** — detect Homebrew vs manual install and give the right upgrade command

## 3. Version Check Mechanism

### Source
GitHub Releases API (public, no auth required):
```
GET https://api.github.com/repos/bryancostanich/Cercano/releases/latest
```
Response includes `tag_name` (e.g. `"v0.7.0"`).

### Comparison
Semantic version comparison against the compiled-in `version` variable. Only consider stable releases (skip pre-release tags).

### Caching
Store the last check result in `~/.config/cercano/update_check.json`:
```json
{
  "latest_version": "0.8.0",
  "checked_at": "2026-03-25T17:00:00Z",
  "download_url": "https://github.com/bryancostanich/Cercano/releases/tag/v0.8.0"
}
```
Cache TTL: **24 hours**. If the cache is fresh, use it without hitting the network.

## 4. Where Prompts Appear

### 4a. `cercano version`
Always performs a fresh check (ignores cache). Output:
```
cercano v0.7.0

A newer version is available: v0.8.0
  Upgrade: brew upgrade cercano
  Release: https://github.com/bryancostanich/Cercano/releases/tag/v0.8.0
```
Or if current:
```
cercano v0.7.0 (up to date)
```

### 4b. `cercano setup`
Check at the start of setup (non-blocking, best-effort). If a newer version is available:
```
Cercano Setup (v0.7.0)

  Note: A newer version is available (v0.8.0).
  Run `brew upgrade cercano` after setup to get the latest features.

Checking prerequisites...
```

### 4c. MCP startup (stderr)
On MCP startup, if the cached check (or a quick async check) shows a newer version:
```
Cercano MCP server (v0.7.0) starting with embedded gRPC server...
[UPDATE] A newer version is available: v0.8.0. Run: brew upgrade cercano
```
This goes to stderr only — the host agent never sees it, but it appears in logs and debug output.

### 4d. First MCP tool response nudge (optional)
Similar to the `cercano_init` nudge, append a note to the first tool response in a session:
```
---
*Note: Cercano v0.8.0 is available (you're running v0.7.0). Run `brew upgrade cercano` to update.*
```
Only shown once per MCP session. Controlled by a flag on the Server struct.

## 5. Install Method Detection

To give the right upgrade command:

1. Run `brew list cercano` — if it succeeds, installed via Homebrew → `brew upgrade cercano`
2. Otherwise, manual install → print the release download URL

Cache the install method in `update_check.json` so we don't shell out on every check.

## 6. Network Failure Handling

- If the GitHub API is unreachable, silently skip the check — never error or block
- If the cached result is stale and the network fails, use the stale cache (better than nothing)
- Timeout: 3 seconds max for the HTTP request

## 7. Non-Goals
- Auto-updating (always prompt, never download/install automatically)
- Checking for pre-release versions
- Update notifications in non-interactive contexts (CI, scripts)
- Homebrew tap management (the formula is already in the repo)
