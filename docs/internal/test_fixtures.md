# Test Fixtures + First-Class Python Validator

**Status:** Planning (design complete, not yet implemented).
**Builds on:** the validator-dispatch work (`validator_dispatch_20260529`).

## Problem

1. **Inline test fixtures.** Each validator test builds its sample project on the fly via `t.TempDir()` plus inline Go string literals (`minimalFsproj`, `validFs`, `brokenFs`, `minimalCargo`, etc.). Samples are synthetic, duplicated across tests, hard to extend, and can't be reused by debugging scripts or future integration tests.
2. **No Python support.** Cercano handles Go, .NET, Rust, and Node; a `pyproject.toml` project is treated as "unknown" and validation is skipped.
3. **Source-tree pollution.** `test/sandbox/calculator.go` is checked in at a path that integration tests both read from and write to (the test writes `calculator_test.go` next to it).

## Goals

- One canonical `test/fixtures/projects/<language>/<scenario>/` tree for every supported language, loaded through a shared loader.
- Tests that mutate a fixture work on a copy in a gitignored sandbox, never on the checked-in source.
- Python becomes first-class: validator + manifest detector + entry in `AutoValidator`'s dispatch map + fixtures.
- Inline-string fixtures fully replaced across validator tests.
- `test/sandbox/` becomes gitignored scratch space; the legacy `calculator.go` moves to a fixture.

## Non-Goals

- Test-runner support in non-Go validators (.NET / Rust / Node / Python stay compile-only).
- Fixture-management CLI, freshness lint rules, or fixture versioning.
- Refactoring tests outside the validator / detection / orchestration paths.
- Fixtures for FS-primitive tools (`read_file`, `write_file`, etc.) — they keep using `t.TempDir()`.

## Architecture

### Directory layout

`test/fixtures/projects/<lang>/<scenario>/` holds canonical samples:
- `_testdata/loader-smoke/` — tiny fixture for loader self-tests.
- `go/` — `valid`, `broken`, `needs-tests` (calculator.go only).
- `dotnet/`, `rust`, `node`, `python/` — each with `valid` and `broken`.

`test/sandbox/` is gitignored per-test scratch. The loader lives at `source/server/internal/testfixtures/` so any internal package can import it without an import cycle.

### Loader API (`internal/testfixtures/fixtures.go`)

- `Open(t, name) string` — absolute path to the read-only fixture; for read-only consumers. Unknown name fails the test with a list of available fixtures.
- `Copy(t, name) string` — copies the fixture into a fresh per-test sandbox under `test/sandbox/` and returns the copy path; for mutating consumers. Auto-cleanup via `t.Cleanup()`, skipped on failure when `KEEP_SANDBOX=1`.

`name` is the relative path under `test/fixtures/projects/`, e.g. `"dotnet/valid"`. Repo root is discovered by walking up from the cwd until `test/fixtures/projects/` is found. Stdlib only.

### Python validator (`internal/tools/python_validator.go`)

Parallels other language validators. Interpreter discovery: `python3`, fallback `python`, else `(Failed, err)` with an install/override hint. Validation command: `<interpreter> -m compileall -q .` in `workDir` — pure compile-check, no venv, no tests, no installs. Exit 0 → `Passed`; non-zero → `Failed` with cleaned combined output.

### Detection (`internal/tools/detect.go`)

Add `KindPython` to the `ProjectKind` enum + `String()` case. Detect `pyproject.toml` as lowest precedence: `Cargo.toml > go.mod > *.sln > *.fsproj/*.csproj > package.json (with build script) > pyproject.toml`.

### Auto-validator wiring (`internal/tools/auto_validator.go`)

`DefaultKindToValidator()` gains `KindPython: NewPythonValidator()`.

## Migration

Replace inline strings with `fixtures.Open` / `fixtures.Copy` in `go_validator_test.go`, `dotnet_validator_test.go`, `rust_validator_test.go`, `node_validator_test.go`. Rust/Node/Python tests use `Copy` (toolchains write `target/`, lockfiles, `__pycache__/` into the project dir). `detect_test.go` and `auto_validator_test.go` add Python subtests only. `sandbox_test.go` switches to `Copy(t, "go/needs-tests")`. `test/sandbox/calculator.go` moves to the `go/needs-tests` fixture; add `test/sandbox/` to `.gitignore`.

## Task list (planned, none complete)

- [ ] Task 1: `testfixtures` loader package (`fixtures.go`, `fixtures_test.go`, loader-smoke fixture).
- [ ] Task 2: Go fixtures (valid/broken/needs-tests) + migrate `go_validator_test.go`.
- [ ] Task 3: .NET fixtures + migrate `dotnet_validator_test.go`.
- [ ] Task 4: Rust fixtures + migrate `rust_validator_test.go` (uses `Copy`).
- [ ] Task 5: Node fixtures + migrate `node_validator_test.go` (uses `Copy`).
- [ ] Task 6: Python detection (`KindPython` + `pyproject.toml`) in `detect.go` + test.
- [ ] Task 7: `PythonValidator` + Python fixtures + tests.
- [ ] Task 8: Wire `KindPython` into `AutoValidator`.
- [ ] Task 9: Migrate `sandbox_test.go`, gitignore `test/sandbox/`, delete legacy `calculator.go`.

## Out of scope (future work)

- Test-runner support in non-Go validators (`dotnet test`, `cargo test`, `npm test`, `pytest`).
- Python interpreter version pinning (honoring `requires-python`).
- Fixtures for unsupported project types (Java, Ruby, Swift, ...).
- Migrating non-validator tests (dispatch, MCP, engine) to the loader.
