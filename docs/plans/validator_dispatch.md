# Project-Aware Validator Dispatch for `cercano_local`

> Status: Planned — not yet built. Migrated from `conductor/tracks/validator_dispatch_20260529/`.
> Issue: [#6 — cercano_local agentic mode hardcoded to `go build` validation; ignores project type](https://github.com/bryancostanich/Cercano/issues/6)

## Overview / Goal

### Problem

`cercano_local`'s agentic generate-validate loop always invokes the Go toolchain (`go test -c -o /dev/null`, falling back to `go build ./...`) to validate generated code. On any non-Go project the validator fails immediately with `cannot find main module`, burning every retry attempt and surfacing a misleading error. `cercano_init` already produces project-aware `.cercano/context.md`, but the validator never reads it.

### Goals

1. The validator picks a compile command appropriate to the project type in `work_dir`.
2. The user can override the auto-detected command via a config file.
3. When no project type is detected, validation is skipped (rather than failing spuriously) and the user is informed.
4. v1 supports Go, .NET (C# / F#), Rust, and Node.
5. Existing Go behavior is preserved.

### Non-Goals

- Behavioral / runtime correctness checking. The validator remains a compile-only oracle.
- Auto-installing missing toolchains.
- Per-file validation. The whole `work_dir` is validated as before.
- Recursive manifest discovery. Only `work_dir` itself is scanned.
- Multi-project / monorepo dispatch. First match wins; user can override via config.

## Design / Approach

### Architecture

```
cmd/cercano/main.go ──┐
cmd/agent/main.go  ──┤── tools.NewAutoValidator(configLoader)
                     ▼
              tools.AutoValidator
        ┌────────────┼────────────┐
        ▼            ▼            ▼
   detect()    overrideFrom    dispatch to one of:
   manifest    .cercano/        Go, Dotnet, Rust,
   scan        config.yaml      Node, Custom, NoOp
```

`AutoValidator` is the only validator wired in `main.go`. It owns detection, override loading, and dispatch to a sub-validator. Each sub-validator implements the extended `Validator` interface.

### Interface change

```go
type Decision int
const ( Passed Decision = iota; Failed; Skipped )

type Validator interface {
    Validate(ctx context.Context, dir string) (Decision, error)
}
```

`Skipped` carries a human-readable warning via a sentinel-wrapped error (`*SkipReason`). The coordinator surfaces this warning in the streamed output instead of treating it as a retryable failure.

### Files

All new validator files under `source/server/internal/tools/`: `validator.go`/`interfaces.go` (interface + `Decision` + `SkipReason`), `auto_validator.go`, `detect.go`, `go_validator.go` (renamed/adapted existing), `dotnet_validator.go`, `rust_validator.go`, `node_validator.go`, `custom_validator.go`, `noop_validator.go`, plus `_test.go` for each. Config loading: new package `source/server/internal/projectconfig/` (`config.go` + `config_test.go`) — separate from `internal/context/` because semantics differ (context is prompt-prepended text; projectconfig is structured behavior).

### Data flow inside `AutoValidator.Validate(ctx, workDir)`

1. Load `.cercano/config.yaml` (cached per `workDir`; cache key includes file mtime).
2. If `validator.skip == true` → `(Skipped, warn("validation skipped per .cercano/config.yaml"))`.
3. If `validator.command` non-empty → `CustomValidator{cmd}`, delegate.
4. Else scan manifests in `workDir` (non-recursive), first match wins:
   1. `Cargo.toml` → `RustValidator`
   2. `go.mod` → `GoValidator`
   3. `*.sln` → `DotnetValidator{target: sln}`
   4. `*.fsproj` or `*.csproj` → `DotnetValidator{target: project}`
   5. `package.json` with non-empty `scripts.build` → `NodeValidator`
5. No match → `(Skipped, warn("no recognized project manifest in <workDir>; validation skipped — set validator.command in .cercano/config.yaml to enable"))`.

### Config schema (`.cercano/config.yaml`)

```yaml
validator:
  command: "dotnet build src/MyProject.fsproj"   # optional, full override
  skip: false                                    # optional; if true, command ignored
```

Unknown keys ignored (forward-compat). Invalid YAML → `(Failed, err)` with "invalid .cercano/config.yaml: ..." message.

### Coordinator changes (`internal/loop/adapters/adapters.go` ~L165)

| Return | Loop action | User-visible |
|---|---|---|
| `Passed, nil` | Continue success path | Code returned |
| `Failed, err` | Feed `err` back to LLM, retry | Existing retry / escalation flow |
| `Skipped, warn` | Exit loop after this generation | Code returned, warning appended to streamed output |

`Skipped` must not trigger retries.

### Sub-validator behavior

| Sub-validator | Command | Pass condition |
|---|---|---|
| `GoValidator` | `go test -c -o /dev/null` then fallback `go build -o /dev/null ./...` | exit 0 |
| `DotnetValidator` | `dotnet build <target>` (sln if present else discovered project) | exit 0 |
| `RustValidator` | `cargo build` | exit 0 |
| `NodeValidator` | `npm run build` | exit 0 |
| `CustomValidator` | user-supplied string via `sh -c` | exit 0 |
| `NoOpValidator` | none | always `Skipped` |

All commands run with `cmd.Dir = workDir`, combined stdout+stderr captured as the error payload on failure.

### Missing-toolchain error

When a sub-validator's binary is not on `PATH`, return `(Failed, err)`: `"<lang> validator: command '<binary>' not found in PATH — install <toolchain> or set validator.command in .cercano/config.yaml to override"`. This is `Failed`, not `Skipped` — the user explicitly has that project type and probably wants to fix the environment.

### Implementation notes

- **Skipped warning transport.** `type SkipReason struct { Reason string }` implementing `error`; coordinator type-asserts to pull the message into streamed output.
- **YAML library.** `gopkg.in/yaml.v3` (likely already a transitive dep).
- **Config caching.** `projectconfig.Loader` keeps `map[workDir]cachedConfig` keyed by absolute path, invalidated on file mtime change.
- **Wiring.** `cmd/cercano/main.go:89` and `cmd/agent/main.go:88` change from `tools.NewGoValidator()` to `tools.NewAutoValidator(projectconfig.NewLoader())`. No call-site interface change.
- **Backward compat.** Go projects hit the `go.mod` branch and run the same `GoValidator`. No behavior change for existing users.

### Out of scope (future work)

Python (`pyproject.toml` / `setup.py`) — listed in issue, lower priority, not v1; per-file validation; test-execution validation; recursive manifest discovery for monorepos.

## Plan / Tasks

Architecture: `tools.AutoValidator` owns detection + dispatch, replacing `tools.NewGoValidator()` at startup. Each language gets a small sub-validator behind a `Validator` interface that returns `(Decision, error)` where `Decision ∈ {Passed, Failed, Skipped}`. The coordinator adapter learns about `Skipped` (exit once, surface warning, no retry).

Tech stack: Go 1.25, `os/exec`, `gopkg.in/yaml.v3`, `t.TempDir()` for fixtures.

> Each task follows TDD flow: write failing test → run/verify fail → implement → run/verify pass → commit.

### Task 1: Extend the `Validator` interface
- [ ] Step 1: Write failing test `tools/decision_test.go` (Decision.String, SkipReason implements error + errors.As).
- [ ] Step 2: Run test to verify it fails.
- [ ] Step 3: Replace `tools/interfaces.go` — add `Decision` enum + `String()`, `SkipReason`, new `Validator` signature.
- [ ] Step 4: Run test to verify it passes (package won't fully build until Task 2).
- [ ] Step 5: Commit.

### Task 2: Adapt `GoValidator` to the new interface
- [ ] Step 1: Update `go_validator_test.go` to consume `(Decision, error)` (Passed when no err, Failed otherwise).
- [ ] Step 2: Run tests to confirm compile failure.
- [ ] Step 3: Update `GoValidator.Validate` to return `(Decision, error)`.
- [ ] Step 4: Run tests to verify pass (wider build still fails until Task 11; optionally stub adapters.go temporarily).
- [ ] Step 5: Commit.

### Task 3: `NoOpValidator`
- [ ] Step 1: Write failing test `noop_validator_test.go` (returns Skipped with reason via *SkipReason).
- [ ] Step 2: Run test to verify it fails.
- [ ] Step 3: Implement `noop_validator.go` (`NewNoOpValidator(reason)`).
- [ ] Step 4: Run test to verify it passes.
- [ ] Step 5: Commit.

### Task 4: `CustomValidator`
- [ ] Step 1: Write failing test `custom_validator_test.go` (passes on zero exit, fails on non-zero with stderr).
- [ ] Step 2: Run test to verify it fails.
- [ ] Step 3: Implement `custom_validator.go` (`sh -c`, `cmd.Dir = workDir`).
- [ ] Step 4: Run test to verify it passes.
- [ ] Step 5: Commit.

### Task 5: Manifest detection
- [ ] Step 1: Write failing test `detect_test.go` (table-driven: go, fsproj, csproj, sln+fsproj, cargo, node w/ build, node w/o build, empty, rust>go precedence, go>fsproj precedence).
- [ ] Step 2: Run test to verify it fails.
- [ ] Step 3: Implement `detect.go` (`ProjectKind` enum, `Detect(workDir)`, `nodeHasBuildScript`).
- [ ] Step 4: Run test to verify it passes.
- [ ] Step 5: Commit.

### Task 6: `projectconfig` loader
- [ ] Step 1: Write failing test `projectconfig/config_test.go` (missing→empty, parses validator block, skip:true, malformed YAML error).
- [ ] Step 2: Run test to verify it fails.
- [ ] Step 3: Implement `projectconfig/config.go` (`Config`, `ValidatorConfig`, `Load(workDir)`).
- [ ] Step 4: Run test to verify it passes.
- [ ] Step 5: Commit.

### Task 7: `DotnetValidator` (PATH-gated integration test)
- [ ] Step 1: Write failing test `dotnet_validator_test.go` (passes on valid .fsproj, fails on broken, missing-binary returns Failed w/ hint).
- [ ] Step 2: Run test to verify it fails.
- [ ] Step 3: Implement `dotnet_validator.go` (LookPath gate, `dotnet build --nologo`).
- [ ] Step 4: Run test to verify it passes (integration SKIP if dotnet absent).
- [ ] Step 5: Commit.

### Task 8: `RustValidator` (PATH-gated integration test)
- [ ] Step 1: Write failing test `rust_validator_test.go` (valid Cargo project passes, broken fails, missing-binary Failed w/ hint).
- [ ] Step 2: Run test to verify it fails.
- [ ] Step 3: Implement `rust_validator.go` (LookPath gate, `cargo build --quiet`).
- [ ] Step 4: Run test to verify it passes (integration SKIP if cargo absent).
- [ ] Step 5: Commit.

### Task 9: `NodeValidator`
- [ ] Step 1: Write failing test `node_validator_test.go` (trivial build passes, failing build fails, missing-binary Failed w/ hint).
- [ ] Step 2: Run test to verify it fails.
- [ ] Step 3: Implement `node_validator.go` (LookPath gate, `npm run build --silent`).
- [ ] Step 4: Run test to verify it passes (integration SKIP if npm absent).
- [ ] Step 5: Commit.

### Task 10: `AutoValidator` orchestrator
- [ ] Step 1: Write failing test `auto_validator_test.go` (skip:true short-circuits, command override → custom, detect+dispatch, no manifest → Skipped, invalid config → Failed).
- [ ] Step 2: Run test to verify it fails.
- [ ] Step 3: Implement `auto_validator.go` (`ConfigLoader`, `KindToValidator`, `NewAutoValidator`, `DefaultKindToValidator`, `DefaultLoader`, `Validate`).
- [ ] Step 4: Run test to verify it passes.
- [ ] Step 5: Commit.

### Task 11: Update coordinator adapter to handle `Skipped`
- [ ] Step 1: Write failing coordinator test in `loop/adk_coordinator_test.go` (Skipped → exit after one generation, no retry, warning in output). Use the real fakes in that file.
- [ ] Step 2: Run test to verify it fails.
- [ ] Step 3: Update the fake validator to the new `(Decision, error)` signature.
- [ ] Step 4: Update `validatorRun` in `adapters.go` (~L165) to switch on Passed/Skipped/Failed; add `"errors"` import.
- [ ] Step 5: Run all `loop/...` tests to verify pass.
- [ ] Step 6: Commit.

### Task 12: Wire `AutoValidator` into both binaries
- [ ] Step 1: Update `cmd/cercano/main.go:89` → `tools.NewAutoValidator(tools.DefaultLoader(), tools.DefaultKindToValidator())`.
- [ ] Step 2: Same change at `cmd/agent/main.go:88`.
- [ ] Step 3: `go build ./...`.
- [ ] Step 4: Full `go test ./...` (gated integration tests SKIP where binaries missing).
- [ ] Step 5: Commit.

### Task 13: Manual smoke verification
- [ ] Step 1: Build the binary (`make build`).
- [ ] Step 2: Reproduce original failure on a non-Go project (`App.fsproj`, no go.mod) — confirm dotnet path or missing-binary hint.
- [ ] Step 3: Verify skip path on a manifest-less directory (one generation, "validation skipped: no recognized project manifest ...", no retries).
- [ ] Step 4: Verify override path (`.cercano/config.yaml` with `command: "true"` passes regardless of contents).
- [ ] Step 5: Verify Go path still works (go.mod + trivial package, unchanged behavior).
- [ ] Step 6: Update docs referencing validator behavior; commit referencing issue #6.

## Open Questions / Notes

None at design time. Sub-validator command-not-found message wording and skipped-warning wording can be polished during implementation.
