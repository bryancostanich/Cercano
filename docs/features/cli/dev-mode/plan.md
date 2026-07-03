# Development Mode (`/d`) + Launcher Generator — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A `/d` slash command that points a cercano-cli session at the Cercano repo and primes the agent to work on its own codebase, plus a generator script that installs the dev launcher with the clone's repo path baked in.

**Architecture:** CLI-side only — no proto or server changes. `/d` resolves the repo root (explicit arg → walk up from cwd → `$CERCANO_REPO`), sets a session-scoped `work_dir` override (the server already chdirs to `work_dir` and injects `.cercano/context.md` from it), and auto-submits a canned kickoff prompt telling the agent to read three orientation docs — including a new `docs/agent/self-dev.md` that documents logs, databases, and build commands. The launcher generator rewrites the existing `cercano-launcher.sh` template with the resolved repo path and adds an `export CERCANO_REPO` so `/d` always resolves when launched via the launcher.

**Tech Stack:** Go 1.21+ (two modules: `source/server`, `source/clients/cli`), Bubble Tea v2 UI, bash for scripts.

**Spec:** `docs/features/cli/dev-mode/design.md` (approved 2026-07-03).

## Global Constraints

- No proto or server (`source/server` Go code) changes. Scripts and docs under `source/server/scripts/` are fine; the server Makefile gains one target.
- CLI module tests: `cd source/clients/cli && go test ./... -count=1`. CLI build: `cd source/clients/cli && go build -o bin/cercano-cli .`
- Commit locally after each task. NEVER `git push`. NEVER include the name "Claude" anywhere in a commit — no Co-Authored-By trailers of any kind.
- All new user-facing copy is plain English (no jargon-y abbreviations in error messages).
- Repo markers (used by both `/d` and the generator): a directory is the Cercano repo root iff `source/server/cmd/cercano` (dir) and `source/clients/cli/main.go` (file) both exist beneath it.

---

### Task 1: `/d` slash command with repo resolution

**Files:**
- Modify: `source/clients/cli/internal/slash/registry.go` (add result kind + field)
- Create: `source/clients/cli/internal/slash/dev.go`
- Test: `source/clients/cli/internal/slash/dev_test.go`

**Interfaces:**
- Consumes: existing `Registry`, `Command`, `Result` types in `registry.go`.
- Produces (used by Task 2):
  - `ResultDevMode ResultKind` and `Result.WorkDir string` — the handler's success result carries the resolved absolute repo root in `WorkDir`.
  - `func RegisterDev(r *Registry)` — registers `/d` (alias `/dev`).
  - `func DevKickoff(repo string) string` — the canned kickoff prompt.
  - `func ResolveDevRepo(explicit, cwd, env string) (string, error)` and `func IsCercanoRepo(dir string) bool` — exported for tests; pure functions of their inputs.

- [ ] **Step 1: Write the failing tests**

Create `source/clients/cli/internal/slash/dev_test.go`:

