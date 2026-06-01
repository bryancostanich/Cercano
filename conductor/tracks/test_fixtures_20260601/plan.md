# Test Fixtures + Python Validator Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Introduce a shared `testfixtures` loader (Open + Copy) backed by a canonical `test/fixtures/projects/<lang>/<scenario>/` tree, migrate every existing inline-string fixture in validator tests over to the loader, add Python as a first-class supported project type (detector + validator + fixtures + wiring), and convert the existing polluting `test/sandbox/calculator.go` setup into a clean read-from-fixture + write-to-sandbox flow with `test/sandbox/` gitignored.

**Architecture:** New `internal/testfixtures/` package provides `Open(t, name)` (returns checked-in path; for read-only consumers) and `Copy(t, name)` (copies into per-test sandbox under `test/sandbox/`; for mutating consumers). Canonical samples live under `test/fixtures/projects/<lang>/<scenario>/`. Python validator parallels existing language validators in shape: `pyproject.toml` detection, `python3` (fallback `python`) compileall command, wired into `DefaultKindToValidator()`.

**Tech Stack:** Go 1.25 stdlib only for the loader, real toolchains (`go`, `dotnet`, `cargo`, `npm`, `python3`/`python`) for the validator integration tests, gated via `exec.LookPath` skips.

**Spec:** [conductor/tracks/test_fixtures_20260601/spec.md](./spec.md)

---

## File Map

| Action | Path (repo-relative) | Purpose |
|---|---|---|
| Create | `source/server/internal/testfixtures/fixtures.go` | `Open(t, name)` + `Copy(t, name)` + repo-root discovery |
| Create | `source/server/internal/testfixtures/fixtures_test.go` | Self-tests for the loader |
| Create | `test/fixtures/projects/_testdata/loader-smoke/a.txt` | Tiny fixture used only by loader self-tests |
| Create | `test/fixtures/projects/_testdata/loader-smoke/sub/b.txt` | Tests Copy preserves subdirs |
| Create | `test/fixtures/projects/go/valid/{go.mod,main.go}` | Compiles cleanly, no tests |
| Create | `test/fixtures/projects/go/broken/{go.mod,broken.go}` | Deliberate compile error |
| Create | `test/fixtures/projects/go/needs-tests/{go.mod,calculator.go}` | Source-only; integration test asks agent to add tests |
| Modify | `source/server/internal/tools/go_validator_test.go` | Replace inline strings with `fixtures.Open` calls |
| Create | `test/fixtures/projects/dotnet/valid/{Lib.fsproj,Lib.fs}` | F# library that builds |
| Create | `test/fixtures/projects/dotnet/broken/{Lib.fsproj,Lib.fs}` | F# with syntax error |
| Modify | `source/server/internal/tools/dotnet_validator_test.go` | Replace inline strings with `fixtures.Open` |
| Create | `test/fixtures/projects/rust/valid/{Cargo.toml,src/lib.rs}` | Rust lib that builds |
| Create | `test/fixtures/projects/rust/broken/{Cargo.toml,src/lib.rs}` | Rust with syntax error |
| Modify | `source/server/internal/tools/rust_validator_test.go` | Replace inline strings with `fixtures.Open` |
| Create | `test/fixtures/projects/node/valid/package.json` | Trivial `build` script that exits 0 |
| Create | `test/fixtures/projects/node/broken/package.json` | `build` script that exits 1 |
| Modify | `source/server/internal/tools/node_validator_test.go` | Replace inline JSON with `fixtures.Open` |
| Modify | `source/server/internal/tools/detect.go` | Add `KindPython` + `pyproject.toml` detection |
| Modify | `source/server/internal/tools/detect_test.go` | Add Python subtest |
| Create | `source/server/internal/tools/python_validator.go` | New `PythonValidator` |
| Create | `source/server/internal/tools/python_validator_test.go` | PATH-gated integration + missing-binary tests |
| Create | `test/fixtures/projects/python/valid/pyproject.toml` | Minimal pyproject |
| Create | `test/fixtures/projects/python/valid/mymod/__init__.py` | Package init |
| Create | `test/fixtures/projects/python/valid/mymod/core.py` | Module that compiles |
| Create | `test/fixtures/projects/python/broken/pyproject.toml` | Same minimal pyproject |
| Create | `test/fixtures/projects/python/broken/mymod/__init__.py` | Package init |
| Create | `test/fixtures/projects/python/broken/mymod/core.py` | Module with syntax error |
| Modify | `source/server/internal/tools/auto_validator.go` | Add `KindPython: NewPythonValidator()` to `DefaultKindToValidator` |
| Modify | `source/server/internal/tools/auto_validator_test.go` | Assert `DefaultKindToValidator()[KindPython]` non-nil |
| Modify | `test/integration/sandbox_test.go` | Switch from hardcoded path to `fixtures.Copy(t, "go/needs-tests")` |
| Delete | `test/sandbox/calculator.go` | Content moves into the `go/needs-tests` fixture |
| Modify | `.gitignore` | Add line: `test/sandbox/` |

---

### Task 1: `testfixtures` loader package

**Files:**
- Create: `source/server/internal/testfixtures/fixtures.go`
- Create: `source/server/internal/testfixtures/fixtures_test.go`
- Create: `test/fixtures/projects/_testdata/loader-smoke/a.txt`
- Create: `test/fixtures/projects/_testdata/loader-smoke/sub/b.txt`

- [ ] **Step 1: Create the loader-smoke fixture content**

