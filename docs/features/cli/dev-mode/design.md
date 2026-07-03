# Development Mode (`/d`) — Design

**Status:** Design approved 2026-07-03. Implementation not started.

Cercano should be able to work on itself. Today, starting a "work on Cercano"
session means manually cd-ing into the repo, re-explaining the architecture,
and pasting paths to logs and databases the agent has no idea exist. This
feature adds a `/d` (development) slash command that primes a session for
self-development in one step: point the session at the Cercano repo, and send
a kickoff instruction telling the agent which docs to read to orient itself —
including a new committed doc that teaches it how to build, test, and inspect
its own logs and databases.

A companion deliverable is a **launcher generator**: a script anyone can run
after cloning that emits the dev launcher (`~/bin/cercano`) with their repo
path baked in, replacing today's copy-and-hope install with a hardcoded
`bryan_costanich` default. The generated launcher exports `CERCANO_REPO`,
which doubles as `/d`'s repo-location fallback.

## Goals

- `/d` from any directory puts the session in development mode: file tools
  operate on the Cercano repo, and the agent is told to orient itself from the
  key docs before taking direction.
- The orientation includes **current** status — what's built, what's
  outstanding — by instructing the agent to read the living docs with its own
  Read tool, not by baking a snapshot into a prompt that goes stale.
- The agent learns operational self-inspection: where its logs are, how to
  query the conversation and telemetry databases.
- A one-command launcher install: `make launcher` (or
  `scripts/install-launcher.sh`) generates `~/bin/cercano` for any clone
  location.

## Non-goals

- **No proto or server changes.** This is CLI-side only (Approach 1 from the
  brainstorm). The server already injects `.cercano/context.md` from whatever
  `work_dir` the CLI sends; `/d` rides that existing mechanism.
- No persistent dev-mode state across sessions. `/d` is per-session; after
  `/resume` you run `/d` again if you want the workDir override back (the
  kickoff turn itself is persisted with the conversation like any other turn).
- No model or permission-mode switching. `/d` composes with `/model`,
  `/permissive`, etc.; it does not own them.
- No generalization to "project modes" for arbitrary repos. This is
  self-development only. If a generic mechanism is wanted later, this is the
  prototype.

## Architecture

### 1. `/d` slash command (`internal/slash/dev.go`)

New `RegisterDev(r *Registry)` following the existing pattern
(`registry.go:10-18` `Command` struct):

- `Name: "d"`, `Aliases: []string{"dev"}`, `Help: "development mode — work on
  Cercano itself"`.
- The handler resolves the repo root (see below) and returns a new result
  kind, `ResultDevMode`, carrying the resolved absolute path in a new
  `Result.WorkDir` field. On failure it returns `ResultText` with a plain
  error message that names all three resolution paths so the user knows how
  to fix it.

**Repo resolution**, in order; first hit wins:

1. **Explicit argument:** `/d <path>` — validate and use.
2. **Walk up from the launch directory:** starting at `os.Getwd()`, walk
   parent directories looking for the repo markers.
3. **`$CERCANO_REPO` env var:** the generated launcher exports this, so any
   session started via the launcher always resolves.

A directory qualifies as the repo root when **both**
`source/server/cmd/cercano` and `source/clients/cli/main.go` exist beneath
it. Two markers from the two separate Go modules make a false positive
implausible; checking file existence keeps resolution purely algorithmic (no
file reads, no git invocation).

### 2. Session workDir override (root model)

Today the model reads `os.Getwd()` fresh at every prompt submission
(`model.go:1562`) and sends it as `work_dir` on the gRPC request; the server
chdirs to it for tool execution and injects `<project-context>` from
`.cercano/context.md` under it.

Changes:

- New `Model.workDirOverride string` field. The submit path uses it when
  non-empty, else falls back to `os.Getwd()` as today.
- On `ResultDevMode` the update handler:
  1. Sets `workDirOverride` to the resolved repo path.
  2. Appends a system entry to the scrollback: `dev mode: working on <path>`.
  3. Auto-submits the canned kickoff prompt (below) through the normal
     `submitPrompt` path — so it streams, persists to the conversation store,
     and counts in the context meter exactly like a typed message.
- Status bar renders a lime `DEV` chip next to the permission-mode chip while
  `workDirOverride` is set, so the mode is always visible.

Running `/d` mid-session is allowed and does the same thing; running it twice
is idempotent apart from re-sending the kickoff.

### 3. Kickoff prompt (const in `dev.go`)

Canned text, versioned with the code:

> You are now in Cercano development mode: this session works on your own
> codebase, at `<repo path>`. Before anything else, orient yourself by
> reading these three documents with your Read tool:
>
> 1. `docs/features/cli/README.md` — the CLI track: architecture, design
>    principles, what's built, deviations from plan, and outstanding tasks.
> 2. `docs/agent/README.md` — the standalone agent: provider layer, tool
>    loop, permission gating, persistence.
> 3. `docs/agent/self-dev.md` — how to build and test both modules, and how
>    to inspect your own logs and databases.
>
> When you've read them, give a two-or-three-sentence summary of the current
> state and stop — the user will direct the work from there.