```go
package slash

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// makeRepo creates a minimal directory tree satisfying the Cercano repo
// markers: source/server/cmd/cercano (dir) + source/clients/cli/main.go (file).
func makeRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "source", "server", "cmd", "cercano"), 0o755); err != nil {
		t.Fatal(err)
	}
	cliDir := filepath.Join(root, "source", "clients", "cli")
	if err := os.MkdirAll(cliDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cliDir, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestResolveDevRepoExplicit(t *testing.T) {
	repo := makeRepo(t)
	got, err := ResolveDevRepo(repo, t.TempDir(), "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != repo {
		t.Fatalf("got %q, want %q", got, repo)
	}
}

func TestResolveDevRepoExplicitInvalid(t *testing.T) {
	if _, err := ResolveDevRepo(t.TempDir(), t.TempDir(), ""); err == nil {
		t.Fatal("want error for explicit path that is not a repo root")
	}
}

func TestResolveDevRepoWalkUp(t *testing.T) {
	repo := makeRepo(t)
	deep := filepath.Join(repo, "source", "clients", "cli") // depth 3 below root
	got, err := ResolveDevRepo("", deep, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != repo {
		t.Fatalf("got %q, want %q", got, repo)
	}
}

func TestResolveDevRepoEnvFallback(t *testing.T) {
	repo := makeRepo(t)
	got, err := ResolveDevRepo("", t.TempDir(), repo) // cwd is NOT inside a repo
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != repo {
		t.Fatalf("got %q, want %q", got, repo)
	}
}

func TestResolveDevRepoMiss(t *testing.T) {
	_, err := ResolveDevRepo("", t.TempDir(), "")
	if err == nil {
		t.Fatal("want error when nothing resolves")
	}
	// The error must name all three resolution paths so the user can fix it.
	for _, want := range []string{"/d <path>", "CERCANO_REPO"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q missing hint %q", err.Error(), want)
		}
	}
}

func TestDevCommandDispatch(t *testing.T) {
	repo := makeRepo(t)
	r := New()
	RegisterDev(r)
	res, ok := r.Dispatch("/d " + repo)
	if !ok {
		t.Fatal("dispatch did not match /d")
	}
	if res.Kind != ResultDevMode {
		t.Fatalf("kind = %v, want ResultDevMode", res.Kind)
	}
	if res.WorkDir != repo {
		t.Fatalf("WorkDir = %q, want %q", res.WorkDir, repo)
	}
}

func TestDevCommandDispatchAlias(t *testing.T) {
	repo := makeRepo(t)
	r := New()
	RegisterDev(r)
	res, ok := r.Dispatch("/dev " + repo)
	if !ok || res.Kind != ResultDevMode {
		t.Fatalf("alias /dev did not dispatch to dev mode: ok=%v kind=%v", ok, res.Kind)
	}
}

func TestDevKickoffNamesTheDocs(t *testing.T) {
	kick := DevKickoff("/tmp/x")
	for _, want := range []string{
		"/tmp/x",
		"docs/features/cli/README.md",
		"docs/agent/README.md",
		"docs/agent/self-dev.md",
	} {
		if !strings.Contains(kick, want) {
			t.Fatalf("kickoff missing %q", want)
		}
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd source/clients/cli && go test ./internal/slash/ -run 'TestResolveDevRepo|TestDevCommand|TestDevKickoff' -v`
Expected: compile FAIL — `undefined: ResolveDevRepo`, `undefined: RegisterDev`, `undefined: ResultDevMode`, etc.

- [ ] **Step 3: Add the result kind and field to `registry.go`**

In `source/clients/cli/internal/slash/registry.go`, extend the const block (after `ResultSetPermissionMode`, line 35):

```go
	ResultSetPermissionMode  // PermissionMode carries the new mode (strict|permissive|bypass)
	ResultDevMode            // WorkDir carries the resolved Cercano repo root
```

And add the field to `Result` (after `PermissionMode`, line 44):

```go
	PermissionMode string // for ResultSetPermissionMode
	WorkDir        string // for ResultDevMode
```

- [ ] **Step 4: Create `dev.go`**

Create `source/clients/cli/internal/slash/dev.go`:

```go
package slash

import (
	"fmt"
	"os"
	"path/filepath"
)

// RegisterDev wires /d (alias /dev) — development mode: point the session's
// working directory at the Cercano repo and prime the agent to work on its
// own codebase. Repo resolution order: explicit argument, walk up from the
// current directory, then the CERCANO_REPO environment variable (which the
// generated dev launcher exports).
func RegisterDev(r *Registry) {
	r.Register(Command{
		Name:    "d",
		Aliases: []string{"dev"},
		Help:    "Development mode — work on the Cercano codebase itself. Usage: /d [repo-path]",
		Handler: func(args []string) Result {
			explicit := ""
			if len(args) > 0 {
				explicit = args[0]
			}
			cwd, _ := os.Getwd()
			repo, err := ResolveDevRepo(explicit, cwd, os.Getenv("CERCANO_REPO"))
			if err != nil {
				return Result{Kind: ResultText, Text: "dev mode: " + err.Error()}
			}
			return Result{Kind: ResultDevMode, WorkDir: repo}
		},
	})
}

// IsCercanoRepo reports whether dir is the root of a Cercano checkout. Two
// markers from the two separate Go modules make a false positive implausible;
// checking existence only keeps resolution purely algorithmic.
func IsCercanoRepo(dir string) bool {
	st, err := os.Stat(filepath.Join(dir, "source", "server", "cmd", "cercano"))
	if err != nil || !st.IsDir() {
		return false
	}
	st, err = os.Stat(filepath.Join(dir, "source", "clients", "cli", "main.go"))
	return err == nil && st.Mode().IsRegular()
}

// ResolveDevRepo resolves the Cercano repo root. A pure function of its
// inputs so tests don't have to manipulate the process cwd or environment:
// the handler passes os.Getwd() and os.Getenv("CERCANO_REPO").
func ResolveDevRepo(explicit, cwd, env string) (string, error) {
	if explicit != "" {
		p, err := filepath.Abs(explicit)
		if err == nil && IsCercanoRepo(p) {
			return p, nil
		}
		return "", fmt.Errorf("%s is not a Cercano repo root (needs source/server/cmd/cercano and source/clients/cli/main.go)", explicit)
	}
	dir := cwd
	for dir != "" {
		if IsCercanoRepo(dir) {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir { // reached the filesystem root
			break
		}
		dir = parent
	}
	if env != "" && IsCercanoRepo(env) {
		return env, nil
	}
	return "", fmt.Errorf("could not find the Cercano repo — run from inside the repo, pass /d <path>, or set CERCANO_REPO (the generated launcher sets it for you)")
}

// DevKickoff returns the canned first prompt sent on entering dev mode. The
// docs are read live by the agent's own Read tool, so orientation always
// reflects current status — nothing baked in here can go stale except paths.
func DevKickoff(repo string) string {
	return fmt.Sprintf(`You are now in Cercano development mode: this session works on your own codebase, at %s. Before anything else, orient yourself by reading these three documents with your Read tool:

1. docs/features/cli/README.md — the CLI track: architecture, design principles, what's built, deviations from plan, and outstanding tasks.
2. docs/agent/README.md — the standalone agent: provider layer, tool loop, permission gating, persistence.
3. docs/agent/self-dev.md — how to build and test both modules, and how to inspect your own logs and databases.

When you've read them, give a two-or-three-sentence summary of the current state and stop — the user will direct the work from there.`, repo)
}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `cd source/clients/cli && go test ./internal/slash/ -count=1 -v -run 'TestResolveDevRepo|TestDevCommand|TestDevKickoff'`
Expected: all 8 PASS. Then run the whole package to check for regressions: `go test ./internal/slash/ -count=1` → PASS.

- [ ] **Step 6: Commit**

```bash
git add source/clients/cli/internal/slash/registry.go source/clients/cli/internal/slash/dev.go source/clients/cli/internal/slash/dev_test.go
git commit -m "feat(cli): /d slash command with Cercano repo resolution"
```

---

### Task 2: Session workDir override + dev-mode entry in the root model

**Files:**
- Modify: `source/clients/cli/internal/ui/model.go` (four spots: field ~line 177, registration ~line 296, submit ~line 1561, runSlash ~line 1730)
- Create: `source/clients/cli/internal/ui/dev_mode_test.go`

**Interfaces:**
- Consumes (from Task 1): `slash.ResultDevMode`, `Result.WorkDir`, `slash.RegisterDev`, `slash.DevKickoff(repo string) string`.
- Produces (used by Task 3): `Model.workDirOverride string` (non-empty ⇒ dev mode active), `func (m Model) effectiveWorkDir() string`, `func (m *Model) applyDevMode(repo string) string`.

- [ ] **Step 1: Write the failing tests**

Create `source/clients/cli/internal/ui/dev_mode_test.go`:

