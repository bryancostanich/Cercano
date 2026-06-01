# Spec: Shared Test Fixtures + First-Class Python Validator

**Track:** `test_fixtures_20260601`
**Status:** Design — pending user review
**Builds on:** `validator_dispatch_20260529` (now complete; see that spec for the validator architecture this extends)

---

## Problem

Two pain points surfaced after the validator-dispatch work landed:

1. **Test fixtures are built inline.** Each validator test creates its sample project on the fly via `t.TempDir()` plus inline Go string literals (e.g. `minimalFsproj`, `validFs`, `brokenFs`, `minimalCargo`, `validRs`, `brokenRs`). The sample projects are small and synthetic, hard to extend, and duplicated across tests. There is no way to point a one-off debugging script or a future integration test at the same canonical sample.
2. **No Python support.** Cercano knows about Go, .NET, Rust, and Node, but a Python project (`pyproject.toml`) is treated as "unknown" and validation is skipped. Python is a common host language and warrants first-class treatment, parallel to the others.

Separately, an existing artifact reflects an earlier ad-hoc fixture pattern: `test/sandbox/calculator.go` is checked in at a path that integration tests both READ from and WRITE to (the test writes `calculator_test.go` next to it, polluting the source tree).

## Goals

1. A single `test/fixtures/projects/<language>/<scenario>/` tree holds canonical sample projects for every supported language. Tests load them through a small shared loader.
2. A test that mutates a fixture works on a copy in a gitignored sandbox, never on the checked-in source.
3. Python is a first-class supported project type: it has a validator, a manifest detector, an entry in `AutoValidator`'s default dispatch map, and its own fixtures.
4. The existing inline-string fixture pattern across validator tests is fully replaced.
5. The existing polluting `test/sandbox/calculator.go` setup is fixed: its content moves to a fixture, and `test/sandbox/` becomes gitignored scratch space.

## Non-Goals

- Test-runner support in non-Go validators (.NET / Rust / Node / Python remain compile-only — same as today).
- Building a fixture-management CLI, lint rules for fixture freshness, or fixture versioning. The fixtures are just checked-in files.
- Refactoring tests outside the validator / detection / orchestration code paths. Dispatch and other-package tests stay as they are.
- Test fixtures for non-project tools (`read_file`, `write_file`, `shell_exec`, `web_fetch`) — those test FS primitives, not whole projects, and continue using `t.TempDir()` directly.

---

## Architecture

### Directory layout

```
test/
├── fixtures/
│   └── projects/
│       ├── _testdata/loader-smoke/   (tiny fixture used only by testfixtures self-tests)
│       ├── go/
│       │   ├── valid/                (compiles cleanly; no tests)
│       │   ├── broken/               (deliberate compile error)
│       │   └── needs-tests/          (calculator.go only; integration test asks the agent to add tests)
│       ├── dotnet/
│       │   ├── valid/                (Lib.fsproj + Lib.fs that builds)
│       │   └── broken/               (same shape with syntax error)
│       ├── rust/
│       │   ├── valid/                (Cargo.toml + src/lib.rs that builds)
│       │   └── broken/
│       ├── node/
│       │   ├── valid/                (package.json with a 'build' script that exits 0)
│       │   └── broken/               (package.json with a 'build' script that exits non-zero)
│       └── python/
│           ├── valid/                (pyproject.toml + a small package that compiles)
│           └── broken/               (pyproject.toml + a module with a syntax error)
├── sandbox/                          (gitignored; per-test scratch dirs land here)
└── integration/                      (existing)

source/server/
├── internal/
│   ├── testfixtures/
│   │   ├── fixtures.go               (loader package: Open + Copy)
│   │   └── fixtures_test.go
│   └── tools/
│       ├── python_validator.go       (new; parallel to dotnet/rust/node/go validators)
│       ├── python_validator_test.go  (PATH-gated)
│       ├── detect.go                 (add KindPython + pyproject.toml detection)
│       ├── auto_validator.go         (add KindPython → NewPythonValidator() in DefaultKindToValidator)
│       └── (existing language validator tests, rewired to use testfixtures)

.gitignore                            (add line: test/sandbox/)
```

`testfixtures` lives at `internal/testfixtures/` so any test in any internal package can import it without an import cycle.

### Loader API (`internal/testfixtures/fixtures.go`)