```bash
mkdir -p test/fixtures/projects/_testdata/loader-smoke/sub
printf "alpha\n" > test/fixtures/projects/_testdata/loader-smoke/a.txt
printf "beta\n" > test/fixtures/projects/_testdata/loader-smoke/sub/b.txt
```

- [ ] **Step 2: Write the failing test**

Create `source/server/internal/testfixtures/fixtures_test.go`:

```go
package testfixtures

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOpen_ReturnsExistingPath(t *testing.T) {
	dir := Open(t, "_testdata/loader-smoke")
	if _, err := os.Stat(filepath.Join(dir, "a.txt")); err != nil {
		t.Fatalf("expected a.txt inside %s: %v", dir, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "sub", "b.txt")); err != nil {
		t.Fatalf("expected sub/b.txt inside %s: %v", dir, err)
	}
}

func TestOpen_UnknownFixtureFailsWithList(t *testing.T) {
	tt := &capturingT{T: t}
	Open(tt, "no-such-fixture-anywhere")
	if !tt.failed {
		t.Fatal("expected Open to mark test as failed for unknown fixture")
	}
	if !strings.Contains(tt.fatalMsg, "no-such-fixture-anywhere") {
		t.Errorf("fatal msg = %q, want it to name the missing fixture", tt.fatalMsg)
	}
}

func TestCopy_ProducesDistinctSandboxedCopies(t *testing.T) {
	a := Copy(t, "_testdata/loader-smoke")
	b := Copy(t, "_testdata/loader-smoke")
	if a == b {
		t.Fatalf("expected distinct sandbox paths, got %s twice", a)
	}
	for _, dir := range []string{a, b} {
		if _, err := os.Stat(filepath.Join(dir, "a.txt")); err != nil {
			t.Errorf("a.txt missing in copy %s: %v", dir, err)
		}
		if _, err := os.Stat(filepath.Join(dir, "sub", "b.txt")); err != nil {
			t.Errorf("sub/b.txt missing in copy %s: %v", dir, err)
		}
	}
}

func TestCopy_CleansUpAtTestEnd(t *testing.T) {
	var copyPath string
	t.Run("inner", func(inner *testing.T) {
		copyPath = Copy(inner, "_testdata/loader-smoke")
		if _, err := os.Stat(copyPath); err != nil {
			inner.Fatalf("sandbox copy missing during test: %v", err)
		}
	})
	// After inner finishes, cleanup should have run.
	if _, err := os.Stat(copyPath); !os.IsNotExist(err) {
		t.Errorf("expected sandbox %s to be removed after test, stat err = %v", copyPath, err)
	}
}

func TestCopy_KeepSandboxOnFailureWhenEnvSet(t *testing.T) {
	t.Setenv("KEEP_SANDBOX", "1")
	var copyPath string
	t.Run("inner", func(inner *testing.T) {
		copyPath = Copy(inner, "_testdata/loader-smoke")
		inner.Fail() // mark inner as failed
	})
	if _, err := os.Stat(copyPath); err != nil {
		t.Errorf("expected sandbox %s preserved under KEEP_SANDBOX=1, stat err = %v", copyPath, err)
	}
	// Clean it up manually so we don't litter.
	_ = os.RemoveAll(copyPath)
}

// capturingT records the first Fatal call so we can assert on it.
type capturingT struct {
	*testing.T
	failed   bool
	fatalMsg string
}

func (c *capturingT) Fatal(args ...any) {
	c.failed = true
	if len(args) > 0 {
		if s, ok := args[0].(string); ok {
			c.fatalMsg = s
		}
	}
}

func (c *capturingT) Fatalf(format string, args ...any) {
	c.failed = true
	c.fatalMsg = format
	for _, a := range args {
		c.fatalMsg += " " + asString(a)
	}
}

func asString(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}
```

- [ ] **Step 3: Run test to verify it fails**

Run: `cd source/server && go test ./internal/testfixtures/ -count=1`
Expected: FAIL — package does not exist.

- [ ] **Step 4: Write the loader implementation**

Create `source/server/internal/testfixtures/fixtures.go`:

```go
// Package testfixtures provides a shared loader for the test/fixtures/projects
// tree. Tests call Open for read-only access or Copy for a writable per-test
// sandbox under test/sandbox/.
package testfixtures

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

const (
	fixturesRel = "test/fixtures/projects"
	sandboxRel  = "test/sandbox"
)

var (
	repoRootOnce sync.Once
	repoRoot     string
	repoRootErr  error
)

// Open returns the absolute path to a read-only fixture under test/fixtures/projects.
// The name is the relative path under that directory, e.g. "go/valid".
// If the fixture is missing the test fails with a clear listing of what does exist.
func Open(t testing.TB, name string) string {
	t.Helper()
	root := mustRepoRoot(t)
	path := filepath.Join(root, fixturesRel, filepath.FromSlash(name))
	info, err := os.Stat(path)
	if err != nil || !info.IsDir() {
		available := listAvailableFixtures(root)
		t.Fatalf("testfixtures.Open(%q): fixture not found at %s; available: %s",
			name, path, strings.Join(available, ", "))
	}
	return path
}

// Copy copies the named fixture into a fresh per-test sandbox directory under
// test/sandbox/ and returns the copy's path. The copy is removed at test end
// via t.Cleanup, unless KEEP_SANDBOX=1 is set AND the test failed.
func Copy(t testing.TB, name string) string {
	t.Helper()
	src := Open(t, name)
	root := mustRepoRoot(t)
	sandboxBase := filepath.Join(root, sandboxRel)
	if err := os.MkdirAll(sandboxBase, 0755); err != nil {
		t.Fatalf("testfixtures.Copy(%q): mkdir sandbox base %s: %v", name, sandboxBase, err)
	}
	safe := strings.ReplaceAll(name, "/", "-")
	dst, err := os.MkdirTemp(sandboxBase, safe+"-")
	if err != nil {
		t.Fatalf("testfixtures.Copy(%q): mkdir sandbox copy: %v", name, err)
	}
	if err := copyTree(src, dst); err != nil {
		_ = os.RemoveAll(dst)
		t.Fatalf("testfixtures.Copy(%q): copy %s -> %s: %v", name, src, dst, err)
	}
	t.Cleanup(func() {
		if t.Failed() && os.Getenv("KEEP_SANDBOX") == "1" {
			return
		}
		_ = os.RemoveAll(dst)
	})
	return dst
}

func mustRepoRoot(t testing.TB) string {
	t.Helper()
	repoRootOnce.Do(func() {
		repoRoot, repoRootErr = findRepoRoot()
	})
	if repoRootErr != nil {
		t.Fatalf("testfixtures: %v", repoRootErr)
	}
	return repoRoot
}

func findRepoRoot() (string, error) {
	wd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	dir := wd
	for {
		if info, err := os.Stat(filepath.Join(dir, fixturesRel)); err == nil && info.IsDir() {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", &repoRootNotFoundError{startedAt: wd}
		}
		dir = parent
	}
}

type repoRootNotFoundError struct {
	startedAt string
}

func (e *repoRootNotFoundError) Error() string {
	return "could not locate " + fixturesRel + " by walking up from " + e.startedAt +
		" — are you running tests outside the Cercano repo?"
}

func listAvailableFixtures(root string) []string {
	out := []string{}
	base := filepath.Join(root, fixturesRel)
	_ = filepath.WalkDir(base, func(path string, d os.DirEntry, err error) error {
		if err != nil || !d.IsDir() || path == base {
			return nil
		}
		rel, _ := filepath.Rel(base, path)
		// Only list two-level paths (lang/scenario), not internal subdirs of fixtures.
		if strings.Count(rel, string(filepath.Separator)) == 1 {
			out = append(out, filepath.ToSlash(rel))
			return filepath.SkipDir
		}
		return nil
	})
	return out
}

func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0755)
		}
		return copyFile(path, target)
	})
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	info, err := in.Stat()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, info.Mode().Perm())
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}
```

- [ ] **Step 5: Run test to verify it passes**

Run: `cd source/server && go test ./internal/testfixtures/ -count=1 -v`
Expected: PASS (all five subtests).

- [ ] **Step 6: Commit**

```bash
git add source/server/internal/testfixtures/ test/fixtures/projects/_testdata/
git commit -m "feat(testfixtures): add Open/Copy loader package + smoke fixture"
```

---

### Task 2: Go fixtures + migrate `go_validator_test.go`

**Files:**
- Create: `test/fixtures/projects/go/valid/go.mod`
- Create: `test/fixtures/projects/go/valid/main.go`
- Create: `test/fixtures/projects/go/broken/go.mod`
- Create: `test/fixtures/projects/go/broken/broken.go`
- Create: `test/fixtures/projects/go/needs-tests/go.mod`
- Create: `test/fixtures/projects/go/needs-tests/calculator.go`
- Modify: `source/server/internal/tools/go_validator_test.go`

- [ ] **Step 1: Create the three Go fixtures**

```bash
mkdir -p test/fixtures/projects/go/valid test/fixtures/projects/go/broken test/fixtures/projects/go/needs-tests
```

Create `test/fixtures/projects/go/valid/go.mod`:

```
module fixture/valid

go 1.21
```

Create `test/fixtures/projects/go/valid/main.go`:

```go
package valid

// Sum returns a + b.
func Sum(a, b int) int { return a + b }
```

Create `test/fixtures/projects/go/broken/go.mod`:

```
module fixture/broken

go 1.21
```

Create `test/fixtures/projects/go/broken/broken.go`:

```go
package broken

// Deliberate compile error: undefined identifier.
func Boom() int { return undefined_identifier }
```

Create `test/fixtures/projects/go/needs-tests/go.mod`:

```
module fixture/needstests

go 1.21
```

Create `test/fixtures/projects/go/needs-tests/calculator.go` (the existing content from `test/sandbox/calculator.go`):

```go
package needstests

import "errors"

// Add returns the sum of a and b.
func Add(a, b int) int {
	return a + b
}

// Subtract returns the difference between a and b.
func Subtract(a, b int) int {
	return a - b
}

// Multiply returns the product of a and b.
func Multiply(a, b int) int {
	return a * b
}

// Divide returns the quotient of a divided by b.
// It returns an error if b is zero.
func Divide(a, b int) (int, error) {
	if b == 0 {
		return 0, errors.New("cannot divide by zero")
	}
	return a / b, nil
}
```

- [ ] **Step 2: Rewrite go_validator_test.go to use the loader**

Replace the contents of `source/server/internal/tools/go_validator_test.go` with:

```go
package tools_test

import (
	"context"
	"testing"

	"cercano/source/server/internal/testfixtures"
	"cercano/source/server/internal/tools"
)

func TestGoValidator_Validate(t *testing.T) {
	v := tools.NewGoValidator()
	ctx := context.Background()

	t.Run("ValidNoTests", func(t *testing.T) {
		dir := testfixtures.Open(t, "go/valid")
		decision, err := v.Validate(ctx, dir)
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if decision != tools.Passed {
			t.Errorf("got decision %s, want passed", decision)
		}
	})

	t.Run("CompilationFailure", func(t *testing.T) {
		dir := testfixtures.Open(t, "go/broken")
		decision, err := v.Validate(ctx, dir)
		if err == nil {
			t.Fatal("expected error for compilation failure, got nil")
		}
		if decision != tools.Failed {
			t.Errorf("got decision %s, want failed", decision)
		}
	})

	t.Run("ValidWithTests", func(t *testing.T) {
		// needs-tests has source but no _test.go file; the validator falls back
		// to 'go build' which should pass. This documents that scenario.
		dir := testfixtures.Open(t, "go/needs-tests")
		decision, err := v.Validate(ctx, dir)
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if decision != tools.Passed {
			t.Errorf("got decision %s, want passed", decision)
		}
	})
}
```

- [ ] **Step 3: Run test to verify it passes**

Run: `cd source/server && go test ./internal/tools/ -run TestGoValidator -count=1 -v`
Expected: PASS (three subtests).

- [ ] **Step 4: Commit**

```bash
git add test/fixtures/projects/go/ source/server/internal/tools/go_validator_test.go
git commit -m "test(tools): migrate GoValidator tests to test/fixtures/projects/go/"
```

---

### Task 3: .NET fixtures + migrate `dotnet_validator_test.go`

**Files:**
- Create: `test/fixtures/projects/dotnet/valid/Lib.fsproj`
- Create: `test/fixtures/projects/dotnet/valid/Lib.fs`
- Create: `test/fixtures/projects/dotnet/broken/Lib.fsproj`
- Create: `test/fixtures/projects/dotnet/broken/Lib.fs`
- Modify: `source/server/internal/tools/dotnet_validator_test.go`

- [ ] **Step 1: Create the fixtures**

```bash
mkdir -p test/fixtures/projects/dotnet/valid test/fixtures/projects/dotnet/broken
```

For BOTH `test/fixtures/projects/dotnet/valid/Lib.fsproj` AND `test/fixtures/projects/dotnet/broken/Lib.fsproj`, write:

```xml
<Project Sdk="Microsoft.NET.Sdk">
  <PropertyGroup>
    <OutputType>Library</OutputType>
    <TargetFramework>net8.0</TargetFramework>
  </PropertyGroup>
  <ItemGroup><Compile Include="Lib.fs" /></ItemGroup>
</Project>
```

Create `test/fixtures/projects/dotnet/valid/Lib.fs`:

```fsharp
module Lib
let add a b = a + b
```

Create `test/fixtures/projects/dotnet/broken/Lib.fs` (deliberate trailing `+`):

```fsharp
module Lib
let add a b = a + b +
```

- [ ] **Step 2: Rewrite dotnet_validator_test.go to use the loader**

Replace the contents of `source/server/internal/tools/dotnet_validator_test.go` with:

```go
package tools

import (
	"context"
	"os/exec"
	"strings"
	"testing"

	"cercano/source/server/internal/testfixtures"
)

func skipIfNoDotnet(t *testing.T) {
	if _, err := exec.LookPath("dotnet"); err != nil {
		t.Skip("dotnet not in PATH; skipping integration test")
	}
}

func TestDotnetValidator_PassesOnValidProject(t *testing.T) {
	skipIfNoDotnet(t)
	dir := testfixtures.Open(t, "dotnet/valid")
	v := NewDotnetValidator()
	decision, err := v.Validate(context.Background(), dir)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if decision != Passed {
		t.Errorf("got decision %s, want passed", decision)
	}
}

func TestDotnetValidator_FailsOnBrokenProject(t *testing.T) {
	skipIfNoDotnet(t)
	dir := testfixtures.Open(t, "dotnet/broken")
	v := NewDotnetValidator()
	decision, err := v.Validate(context.Background(), dir)
	if err == nil {
		t.Fatal("expected error")
	}
	if decision != Failed {
		t.Errorf("got decision %s, want failed", decision)
	}
}

func TestDotnetValidator_MissingBinaryReturnsFailedWithHint(t *testing.T) {
	t.Setenv("PATH", "")
	v := NewDotnetValidator()
	decision, err := v.Validate(context.Background(), t.TempDir())
	if err == nil {
		t.Fatal("expected error")
	}
	if decision != Failed {
		t.Errorf("got decision %s, want failed", decision)
	}
	if !strings.Contains(err.Error(), "validator.command") {
		t.Errorf("err = %q, want it to mention 'validator.command'", err.Error())
	}
}
```

NOTE: The missing-binary test stays on `t.TempDir()` since it doesn't need a real project — it's checking PATH behavior.

- [ ] **Step 3: Run test to verify it passes**

Run: `cd source/server && go test ./internal/tools/ -run TestDotnetValidator -count=1 -v`
Expected: PASS (integration tests run if dotnet on PATH, otherwise skip; missing-binary test always runs).

- [ ] **Step 4: Commit**

```bash
git add test/fixtures/projects/dotnet/ source/server/internal/tools/dotnet_validator_test.go
git commit -m "test(tools): migrate DotnetValidator tests to test/fixtures/projects/dotnet/"
```

---

### Task 4: Rust fixtures + migrate `rust_validator_test.go`

