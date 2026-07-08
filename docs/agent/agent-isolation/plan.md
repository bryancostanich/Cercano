# Agent Execution Isolation — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Isolate each conversation's execution so conversations can't share mutable state (cwd, session, turn), building toward per-conversation worker processes with an in-process fallback for embedded mode.

**Architecture:** One execution core (`TurnRunner`) that owns all per-conversation state with zero process-global mutable state, deployed either as a spawned worker (crash isolation) or in-process (embedded). A decomposed host brokers shared services and fans a conversation's stream out to multiple attached surfaces. See `docs/agent/agent-isolation/design.md`.

**Tech Stack:** Go 1.21+, gRPC/protobuf, SQLite (`modernc.org/sqlite`), the existing `internal/meridian` supervisor pattern (Setpgid group, pidfile reap, health watch).

## Global Constraints

- Go module: `cercano/source/server`. Build: `cd source/server && go build ./...`. Test: `go test ./... -count=1`.
- No `os.Chdir` in request-handling code — ever. Process cwd is not per-conversation state.
- Commit messages: never the word "Claude" anywhere (message or trailers).
- The runner core must hold zero process-global mutable state — this is the load-bearing invariant, guarded by a concurrent-two-runners cross-talk test.
- Follow existing patterns; don't restructure unrelated code.

---

# Phase 1 — cwd fix (full plan, ships independently)

Threads the turn's `WorkDir` into tool execution and deletes the process-global `os.Chdir`. Fixes the live cross-repo bug today and forces `WorkDir` explicit in the tool path — the first step of the runner owning its execution state. `capabilities.Call` already has a `WorkDir` field; today it's always `""` because nothing sets it, so tools fall back to process cwd via the chdir.

**File structure for phase 1:**
- Create `internal/agenttools/workdir.go` — ctx carrier `WithWorkDir`/`WorkDirFromContext`.
- Create `internal/capabilities/builtins/paths.go` — `resolvePath` helper.
- Modify `internal/agent/toolloop.go` — `ToolLoopInput.WorkDir`; wrap ctx.
- Modify `internal/capabilities/agentadapter/adapter.go` — set `call.WorkDir` from ctx.
- Modify `internal/capabilities/builtins/{run,fs_read,fs_write,grep,git_read,git_write}.go` — resolve against `call.WorkDir`.
- Modify `internal/server/server.go` — delete the `os.Chdir`; set `WorkDir` on the loop input.
- Modify `internal/server/agentic_dispatch.go` — set `WorkDir` on the subagent loop input.

## Task 1: WorkDir plumbing — ctx carrier, resolve helper, adapter wiring

**Files:**
- Create: `internal/agenttools/workdir.go`
- Create: `internal/agenttools/workdir_test.go`
- Create: `internal/capabilities/builtins/paths.go`
- Create: `internal/capabilities/builtins/paths_test.go`
- Modify: `internal/agent/toolloop.go` (add `WorkDir` field; wrap ctx in `RunToolLoop`)
- Modify: `internal/capabilities/agentadapter/adapter.go` (`capTool.Execute` sets `call.WorkDir`)
- Test: `internal/capabilities/agentadapter/adapter_test.go`

**Interfaces:**
- Produces: `agenttools.WithWorkDir(ctx context.Context, dir string) context.Context`, `agenttools.WorkDirFromContext(ctx context.Context) string`, `builtins.resolvePath(workDir, p string) string`, `agent.ToolLoopInput.WorkDir string`.

- [ ] **Step 1: Write the failing test for the resolve helper**

`internal/capabilities/builtins/paths_test.go`:
```go
package builtins

import "testing"

func TestResolvePath(t *testing.T) {
	cases := []struct{ name, workDir, p, want string }{
		{"relative joins workdir", "/repo", "internal/x.go", "/repo/internal/x.go"},
		{"absolute unchanged", "/repo", "/etc/hosts", "/etc/hosts"},
		{"empty workdir unchanged", "", "internal/x.go", "internal/x.go"},
		{"empty path unchanged", "/repo", "", ""},
		{"dot resolves to workdir", "/repo", ".", "/repo"},
	}
	for _, c := range cases {
		if got := resolvePath(c.workDir, c.p); got != c.want {
			t.Errorf("%s: resolvePath(%q,%q)=%q want %q", c.name, c.workDir, c.p, got, c.want)
		}
	}
}
```