The doc-reading happens through the normal tool loop (R-tier, silent), so the
orientation reflects whatever the docs say *today*.

### 4. Self-development doc (`docs/agent/self-dev.md`)

New committed doc; the single home for operational self-knowledge. Content:

- **Layout & build:** two binaries / two Go modules; `go build` + `go test`
  commands for `source/server` and `source/clients/cli`; the dev launcher's
  rebuild-if-stale behavior; the agent-is-a-singleton caveat (a rebuilt
  binary does nothing until the stale agent is killed — the launcher does
  this).
- **Logs:**
  - `$TMPDIR/cercano-server.log` — stdout/stderr of an auto-launched agent
    (`pkg/agentclient/client.go`).
  - `~/.config/cercano/crash.log` — agent crash reports.
  - `~/.config/cercano/meridian.log` — Meridian proxy diagnostics.
  - `~/.cercano-dispatch.log` — MCP dispatch-mode diagnostics.
- **Databases** (SQLite, WAL mode — query in place with `sqlite3`; don't copy
  the `.db` without its `-wal`/`-shm` siblings):
  - `~/.config/cercano/conversations.db` (override:
    `$CERCANO_CONVERSATIONS_DB`) — `conversations` + `turns`; tool-calling
    turns carry an Anthropic-wire-shape block array in `content_json`.
    Include 2–3 worked example queries (list recent conversations; dump a
    conversation's turns; find turns containing a given tool call).
  - `~/.config/cercano/telemetry.db` — usage/token metrics.
- **Config surfaces:** `config.yaml`, `permissions.yaml`, `ui.yaml`,
  `mcp.yaml` under `~/.config/cercano/`.
- **Where outstanding work is tracked:** the task rollups in
  `docs/features/cli/README.md` and the Feature TODOs in the top-level
  `README.md`.

### 5. Launcher generator (`source/server/scripts/install-launcher.sh` + `make launcher`)

Today `cercano-launcher.sh` is installed by hand-copying and defaults
`REPO` to a hardcoded personal path. The generator replaces the install step:

- `install-launcher.sh` resolves the repo root from its own location
  (`$(dirname "$0")/../../..`, normalized) — no guessing.
- It emits `~/bin/cercano` by copying `cercano-launcher.sh` and rewriting the
  `REPO=` default line to the resolved path via `sed`. One source of truth:
  the launcher script stays the canonical template; the generator only
  substitutes the path. `CERCANO_REPO` remains an env override.
- `cercano-launcher.sh` itself gains one line: `export CERCANO_REPO="$REPO"`
  after resolution, so the CLI process (and therefore `/d`) inherits it.
- Safety: the emitted file carries a `# generated by install-launcher.sh`
  marker. The generator refuses to overwrite an existing `~/bin/cercano`
  that lacks the marker unless `--force` is given.
- Post-install: `chmod +x`; warn (don't fail) if `~/bin` is not on `PATH`.
- `make launcher` in `source/server/Makefile` invokes the script.

## Error handling

- `/d` with no resolvable repo → scrollback message listing the three
  resolution paths ("run from inside the repo, pass `/d <path>`, or set
  `CERCANO_REPO` — the generated launcher sets it for you").
- `/d <path>` where the markers don't match → error naming the missing
  marker; the override is **not** set.
- Generator: missing `~/bin` is created; unwritable target or marker
  mismatch → non-zero exit with a one-line reason.

## Testing

- **Repo resolution unit tests** (`internal/slash/dev_test.go`): temp-dir
  trees exercising explicit-arg valid/invalid, walk-up hit at depth ≥ 2,
  env-var fallback, and total miss. Pure filesystem, no git needed.
- **Result plumbing test**: `ResultDevMode` sets the override and enqueues
  the kickoff submit (model-level test, matching existing slash dispatch
  tests).
- **Generator test** (`scripts/install-launcher_test.sh` or a Go test
  shelling out): run against a temp `HOME`, assert emitted file has the baked
  path, the marker, the export line, and exec bit; assert refusal without
  `--force` on a marker-less pre-existing file.
- **Manual acceptance:** from a directory outside the repo with
  `CERCANO_REPO` set, `/d` → `DEV` chip appears, kickoff streams, agent reads
  the three docs and summarizes; a follow-up "read internal/slash/registry.go"
  resolves against the repo, proving the workDir override.

## Open questions

- Should `/resume` of a conversation whose last turns ran in dev mode
  auto-restore the workDir override? Deferred: turns persist `WorkDir`
  already, so a later change could restore it from the last turn's value.
- Whether `.cercano/context.md` in the Cercano repo should be regenerated to
  complement (not duplicate) `self-dev.md`. Out of scope here; the injection
  happens automatically either way.