**Files:**
- Create: `test/fixtures/projects/rust/valid/Cargo.toml`
- Create: `test/fixtures/projects/rust/valid/src/lib.rs`
- Create: `test/fixtures/projects/rust/broken/Cargo.toml`
- Create: `test/fixtures/projects/rust/broken/src/lib.rs`
- Modify: `source/server/internal/tools/rust_validator_test.go`

- [ ] **Step 1: Create the fixtures**

```bash
mkdir -p test/fixtures/projects/rust/valid/src test/fixtures/projects/rust/broken/src
```

For BOTH `test/fixtures/projects/rust/valid/Cargo.toml` AND `test/fixtures/projects/rust/broken/Cargo.toml`, write:

```toml
[package]
name = "x"
version = "0.1.0"
edition = "2021"
[lib]
path = "src/lib.rs"
```

Create `test/fixtures/projects/rust/valid/src/lib.rs`:

```rust
pub fn add(a: i32, b: i32) -> i32 { a + b }
```

Create `test/fixtures/projects/rust/broken/src/lib.rs` (deliberate missing brace):

```rust
pub fn add(a: i32, b: i32) -> i32 { a + b
```

- [ ] **Step 2: Rewrite rust_validator_test.go to use the loader**

Replace the contents of `source/server/internal/tools/rust_validator_test.go` with:

```go
package tools

import (
	"context"
	"os/exec"
	"strings"
	"testing"

	"cercano/source/server/internal/testfixtures"
)

func skipIfNoCargo(t *testing.T) {
	if _, err := exec.LookPath("cargo"); err != nil {
		t.Skip("cargo not in PATH; skipping integration test")
	}
}

func TestRustValidator_PassesOnValidProject(t *testing.T) {
	skipIfNoCargo(t)
	dir := testfixtures.Open(t, "rust/valid")
	v := NewRustValidator()
	decision, err := v.Validate(context.Background(), dir)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if decision != Passed {
		t.Errorf("got decision %s, want passed", decision)
	}
}

func TestRustValidator_FailsOnBrokenProject(t *testing.T) {
	skipIfNoCargo(t)
	dir := testfixtures.Open(t, "rust/broken")
	v := NewRustValidator()
	decision, err := v.Validate(context.Background(), dir)
	if err == nil {
		t.Fatal("expected error")
	}
	if decision != Failed {
		t.Errorf("got decision %s, want failed", decision)
	}
}

func TestRustValidator_MissingBinaryReturnsFailedWithHint(t *testing.T) {
	t.Setenv("PATH", "")
	v := NewRustValidator()
	decision, err := v.Validate(context.Background(), t.TempDir())
	if err == nil {
		t.Fatal("expected error")
	}
	if decision != Failed {
		t.Errorf("got decision %s, want failed", decision)
	}
	if !strings.Contains(err.Error(), "validator.command") {
		t.Errorf("err = %q, want it to contain 'validator.command'", err.Error())
	}
}
```

NOTE: cargo's build pollutes the fixture directory with a `target/` folder. To avoid this in the read-only fixture, the test uses `testfixtures.Copy` instead.

Actually — re-think. `cargo build` writes a `target/` directory and a `Cargo.lock` to the project root. Running it against the read-only fixture will mutate the fixture. Switch the two integration subtests to `testfixtures.Copy` instead of `Open`:

```go
// Use Copy, not Open: cargo build writes Cargo.lock + target/ into the project.
dir := testfixtures.Copy(t, "rust/valid")
```

And the same for `rust/broken`.

- [ ] **Step 3: Run test to verify it passes**

Run: `cd source/server && go test ./internal/tools/ -run TestRustValidator -count=1 -v`
Expected: PASS (integration tests run if cargo on PATH; sandbox copies are cleaned up).

- [ ] **Step 4: Commit**

```bash
git add test/fixtures/projects/rust/ source/server/internal/tools/rust_validator_test.go
git commit -m "test(tools): migrate RustValidator tests to test/fixtures/projects/rust/"
```

---

### Task 5: Node fixtures + migrate `node_validator_test.go`

**Files:**
- Create: `test/fixtures/projects/node/valid/package.json`
- Create: `test/fixtures/projects/node/broken/package.json`
- Modify: `source/server/internal/tools/node_validator_test.go`

- [ ] **Step 1: Create the fixtures**

```bash
mkdir -p test/fixtures/projects/node/valid test/fixtures/projects/node/broken
```

Create `test/fixtures/projects/node/valid/package.json`:

```json
{
  "name": "x",
  "version": "0.0.1",
  "scripts": {
    "build": "exit 0"
  }
}
```

Create `test/fixtures/projects/node/broken/package.json`:

```json
{
  "name": "x",
  "version": "0.0.1",
  "scripts": {
    "build": "exit 1"
  }
}
```

- [ ] **Step 2: Rewrite node_validator_test.go to use the loader**

Replace the contents of `source/server/internal/tools/node_validator_test.go` with:

```go
package tools

import (
	"context"
	"os/exec"
	"strings"
	"testing"

	"cercano/source/server/internal/testfixtures"
)

func skipIfNoNpm(t *testing.T) {
	if _, err := exec.LookPath("npm"); err != nil {
		t.Skip("npm not in PATH; skipping integration test")
	}
}

func TestNodeValidator_PassesOnTrivialBuild(t *testing.T) {
	skipIfNoNpm(t)
	// Use Copy: npm may write a package-lock.json or similar artifacts.
	dir := testfixtures.Copy(t, "node/valid")
	v := NewNodeValidator()
	decision, err := v.Validate(context.Background(), dir)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if decision != Passed {
		t.Errorf("got decision %s, want passed", decision)
	}
}

func TestNodeValidator_FailsOnFailingBuild(t *testing.T) {
	skipIfNoNpm(t)
	dir := testfixtures.Copy(t, "node/broken")
	v := NewNodeValidator()
	decision, err := v.Validate(context.Background(), dir)
	if err == nil {
		t.Fatal("expected error")
	}
	if decision != Failed {
		t.Errorf("got decision %s, want failed", decision)
	}
}

func TestNodeValidator_MissingBinaryReturnsFailedWithHint(t *testing.T) {
	t.Setenv("PATH", "")
	v := NewNodeValidator()
	decision, err := v.Validate(context.Background(), t.TempDir())
	if err == nil {
		t.Fatal("expected error")
	}
	if decision != Failed {
		t.Errorf("got decision %s, want failed", decision)
	}
	if !strings.Contains(err.Error(), "validator.command") {
		t.Errorf("err = %q, want it to contain 'validator.command'", err.Error())
	}
}
```

- [ ] **Step 3: Run test to verify it passes**

Run: `cd source/server && go test ./internal/tools/ -run TestNodeValidator -count=1 -v`
Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add test/fixtures/projects/node/ source/server/internal/tools/node_validator_test.go
git commit -m "test(tools): migrate NodeValidator tests to test/fixtures/projects/node/"
```

---

### Task 6: Add Python detection (`KindPython` + `pyproject.toml`)

**Files:**
- Modify: `source/server/internal/tools/detect.go`
- Modify: `source/server/internal/tools/detect_test.go`

- [ ] **Step 1: Add the failing detection subtest**

Open `source/server/internal/tools/detect_test.go`. Find the `cases` slice in `TestDetect` and add one new entry after the `"node without build"` line:

```go
		{"python", map[string]string{"pyproject.toml": "[project]\nname=\"x\"\n"}, KindPython},
```

Also extend the precedence test set with one entry that asserts `pyproject.toml` is the lowest priority (so `go.mod` beats it):

```go
		{"go beats python", map[string]string{"go.mod": "module x", "pyproject.toml": ""}, KindGo},
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd source/server && go test ./internal/tools/ -run TestDetect -count=1`
Expected: FAIL — `undefined: KindPython`.

- [ ] **Step 3: Add KindPython + detection**

Edit `source/server/internal/tools/detect.go`:

Add `KindPython` to the iota block:

```go
const (
	KindUnknown ProjectKind = iota
	KindGo
	KindRust
	KindDotnetSolution
	KindDotnetProject
	KindNode
	KindPython
)
```

Add the `"python"` case to `String()`:

```go
	case KindPython:
		return "python"
```

In `Detect()`, declare a `hasPyProject` flag with the others:

```go
	hasCargo, hasGoMod, hasSln, hasDotnetProj, hasPackageJSON, hasPyProject := false, false, false, false, false, false
```

Add a case to the entry-scan switch:

```go
		case name == "pyproject.toml":
			hasPyProject = true
```

Add the dispatch case to the precedence switch, just before the default:

```go
	case hasPyProject:
		return KindPython, nil
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd source/server && go test ./internal/tools/ -run TestDetect -count=1 -v`
Expected: PASS (all existing subtests + the two new Python ones).

- [ ] **Step 5: Commit**

```bash
git add source/server/internal/tools/detect.go source/server/internal/tools/detect_test.go
git commit -m "feat(tools): add KindPython detection (pyproject.toml)"
```

---

### Task 7: `PythonValidator` + Python fixtures

**Files:**
- Create: `source/server/internal/tools/python_validator.go`
- Create: `source/server/internal/tools/python_validator_test.go`
- Create: `test/fixtures/projects/python/valid/pyproject.toml`
- Create: `test/fixtures/projects/python/valid/mymod/__init__.py`
- Create: `test/fixtures/projects/python/valid/mymod/core.py`
- Create: `test/fixtures/projects/python/broken/pyproject.toml`
- Create: `test/fixtures/projects/python/broken/mymod/__init__.py`
- Create: `test/fixtures/projects/python/broken/mymod/core.py`

- [ ] **Step 1: Create the Python fixtures**

```bash
mkdir -p test/fixtures/projects/python/valid/mymod test/fixtures/projects/python/broken/mymod
```

For BOTH `pyproject.toml` files (valid and broken), write:

```toml
[project]
name = "mymod"
version = "0.0.1"
```

Create both `mymod/__init__.py` files (valid and broken) with the same content:

```python
from .core import greet
```

Create `test/fixtures/projects/python/valid/mymod/core.py`:

```python
def greet(name: str) -> str:
    return f"hello, {name}"
```

Create `test/fixtures/projects/python/broken/mymod/core.py` (deliberate syntax error — unterminated string):

```python
def greet(name: str) -> str:
    return f"hello, {name
```

- [ ] **Step 2: Write the failing test**

Create `source/server/internal/tools/python_validator_test.go`:

```go
package tools

import (
	"context"
	"os/exec"
	"strings"
	"testing"

	"cercano/source/server/internal/testfixtures"
)

func skipIfNoPython(t *testing.T) {
	if _, err := exec.LookPath("python3"); err == nil {
		return
	}
	if _, err := exec.LookPath("python"); err == nil {
		return
	}
	t.Skip("neither python3 nor python in PATH; skipping integration test")
}

func TestPythonValidator_PassesOnValidProject(t *testing.T) {
	skipIfNoPython(t)
	// Use Copy: compileall writes __pycache__/ directories.
	dir := testfixtures.Copy(t, "python/valid")
	v := NewPythonValidator()
	decision, err := v.Validate(context.Background(), dir)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if decision != Passed {
		t.Errorf("got decision %s, want passed", decision)
	}
}