- [ ] **Step 2: Run it, verify it fails**

Run: `cd source/server && go test ./internal/capabilities/builtins/ -run TestResolvePath`
Expected: FAIL — `undefined: resolvePath`.

- [ ] **Step 3: Write the resolve helper**

`internal/capabilities/builtins/paths.go`:
```go
package builtins

import "path/filepath"

// resolvePath resolves p against workDir when p is relative; an absolute p is
// returned unchanged, and an empty workDir leaves p as-is (process-cwd
// fallback). Replaces the process-global os.Chdir the turn handler did, so
// concurrent turns in different workspaces never share a cwd.
func resolvePath(workDir, p string) string {
	if p == "" || workDir == "" || filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(workDir, p)
}
```

- [ ] **Step 4: Run it, verify pass**

Run: `go test ./internal/capabilities/builtins/ -run TestResolvePath`
Expected: PASS.

- [ ] **Step 5: Write the failing test for the ctx carrier**

`internal/agenttools/workdir_test.go`:
```go
package agenttools

import (
	"context"
	"testing"
)

func TestWithWorkDir_RoundTrip(t *testing.T) {
	if got := WorkDirFromContext(context.Background()); got != "" {
		t.Errorf("bare ctx WorkDir = %q, want empty", got)
	}
	ctx := WithWorkDir(context.Background(), "/repo")
	if got := WorkDirFromContext(ctx); got != "/repo" {
		t.Errorf("WorkDirFromContext = %q, want /repo", got)
	}
	// Empty dir is a no-op (does not shadow an outer value).
	if got := WorkDirFromContext(WithWorkDir(ctx, "")); got != "/repo" {
		t.Errorf("empty WithWorkDir should not clear; got %q", got)
	}
}
```

- [ ] **Step 6: Run it, verify it fails**

Run: `go test ./internal/agenttools/ -run TestWithWorkDir_RoundTrip`
Expected: FAIL — `undefined: WithWorkDir`.

- [ ] **Step 7: Write the ctx carrier**

`internal/agenttools/workdir.go`:
```go
package agenttools

import "context"

type workDirKey struct{}

// WithWorkDir attaches the turn's working directory to ctx so tool execution
// resolves relative paths against it instead of the process cwd. Empty dir is a
// no-op (falls back to process cwd — the pre-isolation behavior).
func WithWorkDir(ctx context.Context, dir string) context.Context {
	if dir == "" {
		return ctx
	}
	return context.WithValue(ctx, workDirKey{}, dir)
}

// WorkDirFromContext returns the working directory attached by WithWorkDir, or "".
func WorkDirFromContext(ctx context.Context) string {
	if v, ok := ctx.Value(workDirKey{}).(string); ok {
		return v
	}
	return ""
}
```

- [ ] **Step 8: Run it, verify pass**

Run: `go test ./internal/agenttools/ -run TestWithWorkDir_RoundTrip`
Expected: PASS.

- [ ] **Step 9: Add `WorkDir` to `ToolLoopInput` and wrap ctx in `RunToolLoop`**

In `internal/agent/toolloop.go`, add to the `ToolLoopInput` struct (near `System string`):
```go
	// WorkDir is the turn's working directory. Threaded onto ctx so tools
	// resolve relative paths against it — never via process cwd.
	WorkDir string
```
At the top of `func RunToolLoop(ctx context.Context, in ToolLoopInput) (...)`, immediately after the signature, rebind ctx:
```go
	ctx = agenttools.WithWorkDir(ctx, in.WorkDir)
```
(`agenttools` is already imported in this file.)

- [ ] **Step 10: Write the failing adapter test**