```go
package ui

import (
	"os"
	"strings"
	"testing"
)

func TestEffectiveWorkDirDefaultsToCwd(t *testing.T) {
	m := &Model{}
	wd, _ := os.Getwd()
	if got := m.effectiveWorkDir(); got != wd {
		t.Fatalf("got %q, want cwd %q", got, wd)
	}
}

func TestEffectiveWorkDirUsesOverride(t *testing.T) {
	m := &Model{workDirOverride: "/tmp/cercano-repo"}
	if got := m.effectiveWorkDir(); got != "/tmp/cercano-repo" {
		t.Fatalf("got %q, want override", got)
	}
}

func TestApplyDevMode(t *testing.T) {
	m := &Model{}
	kick := m.applyDevMode("/tmp/cercano-repo")
	if m.workDirOverride != "/tmp/cercano-repo" {
		t.Fatalf("override = %q, want /tmp/cercano-repo", m.workDirOverride)
	}
	if !strings.Contains(kick, "docs/agent/self-dev.md") {
		t.Fatalf("kickoff missing doc pointer: %q", kick)
	}
	// A visible system entry announces the mode switch.
	entries := m.chat.Entries()
	if len(entries) == 0 || !strings.Contains(entries[len(entries)-1].Content, "/tmp/cercano-repo") {
		t.Fatalf("no dev-mode system entry appended: %+v", entries)
	}
}
```

(`m.chat.Entries()` and `AppendEntry` are existing chatView methods — `chat_view.go:141,144`; both work on a zero-value chatView.)

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd source/clients/cli && go test ./internal/ui/ -run 'TestEffectiveWorkDir|TestApplyDevMode' -v`
Expected: compile FAIL — `m.workDirOverride undefined`, `m.effectiveWorkDir undefined`, `m.applyDevMode undefined`.

- [ ] **Step 3: Add the field, helpers, registration, and dispatch case**

In `source/clients/cli/internal/ui/model.go`:

**(a)** Next to the `permissionMode` field (~line 177), add:

```go
	// workDirOverride, when non-empty, replaces os.Getwd() as the work_dir
	// sent with every turn. Set by /d (development mode); empty = normal.
	workDirOverride string
```

**(b)** In the slash registration block (~line 296, after `slash.RegisterContextView(reg)`), add:

```go
	slash.RegisterDev(reg)
```

**(c)** In `submit` (~lines 1561–1563), replace:

```go
	// Pass cwd so the agent prepends .cercano/context.md if present.
	wd, _ := os.Getwd()
	driver := &mainAgentDriver{agent: m.agent, convID: m.convID, workDir: wd}
```

with:

```go
	// Pass the effective workDir so the agent chdirs there and prepends its
	// .cercano/context.md if present (/d pins this to the Cercano repo).
	driver := &mainAgentDriver{agent: m.agent, convID: m.convID, workDir: m.effectiveWorkDir()}
```

**(d)** In `runSlash` (~line 1730), add a case before `case slash.ResultText:`:

```go
	case slash.ResultDevMode:
		kickoff := m.applyDevMode(res.WorkDir)
		m.refreshViewport()
		return m.submit(kickoff, nil)
```

**(e)** Add the two methods near `submit`:

```go
// effectiveWorkDir returns the work_dir to send with a turn: the /d override
// when set, else the process cwd.
func (m Model) effectiveWorkDir() string {
	if m.workDirOverride != "" {
		return m.workDirOverride
	}
	wd, _ := os.Getwd()
	return wd
}