func TestPythonValidator_FailsOnBrokenProject(t *testing.T) {
	skipIfNoPython(t)
	dir := testfixtures.Copy(t, "python/broken")
	v := NewPythonValidator()
	decision, err := v.Validate(context.Background(), dir)
	if err == nil {
		t.Fatal("expected error")
	}
	if decision != Failed {
		t.Errorf("got decision %s, want failed", decision)
	}
	if !strings.Contains(err.Error(), "core.py") {
		t.Errorf("err = %q, want it to mention 'core.py' (the broken file)", err.Error())
	}
}

func TestPythonValidator_MissingBinaryReturnsFailedWithHint(t *testing.T) {
	t.Setenv("PATH", "")
	v := NewPythonValidator()
	decision, err := v.Validate(context.Background(), t.TempDir())
	if err == nil {
		t.Fatal("expected error")
	}
	if decision != Failed {
		t.Errorf("got decision %s, want failed", decision)
	}
	if !strings.Contains(err.Error(), "python3") || !strings.Contains(err.Error(), "validator.command") {
		t.Errorf("err = %q, want it to mention 'python3' and 'validator.command'", err.Error())
	}
}
```

- [ ] **Step 3: Run test to verify it fails**

Run: `cd source/server && go test ./internal/tools/ -run TestPythonValidator -count=1`
Expected: FAIL — `undefined: NewPythonValidator`.

- [ ] **Step 4: Write the implementation**

Create `source/server/internal/tools/python_validator.go`:

```go
package tools

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
)

// PythonValidator runs `python -m compileall .` (or `python3` fallback) in workDir.
// Compile-only: walks the tree, compiles every .py file, reports syntax errors.
// Does not need a venv, does not run tests, does not install anything.
type PythonValidator struct{}

func NewPythonValidator() *PythonValidator { return &PythonValidator{} }