`internal/capabilities/agentadapter/adapter_test.go` — add:
```go
func TestCapTool_ThreadsWorkDirFromContext(t *testing.T) {
	var seen string
	cap := stubCapability{exec: func(ctx context.Context, call *capabilities.Call) (*capabilities.Result, error) {
		seen = call.WorkDir
		return capabilities.NewTextResult("ok"), nil
	}}
	tool := AsTool(cap, "Stub", capabilities.Services{})
	ctx := agenttools.WithWorkDir(context.Background(), "/repo")
	if _, err := tool.Execute(ctx, []byte(`{}`)); err != nil {
		t.Fatal(err)
	}
	if seen != "/repo" {
		t.Errorf("call.WorkDir = %q, want /repo", seen)
	}
}
```
If `stubCapability` doesn't exist in this test file, add a minimal one:
```go
type stubCapability struct {
	exec func(context.Context, *capabilities.Call) (*capabilities.Result, error)
}

func (s stubCapability) Name() string                 { return "stub" }
func (s stubCapability) Description() string           { return "" }
func (s stubCapability) Tier() capabilities.Tier       { return capabilities.TierRead }
func (s stubCapability) Schema() string                { return `{"type":"object"}` }
func (s stubCapability) Execute(ctx context.Context, c *capabilities.Call) (*capabilities.Result, error) {
	return s.exec(ctx, c)
}
```
(Check the real `capabilities.Capability` interface and `Tier` constant names in `internal/capabilities/capability.go`; match them exactly.)

- [ ] **Step 11: Run it, verify it fails**

Run: `go test ./internal/capabilities/agentadapter/ -run TestCapTool_ThreadsWorkDirFromContext`
Expected: FAIL — `call.WorkDir` is `""`.

- [ ] **Step 12: Wire the adapter**

In `internal/capabilities/agentadapter/adapter.go`, `capTool.Execute`, add `WorkDir` to the `Call`:
```go
	call := &capabilities.Call{
		Args:              args,
		WorkDir:           agenttools.WorkDirFromContext(ctx),
		RequestPermission: func(context.Context, string) (bool, error) { return true, nil },
		Emit:              func(string) {},
		Svc:               t.svc,
	}
```

- [ ] **Step 13: Run it, verify pass; then the package**

Run: `go test ./internal/capabilities/agentadapter/ -run TestCapTool_ThreadsWorkDirFromContext`
Expected: PASS.
Run: `go build ./... && go test ./internal/agent/ ./internal/agenttools/ ./internal/capabilities/... -count=1`
Expected: PASS.

- [ ] **Step 14: Commit**

```bash
git add internal/agenttools/workdir.go internal/agenttools/workdir_test.go \
        internal/capabilities/builtins/paths.go internal/capabilities/builtins/paths_test.go \
        internal/agent/toolloop.go internal/capabilities/agentadapter/adapter.go \
        internal/capabilities/agentadapter/adapter_test.go
git commit -m "feat(tools): thread WorkDir onto ctx into tool execution"
```

## Task 2: Bash resolves its cwd from WorkDir

**Files:**
- Modify: `internal/capabilities/builtins/run.go` (~line 67-70)
- Test: `internal/capabilities/builtins/run_test.go`

**Interfaces:**
- Consumes: `builtins.resolvePath`, `capabilities.Call.WorkDir`.

- [ ] **Step 1: Write the failing test**

