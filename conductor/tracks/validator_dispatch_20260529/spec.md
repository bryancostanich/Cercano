# Spec: Project-Aware Validator Dispatch for `cercano_local`

**Track:** `validator_dispatch_20260529`
**Issue:** [#6 — cercano_local agentic mode hardcoded to `go build` validation; ignores project type](https://github.com/bryancostanich/Cercano/issues/6)
**Status:** Design — pending user review

---

## Problem

`cercano_local`'s agentic generate-validate loop always invokes the Go toolchain (`go test -c -o /dev/null`, falling back to `go build ./...`) to validate generated code. On any non-Go project the validator fails immediately with `cannot find main module`, burning every retry attempt and surfacing a misleading error. `cercano_init` already produces project-aware `.cercano/context.md`, but the validator never reads it.

## Goals

1. The validator picks a compile command appropriate to the project type in `work_dir`.
2. The user can override the auto-detected command via a config file.
3. When no project type is detected, validation is skipped (rather than failing spuriously) and the user is informed.
4. v1 supports Go, .NET (C# / F#), Rust, and Node.
5. Existing Go behavior is preserved.

## Non-Goals

- Behavioral / runtime correctness checking. The validator remains a compile-only oracle.
- Auto-installing missing toolchains.
- Per-file validation. The whole `work_dir` is validated as before.
- Recursive manifest discovery. Only `work_dir` itself is scanned.
- Multi-project / monorepo dispatch. First match wins; user can override via config.

---

## Architecture

```
cmd/cercano/main.go ──┐
cmd/agent/main.go  ──┤── tools.NewAutoValidator(configLoader)
                     │
                     ▼
              tools.AutoValidator
                     │
        ┌────────────┼────────────┐
        ▼            ▼            ▼
   detect()    overrideFrom    dispatch to
   manifest    .cercano/        one of:
   scan        config.yaml      Go, Dotnet,
                                Rust, Node,
                                Custom, NoOp
```

`AutoValidator` is the only validator wired in `main.go`. It owns detection, override loading, and dispatch to a sub-validator. Each sub-validator implements the existing `Validator` interface (extended — see below).

### Interface change

```go
type Decision int

const (
    Passed Decision = iota
    Failed
    Skipped
)

type Validator interface {
    Validate(ctx context.Context, dir string) (Decision, error)
}
```

`Skipped` carries a human-readable warning via the returned `error` value (sentinel-wrapped — see Implementation Notes). The coordinator surfaces this warning in the streamed output instead of treating it as a retryable failure.

### Files

All new files live under `source/server/internal/tools/`:

| File | Purpose |
|---|---|
| `validator.go` | `Validator` interface + `Decision` enum |
| `auto_validator.go` | `AutoValidator`: detect → dispatch |
| `auto_validator_test.go` | Unit tests w/ mock sub-validators |
| `detect.go` | Manifest scanning (pure, table-driven) |
| `detect_test.go` | Tempdir fixtures, all precedence cases |
| `go_validator.go` | Renamed existing validator; one sub-validator |
| `dotnet_validator.go` | `dotnet build` |
| `dotnet_validator_test.go` | Gated on `dotnet` in PATH |
| `rust_validator.go` | `cargo build` |
| `rust_validator_test.go` | Gated on `cargo` in PATH |
| `node_validator.go` | `npm run build` |
| `node_validator_test.go` | Gated on `npm` in PATH |
| `custom_validator.go` | Runs a user-specified shell command |
| `custom_validator_test.go` | Mocked exec |
| `noop_validator.go` | Returns `Skipped` w/ warning |
| `noop_validator_test.go` | |

Config loading: new package `source/server/internal/projectconfig/` with `config.go` and `config_test.go` (separate from `internal/context/` because semantics differ — context is prompt-prepended text, projectconfig is structured behavior).

---

## Data flow

### Per-call flow inside `AutoValidator.Validate(ctx, workDir)`

1. Load `.cercano/config.yaml` (cached per `workDir`; cache key includes file mtime).
2. If `validator.skip == true` → return `(Skipped, warn("validation skipped per .cercano/config.yaml"))`.
3. If `validator.command` is non-empty → instantiate `CustomValidator{cmd}`, delegate.
4. Else scan manifests in `workDir` (non-recursive) in this precedence order; first match wins:
   1. `Cargo.toml` → `RustValidator`
   2. `go.mod` → `GoValidator`
   3. `*.sln` → `DotnetValidator{target: sln}`
   4. `*.fsproj` or `*.csproj` → `DotnetValidator{target: project}`
   5. `package.json` with a non-empty `scripts.build` field → `NodeValidator`
5. No match → return `(Skipped, warn("no recognized project manifest in <workDir>; validation skipped — set validator.command in .cercano/config.yaml to enable"))`.

### Config schema (`.cercano/config.yaml`)

```yaml
validator:
  command: "dotnet build src/MyProject.fsproj"   # optional, full override
  skip: false                                    # optional; if true, command ignored
```

Unknown keys are ignored (forward-compatibility). Invalid YAML → `AutoValidator` returns `(Failed, err)` with a clear "invalid .cercano/config.yaml: ..." message — surfaced to the user once, then they'll fix it.

### Coordinator changes (`source/server/internal/loop/adapters/adapters.go` ~ L165)

`validatorRun` currently calls `validator.Validate(ctx, workDir)` and treats `nil` as success / any `error` as failure. New behavior:

| Return | Loop action | User-visible |
|---|---|---|
| `Passed, nil` | Continue success path (current behavior) | Code returned |
| `Failed, err` | Feed `err` output back to LLM, retry | Existing retry / escalation flow |
| `Skipped, warn` | Exit loop after this generation | Code returned **with warning appended to streamed output** |

`Skipped` must not trigger retries. One generation, return as-is, surface the warning.

### Sub-validator behavior

| Sub-validator | Command | Pass condition |
|---|---|---|
| `GoValidator` | `go test -c -o /dev/null` then fallback `go build -o /dev/null ./...` | exit 0 |
| `DotnetValidator` | `dotnet build <target>` (sln if present else discovered project) | exit 0 |
| `RustValidator` | `cargo build` | exit 0 |
| `NodeValidator` | `npm run build` | exit 0 |
| `CustomValidator` | user-supplied string via `sh -c` | exit 0 |
| `NoOpValidator` | none | always `Skipped` |

All commands run with `cmd.Dir = workDir` and combined stdout+stderr captured as the error payload on failure.

### Missing-toolchain error

When a sub-validator's binary is not on `PATH`, return `(Failed, err)` with:
`"<lang> validator: command '<binary>' not found in PATH — install <toolchain> or set validator.command in .cercano/config.yaml to override"`.

This is a `Failed`, not `Skipped`, because the user explicitly has that project type — they probably want to fix the environment, not silently skip.

---

## Testing

### Unit tests (fast, no external binaries)

- **`detect_test.go`** — table-driven against `t.TempDir()` fixtures with touched manifest files. Cases:
  - `go.mod` alone → `go`
  - `*.fsproj` alone → `dotnet (project)`
  - `*.sln` + `*.fsproj` → `dotnet (sln)`
  - `*.csproj` alone → `dotnet (project)`
  - `Cargo.toml` alone → `rust`
  - `package.json` with `scripts.build` → `node`
  - `package.json` without `scripts.build` → no match
  - empty dir → no match
  - `Cargo.toml` + `go.mod` → `rust` (precedence)
  - `go.mod` + `*.fsproj` → `go` (precedence)
- **`auto_validator_test.go`** — mock `Validator`s + mock config loader. Cases:
  - `validator.skip: true` → `Skipped`, no sub-validator called
  - `validator.command` set → `CustomValidator` invoked with exact command
  - manifest match → correct sub-validator dispatched with `workDir`
  - no manifest → `Skipped` with the documented warning text
  - invalid YAML → `Failed` with parse error message
- **`noop_validator_test.go`** — always returns `Skipped`, warning matches.
- **`custom_validator_test.go`** — uses a known shell command (`exit 0` / `exit 1`) to assert pass/fail without depending on a real toolchain.
- **`projectconfig/config_test.go`** — parsing happy path, missing file (returns empty config + no error), malformed YAML, `skip + command` interaction (skip wins).

### Integration tests (gated)

Skip with `t.Skip()` when the relevant binary isn't on PATH:

- **`dotnet_validator_test.go`** — tempdir with minimal `.fsproj` that compiles → `Passed`; one with a syntax error → `Failed` with output containing the error.
- **`rust_validator_test.go`** — tempdir with `Cargo.toml` + minimal `src/lib.rs` → `Passed`; with a compile error → `Failed`.
- **`node_validator_test.go`** — `package.json` with `"build": "exit 0"` → `Passed`; `"build": "exit 1"` → `Failed`. (No npm install required because the script is trivial.)
- **`go_validator_test.go`** (existing) — keep, adapt to `Decision` return type.

### Coordinator test (`loop/adk_coordinator_test.go`)

Add: validator returns `Skipped` → loop exits after one generation, output contains the warning string, no retry attempted, no cloud escalation.

### Fixtures

Tempdirs built in-test via `t.TempDir()` + `os.WriteFile`. No checked-in sample projects.

---

## Implementation notes

- **Skipped warning transport.** Easiest mechanism: define `type SkipReason struct { Reason string }; func (s *SkipReason) Error() string { ... }`. `Skipped` decisions return a `*SkipReason` so the coordinator can type-assert and pull the message into the streamed output without overloading `error` semantics.
- **YAML library.** Use `gopkg.in/yaml.v3` (likely already a transitive dep — confirm during impl; if not, add it).
- **Config caching.** `projectconfig.Loader` keeps a `map[workDir]cachedConfig` keyed by absolute path, invalidated on file mtime change. Avoids re-reading on every iteration of the retry loop.
- **Wiring.** Both `cmd/cercano/main.go:89` and `cmd/agent/main.go:88` change from `tools.NewGoValidator()` to `tools.NewAutoValidator(projectconfig.NewLoader())`. No interface change at the call site (the existing `validator` variable just holds the new type).
- **Backward compat.** A Go project (which is the only thing that worked before) hits the `go.mod` branch and runs the same `GoValidator`. No behavior change for existing users.

## Open questions

None at design time. Sub-validator command-not-found message wording and skipped-warning wording can be polished during impl.

## Out of scope (future work)

- Python (`pyproject.toml` / `setup.py`) — listed in issue but lower priority; not in v1.
- Per-file validation (only edit the touched file's package/crate/project).
- Test-execution validation (currently compile-only).
- Recursive manifest discovery for monorepos.