// applyDevMode enters development mode: pin the session workDir to the repo
// and return the canned kickoff prompt for the caller to submit through the
// normal chat path (so it streams, persists, and meters like a typed turn).
func (m *Model) applyDevMode(repo string) string {
	m.workDirOverride = repo
	m.chat.AppendEntry(&Entry{Role: RoleSystem, Content: "dev mode: working on " + repo})
	return slash.DevKickoff(repo)
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd source/clients/cli && go test ./internal/ui/ -run 'TestEffectiveWorkDir|TestApplyDevMode' -count=1 -v`
Expected: 3 PASS. Then full module: `go test ./... -count=1` → PASS, and `go build -o bin/cercano-cli .` → clean.

- [ ] **Step 5: Commit**

```bash
git add source/clients/cli/internal/ui/model.go source/clients/cli/internal/ui/dev_mode_test.go
git commit -m "feat(cli): dev-mode workDir override + kickoff on /d"
```

---

### Task 3: DEV status-bar chip

**Files:**
- Modify: `source/clients/cli/internal/ui/model.go` (chip function near `renderPermissionModeChip` ~line 3178; call site ~line 3094)
- Test: `source/clients/cli/internal/ui/dev_mode_test.go` (append)

**Interfaces:**
- Consumes (from Task 2): `Model.workDirOverride`.
- Produces: `func (m Model) renderDevChip() string` — empty when dev mode is off.

- [ ] **Step 1: Write the failing test**

Append to `source/clients/cli/internal/ui/dev_mode_test.go`:

```go
func TestRenderDevChip(t *testing.T) {
	off := Model{}
	if got := off.renderDevChip(); got != "" {
		t.Fatalf("chip should be empty when dev mode off, got %q", got)
	}
	on := Model{workDirOverride: "/tmp/cercano-repo"}
	if got := on.renderDevChip(); !strings.Contains(got, "DEV") {
		t.Fatalf("chip missing DEV label: %q", got)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd source/clients/cli && go test ./internal/ui/ -run TestRenderDevChip -v`
Expected: compile FAIL — `renderDevChip undefined`.

- [ ] **Step 3: Implement the chip and wire it into the status bar**

In `source/clients/cli/internal/ui/model.go`, after `renderPermissionModeChip` (~line 3194), add:

```go
// renderDevChip shows a lime DEV marker while the /d workDir override is
// active, so it stays visible that tools are pointed at the Cercano repo.
func (m Model) renderDevChip() string {
	if m.workDirOverride == "" {
		return ""
	}
	return m.styles.BorderDim.Render("  ·  ") + m.styles.Accent.Render("DEV")
}
```

At the status-bar assembly (~line 3094), directly after the `m.renderPermissionModeChip(),` argument, add:

```go
		m.renderDevChip(),
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd source/clients/cli && go test ./internal/ui/ -run TestRenderDevChip -count=1 -v`
Expected: PASS. Then `go test ./... -count=1` → PASS (status-bar golden tests, if any, may need their expected strings updated — the chip renders empty when `workDirOverride` is empty, so existing goldens should be unaffected; investigate any failure rather than blindly regolden).

- [ ] **Step 5: Commit**

```bash
git add source/clients/cli/internal/ui/model.go source/clients/cli/internal/ui/dev_mode_test.go
git commit -m "feat(cli): DEV status-bar chip while dev mode active"
```

---

### Task 4: Self-development doc (`docs/agent/self-dev.md`)

**Files:**
- Create: `docs/agent/self-dev.md`

**Interfaces:**
- Consumes: nothing (pure docs).
- Produces: the third document named by `DevKickoff` (Task 1) — the path string `docs/agent/self-dev.md` must match exactly.

- [ ] **Step 1: Write the doc**

Create `docs/agent/self-dev.md` with exactly this content:

```markdown
# Self-Development Guide

How to build, test, and inspect Cercano when working on Cercano itself. This
doc is read by the agent on entering development mode (`/d`), alongside
`docs/features/cli/README.md` (CLI track status + outstanding work) and
`docs/agent/README.md` (agent architecture).

## Layout & build

Two binaries from two Go modules:

| Binary | Module | Role |
|---|---|---|
| `cercano` | `source/server` | agent gRPC server, MCP host — a singleton |
| `cercano-cli` | `source/clients/cli` | terminal UI, thin gRPC client |

```bash
# Server (agent + MCP)
cd source/server && go build -o bin/cercano ./cmd/cercano/
cd source/server && go test ./... -count=1

# CLI (separate Go module; depends on server pkg/ via replace directive)
cd source/clients/cli && go build -o bin/cercano-cli .
cd source/clients/cli && go test ./... -count=1
```

**Singleton caveat:** the agent server is a singleton on `:50052`. A rebuilt
binary changes nothing until the running agent is killed — a stale agent will
happily keep serving old code. The dev launcher (`~/bin/cercano`, generated by
`source/server/scripts/install-launcher.sh`) handles this: it rebuilds either
binary whose sources changed and kills agents older than the fresh binary
before exec'ing the CLI.

## Logs

| Path | What it is |
|---|---|
| `$TMPDIR/cercano-server.log` | stdout/stderr of an auto-launched agent |
| `~/.config/cercano/crash.log` | agent crash reports |
| `~/.config/cercano/meridian.log` | Meridian proxy diagnostics |
| `~/.cercano-dispatch.log` | MCP dispatch-mode diagnostics |

The auto-launch log path comes from `pkg/agentclient/client.go` (it prints as
"auto-launched agent server (log: …)" on CLI startup). When debugging agent
behavior, check the server log first — tool-loop iterations, provider errors,
and permission decisions all land there.

## Databases

SQLite in WAL mode. Query in place with `sqlite3`; if you must copy a
database, take its `-wal` and `-shm` siblings too or you'll read a stale
snapshot. Timestamps are integer unix **seconds**.

### `~/.config/cercano/conversations.db`

Override with `$CERCANO_CONVERSATIONS_DB`. Schema source of truth:
`source/server/internal/conversation/schema.sql`. Tables: `conversations`
(id, title, project_dir, recap, timestamps), `turns` (role, content,
token counts, created_at), `conversation_compaction` (derived summaries).
Tool-calling turns carry an ordered Anthropic-wire-shape block array in the
`content_json` column; text-only turns use `content`.

```bash
# Ten most recent conversations
sqlite3 ~/.config/cercano/conversations.db \
  "SELECT id, title, datetime(last_turn_at,'unixepoch') FROM conversations \
   ORDER BY last_turn_at DESC LIMIT 10;"

# A conversation's turns, in order (truncate long bodies)
sqlite3 ~/.config/cercano/conversations.db \
  "SELECT role, substr(coalesce(nullif(content,''), content_json),1,120) \
   FROM turns WHERE conversation_id='<id>' ORDER BY created_at;"

# Turns whose tool calls mention a given tool
sqlite3 ~/.config/cercano/conversations.db \
  "SELECT conversation_id, datetime(created_at,'unixepoch') FROM turns \
   WHERE content_json LIKE '%\"name\":\"Edit\"%' ORDER BY created_at DESC LIMIT 20;"
```

### `~/.config/cercano/telemetry.db`

Usage metrics: local/cloud requests, token counts, per-tool and per-day
breakdowns. Read via the `cercano_stats` MCP tool or `cercano stats` CLI
command; raw SQL works too (`.tables` to explore — the schema is small).

## Config surfaces

All under `~/.config/cercano/`: `config.yaml` (server: models, endpoints,
cloud provider), `permissions.yaml` (permission mode + allowlist), `ui.yaml`
(CLI), `mcp.yaml` (hosted MCP servers; per-project override in
`.cercano/mcp.yaml`).

## Where outstanding work is tracked

- `docs/features/cli/README.md` — task rollups per phase, deviations from
  plan, and the "still outstanding" list for the CLI track.
- `README.md` (repo root) — Feature TODOs: new features and existing
  improvements across the whole project.
- `docs/features/` and `docs/agent/` — per-feature `design.md` / `plan.md`
  pairs; a plan's unchecked boxes are its remaining work.
```

- [ ] **Step 2: Verify the kickoff path matches**

Run: `grep -n "self-dev.md" source/clients/cli/internal/slash/dev.go docs/agent/self-dev.md`
Expected: the kickoff prompt references `docs/agent/self-dev.md` and the file now exists at that path.

- [ ] **Step 3: Commit**

```bash
git add docs/agent/self-dev.md
git commit -m "docs: self-development guide for dev mode orientation"
```

---

### Task 5: Launcher `CERCANO_REPO` export + generator script + make target

**Files:**
- Modify: `source/server/scripts/cercano-launcher.sh` (one line after `REPO=` resolution, ~line 31)
- Create: `source/server/scripts/install-launcher.sh`
- Create: `source/server/scripts/install-launcher_test.sh`
- Modify: `source/server/Makefile` (add `launcher` target)

**Interfaces:**
- Consumes: `cercano-launcher.sh` as the single-source template (the generator rewrites its `REPO=` default line; it does not maintain a second copy of the launcher body).
- Produces: `~/bin/cercano` with the marker line `# generated by install-launcher.sh`, a baked `REPO="${CERCANO_REPO:-<abs repo root>}"` default, and `export CERCANO_REPO="$REPO"` — which is what makes `/d`'s env fallback (Task 1) always resolve for launcher-started sessions.

- [ ] **Step 1: Write the failing test script**

Create `source/server/scripts/install-launcher_test.sh` (mode 0755):

```bash
#!/usr/bin/env bash
# Test for install-launcher.sh. Runs the generator against a temp HOME and
# asserts the emitted launcher. Invoke directly: scripts/install-launcher_test.sh
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../../.." && pwd)"
TMP_HOME="$(mktemp -d)"
trap 'rm -rf "$TMP_HOME"' EXIT

fail() { echo "FAIL: $1" >&2; exit 1; }

# 1. Fresh install bakes the repo path, marker, export line, and exec bit.
HOME="$TMP_HOME" "$SCRIPT_DIR/install-launcher.sh" >/dev/null
DEST="$TMP_HOME/bin/cercano"
[[ -x "$DEST" ]] || fail "no executable at $DEST"
grep -qF "# generated by install-launcher.sh" "$DEST" || fail "marker line missing"
grep -qF "REPO=\"\${CERCANO_REPO:-$REPO_ROOT}\"" "$DEST" || fail "repo path not baked into REPO default"
grep -qF 'export CERCANO_REPO="$REPO"' "$DEST" || fail "export CERCANO_REPO line missing"

# 2. Refuses to overwrite a file it did not generate.
echo "#!/bin/sh" > "$DEST"
if HOME="$TMP_HOME" "$SCRIPT_DIR/install-launcher.sh" >/dev/null 2>&1; then
    fail "overwrote a non-generated file without --force"
fi

# 3. --force overwrites anything.
HOME="$TMP_HOME" "$SCRIPT_DIR/install-launcher.sh" --force >/dev/null
grep -qF "# generated by install-launcher.sh" "$DEST" || fail "--force did not regenerate"

# 4. Re-running over its own output succeeds without --force (marker present).
HOME="$TMP_HOME" "$SCRIPT_DIR/install-launcher.sh" >/dev/null || fail "re-run over own output failed"

echo "PASS"
```

- [ ] **Step 2: Run it to verify it fails**

Run: `chmod +x source/server/scripts/install-launcher_test.sh && source/server/scripts/install-launcher_test.sh`
Expected: FAIL — `install-launcher.sh: No such file or directory`.

- [ ] **Step 3: Add the export line to the launcher template**

In `source/server/scripts/cercano-launcher.sh`, directly after the `REPO=` line (~line 31):

```bash
REPO="${CERCANO_REPO:-$HOME/git_repos/bryan_costanich/cercano}"
# Exported so the CLI (and its /d development mode) can find the repo.
export CERCANO_REPO="$REPO"
```

(The first line already exists — only the comment + `export` line are added.)

- [ ] **Step 4: Write the generator**

Create `source/server/scripts/install-launcher.sh` (mode 0755):

```bash
#!/usr/bin/env bash
# Generates the dev launcher at ~/bin/cercano from cercano-launcher.sh, with
# THIS clone's repo path baked in as the default (CERCANO_REPO still
# overrides at runtime). Safe to re-run; refuses to clobber a file it did
# not generate unless --force is given.
#
# Usage: scripts/install-launcher.sh [--force]     (or: make launcher)
set -euo pipefail

FORCE=0
[[ "${1:-}" == "--force" ]] && FORCE=1

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../../.." && pwd)"
TEMPLATE="$SCRIPT_DIR/cercano-launcher.sh"
DEST_DIR="$HOME/bin"
DEST="$DEST_DIR/cercano"
MARKER="# generated by install-launcher.sh"

[[ -f "$TEMPLATE" ]] || { echo "install-launcher: template not found: $TEMPLATE" >&2; exit 1; }
if [[ ! -d "$REPO_ROOT/source/server/cmd/cercano" || ! -f "$REPO_ROOT/source/clients/cli/main.go" ]]; then
    echo "install-launcher: $REPO_ROOT does not look like the Cercano repo root" >&2
    exit 1
fi

if [[ -e "$DEST" && $FORCE -eq 0 ]] && ! grep -qF "$MARKER" "$DEST"; then
    echo "install-launcher: $DEST exists and was not generated by this script — rerun with --force to overwrite" >&2
    exit 1
fi

mkdir -p "$DEST_DIR"
# Emit: shebang, marker, then the template body with the REPO default
# rewritten to this clone. The launcher script stays the single source of
# truth; only the default path is substituted.
{
    head -n 1 "$TEMPLATE"
    echo "$MARKER"
    tail -n +2 "$TEMPLATE" | sed "s|^REPO=.*|REPO=\"\${CERCANO_REPO:-$REPO_ROOT}\"|"
} > "$DEST"
chmod +x "$DEST"
echo "installed $DEST (repo: $REPO_ROOT)"

case ":$PATH:" in
    *":$DEST_DIR:"*) ;;
    *) echo "warning: $DEST_DIR is not on your PATH" >&2 ;;
esac
```

- [ ] **Step 5: Run the test to verify it passes**

Run: `chmod +x source/server/scripts/install-launcher.sh && source/server/scripts/install-launcher_test.sh`
Expected: `PASS` (the test greps every load-bearing line of the emitted launcher; `bash -n` syntax-checking is unnecessary since the body is a verbatim copy of the template).

- [ ] **Step 6: Add the make target**

In `source/server/Makefile`: add `launcher` to the `.PHONY` line, and the target after `dev`:

```make
.PHONY: all build agent mcp dev clean test launcher
```

```make
# Install the dev launcher (~/bin/cercano) with this clone's path baked in.
launcher:
	scripts/install-launcher.sh
```

Run: `cd source/server && make -n launcher`
Expected: prints `scripts/install-launcher.sh`.

- [ ] **Step 7: Commit**

```bash
git add source/server/scripts/cercano-launcher.sh source/server/scripts/install-launcher.sh source/server/scripts/install-launcher_test.sh source/server/Makefile
git commit -m "feat(scripts): launcher generator with baked repo path + CERCANO_REPO export"
```

---

### Task 6: Documentation + manual acceptance

**Files:**
- Modify: `docs/agent/README.md` (slash command table, ~line 57)
- Modify: `docs/features/cli/dev-mode/design.md` (status line)

**Interfaces:**
- Consumes: everything prior.
- Produces: user-facing docs for `/d` and the generator.

- [ ] **Step 1: Add `/d` to the agent README's slash table and install docs**

In `docs/agent/README.md`, add a row to the slash-commands table (alphabetical-ish placement near the top is fine):

```markdown
| `/d [repo-path]` | Development mode — point the session at the Cercano repo and prime the agent to work on itself |
```

And in the "Install the dev launcher" section, replace the manual `cp`/`chmod` instructions with:

```markdown
### Install the dev launcher

The launcher rebuilds-if-stale and kills stale agents on each invocation.
Generate it (bakes this clone's path in, exports `CERCANO_REPO`):

```bash
cd source/server && make launcher
```
```

- [ ] **Step 2: Flip the design doc status**

In `docs/features/cli/dev-mode/design.md`, change the status line to:

```markdown
**Status:** Implemented 2026-07-03. See plan.md for the task breakdown.
```

(Use the actual completion date.)

- [ ] **Step 3: Manual acceptance walk-through**

With both binaries rebuilt (run the generated launcher, or `make dev` + CLI build):

1. From a directory **outside** the repo, with `CERCANO_REPO` set (launcher does this): `/d` → scrollback shows `dev mode: working on <repo>`; status bar shows lime `DEV` chip; kickoff streams; agent reads the three docs (R-tier, silent) and gives a short summary, then stops.
2. Follow up with "show me internal/slash/registry.go" → resolves against the repo, proving the workDir override reaches the agent.
3. `/d /some/random/dir` → error naming the marker requirement; no chip appears; override unchanged.
4. From inside the repo with `CERCANO_REPO` unset: `/d` → walk-up resolution works.

Record any deviations as bugs; do not ship with a failing acceptance item.

- [ ] **Step 4: Commit**

```bash
git add docs/agent/README.md docs/features/cli/dev-mode/design.md
git commit -m "docs: /d development mode + launcher generator"
```