Add to `internal/capabilities/builtins/run_test.go`:
```go
func TestRun_DefaultsCwdToWorkDir(t *testing.T) {
	dir := t.TempDir()
	call := &capabilities.Call{
		WorkDir: dir,
		Args:    []byte(`{"cmd":["pwd"]}`),
		Emit:    func(string) {},
	}
	res, err := runCmdCap{}.Execute(context.Background(), call)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(res.Text); got != dir {
		t.Errorf("pwd = %q, want WorkDir %q", got, dir)
	}
}
```
(Match the real cap type name — grep `internal/capabilities/builtins/run.go` for the `Execute` receiver; it may be `runCmdCap`/`runCap`. Use whatever it actually is. `res.Text` — match `capabilities.Result`'s text field.)

- [ ] **Step 2: Run it, verify it fails**

Run: `go test ./internal/capabilities/builtins/ -run TestRun_DefaultsCwdToWorkDir`
Expected: FAIL — pwd is the process cwd, not `dir`.

- [ ] **Step 3: Default cmd.Dir to WorkDir**

In `run.go`, replace the existing cwd block:
```go
	if a.Cwd != "" {
		cmd.Dir = a.Cwd
	}
```
with:
```go
	dir := a.Cwd
	if dir == "" {
		dir = call.WorkDir
	}
	if dir != "" {
		cmd.Dir = dir
	}
```

- [ ] **Step 4: Run it, verify pass**

Run: `go test ./internal/capabilities/builtins/ -run TestRun_DefaultsCwdToWorkDir`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/capabilities/builtins/run.go internal/capabilities/builtins/run_test.go
git commit -m "feat(tools): Bash defaults its cwd to the turn WorkDir"
```

## Task 3: fs_read resolves paths against WorkDir

**Files:**
- Modify: `internal/capabilities/builtins/fs_read.go` (Read/LS/stat Execute methods)
- Test: `internal/capabilities/builtins/fs_read_test.go`

- [ ] **Step 1: Write the failing test**

Add to `fs_read_test.go`:
```go
func TestReadFile_RelativePathResolvesAgainstWorkDir(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "hello.txt"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	call := &capabilities.Call{WorkDir: dir, Args: []byte(`{"path":"hello.txt"}`), Emit: func(string) {}}
	res, err := readFileCap{}.Execute(context.Background(), call)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.Contains(res.Text, "hi") {
		t.Errorf("content = %q, want to contain hi", res.Text)
	}
}
```
(Match the real cap type name for Read — grep `fs_read.go`.)

- [ ] **Step 2: Run it, verify it fails**

Run: `go test ./internal/capabilities/builtins/ -run TestReadFile_RelativePathResolvesAgainstWorkDir`
Expected: FAIL — relative path resolves against process cwd, file not found.

- [ ] **Step 3: Resolve a.Path early in each Execute**

In `fs_read.go`, in the Read, LS, and stat `Execute` methods, immediately after unpacking `a` from `call.Args` and the existing `if a.Path == "" { ... }` guard, add:
```go
	a.Path = resolvePath(call.WorkDir, a.Path)
```
All downstream uses (`os.ReadFile`, `os.ReadDir`, `os.Stat`, `filepath.Abs`, `filepath.Join(a.Path, name)`) then operate on the resolved path.

- [ ] **Step 4: Run it, verify pass**

Run: `go test ./internal/capabilities/builtins/ -run TestReadFile_RelativePathResolvesAgainstWorkDir`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/capabilities/builtins/fs_read.go internal/capabilities/builtins/fs_read_test.go
git commit -m "feat(tools): fs_read resolves relative paths against WorkDir"
```

## Task 4: fs_write resolves paths against WorkDir

**Files:**
- Modify: `internal/capabilities/builtins/fs_write.go` (Write + Edit Execute)
- Test: `internal/capabilities/builtins/fs_write_test.go`

- [ ] **Step 1: Write the failing test**

Add to `fs_write_test.go`:
```go
func TestWriteFile_RelativePathResolvesAgainstWorkDir(t *testing.T) {
	dir := t.TempDir()
	call := &capabilities.Call{WorkDir: dir, Args: []byte(`{"path":"out.txt","content":"data"}`), Emit: func(string) {}}
	if _, err := writeFileCap{}.Execute(context.Background(), call); err != nil {
		t.Fatalf("write: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(dir, "out.txt"))
	if err != nil || string(b) != "data" {
		t.Errorf("file not written under WorkDir: b=%q err=%v", b, err)
	}
}
```
(Match the real cap type name for Write.)

- [ ] **Step 2: Run it, verify it fails**

Run: `go test ./internal/capabilities/builtins/ -run TestWriteFile_RelativePathResolvesAgainstWorkDir`
Expected: FAIL — the file lands under the process cwd, not `dir`.

- [ ] **Step 3: Resolve a.Path early in Write and Edit Execute**

In `fs_write.go`, in both the Write and Edit `Execute` methods, right after unpacking `a` and its `if a.Path == "" { ... }` guard, add:
```go
	a.Path = resolvePath(call.WorkDir, a.Path)
```

- [ ] **Step 4: Run it, verify pass**

Run: `go test ./internal/capabilities/builtins/ -run TestWriteFile_RelativePathResolvesAgainstWorkDir`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/capabilities/builtins/fs_write.go internal/capabilities/builtins/fs_write_test.go
git commit -m "feat(tools): fs_write resolves relative paths against WorkDir"
```

## Task 5: grep + git tools default to WorkDir

**Files:**
- Modify: `internal/capabilities/builtins/grep.go` (~line 60), `git_read.go` (~47,171), `git_write.go` (~52,102)
- Test: `internal/capabilities/builtins/grep_test.go`, `git_read_test.go`

- [ ] **Step 1: Write the failing test (grep)**

Add to `grep_test.go`:
```go
func TestGrep_SearchesWorkDirByDefault(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("NEEDLE here"), 0o644); err != nil {
		t.Fatal(err)
	}
	call := &capabilities.Call{WorkDir: dir, Args: []byte(`{"pattern":"NEEDLE"}`), Emit: func(string) {}}
	res, err := grepCap{}.Execute(context.Background(), call)
	if err != nil {
		t.Fatalf("grep: %v", err)
	}
	if !strings.Contains(res.Text, "NEEDLE") {
		t.Errorf("grep missed the file under WorkDir: %q", res.Text)
	}
}
```
(Match the real grep cap type name.)

- [ ] **Step 2: Run it, verify it fails**

Run: `go test ./internal/capabilities/builtins/ -run TestGrep_SearchesWorkDirByDefault`
Expected: FAIL — grep runs against process cwd.

- [ ] **Step 3: Resolve/default dir in grep and git tools**

In `grep.go`, after the `if a.Path == "" { a.Path = "." }` default, add:
```go
	a.Path = resolvePath(call.WorkDir, a.Path)