```go
// Open returns the absolute path to the read-only fixture directory.
// Use this when the test only reads from the fixture and does not modify it.
//
// If the fixture name is unknown, the test fails with a clear error listing
// the available fixtures so a typo is immediately visible.
func Open(t testing.TB, name string) string

// Copy copies the named fixture into a fresh per-test sandbox directory
// under test/sandbox/ and returns the copy's path. Tests can mutate the
// copy freely. Cleanup is automatic via t.Cleanup() at test end.
//
// If the env var KEEP_SANDBOX=1 is set, the sandbox copy is not removed
// after a failed test, so the artifacts can be inspected.
func Copy(t testing.TB, name string) string
```

`name` is the relative path under `test/fixtures/projects/`, e.g. `"dotnet/valid"`.

**Repo-root discovery.** The loader walks up from the current working directory until it finds a directory containing `test/fixtures/projects/`. This works regardless of which package's test directory it's called from. If discovery fails, the test fails with `"could not locate test/fixtures/projects/ — are you running tests outside the Cercano repo?"`.

**Copy mechanics.** `Copy` calls `os.MkdirTemp("<repo-root>/test/sandbox/", "<fixture-name>-")` where `<fixture-name>` has any `/` replaced with `-` for filename safety. The fixture tree is walked with `filepath.WalkDir` and copied file-by-file preserving mode. `t.Cleanup` runs `os.RemoveAll` on the sandbox path unless `t.Failed() && os.Getenv("KEEP_SANDBOX") == "1"`.

**Concurrency.** Two tests calling `Copy("go/valid")` get distinct sandbox paths via `MkdirTemp`'s random suffix. No locking needed.

### Python validator (`internal/tools/python_validator.go`)

Parallel to the other language validators:

```go
type PythonValidator struct{}

func NewPythonValidator() *PythonValidator
func (v *PythonValidator) Validate(ctx context.Context, workDir string) (Decision, error)
```

**Interpreter discovery.** `Validate` looks up the interpreter once at the start of the call:

1. `exec.LookPath("python3")` — if found, use this binary.
2. Otherwise `exec.LookPath("python")` — if found, use this binary.
3. Otherwise return `(Failed, err)` with the message: `"python validator: neither 'python3' nor 'python' found in PATH — install Python 3 or set validator.command in .cercano/config.yaml to override"`.

**Validation command.** `<interpreter> -m compileall -q .` run in `workDir`. This walks the directory, compiles every `.py` file, and reports syntax errors. It needs no venv, runs no tests, and installs nothing — it's a pure compile-check, matching Cercano's "compile-only oracle" philosophy.

**Result mapping.**
- Exit 0 → `(Passed, nil)`.
- Non-zero exit → `(Failed, fmt.Errorf("python compile failed:\n%s", cleanOutput(string(out))))` where `out` is the combined stdout+stderr.

### Detection (`internal/tools/detect.go`)

Add `KindPython` to the `ProjectKind` enum and its `String()` case (`"python"`). Add detection for `pyproject.toml`:

- In the per-entry scan loop, treat a file named `pyproject.toml` as setting a `hasPyProject` flag.
- In the precedence switch, add `case hasPyProject: return KindPython, nil` between the existing Node case and the default. Final precedence list (first match wins): `Cargo.toml > go.mod > *.sln > *.fsproj/*.csproj > package.json (with build script) > pyproject.toml`.

### Auto-validator wiring (`internal/tools/auto_validator.go`)

`DefaultKindToValidator()` gains one entry: `KindPython: NewPythonValidator()`.

---

## Data flow

No changes to the validator dispatch flow — the architecture is unchanged from `validator_dispatch_20260529`. Detection happens, the matching sub-validator is invoked, and the result propagates through `AutoValidator` as before.

The new fixture loader is purely a test-time helper; it has no runtime presence in any binary.

---

## Migration plan (existing → new)