func (v *PythonValidator) Validate(ctx context.Context, workDir string) (Decision, error) {
	bin := ""
	if _, err := exec.LookPath("python3"); err == nil {
		bin = "python3"
	} else if _, err := exec.LookPath("python"); err == nil {
		bin = "python"
	} else {
		return Failed, errors.New("python validator: neither 'python3' nor 'python' found in PATH — install Python 3 or set validator.command in .cercano/config.yaml to override")
	}
	cmd := exec.CommandContext(ctx, bin, "-m", "compileall", "-q", ".")
	cmd.Dir = workDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return Failed, fmt.Errorf("python compile failed:\n%s", cleanOutput(string(out)))
	}
	return Passed, nil
}
```

- [ ] **Step 5: Run test to verify it passes**

Run: `cd source/server && go test ./internal/tools/ -run TestPythonValidator -count=1 -v`
Expected: PASS (integration tests run if python3/python on PATH; missing-binary test always runs).

- [ ] **Step 6: Commit**

```bash
git add source/server/internal/tools/python_validator.go source/server/internal/tools/python_validator_test.go test/fixtures/projects/python/
git commit -m "feat(tools): add PythonValidator + fixtures (python3 with python fallback)"
```

---

### Task 8: Wire `KindPython` into `AutoValidator`

**Files:**
- Modify: `source/server/internal/tools/auto_validator.go`
- Modify: `source/server/internal/tools/auto_validator_test.go`

- [ ] **Step 1: Add the failing test**

In `source/server/internal/tools/auto_validator_test.go`, add a new test function at the end of the file:

```go
func TestDefaultKindToValidator_IncludesPython(t *testing.T) {
	m := DefaultKindToValidator()
	v, ok := m[KindPython]
	if !ok || v == nil {
		t.Fatalf("expected KindPython entry in DefaultKindToValidator, got %+v", m)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd source/server && go test ./internal/tools/ -run TestDefaultKindToValidator -count=1`
Expected: FAIL — `KindPython` entry missing from the map.

- [ ] **Step 3: Add KindPython to DefaultKindToValidator**

In `source/server/internal/tools/auto_validator.go`, find `DefaultKindToValidator()` and add the Python entry:

```go
func DefaultKindToValidator() KindToValidator {
	return KindToValidator{
		KindGo:             NewGoValidator(),
		KindRust:           NewRustValidator(),
		KindDotnetSolution: NewDotnetValidator(),
		KindDotnetProject:  NewDotnetValidator(),
		KindNode:           NewNodeValidator(),
		KindPython:         NewPythonValidator(),
	}
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd source/server && go test ./internal/tools/ -run TestDefaultKindToValidator -count=1 -v`
Expected: PASS.

Then verify the whole package and full project still pass:

```bash
cd source/server && go test ./... -count=1
```

Expected: all packages green.

- [ ] **Step 5: Commit**

```bash
git add source/server/internal/tools/auto_validator.go source/server/internal/tools/auto_validator_test.go
git commit -m "feat(tools): wire PythonValidator into AutoValidator default dispatch map"
```

---

### Task 9: Migrate `sandbox_test.go` + gitignore the sandbox + delete the legacy file

**Files:**
- Modify: `source/server/test/integration/sandbox_test.go`
- Delete: `test/sandbox/calculator.go` (repo-root path)
- Modify: `.gitignore`

- [ ] **Step 1: Read the existing sandbox_test.go to understand the path math**

Run: `cat source/server/test/integration/sandbox_test.go`
Expected: see the `sandboxDir := filepath.Join(wd, "../../../..", "test", "sandbox")` line and the `targetFile := filepath.Join(sandboxDir, "calculator.go")` line.

- [ ] **Step 2: Rewrite the test to use the fixture loader**

Replace the existing path-math + read-file block in `source/server/test/integration/sandbox_test.go` (lines computing `wd`, `sandboxDir`, `targetFile`, and the `os.ReadFile`) with a call to `testfixtures.Copy`. The new structure of the test body looks like:

```go
// 1. Setup: copy the needs-tests fixture into a sandbox the test can mutate.
sandboxDir := testfixtures.Copy(t, "go/needs-tests")
targetFile := filepath.Join(sandboxDir, "calculator.go")

// 2. Read Target Code
content, err := os.ReadFile(targetFile)
if err != nil {
    t.Fatalf("Failed to read calculator.go: %v", err)
}

// 3. Initialize Agent Components
provider := llm.NewLocalModelProvider(ollama.NewOllamaEngine("http://localhost:11434"), "qwen3-coder")
handler := tools.NewGenericGenerator(provider)
validator := tools.NewGoValidator()
coordinator := loop.NewGenerationCoordinator(handler, handler, validator)

// 4. Generate and Verify Tests with Self-Correction
ctx, cancel := context.WithTimeout(context.Background(), 300*time.Second)
defer cancel()

t.Log("Generating and verifying tests for calculator.go (with self-correction)...")
finalCode, err := coordinator.Coordinate(ctx, "Write table-driven unit tests for the following Go code using the standard 'testing' package.", string(content), sandboxDir, "calculator_test.go", nil)
// ... (rest of the existing assertions unchanged)
```

Add the import to the file's import block:

```go
"cercano/source/server/internal/testfixtures"
```

Remove the now-unused imports (`os`, `path/filepath` — keep them if other parts of the file still need them; `os` likely still needed for `os.ReadFile` + `os.Getenv("SANDBOX_TEST")`; `path/filepath` still needed for `Join`).

Also: keep the `SANDBOX_TEST=1` skip guard at the top of the test — this is an opt-in test that hits real Ollama, not a unit test.

- [ ] **Step 3: Delete the legacy committed sandbox file**

```bash
git rm test/sandbox/calculator.go
```

(The directory is left behind by git automatically once empty; the next test run will recreate it under the sandbox-creating code in `testfixtures.Copy`.)

- [ ] **Step 4: Add the sandbox to .gitignore**

Append one line to `.gitignore`:

```
test/sandbox/
```

- [ ] **Step 5: Run the migrated test (smoke check, gated on SANDBOX_TEST=1)**

Run: `cd source/server && SANDBOX_TEST=1 go test ./test/integration/ -run TestSandbox -count=1 -v`
Expected: if Ollama is running with the configured model, the test runs end-to-end and PASSES; if not, it fails with a clear connect error (same behavior as before the migration). Either way the test should NOT write into `test/sandbox/calculator.go` (which no longer exists at the source path).

If Ollama is not available, run without the env var to confirm the skip path is intact:

```bash
cd source/server && go test ./test/integration/ -run TestSandbox -count=1 -v
```

Expected: SKIP.

- [ ] **Step 6: Confirm the sandbox dir is properly ignored**

```bash
ls test/sandbox/ 2>&1 || echo "sandbox empty or missing — that's fine"
git status --short test/sandbox/ .gitignore
```

Expected: `git status` shows `.gitignore` as modified; if `test/sandbox/` has any leftover files from earlier runs, they should NOT appear in `git status` output (they're ignored).

- [ ] **Step 7: Commit**

```bash
git add source/server/test/integration/sandbox_test.go .gitignore
git commit -m "refactor(test): sandbox_test reads from go/needs-tests fixture; gitignore test/sandbox/

The legacy test/sandbox/calculator.go has been moved into the canonical
fixture tree at test/fixtures/projects/go/needs-tests/. The sandbox_test
now copies that fixture into a gitignored per-test sandbox under
test/sandbox/, writes the generated test alongside the copy, and cleans
up at test end. This stops the integration test from polluting the
source tree."
```

---

## Self-Review

**1. Spec coverage:**

| Spec section | Task(s) |
|---|---|
| testfixtures loader (Open + Copy + repo-root discovery) | T1 |
| Loader-smoke fixture | T1 |
| Go fixtures (valid, broken, needs-tests) + validator test migration | T2 |
| .NET fixtures (valid, broken) + validator test migration | T3 |
| Rust fixtures (valid, broken) + validator test migration | T4 |
| Node fixtures (valid, broken) + validator test migration | T5 |
| Python detection (KindPython + pyproject.toml) | T6 |
| PythonValidator + fixtures + tests | T7 |
| AutoValidator wiring for KindPython | T8 |
| sandbox_test.go rewire + sandbox gitignore + legacy file removal | T9 |

All spec sections accounted for.

**2. Placeholder scan:** None. Each step has either concrete code or a concrete command + expected output. The note in T4 about cargo polluting + the equivalent in T5/T7 are real implementation-relevant gotchas, not placeholders.

**3. Type consistency:** `Open(t, name)`, `Copy(t, name)`, `KindPython`, `NewPythonValidator`, `DefaultKindToValidator`, `ProjectKind` — all referenced consistently across the tasks that define and use them. The fixture path strings (`"go/valid"`, `"dotnet/broken"`, `"python/valid"`, etc.) match the directory names created earlier in the same task or earlier tasks.