```
In `git_read.go`, replace each `if a.Path != "" { cmd.Dir = a.Path }` with:
```go
	dir := a.Path
	if dir == "" {
		dir = call.WorkDir
	}
	if dir != "" {
		cmd.Dir = dir
	}
```
In `git_write.go`, replace each `if a.Cwd != "" { cmd.Dir = a.Cwd }` with the same `dir := a.Cwd; if dir == "" { dir = call.WorkDir }; if dir != "" { cmd.Dir = dir }` shape.

- [ ] **Step 4: Run it, verify pass; then the whole builtins package**

Run: `go test ./internal/capabilities/builtins/ -count=1`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/capabilities/builtins/grep.go internal/capabilities/builtins/git_read.go \
        internal/capabilities/builtins/git_write.go internal/capabilities/builtins/grep_test.go
git commit -m "feat(tools): grep and git tools default their dir to WorkDir"
```

## Task 6: Delete os.Chdir, set WorkDir on the loop, add the cross-talk guard

**Files:**
- Modify: `internal/server/server.go` (delete the `os.Chdir` block ~2336-2344; add `WorkDir` to the `ToolLoopInput` in `runMainLoop` ~2681)
- Modify: `internal/server/agentic_dispatch.go` (add `WorkDir: spec.WorkDir` to the subagent `ToolLoopInput` ~222)
- Test: `internal/capabilities/builtins/paths_test.go` (concurrent cross-talk guard)

**Interfaces:**
- Consumes: `agent.ToolLoopInput.WorkDir` (Task 1).

- [ ] **Step 1: Write the failing guard test**

This is the invariant test — two tool calls with different WorkDirs, concurrently, must not cross. Add to `paths_test.go`:
```go
func TestConcurrentWrites_NoWorkDirCrossTalk(t *testing.T) {
	dirA, dirB := t.TempDir(), t.TempDir()
	run := func(dir, name string) error {
		call := &capabilities.Call{
			WorkDir: dir,
			Args:    []byte(`{"path":"` + name + `","content":"x"}`),
			Emit:    func(string) {},
		}
		_, err := writeFileCap{}.Execute(context.Background(), call)
		return err
	}
	var wg sync.WaitGroup
	errs := make([]error, 2)
	wg.Add(2)
	go func() { defer wg.Done(); errs[0] = run(dirA, "a.txt") }()
	go func() { defer wg.Done(); errs[1] = run(dirB, "b.txt") }()
	wg.Wait()
	if errs[0] != nil || errs[1] != nil {
		t.Fatalf("writes errored: %v %v", errs[0], errs[1])
	}
	// Each file must be under its OWN workdir — impossible if they shared a cwd.
	if _, err := os.Stat(filepath.Join(dirA, "a.txt")); err != nil {
		t.Errorf("a.txt not under dirA: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dirB, "b.txt")); err != nil {
		t.Errorf("b.txt not under dirB: %v", err)
	}
}
```
(Imports: `sync`, `os`, `path/filepath`, `context`, `capabilities`.)