| Test file | Change |
|---|---|
| `internal/tools/dotnet_validator_test.go` | Replace `minimalFsproj` / `validFs` / `brokenFs` inline strings with `fixtures.Open(t, "dotnet/valid")` / `fixtures.Open(t, "dotnet/broken")`. |
| `internal/tools/rust_validator_test.go` | Replace `minimalCargo` / `validRs` / `brokenRs` inline strings with `fixtures.Open(t, "rust/valid")` / `fixtures.Open(t, "rust/broken")`. |
| `internal/tools/node_validator_test.go` | Replace inline JSON strings with `fixtures.Open(t, "node/valid")` / `fixtures.Open(t, "node/broken")`. |
| `internal/tools/go_validator_test.go` | The three subtests currently inline `go.mod` and `.go` files into a tempdir. Rewire to use `fixtures.Open(t, "go/valid")` / `fixtures.Open(t, "go/broken")` / `fixtures.Open(t, "go/needs-tests")` respectively. (`needs-tests` is the existing pass-with-tests case.) |
| `internal/tools/detect_test.go` | No migration — these tests use manifest-stub combinations, not real projects. Add one new subtest: `pyproject.toml alone → KindPython`. |
| `internal/tools/auto_validator_test.go` | No migration — these test orchestration with stubs. No new cases needed; Python falls out of the existing parameterization. |
| `test/integration/sandbox_test.go` | Rewire: `dir := fixtures.Copy(t, "go/needs-tests")`, read `dir/calculator.go`, write the generated test back to `dir/calculator_test.go`. Validator runs against `dir`. |
| `test/sandbox/calculator.go` (existing file) | Moved to `test/fixtures/projects/go/needs-tests/calculator.go`. Old location becomes gitignored sandbox scratch. |

After this migration, no validator test contains inline fixture strings — everything loads from the fixture tree.

---

## Error handling

- **Unknown fixture name in `Open`/`Copy`.** Test fails immediately with a message listing the names that DO exist under `test/fixtures/projects/`. No silent fallback.
- **Loader cannot find repo root.** Test fails with the message above; this only happens if the loader is invoked outside the repo (e.g. during a `go install` of a dependent module that imports `testfixtures` accidentally — shouldn't happen).
- **Copy fails partway** (disk full, permission denied). Test fails with the underlying error wrapped: `"fixtures.Copy(%q): %w"`. `t.Cleanup` still runs and attempts to remove whatever was copied.
- **Python interpreter not on PATH.** Validator returns `(Failed, err)` with the two-binary message above. Tests gated on the interpreter's presence skip.

## Testing strategy

| Component | Test approach |
|---|---|
| `testfixtures` loader | Self-tests under `internal/testfixtures/fixtures_test.go`. Uses a private fixture at `test/fixtures/projects/_testdata/loader-smoke/`. Covers `Open` happy + unknown-name, `Copy` distinctness + tree integrity + cleanup, `KEEP_SANDBOX=1` retention. |
| `PythonValidator` | `internal/tools/python_validator_test.go`. Three subtests: happy (Python on PATH, `python/valid` → Passed), failure (`python/broken` → Failed with syntax error in message), missing-binary (`PATH=""` → Failed with two-binary message). PATH-gated. |
| `Detect()` | One new subtest in `TestDetect`: `pyproject.toml alone → KindPython`. |
| `AutoValidator` | One new subtest: `DefaultKindToValidator()[KindPython]` is non-nil. Existing dispatch test for KindPython works without modification. |
| Migrated tests (Go/.NET/Rust/Node validators) | Existing asserts retained; only fixture acquisition changes. Tests passing before and after = migration correct. |
| Rewired `sandbox_test.go` | Same assertions as today; only the fixture loading and write target change. |

## Implementation notes

- **Fixture content uniformity.** Each `valid/` fixture compiles cleanly with its native toolchain; each `broken/` has a deliberate syntax error in exactly one source file. The error should be obviously deliberate (e.g. an extra `+` at end-of-line in F#, an unterminated function in Rust) so a maintainer can see the intent without reading docs.
- **Fixture stability.** The fixtures are part of the test surface. Changing them is a test-affecting change and should be explicit in a commit.
- **`.gitignore` change.** Adding `test/sandbox/` is one line. The existing committed file `test/sandbox/calculator.go` is removed by the migration commit; the empty `test/sandbox/` directory is created by tests at runtime as needed.
- **No new external deps.** Loader uses stdlib only. Python validator uses `os/exec`. Fixture content for `.fsproj` / `Cargo.toml` / `package.json` / `pyproject.toml` is just checked-in text files; no manifest generation tools.

## Open questions

None at design time.

## Out of scope (future work)

- Adding test-runner support to non-Go validators (`dotnet test`, `cargo test`, `npm test`, `pytest`).
- Python interpreter version pinning (e.g. honor `requires-python` in `pyproject.toml`).
- Fixtures for project types Cercano doesn't yet support (Java, Ruby, Swift, etc.). Those land when the corresponding validator does.
- Migrating non-validator tests (dispatch, MCP, engine) to use the fixture loader. Today they don't have a fixture concept; if that changes, they can adopt the loader.
