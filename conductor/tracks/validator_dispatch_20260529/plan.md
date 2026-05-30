# Validator Dispatch Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the hardcoded `go build` validator in `cercano_local`'s agentic loop with an `AutoValidator` that detects project type from manifests, dispatches to a per-language sub-validator, honors a `.cercano/config.yaml` override, and skips validation (with a warning) when nothing is detected.

**Architecture:** A new `tools.AutoValidator` owns detection + dispatch and replaces `tools.NewGoValidator()` at startup. Each language gets a small sub-validator behind a `Validator` interface that now returns `(Decision, error)` where `Decision ∈ {Passed, Failed, Skipped}`. The coordinator adapter is taught about `Skipped` so it exits the loop once and surfaces the warning instead of retrying.

**Tech Stack:** Go 1.25, `os/exec`, `gopkg.in/yaml.v3` (already a dep), `t.TempDir()` for fixtures.

**Spec:** [conductor/tracks/validator_dispatch_20260529/spec.md](./spec.md)

---

## File Map

| Action | Path | Purpose |
|---|---|---|
| Modify | `source/server/internal/tools/interfaces.go` | Replace `Validator` interface; add `Decision`, `SkipReason` |
| Modify | `source/server/internal/tools/go_validator.go` | Adapt to new `(Decision, error)` return |
| Modify | `source/server/internal/tools/go_validator_test.go` | Update assertions |
| Create | `source/server/internal/tools/noop_validator.go` | Always returns `Skipped` |
| Create | `source/server/internal/tools/noop_validator_test.go` | |
| Create | `source/server/internal/tools/custom_validator.go` | Runs user-supplied `sh -c` |
| Create | `source/server/internal/tools/custom_validator_test.go` | |
| Create | `source/server/internal/tools/dotnet_validator.go` | `dotnet build` |
| Create | `source/server/internal/tools/dotnet_validator_test.go` | Gated on `dotnet` in PATH |
| Create | `source/server/internal/tools/rust_validator.go` | `cargo build` |
| Create | `source/server/internal/tools/rust_validator_test.go` | Gated on `cargo` in PATH |
| Create | `source/server/internal/tools/node_validator.go` | `npm run build` |
| Create | `source/server/internal/tools/node_validator_test.go` | Gated on `npm` in PATH |
| Create | `source/server/internal/tools/detect.go` | Manifest scan (pure) |
| Create | `source/server/internal/tools/detect_test.go` | Tempdir fixtures |
| Create | `source/server/internal/projectconfig/config.go` | `.cercano/config.yaml` loader |
| Create | `source/server/internal/projectconfig/config_test.go` | |
| Create | `source/server/internal/tools/auto_validator.go` | Detect → dispatch orchestrator |
| Create | `source/server/internal/tools/auto_validator_test.go` | With mocks |
| Modify | `source/server/internal/loop/adapters/adapters.go` (~L165) | Handle `Skipped` decision |
| Modify | `source/server/internal/loop/adk_coordinator_test.go` | Add Skipped-case |
| Modify | `source/server/cmd/cercano/main.go:89` | Wire `NewAutoValidator(...)` |
| Modify | `source/server/cmd/agent/main.go:88` | Wire `NewAutoValidator(...)` |

---

### Task 1: Extend the `Validator` interface

**Files:**
- Modify: `source/server/internal/tools/interfaces.go`

- [ ] **Step 1: Write the failing test**

Create `source/server/internal/tools/decision_test.go`:

```go
package tools

import (
	"errors"
	"testing"
)

func TestDecisionString(t *testing.T) {
	cases := map[Decision]string{
		Passed:  "passed",
		Failed:  "failed",
		Skipped: "skipped",
	}
	for d, want := range cases {
		if got := d.String(); got != want {
			t.Errorf("Decision(%d).String() = %q, want %q", d, got, want)
		}
	}
}

func TestSkipReasonImplementsError(t *testing.T) {
	var err error = &SkipReason{Reason: "no manifest"}
	if err.Error() != "no manifest" {
		t.Errorf("SkipReason.Error() = %q, want %q", err.Error(), "no manifest")
	}
	var sr *SkipReason
	if !errors.As(err, &sr) {
		t.Fatalf("errors.As did not unwrap SkipReason")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd source/server && go test ./internal/tools/ -run TestDecision -count=1`
Expected: FAIL — `undefined: Decision`, `undefined: SkipReason`.

- [ ] **Step 3: Replace `Validator` and add new types**

Replace the contents of `source/server/internal/tools/interfaces.go` with:

```go
package tools

import "context"

// CodeGenerator defines the interface for generating and fixing code.
type CodeGenerator interface {
	Generate(ctx context.Context, instruction string, code string) (string, error)
	Fix(ctx context.Context, code string, errorMsg string) (string, error)
}

// Decision is the outcome of a Validate call.
type Decision int

const (
	// Passed: validation succeeded.
	Passed Decision = iota
	// Failed: validation ran and returned a non-zero status; the returned error
	// contains the output to be fed back to the LLM.
	Failed
	// Skipped: no validation was performed; the returned error is a *SkipReason
	// the coordinator should surface to the user. Skipped MUST NOT trigger retries.
	Skipped
)

func (d Decision) String() string {
	switch d {
	case Passed:
		return "passed"
	case Failed:
		return "failed"
	case Skipped:
		return "skipped"
	default:
		return "unknown"
	}
}

// SkipReason is the sentinel error returned alongside a Skipped decision. It
// lets the coordinator type-assert and pull the message into the streamed output.
type SkipReason struct {
	Reason string
}

func (s *SkipReason) Error() string { return s.Reason }

// Validator runs validation logic in the specified directory.
type Validator interface {
	Validate(ctx context.Context, workDir string) (Decision, error)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd source/server && go test ./internal/tools/ -run TestDecision -count=1`