- [ ] **Step 2: Run it, verify it passes already**

Run: `go test ./internal/capabilities/builtins/ -run TestConcurrentWrites_NoWorkDirCrossTalk -race`
Expected: PASS — Tasks 1+4 already made writes WorkDir-relative. (This test is the standing guard for the invariant; it passes now and must never regress.)

- [ ] **Step 3: Set WorkDir on the main loop input**

In `internal/server/server.go`, `runMainLoop`, add to the `agent.ToolLoopInput{...}`:
```go
		WorkDir:             req.GetWorkDir(),
```

- [ ] **Step 4: Set WorkDir on the dispatch subagent input**

In `internal/server/agentic_dispatch.go`, in the `agent.ToolLoopInput{...}` built for the subagent loop, add:
```go
		WorkDir:        spec.WorkDir,
```

- [ ] **Step 5: Delete the os.Chdir block**

In `internal/server/server.go`, delete the entire block (the F2 comment through the closing brace, ~2329-2344):
```go
	// F2: propagate WorkDir into tool execution. ...
	if req.GetWorkDir() != "" {
		if prev, err := os.Getwd(); err == nil {
			if err := os.Chdir(req.GetWorkDir()); err == nil {
				defer os.Chdir(prev)
			} else {
				fmt.Fprintf(os.Stderr, "[tool-loop] chdir(%s) failed: %v\n", req.GetWorkDir(), err)
			}
		}
	}
```
If `os` is now unused in the file, `goimports` will flag it — leave it if still used elsewhere (it is).

- [ ] **Step 6: Build and run the full server suite**

Run: `go build ./... && go test ./... -count=1`
Expected: PASS. (Skip the known-flaky `TestDrainThenStop_HardStopsAfterGrace` only if it flakes under `-race`; without `-race` it passes.)

- [ ] **Step 7: Commit**

```bash
git add internal/server/server.go internal/server/agentic_dispatch.go internal/capabilities/builtins/paths_test.go
git commit -m "fix(server): delete process-global os.Chdir; turns resolve paths via WorkDir"
```

**Phase 1 done:** the live cross-repo cwd bug is fixed; the guard test protects the invariant. No `os.Chdir` remains in request handling.

---

# Phases 2–6 — broad strokes

Full plans written when we reach each phase; the seam in phase 3 will inform 4–6. These are the shape, file structure, and key interfaces — not step-by-step TDD.

## Phase 2 — Decompose the host into services

**Goal:** split the `Server` god-object into focused services behind interfaces, wired by a thin composition root. Pure refactor, no behavior change, in-process.

**New packages (one responsibility each), extracting from `internal/server/server.go`:**
- `internal/hostsvc/config` — canonical config + secrets, persist, change-notify (`currentConfig`/`cfgMu`, `secrets`, perm-broadcast).
- `internal/hostsvc/providers` — resolve provider by config+locus (`cloud/openLLMProvider`, `cloudFactory`, `router`, `coordinator`, `EngineRegistry`, `catalogManager`).
- `internal/hostsvc/runtimes` — spawned-subprocess supervision (`meridianMgr`, `runtimeManager`, `mcpManager`) — the family workers later join.
- `internal/hostsvc/persistence` — conversation store, retention, compaction, project context.
- `internal/hostsvc/tools` — tool + capability catalog + dispatch.
- `internal/hostsvc/permissions` — `permStore` + `pendingDecisions`.
- `internal/server` shrinks to the gRPC front door: hold references to the services, delegate.

**Key interfaces (each a small Go interface the front door depends on, not the concrete type):** `ConfigService`, `ProviderResolver`, `RuntimeSupervisors`, `ConversationStore`, `ToolCatalog`, `PermissionBroker`. Each owns its own synchronization (retires `cfgMu`/`permBcastMu` from the shared struct).

**Right-sizing:** one service extraction per task (move fields + methods, add the interface, repoint the front door, keep tests green). Independently reviewable; no behavior change per task.

## Phase 3 — Extract `TurnRunner` (in-process wrapper)

**Goal:** pull turn execution behind a `TurnRunner` interface whose implementation owns all per-conversation state with zero process-global mutable state; run it in-process (goroutine wrapper).

**File structure:**
- `internal/runner` — `TurnRunner` interface + `Core` (the execution engine: tool loop, owned cwd via WorkDir, owned session id, owned turn generation). Consumes the phase-2 services via narrow interfaces.
- `internal/server` — replace the inline `streamProcessRequestWithToolLoop` core with a call into an in-process `TurnRunner`.

**Key interface:**
```
type TurnRunner interface {
    RunTurn(ctx context.Context, req TurnRequest, sink EventSink) (TurnResult, error)
}
```
where `TurnRequest` carries conversation id, input, WorkDir, config snapshot, granted tools; `EventSink` is the existing event vocabulary (token/tool/permission/done); permission + persist are callbacks on the request.

**Load-bearing task:** the concurrent-two-runners cross-talk guard test (two `TurnRunner`s in one process, different WorkDirs and session ids, assert no cross-talk) — this is the invariant that makes both deployments safe, and it *is* embedded mode. **The correctness bug class is structurally dead at the end of this phase, before workers exist.**

## Phase 4 — Host broker + multi-surface attach (in-process)

**Goal:** per-conversation event fan-out; attach/detach; snapshot-on-attach. Still in-process runner.

**File structure:**
- `internal/broker` — per-conversation hub: owns the conv→runner mapping, fans runner events out to N attached surfaces, funnels submits in (serialized by the existing turn-exclusivity fence), tracks the driving surface for permission routing. Extends the `eventHub` fan-out concept, scoped per conversation.
- `source/proto/agent.proto` — add `Attach`/`Detach` RPCs and a `ConversationSnapshot` message (transcript + is-streaming). Regenerate (`docs/agent/proto-regen.md`).
- CLI: attach on open, render snapshot, subscribe to fan-out. Detach on close.

**Deliverable:** the same conversation live in CLI + VS Code simultaneously. Testable without any process boundary.

## Phase 5 — Worker wrapper + transport

**Goal:** flip `TurnRunner` from in-process to a spawned child process over a bidi gRPC `RunTurn` stream. Crash isolation lands here.

**File structure:**
- `cmd/cercano` — a `worker` subcommand: run one `TurnRunner`, serve `RunTurn` over a per-worker endpoint (unix socket or loopback).
- `internal/runtimes/worker` — host-side supervisor per conversation: spawn (Setpgid group + pidfile, reusing `internal/meridian`'s pattern), health-watch, idle-reap; the `workerRunner` impl of `TurnRunner` that drives the child's `RunTurn` stream.
- The broker selects in-process vs worker `TurnRunner` by config (workers default; in-process for embedded).

**Reuse:** the Setpgid/pidfile/reaper/`DrainThenStop` machinery from this session's Meridian work — workers are another supervised child family.

## Phase 6 — Harden

Idle-reap tuning, worker health/restart, crash-mid-turn recovery (host persists the partial, marks failed, surfaces "interrupted", reaps, next-turn respawns — using the generation fence), permission-driver detach fallback, multi-log observability (per-worker logs — which also retires the shared-singleton-log noise class for good).

---

## Self-review notes

- **Spec coverage:** phase 1 ↔ design "cwd fix (phase 1)"; phases 2–6 ↔ design roadmap 2–6 one-to-one. One-core-two-wrappers ↔ phase 3 (core) + phase 5 (worker) + phase 3 in-process wrapper. Multi-surface ↔ phase 4. Failure semantics ↔ phase 6. Host decomposition ↔ phase 2. Covered.
- **Type consistency:** `WorkDir` field name used consistently (`ToolLoopInput.WorkDir`, `Call.WorkDir`, `WithWorkDir`, `resolvePath(workDir, p)`). `TurnRunner.RunTurn` used consistently in phases 3/5.
- **Known unknowns (resolve at implementation, flagged not hidden):** exact builtin cap *type names* (e.g. `readFileCap`/`writeFileCap`/`grepCap`/`runCmdCap`) and `capabilities.Result` text field name — grep each file before writing its test; the plan says so at each task.