Expected: PASS.

The package will not yet build because `GoValidator.Validate` still has the old signature — Task 2 fixes that.

- [ ] **Step 5: Commit**

```bash
git add source/server/internal/tools/interfaces.go source/server/internal/tools/decision_test.go
git commit -m "refactor(tools): extend Validator interface with Decision return"
```

---

### Task 2: Adapt `GoValidator` to the new interface

**Files:**
- Modify: `source/server/internal/tools/go_validator.go`
- Modify: `source/server/internal/tools/go_validator_test.go`

- [ ] **Step 1: Update the test to expect the new signature**

Read the current test file first to preserve cases — then update each assertion to consume `(Decision, error)` instead of `error`. For each case that previously asserted "no error returned", change to:

```go
decision, err := v.Validate(ctx, dir)
if err != nil {
	t.Fatalf("unexpected err: %v", err)
}
if decision != Passed {
	t.Fatalf("got decision %s, want passed", decision)
}
```

For each case that previously asserted "got error", change to:

```go
decision, err := v.Validate(ctx, dir)
if err == nil {
	t.Fatalf("expected error, got nil")
}
if decision != Failed {
	t.Fatalf("got decision %s, want failed", decision)
}
```

- [ ] **Step 2: Run tests to confirm they fail to compile**

Run: `cd source/server && go test ./internal/tools/ -count=1`
Expected: build error — `cannot use ... as (Decision, error)`.

- [ ] **Step 3: Update `GoValidator.Validate`**

Replace `Validate` in `source/server/internal/tools/go_validator.go`:

```go
// Validate runs 'go test' if tests exist, or 'go build' otherwise.
func (v *GoValidator) Validate(ctx context.Context, dir string) (Decision, error) {
	cmd := exec.CommandContext(ctx, "go", "test", "-c", "-o", "/dev/null")
	cmd.Dir = dir
	output, err := cmd.CombinedOutput()

	if err != nil {
		outStr := string(output)
		if strings.Contains(outStr, "no test files") {
			buildCmd := exec.CommandContext(ctx, "go", "build", "-o", "/dev/null", "./...")
			buildCmd.Dir = dir
			buildOutput, buildErr := buildCmd.CombinedOutput()
			if buildErr != nil {
				return Failed, fmt.Errorf("build failed:\n%s", cleanOutput(string(buildOutput)))
			}
			return Passed, nil
		}
		return Failed, fmt.Errorf("compilation failed:\n%s", cleanOutput(outStr))
	}

	cmdRun := exec.CommandContext(ctx, "go", "test", "-v")
	cmdRun.Dir = dir
	outputRun, err := cmdRun.CombinedOutput()
	if err != nil {
		return Failed, fmt.Errorf("tests failed:\n%s", cleanOutput(string(outputRun)))
	}

	return Passed, nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd source/server && go test ./internal/tools/ -count=1`
Expected: PASS.

The wider build will still fail because `adapters.go` calls the old signature — that gets fixed in Task 11. To keep the rest of the build moving in the meantime, you can temporarily patch `adapters.go:165` to discard the new Decision (`_, err := validator.Validate(...)`); revert in Task 11.

- [ ] **Step 5: Commit**

```bash
git add source/server/internal/tools/go_validator.go source/server/internal/tools/go_validator_test.go
git commit -m "refactor(tools): adapt GoValidator to Decision return"
```

---

### Task 3: `NoOpValidator`

**Files:**
- Create: `source/server/internal/tools/noop_validator.go`
- Create: `source/server/internal/tools/noop_validator_test.go`

- [ ] **Step 1: Write the failing test**

Create `source/server/internal/tools/noop_validator_test.go`:

```go
package tools

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestNoOpValidator_ReturnsSkipped(t *testing.T) {
	v := NewNoOpValidator("custom reason text")
	decision, err := v.Validate(context.Background(), "/anywhere")
	if decision != Skipped {
		t.Fatalf("got decision %s, want skipped", decision)
	}
	var sr *SkipReason
	if !errors.As(err, &sr) {
		t.Fatalf("expected *SkipReason, got %T (%v)", err, err)
	}
	if !strings.Contains(sr.Reason, "custom reason text") {
		t.Errorf("reason = %q, want it to contain %q", sr.Reason, "custom reason text")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd source/server && go test ./internal/tools/ -run TestNoOpValidator -count=1`
Expected: FAIL — `undefined: NewNoOpValidator`.

- [ ] **Step 3: Write implementation**

Create `source/server/internal/tools/noop_validator.go`:

```go
package tools

import "context"

// NoOpValidator skips validation and returns a Skipped decision with a reason.
type NoOpValidator struct {
	reason string
}

// NewNoOpValidator returns a validator that always Skipped with the given reason.
func NewNoOpValidator(reason string) *NoOpValidator {
	return &NoOpValidator{reason: reason}
}

func (v *NoOpValidator) Validate(_ context.Context, _ string) (Decision, error) {
	return Skipped, &SkipReason{Reason: v.reason}
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd source/server && go test ./internal/tools/ -run TestNoOpValidator -count=1`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add source/server/internal/tools/noop_validator.go source/server/internal/tools/noop_validator_test.go
git commit -m "feat(tools): add NoOpValidator returning Skipped with reason"
```

---

### Task 4: `CustomValidator`

**Files:**
- Create: `source/server/internal/tools/custom_validator.go`
- Create: `source/server/internal/tools/custom_validator_test.go`

- [ ] **Step 1: Write the failing test**

```go
package tools

import (
	"context"
	"strings"
	"testing"
)

func TestCustomValidator_PassesOnZeroExit(t *testing.T) {
	v := NewCustomValidator("true")
	decision, err := v.Validate(context.Background(), t.TempDir())
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if decision != Passed {
		t.Fatalf("got decision %s, want passed", decision)
	}
}

func TestCustomValidator_FailsOnNonZeroExit(t *testing.T) {
	v := NewCustomValidator("echo boom >&2; exit 1")
	decision, err := v.Validate(context.Background(), t.TempDir())
	if err == nil {
		t.Fatal("expected error")
	}
	if decision != Failed {
		t.Fatalf("got decision %s, want failed", decision)
	}
	if !strings.Contains(err.Error(), "boom") {
		t.Errorf("err = %q, want it to contain stderr output 'boom'", err.Error())
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd source/server && go test ./internal/tools/ -run TestCustomValidator -count=1`
Expected: FAIL — `undefined: NewCustomValidator`.

- [ ] **Step 3: Write implementation**

Create `source/server/internal/tools/custom_validator.go`:

```go
package tools

import (
	"context"
	"fmt"
	"os/exec"
)

// CustomValidator runs a user-supplied shell command via 'sh -c' in workDir.
type CustomValidator struct {
	command string
}

// NewCustomValidator returns a validator that runs `sh -c <command>` in workDir.
func NewCustomValidator(command string) *CustomValidator {
	return &CustomValidator{command: command}
}

func (v *CustomValidator) Validate(ctx context.Context, workDir string) (Decision, error) {
	cmd := exec.CommandContext(ctx, "sh", "-c", v.command)
	cmd.Dir = workDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return Failed, fmt.Errorf("custom validator failed: %s\n%s", err, cleanOutput(string(out)))
	}
	return Passed, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd source/server && go test ./internal/tools/ -run TestCustomValidator -count=1`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add source/server/internal/tools/custom_validator.go source/server/internal/tools/custom_validator_test.go
git commit -m "feat(tools): add CustomValidator for user-supplied commands"
```

---

### Task 5: Manifest detection

**Files:**
- Create: `source/server/internal/tools/detect.go`
- Create: `source/server/internal/tools/detect_test.go`

- [ ] **Step 1: Write the failing test**

Create `source/server/internal/tools/detect_test.go`:

```go
package tools

import (
	"os"
	"path/filepath"
	"testing"
)

func writeFiles(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for name, body := range files {
		full := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestDetect(t *testing.T) {
	cases := []struct {
		name  string
		files map[string]string
		want  ProjectKind
	}{
		{"go", map[string]string{"go.mod": "module x\ngo 1.21\n"}, KindGo},
		{"fsproj", map[string]string{"App.fsproj": "<Project/>"}, KindDotnetProject},
		{"csproj", map[string]string{"App.csproj": "<Project/>"}, KindDotnetProject},
		{"sln plus fsproj", map[string]string{"App.sln": "", "src/App.fsproj": "<Project/>"}, KindDotnetSolution},
		{"cargo", map[string]string{"Cargo.toml": "[package]\nname='x'\n"}, KindRust},
		{"node with build", map[string]string{"package.json": `{"scripts":{"build":"webpack"}}`}, KindNode},
		{"node without build", map[string]string{"package.json": `{"scripts":{"test":"jest"}}`}, KindUnknown},
		{"empty", map[string]string{}, KindUnknown},
		{"rust beats go", map[string]string{"Cargo.toml": "", "go.mod": "module x"}, KindRust},
		{"go beats fsproj", map[string]string{"go.mod": "module x", "App.fsproj": "<Project/>"}, KindGo},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			writeFiles(t, dir, tc.files)
			got, err := Detect(dir)
			if err != nil {
				t.Fatalf("unexpected err: %v", err)
			}
			if got != tc.want {
				t.Errorf("Detect(%v) = %s, want %s", tc.files, got, tc.want)
			}
		})
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd source/server && go test ./internal/tools/ -run TestDetect -count=1`
Expected: FAIL — `undefined: ProjectKind`, `undefined: Detect`.

- [ ] **Step 3: Write implementation**

Create `source/server/internal/tools/detect.go`:

```go
package tools

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// ProjectKind identifies the build system detected in a directory.
type ProjectKind int

const (
	KindUnknown ProjectKind = iota
	KindGo
	KindRust
	KindDotnetSolution
	KindDotnetProject
	KindNode
)

func (k ProjectKind) String() string {
	switch k {
	case KindGo:
		return "go"
	case KindRust:
		return "rust"
	case KindDotnetSolution:
		return "dotnet-solution"
	case KindDotnetProject:
		return "dotnet-project"
	case KindNode:
		return "node"
	default:
		return "unknown"
	}
}

// Detect scans workDir (non-recursive) for a recognized project manifest.
// Precedence (first match wins): Cargo.toml > go.mod > *.sln > *.fsproj/*.csproj
// > package.json (only if scripts.build is non-empty).
func Detect(workDir string) (ProjectKind, error) {
	entries, err := os.ReadDir(workDir)
	if err != nil {
		return KindUnknown, err
	}

	hasCargo, hasGoMod, hasSln, hasDotnetProj, hasPackageJSON := false, false, false, false, false
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		switch {
		case name == "Cargo.toml":
			hasCargo = true
		case name == "go.mod":
			hasGoMod = true
		case filepath.Ext(name) == ".sln":
			hasSln = true
		case filepath.Ext(name) == ".fsproj", filepath.Ext(name) == ".csproj":
			hasDotnetProj = true
		case name == "package.json":
			hasPackageJSON = true
		}
	}

	switch {
	case hasCargo:
		return KindRust, nil
	case hasGoMod:
		return KindGo, nil
	case hasSln:
		return KindDotnetSolution, nil
	case hasDotnetProj:
		return KindDotnetProject, nil
	case hasPackageJSON && nodeHasBuildScript(filepath.Join(workDir, "package.json")):
		return KindNode, nil
	default:
		return KindUnknown, nil
	}
}

func nodeHasBuildScript(path string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	var pkg struct {
		Scripts map[string]string `json:"scripts"`
	}
	if err := json.Unmarshal(data, &pkg); err != nil {
		return false
	}
	build, ok := pkg.Scripts["build"]
	return ok && build != ""
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd source/server && go test ./internal/tools/ -run TestDetect -count=1 -v`
Expected: PASS (all subtests).

- [ ] **Step 5: Commit**

```bash
git add source/server/internal/tools/detect.go source/server/internal/tools/detect_test.go
git commit -m "feat(tools): add manifest-based project Detect()"
```

---

### Task 6: `projectconfig` loader

**Files:**
- Create: `source/server/internal/projectconfig/config.go`
- Create: `source/server/internal/projectconfig/config_test.go`

- [ ] **Step 1: Write the failing test**

Create `source/server/internal/projectconfig/config_test.go`:

```go
package projectconfig

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeCfg(t *testing.T, dir, body string) {
	t.Helper()
	cercanoDir := filepath.Join(dir, ".cercano")
	if err := os.MkdirAll(cercanoDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cercanoDir, "config.yaml"), []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestLoad_MissingFileReturnsEmpty(t *testing.T) {
	cfg, err := Load(t.TempDir())
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if cfg.Validator.Skip || cfg.Validator.Command != "" {
		t.Errorf("expected zero-value config, got %+v", cfg)
	}
}

func TestLoad_ParsesValidatorBlock(t *testing.T) {
	dir := t.TempDir()
	writeCfg(t, dir, "validator:\n  command: dotnet build src/App.fsproj\n  skip: false\n")
	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if cfg.Validator.Command != "dotnet build src/App.fsproj" {
		t.Errorf("got command %q", cfg.Validator.Command)
	}
	if cfg.Validator.Skip {
		t.Errorf("expected skip=false")
	}
}

func TestLoad_SkipTrue(t *testing.T) {
	dir := t.TempDir()
	writeCfg(t, dir, "validator:\n  skip: true\n")
	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if !cfg.Validator.Skip {
		t.Errorf("expected skip=true")
	}
}

func TestLoad_MalformedYAMLReturnsError(t *testing.T) {
	dir := t.TempDir()
	writeCfg(t, dir, "validator: [::not yaml")
	_, err := Load(dir)
	if err == nil {
		t.Fatal("expected error on malformed yaml")
	}
	if !strings.Contains(err.Error(), "invalid .cercano/config.yaml") {
		t.Errorf("err = %q, want it to contain 'invalid .cercano/config.yaml'", err.Error())
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd source/server && go test ./internal/projectconfig/ -count=1`
Expected: FAIL — package does not exist.

- [ ] **Step 3: Write implementation**

Create `source/server/internal/projectconfig/config.go`:

```go
// Package projectconfig loads .cercano/config.yaml for a project directory.
package projectconfig

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// Config is the on-disk schema for .cercano/config.yaml.
type Config struct {
	Validator ValidatorConfig `yaml:"validator"`
}

type ValidatorConfig struct {
	Command string `yaml:"command"`
	Skip    bool   `yaml:"skip"`
}

// Load reads .cercano/config.yaml under workDir. A missing file returns the
// zero-value Config and no error. A malformed file returns an error wrapping
// the parse failure with the prefix "invalid .cercano/config.yaml".
func Load(workDir string) (Config, error) {
	path := filepath.Join(workDir, ".cercano", "config.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Config{}, nil
		}
		return Config{}, err
	}
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return Config{}, fmt.Errorf("invalid .cercano/config.yaml: %w", err)
	}
	return cfg, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd source/server && go test ./internal/projectconfig/ -count=1 -v`
Expected: PASS (all four subtests).

- [ ] **Step 5: Commit**

```bash
git add source/server/internal/projectconfig/
git commit -m "feat(projectconfig): add .cercano/config.yaml loader"
```

---

### Task 7: `DotnetValidator` (with gated integration test)

**Files:**
- Create: `source/server/internal/tools/dotnet_validator.go`
- Create: `source/server/internal/tools/dotnet_validator_test.go`

- [ ] **Step 1: Write the failing test**

Create `source/server/internal/tools/dotnet_validator_test.go`:

```go
package tools

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

const minimalFsproj = `<Project Sdk="Microsoft.NET.Sdk">
  <PropertyGroup>
    <OutputType>Library</OutputType>
    <TargetFramework>net8.0</TargetFramework>
  </PropertyGroup>
  <ItemGroup><Compile Include="Lib.fs" /></ItemGroup>
</Project>
`

const validFs = "module Lib\nlet add a b = a + b\n"
const brokenFs = "module Lib\nlet add a b = a + b +\n"

func skipIfNoDotnet(t *testing.T) {
	if _, err := exec.LookPath("dotnet"); err != nil {
		t.Skip("dotnet not in PATH; skipping integration test")
	}
}

func TestDotnetValidator_PassesOnValidProject(t *testing.T) {
	skipIfNoDotnet(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "Lib.fsproj"), []byte(minimalFsproj), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "Lib.fs"), []byte(validFs), 0644); err != nil {
		t.Fatal(err)
	}
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
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "Lib.fsproj"), []byte(minimalFsproj), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "Lib.fs"), []byte(brokenFs), 0644); err != nil {
		t.Fatal(err)
	}
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
	if want := "validator.command"; !contains(err.Error(), want) {
		t.Errorf("err = %q, want it to contain %q", err.Error(), want)
	}
}

func contains(s, sub string) bool { return len(s) >= len(sub) && (s == sub || (len(s) > 0 && (indexOf(s, sub) >= 0))) }
func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd source/server && go test ./internal/tools/ -run TestDotnetValidator -count=1`
Expected: FAIL — `undefined: NewDotnetValidator`.

- [ ] **Step 3: Write implementation**

Create `source/server/internal/tools/dotnet_validator.go`:

```go
package tools

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
)

// DotnetValidator runs `dotnet build` in workDir.
type DotnetValidator struct{}

func NewDotnetValidator() *DotnetValidator { return &DotnetValidator{} }

func (v *DotnetValidator) Validate(ctx context.Context, workDir string) (Decision, error) {
	if _, err := exec.LookPath("dotnet"); err != nil {
		return Failed, errors.New("dotnet validator: command 'dotnet' not found in PATH — install .NET SDK or set validator.command in .cercano/config.yaml to override")
	}
	cmd := exec.CommandContext(ctx, "dotnet", "build", "--nologo", "-clp:NoSummary")
	cmd.Dir = workDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return Failed, fmt.Errorf("dotnet build failed:\n%s", cleanOutput(string(out)))
	}
	return Passed, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd source/server && go test ./internal/tools/ -run TestDotnetValidator -count=1 -v`
Expected: PASS. The two integration tests will say SKIP if dotnet isn't installed; the missing-binary test always runs.

- [ ] **Step 5: Commit**

```bash
git add source/server/internal/tools/dotnet_validator.go source/server/internal/tools/dotnet_validator_test.go
git commit -m "feat(tools): add DotnetValidator with PATH-gated tests"
```

---

### Task 8: `RustValidator` (with gated integration test)

**Files:**
- Create: `source/server/internal/tools/rust_validator.go`
- Create: `source/server/internal/tools/rust_validator_test.go`

- [ ] **Step 1: Write the failing test**

Create `source/server/internal/tools/rust_validator_test.go`:

```go
package tools

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

const minimalCargo = `[package]
name = "x"
version = "0.1.0"
edition = "2021"
[lib]
path = "src/lib.rs"
`

const validRs = "pub fn add(a: i32, b: i32) -> i32 { a + b }\n"
const brokenRs = "pub fn add(a: i32, b: i32) -> i32 { a + b\n"

func skipIfNoCargo(t *testing.T) {
	if _, err := exec.LookPath("cargo"); err != nil {
		t.Skip("cargo not in PATH; skipping integration test")
	}
}

func TestRustValidator_PassesOnValidProject(t *testing.T) {
	skipIfNoCargo(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "Cargo.toml"), []byte(minimalCargo), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "src"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "src/lib.rs"), []byte(validRs), 0644); err != nil {
		t.Fatal(err)
	}
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
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "Cargo.toml"), []byte(minimalCargo), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "src"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "src/lib.rs"), []byte(brokenRs), 0644); err != nil {
		t.Fatal(err)
	}
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
	if !contains(err.Error(), "validator.command") {
		t.Errorf("err = %q, want it to contain 'validator.command'", err.Error())
	}
}
```

(`contains` and `indexOf` helpers from Task 7 are package-local; reuse them.)

- [ ] **Step 2: Run test to verify it fails**

Run: `cd source/server && go test ./internal/tools/ -run TestRustValidator -count=1`
Expected: FAIL — `undefined: NewRustValidator`.

- [ ] **Step 3: Write implementation**

Create `source/server/internal/tools/rust_validator.go`:

```go
package tools

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
)

// RustValidator runs `cargo build` in workDir.
type RustValidator struct{}

func NewRustValidator() *RustValidator { return &RustValidator{} }

func (v *RustValidator) Validate(ctx context.Context, workDir string) (Decision, error) {
	if _, err := exec.LookPath("cargo"); err != nil {
		return Failed, errors.New("rust validator: command 'cargo' not found in PATH — install the Rust toolchain or set validator.command in .cercano/config.yaml to override")
	}
	cmd := exec.CommandContext(ctx, "cargo", "build", "--quiet")
	cmd.Dir = workDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return Failed, fmt.Errorf("cargo build failed:\n%s", cleanOutput(string(out)))
	}
	return Passed, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd source/server && go test ./internal/tools/ -run TestRustValidator -count=1 -v`
Expected: PASS (integration tests SKIP if cargo absent).

- [ ] **Step 5: Commit**

```bash
git add source/server/internal/tools/rust_validator.go source/server/internal/tools/rust_validator_test.go
git commit -m "feat(tools): add RustValidator with PATH-gated tests"
```

---

### Task 9: `NodeValidator`

**Files:**
- Create: `source/server/internal/tools/node_validator.go`
- Create: `source/server/internal/tools/node_validator_test.go`

- [ ] **Step 1: Write the failing test**

```go
package tools

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func skipIfNoNpm(t *testing.T) {
	if _, err := exec.LookPath("npm"); err != nil {
		t.Skip("npm not in PATH; skipping integration test")
	}
}

func TestNodeValidator_PassesOnTrivialBuild(t *testing.T) {
	skipIfNoNpm(t)
	dir := t.TempDir()
	pkg := `{"name":"x","version":"0.0.1","scripts":{"build":"exit 0"}}`
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(pkg), 0644); err != nil {
		t.Fatal(err)
	}
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
	dir := t.TempDir()
	pkg := `{"name":"x","version":"0.0.1","scripts":{"build":"exit 1"}}`
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(pkg), 0644); err != nil {
		t.Fatal(err)
	}
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
	if !contains(err.Error(), "validator.command") {
		t.Errorf("err = %q, want it to contain 'validator.command'", err.Error())
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd source/server && go test ./internal/tools/ -run TestNodeValidator -count=1`
Expected: FAIL — `undefined: NewNodeValidator`.

- [ ] **Step 3: Write implementation**

Create `source/server/internal/tools/node_validator.go`:

```go
package tools

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
)

// NodeValidator runs `npm run build` in workDir.
type NodeValidator struct{}

func NewNodeValidator() *NodeValidator { return &NodeValidator{} }

func (v *NodeValidator) Validate(ctx context.Context, workDir string) (Decision, error) {
	if _, err := exec.LookPath("npm"); err != nil {
		return Failed, errors.New("node validator: command 'npm' not found in PATH — install Node.js or set validator.command in .cercano/config.yaml to override")
	}
	cmd := exec.CommandContext(ctx, "npm", "run", "build", "--silent")
	cmd.Dir = workDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return Failed, fmt.Errorf("npm run build failed:\n%s", cleanOutput(string(out)))
	}
	return Passed, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd source/server && go test ./internal/tools/ -run TestNodeValidator -count=1 -v`
Expected: PASS (integration tests SKIP if npm absent).

- [ ] **Step 5: Commit**

```bash
git add source/server/internal/tools/node_validator.go source/server/internal/tools/node_validator_test.go
git commit -m "feat(tools): add NodeValidator with PATH-gated tests"
```

---

### Task 10: `AutoValidator` orchestrator

**Files:**
- Create: `source/server/internal/tools/auto_validator.go`
- Create: `source/server/internal/tools/auto_validator_test.go`

- [ ] **Step 1: Write the failing test**

Create `source/server/internal/tools/auto_validator_test.go`:

```go
package tools

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cercano/source/server/internal/projectconfig"
)

type fakeLoader struct {
	cfg projectconfig.Config
	err error
}

func (f fakeLoader) Load(_ string) (projectconfig.Config, error) { return f.cfg, f.err }

type recordingValidator struct {
	called  bool
	workDir string
	ret     Decision
	err     error
}

func (r *recordingValidator) Validate(_ context.Context, dir string) (Decision, error) {
	r.called = true
	r.workDir = dir
	return r.ret, r.err
}

func writeManifest(t *testing.T, dir, name, body string) string {
	t.Helper()
	full := filepath.Join(dir, name)
	if err := os.WriteFile(full, []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestAutoValidator_SkipTrueShortCircuits(t *testing.T) {
	rec := &recordingValidator{ret: Passed}
	av := NewAutoValidator(fakeLoader{cfg: projectconfig.Config{
		Validator: projectconfig.ValidatorConfig{Skip: true},
	}}, KindToValidator{KindGo: rec})
	decision, err := av.Validate(context.Background(), t.TempDir())
	if decision != Skipped {
		t.Fatalf("got %s, want Skipped", decision)
	}
	var sr *SkipReason
	if !errors.As(err, &sr) {
		t.Fatalf("expected *SkipReason, got %v", err)
	}
	if rec.called {
		t.Error("sub-validator should not be called when skip=true")
	}
}

func TestAutoValidator_CommandOverrideDispatchesCustom(t *testing.T) {
	dir := t.TempDir()
	writeManifest(t, dir, "go.mod", "module x")
	av := NewAutoValidator(fakeLoader{cfg: projectconfig.Config{
		Validator: projectconfig.ValidatorConfig{Command: "true"},
	}}, KindToValidator{})
	decision, err := av.Validate(context.Background(), dir)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if decision != Passed {
		t.Fatalf("got %s, want Passed", decision)
	}
}

func TestAutoValidator_DetectsAndDispatches(t *testing.T) {
	dir := t.TempDir()
	writeManifest(t, dir, "go.mod", "module x")
	rec := &recordingValidator{ret: Passed}
	av := NewAutoValidator(fakeLoader{}, KindToValidator{KindGo: rec})
	decision, err := av.Validate(context.Background(), dir)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if decision != Passed {
		t.Fatalf("got %s, want Passed", decision)
	}
	if !rec.called || rec.workDir != dir {
		t.Errorf("expected sub-validator called with dir=%s, got called=%v dir=%s", dir, rec.called, rec.workDir)
	}
}

func TestAutoValidator_NoManifestReturnsSkipped(t *testing.T) {
	dir := t.TempDir()
	av := NewAutoValidator(fakeLoader{}, KindToValidator{})
	decision, err := av.Validate(context.Background(), dir)
	if decision != Skipped {
		t.Fatalf("got %s, want Skipped", decision)
	}
	var sr *SkipReason
	if !errors.As(err, &sr) {
		t.Fatalf("expected *SkipReason, got %v", err)
	}
	if !strings.Contains(sr.Reason, "no recognized project manifest") {
		t.Errorf("reason = %q, want it to mention 'no recognized project manifest'", sr.Reason)
	}
}

func TestAutoValidator_InvalidConfigReturnsFailed(t *testing.T) {
	av := NewAutoValidator(fakeLoader{err: errors.New("invalid .cercano/config.yaml: bad")}, KindToValidator{})
	decision, err := av.Validate(context.Background(), t.TempDir())
	if decision != Failed {
		t.Fatalf("got %s, want Failed", decision)
	}
	if err == nil || !strings.Contains(err.Error(), "invalid .cercano/config.yaml") {
		t.Errorf("err = %v, want it to contain 'invalid .cercano/config.yaml'", err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd source/server && go test ./internal/tools/ -run TestAutoValidator -count=1`
Expected: FAIL — `undefined: NewAutoValidator`, `undefined: KindToValidator`.

- [ ] **Step 3: Write implementation**

Create `source/server/internal/tools/auto_validator.go`:

```go
package tools

import (
	"context"

	"cercano/source/server/internal/projectconfig"
)

// ConfigLoader loads project configuration for a given workDir.
type ConfigLoader interface {
	Load(workDir string) (projectconfig.Config, error)
}

// KindToValidator maps a detected ProjectKind to the sub-validator that runs for it.
type KindToValidator map[ProjectKind]Validator

// AutoValidator detects the project type in workDir and dispatches to the
// appropriate sub-validator, honoring overrides from .cercano/config.yaml.
type AutoValidator struct {
	loader ConfigLoader
	subs   KindToValidator
}

// NewAutoValidator wires up an AutoValidator with the given config loader and
// sub-validator map. Use DefaultKindToValidator() to get the built-in mapping.
func NewAutoValidator(loader ConfigLoader, subs KindToValidator) *AutoValidator {
	return &AutoValidator{loader: loader, subs: subs}
}

// DefaultKindToValidator returns the built-in mapping used by the production binaries.
func DefaultKindToValidator() KindToValidator {
	return KindToValidator{
		KindGo:             NewGoValidator(),
		KindRust:           NewRustValidator(),
		KindDotnetSolution: NewDotnetValidator(),
		KindDotnetProject:  NewDotnetValidator(),
		KindNode:           NewNodeValidator(),
	}
}

func (v *AutoValidator) Validate(ctx context.Context, workDir string) (Decision, error) {
	cfg, err := v.loader.Load(workDir)
	if err != nil {
		return Failed, err
	}
	if cfg.Validator.Skip {
		return Skipped, &SkipReason{Reason: "validation skipped per .cercano/config.yaml"}
	}
	if cfg.Validator.Command != "" {
		return NewCustomValidator(cfg.Validator.Command).Validate(ctx, workDir)
	}

	kind, derr := Detect(workDir)
	if derr != nil {
		return Skipped, &SkipReason{Reason: "could not read workDir for manifest detection: " + derr.Error()}
	}
	if kind == KindUnknown {
		return Skipped, &SkipReason{Reason: "no recognized project manifest in " + workDir + "; validation skipped — set validator.command in .cercano/config.yaml to enable"}
	}
	sub, ok := v.subs[kind]
	if !ok {
		return Skipped, &SkipReason{Reason: "no validator registered for project kind " + kind.String()}
	}
	return sub.Validate(ctx, workDir)
}

// loaderFunc adapts projectconfig.Load to the ConfigLoader interface.
type loaderFunc func(string) (projectconfig.Config, error)

func (f loaderFunc) Load(workDir string) (projectconfig.Config, error) { return f(workDir) }

// DefaultLoader returns the production ConfigLoader.
func DefaultLoader() ConfigLoader { return loaderFunc(projectconfig.Load) }
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd source/server && go test ./internal/tools/ -run TestAutoValidator -count=1 -v`
Expected: PASS (all five subtests).

- [ ] **Step 5: Commit**

```bash
git add source/server/internal/tools/auto_validator.go source/server/internal/tools/auto_validator_test.go
git commit -m "feat(tools): add AutoValidator (detect + dispatch + config override)"
```

---

### Task 11: Update coordinator adapter to handle `Skipped`

**Files:**
- Modify: `source/server/internal/loop/adapters/adapters.go` (around line 165)
- Modify: `source/server/internal/loop/adk_coordinator_test.go`

- [ ] **Step 1: Write the failing coordinator test**

Open `source/server/internal/loop/adk_coordinator_test.go` and find the existing test patterns (a fake validator already exists). Add this test next to the others (adapt the helper names if the file uses different ones):

```go
func TestCoordinator_SkippedValidationExitsLoopWithWarning(t *testing.T) {
	// Validator returns Skipped on the first call. Loop must exit after one
	// generation, not retry, and the warning must appear in streamed output.
	skipReason := "no recognized project manifest in /tmp/x; validation skipped"
	validator := &fakeValidator{
		responses: []validatorResponse{
			{decision: tools.Skipped, err: &tools.SkipReason{Reason: skipReason}},
		},
	}
	c := newTestCoordinator(t, validator, /* generator returns "ok" */)

	var out strings.Builder
	for ev := range c.CoordinateStream(ctx, /* request with file_path + work_dir */) {
		out.WriteString(ev.Text())
	}

	if validator.callCount != 1 {
		t.Errorf("validator called %d times, want 1 (Skipped must not retry)", validator.callCount)
	}
	if !strings.Contains(out.String(), skipReason) {
		t.Errorf("output = %q, want it to contain skip reason %q", out.String(), skipReason)
	}
}
```

NOTE: the test names (`fakeValidator`, `validatorResponse`, `newTestCoordinator`) reflect what's likely there — read the actual file and use the real fakes. The shape (validator returns Skipped, loop exits in 1 call, output contains reason) is the contract.

- [ ] **Step 2: Run test to verify it fails**

Run: `cd source/server && go test ./internal/loop/ -run TestCoordinator_Skipped -count=1`
Expected: FAIL — either compile error (if `fakeValidator` doesn't yet return `Decision`) or runtime mismatch.

- [ ] **Step 3: Update the fake validator (in the test file) to the new signature**

Whatever the existing fake is, change `Validate(ctx, dir) error` to `Validate(ctx, dir) (Decision, error)`. Update all existing test setups that produced an error-only fake to also specify a Decision (`Passed` when err==nil, `Failed` otherwise).

- [ ] **Step 4: Update `validatorRun` in `adapters.go`**

In `source/server/internal/loop/adapters/adapters.go`, replace lines 165–199 (the block from `err := validator.Validate(ctx, workDir)` through the existing yield of the failure event) with:

```go
				decision, vErr := validator.Validate(ctx, workDir)

				ev := session.NewEvent(ctx.InvocationID())

				switch decision {
				case tools.Passed:
					ev.Actions.Escalate = true
					ev.LLMResponse.Content = genai.NewContentFromText("validation passed", genai.RoleModel)
					yield(ev, nil)
					return

				case tools.Skipped:
					// Exit the loop after this generation; no retry, no escalation.
					ev.Actions.Escalate = true
					reason := "validation skipped"
					var sr *tools.SkipReason
					if errors.As(vErr, &sr) {
						reason = sr.Reason
					}
					ev.LLMResponse.Content = genai.NewContentFromText(
						fmt.Sprintf("validation skipped: %s", reason),
						genai.RoleModel,
					)
					yield(ev, nil)
					return

				case tools.Failed:
					// Failure path: bump counter, optionally set use_cloud.
					failures := 0
					if raw, stateErr := state.Get(StateKeyValidationFailures); stateErr == nil {
						if v, ok := raw.(int); ok {
							failures = v
						}
					}
					failures++

					ev.Actions.StateDelta = map[string]any{
						StateKeyValidationFailures:  failures,
						StateKeyLastValidationError: vErr.Error(),
					}
					if failures >= escalationThreshold {
						ev.Actions.StateDelta[StateKeyUseCloud] = true
					}
					ev.LLMResponse.Content = genai.NewContentFromText(
						fmt.Sprintf("validation failed: %s", vErr.Error()),
						genai.RoleModel,
					)
					yield(ev, nil)
				}
```

Add `"errors"` to the import block at the top of the file if it isn't already there.

- [ ] **Step 5: Run all tests in loop package**

Run: `cd source/server && go test ./internal/loop/... -count=1`
Expected: PASS — including existing tests (which still cover Passed and Failed paths) and the new Skipped test.

- [ ] **Step 6: Commit**

```bash
git add source/server/internal/loop/
git commit -m "feat(loop): adapter handles Skipped decision, exits without retry"
```

---

### Task 12: Wire `AutoValidator` into both binaries

**Files:**
- Modify: `source/server/cmd/cercano/main.go:89`
- Modify: `source/server/cmd/agent/main.go:88`

- [ ] **Step 1: Update `cmd/cercano/main.go`**

Find line 89 (`validator := tools.NewGoValidator()`) and replace with:

```go
	validator := tools.NewAutoValidator(tools.DefaultLoader(), tools.DefaultKindToValidator())
```

- [ ] **Step 2: Update `cmd/agent/main.go`**

Find line 88 (same `tools.NewGoValidator()` call) and apply the same replacement.

- [ ] **Step 3: Verify the full build**

Run: `cd source/server && go build ./...`
Expected: PASS (no errors).

- [ ] **Step 4: Run the full test suite**

Run: `cd source/server && go test ./... -count=1`
Expected: PASS (gated integration tests SKIP where binaries are missing).

- [ ] **Step 5: Commit**

```bash
git add source/server/cmd/cercano/main.go source/server/cmd/agent/main.go
git commit -m "feat(cmd): wire AutoValidator into cercano and agent binaries"
```

---

### Task 13: Manual smoke verification

**Files:** none

- [ ] **Step 1: Build the binary**

```bash
cd source/server && make build
```

- [ ] **Step 2: Reproduce the original failure scenario on a non-Go project**

Create a tempdir with just `App.fsproj` and no go.mod. Invoke `cercano_local` (or call the underlying gRPC `ProcessRequest`) with `file_path=App.fs` and `work_dir=<tempdir>`. The current binary would have failed with `cannot find main module`. After this change:

- If `dotnet` is installed → the dotnet validator runs (will fail if the .fs file doesn't compile, but with a dotnet error, not a go error).
- If `dotnet` is not installed → returns `Failed` with the `'dotnet' not found in PATH` hint.

- [ ] **Step 3: Verify skip path on a directory with no manifest**

Empty tempdir, same `cercano_local` invocation. Expected: one generation, response contains `validation skipped: no recognized project manifest in ...`. No retry attempts.

- [ ] **Step 4: Verify override path**

In a tempdir, write `.cercano/config.yaml`:

```yaml
validator:
  command: "true"
```

`cercano_local` should pass validation regardless of project contents.

- [ ] **Step 5: Verify Go path still works**

Tempdir with `go.mod` + a trivial package. Expected: same behavior as before this change.

- [ ] **Step 6: Update the issue and commit any docs changes**

If `docs/` references the validator's behavior, update accordingly. Reference issue #6 in any final commit.

```bash
git commit --allow-empty -m "chore(validator): close #6 — project-aware validator dispatch"
```

---

## Self-Review (already performed by plan author)

- **Spec coverage:** every spec section maps to a task (interface change → T1, GoValidator adapt → T2, NoOp → T3, Custom → T4, detection → T5, projectconfig → T6, dotnet/rust/node → T7–9, AutoValidator → T10, adapter+coordinator → T11, wiring → T12, manual verify → T13).
- **Placeholders:** none. Each step has either real code or an exact command + expected output. Task 11's fake-validator helper names are flagged as "use the real ones" rather than guessed.
- **Type consistency:** `Validator`, `Decision`, `SkipReason`, `ProjectKind`, `Config`, `ValidatorConfig`, `ConfigLoader`, `KindToValidator`, `AutoValidator` — all referenced names appear in the task that defines them, before any task that uses them.
