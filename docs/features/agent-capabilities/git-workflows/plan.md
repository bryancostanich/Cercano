# Git Workflow Tools Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the agent's command-by-command git orchestration with a small set of deterministic, high-level git workflows (worktree, land, checkpoint, recover, bisect, history ops) exposed as capabilities on both surfaces.

**Architecture:** A new pure-git package `internal/gitflow` holds deterministic Go functions that shell out to `git` (no model in the control flow). Thin capability wrappers in `internal/capabilities/builtins/` expose them on both surfaces. The single model seam — independent risk review of a conflict resolution before landing — lives in the `git_land` *capability* (via `Services.Dispatch`), never in `gitflow`, so the engine stays pure and unit-testable against temp repos.

**Tech Stack:** Go 1.26; `os/exec` (`exec.CommandContext`, the pattern the existing git capabilities use); `gopkg.in/yaml.v3` (the repo's config format); `internal/capabilities` (0a); the `review` capability + `Services.Dispatch` (dispatch engine).

## Global Constraints

- **Deterministic engine, model only at seams.** `internal/gitflow` contains no model calls and no imports of `internal/capabilities`/`internal/dispatch`. Conflict *resolution* is done by the agent (outside the engine, on the feature branch); risk *review* is invoked from the `git_land` capability via `Services.Dispatch`.
- **Never auto-push.** No `gitflow` function and no new capability runs `git push`. Push stays the existing explicit `git_push`. Land stops at the local fast-forward and reports the push command.
- **No hardcoded `"main"`.** The trunk branch and test command are resolved: explicit override → `.cercano/gitflow.yaml` → auto-detect → error asking for config. Never a literal default branch name.
- **Commit-message rules (enforced in code):** reject any commit subject/body containing the case-insensitive substring `claude`; never add a `Co-Authored-By` trailer.
- **Never commit on the trunk branch** in `checkpoint`; commit only on the current non-trunk branch.
- **Safety ref before any history-mutating workflow** (`land`, history ops): record the affected branch/trunk SHA so `git_recover` can undo it.
- Capability tiers: `git_worktree`, `checkpoint`, history ops, branch hygiene = `TierW`; `git_land`, `git_recover` = `TierX`. All on `SurfaceAgent | SurfaceMCP`.
- Error strings are prefixed with the capability/function name (house style: `"git_land: ..."`).
- `go test ./...` green and `gofmt` clean in `source/server` after every task.
- Commit messages must not contain the word "Claude"; no `Co-Authored-By` trailer.

---

## File Structure

- `internal/gitflow/git.go` — `Repo` type + `run` exec helper + small queries (`Clean`, `CurrentBranch`, `RevParse`, `MergeBase`, `IsAncestor`, `CommitsBetween`).
- `internal/gitflow/config.go` — `Config` struct + `Resolve(workDir string, override Config) (Config, error)`.
- `internal/gitflow/safety.go` — `RecordSafety`, `ReadSafety`, ref naming under `refs/cercano/safety/`.
- `internal/gitflow/worktree.go` — `CreateWorktree`.
- `internal/gitflow/checkpoint.go` — `Checkpoint` (message assembly + guards).
- `internal/gitflow/land.go` — `Divergence`, `Land`, `LandContinue`, `Finalize`, conflict detection.
- `internal/gitflow/recover.go` — `Recover` (abort in-progress op / reset to safety ref).
- `internal/gitflow/bisect.go` — `BisectRun`.
- `internal/gitflow/history.go` — `SquashToOne`, `Autosquash`, `Stash`, `Unstash`.
- `internal/capabilities/builtins/gitflow_worktree.go`, `gitflow_checkpoint.go`, `gitflow_land.go`, `gitflow_recover.go`, `gitflow_bisect.go`, `gitflow_history.go` — capability wrappers.
- `internal/capabilities/builtins/builtins.go` — registration (+ count test bumps).

---

## Phase 1 — gitflow foundation

### Task 1: `Repo` + exec helper + basic queries

**Files:**
- Create: `source/server/internal/gitflow/git.go`
- Test: `source/server/internal/gitflow/git_test.go`

**Interfaces:**
- Produces: `type Repo struct { Dir string }`; `func Open(dir string) (*Repo, error)`; `func (r *Repo) run(ctx context.Context, args ...string) (string, error)`; `func (r *Repo) Clean(ctx context.Context) (bool, error)`; `func (r *Repo) CurrentBranch(ctx context.Context) (string, error)`; `func (r *Repo) RevParse(ctx context.Context, ref string) (string, error)`; `func (r *Repo) IsAncestor(ctx context.Context, a, b string) (bool, error)`; `func (r *Repo) MergeBase(ctx context.Context, a, b string) (string, error)`; `func (r *Repo) CommitsBetween(ctx context.Context, from, to string) (int, error)`.

- [ ] **Step 1: Write the failing test**

```go
package gitflow

import (
	"context"
	"os/exec"
	"testing"
)

// newTestRepo creates a temp git repo with one commit on branch "main".
func newTestRepo(t *testing.T) *Repo {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-b", "main")
	run("config", "user.email", "t@example.com")
	run("config", "user.name", "t")
	run("commit", "--allow-empty", "-m", "root")
	r, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestRepoBasics(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()

	clean, err := r.Clean(ctx)
	if err != nil || !clean {
		t.Fatalf("expected clean tree: clean=%v err=%v", clean, err)
	}
	br, err := r.CurrentBranch(ctx)
	if err != nil || br != "main" {
		t.Fatalf("branch: %q err=%v", br, err)
	}
	if _, err := r.RevParse(ctx, "HEAD"); err != nil {
		t.Fatalf("rev-parse HEAD: %v", err)
	}
	anc, err := r.IsAncestor(ctx, "HEAD", "HEAD")
	if err != nil || !anc {
		t.Fatalf("HEAD should be ancestor of itself: %v %v", anc, err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd source/server && go test ./internal/gitflow/ -run TestRepoBasics -v`
Expected: FAIL — package/`Open` undefined.

- [ ] **Step 3: Write the implementation**

```go
// Package gitflow holds deterministic, high-level git workflows. Every function
// shells out to git via exec; there is no model in the control flow and no
// dependency on the capability or dispatch layers. Workflows are exposed to the
// agent through thin capability wrappers in internal/capabilities/builtins.
package gitflow

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// Repo is a working directory backed by a git repository.
type Repo struct{ Dir string }

// Open returns a Repo for dir after confirming it is inside a work tree.
func Open(dir string) (*Repo, error) {
	r := &Repo{Dir: dir}
	if out, err := r.run(context.Background(), "rev-parse", "--is-inside-work-tree"); err != nil {
		return nil, fmt.Errorf("gitflow: %s is not a git work tree: %w", dir, err)
	} else if strings.TrimSpace(out) != "true" {
		return nil, fmt.Errorf("gitflow: %s is not a git work tree", dir)
	}
	return r, nil
}

// run executes git with args in the repo dir and returns trimmed combined output.
func (r *Repo) run(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = r.Dir
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	err := cmd.Run()
	out := strings.TrimSpace(buf.String())
	if err != nil {
		return out, fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, out)
	}
	return out, nil
}

// Clean reports whether the working tree has no staged or unstaged changes.
func (r *Repo) Clean(ctx context.Context) (bool, error) {
	out, err := r.run(ctx, "status", "--porcelain")
	if err != nil {
		return false, err
	}
	return out == "", nil
}

// CurrentBranch returns the checked-out branch name (or an error in detached HEAD).
func (r *Repo) CurrentBranch(ctx context.Context) (string, error) {
	out, err := r.run(ctx, "symbolic-ref", "--short", "HEAD")
	if err != nil {
		return "", err
	}
	return out, nil
}

// RevParse resolves a ref to a full SHA.
func (r *Repo) RevParse(ctx context.Context, ref string) (string, error) {
	return r.run(ctx, "rev-parse", ref)
}

// IsAncestor reports whether a is an ancestor of b (a..b fast-forwardable).
func (r *Repo) IsAncestor(ctx context.Context, a, b string) (bool, error) {
	cmd := exec.CommandContext(ctx, "git", "merge-base", "--is-ancestor", a, b)
	cmd.Dir = r.Dir
	err := cmd.Run()
	if err == nil {
		return true, nil
	}
	if ee, ok := err.(*exec.ExitError); ok && ee.ExitCode() == 1 {
		return false, nil
	}
	return false, fmt.Errorf("git merge-base --is-ancestor: %w", err)
}

// MergeBase returns the best common ancestor of a and b.
func (r *Repo) MergeBase(ctx context.Context, a, b string) (string, error) {
	return r.run(ctx, "merge-base", a, b)
}

// CommitsBetween returns the number of commits in from..to (commits on to not on from).
func (r *Repo) CommitsBetween(ctx context.Context, from, to string) (int, error) {
	out, err := r.run(ctx, "rev-list", "--count", from+".."+to)
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(out)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd source/server && go test ./internal/gitflow/ -run TestRepoBasics -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git -C <worktree> add source/server/internal/gitflow/git.go source/server/internal/gitflow/git_test.go
git -C <worktree> commit -m "feat(gitflow): Repo + exec helper + basic git queries"
```

### Task 2: Config resolution

**Files:**
- Create: `source/server/internal/gitflow/config.go`
- Test: `source/server/internal/gitflow/config_test.go`

**Interfaces:**
- Consumes: `Repo` (Task 1).
- Produces:
  ```go
  type Config struct {
      Trunk          string            `yaml:"trunk"`
      TestCommand    string            `yaml:"test_command"`
      SensitivePaths []string          `yaml:"sensitive_paths"`
      ReviewFloor    int               `yaml:"review_floor"` // hand-edited file count that forces review; default 5
      Regen          map[string]string `yaml:"regen"`        // path-glob -> regen command
  }
  func Resolve(ctx context.Context, r *Repo, override Config) (Config, error)
  ```
- Resolution per field: a non-zero `override` field wins; else `.cercano/gitflow.yaml` in the repo dir; else for `Trunk` only, auto-detect via `git symbolic-ref refs/remotes/origin/HEAD` (strip `origin/`), and if that fails, error asking the caller to set `trunk`. `ReviewFloor` defaults to 5 when unset. Missing file is not an error (zero Config), but an unresolved `Trunk` is.

- [ ] **Step 1: Write the failing test**

```go
package gitflow

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestResolveReadsCercanoConfigAndOverride(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	dir := filepath.Join(r.Dir, ".cercano")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	yaml := "trunk: develop\ntest_command: go test ./...\nreview_floor: 3\nsensitive_paths:\n  - \"source/server/internal/server/**\"\n"
	if err := os.WriteFile(filepath.Join(dir, "gitflow.yaml"), []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Resolve(ctx, r, Config{})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Trunk != "develop" || cfg.TestCommand != "go test ./..." || cfg.ReviewFloor != 3 {
		t.Fatalf("config not read: %+v", cfg)
	}

	// Override wins over file.
	cfg2, err := Resolve(ctx, r, Config{Trunk: "main"})
	if err != nil {
		t.Fatal(err)
	}
	if cfg2.Trunk != "main" {
		t.Fatalf("override should win, got %q", cfg2.Trunk)
	}
}

func TestResolveDefaultsReviewFloor(t *testing.T) {
	r := newTestRepo(t)
	// No .cercano config, but pass trunk override so Trunk resolves.
	cfg, err := Resolve(context.Background(), r, Config{Trunk: "main"})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ReviewFloor != 5 {
		t.Fatalf("review_floor default should be 5, got %d", cfg.ReviewFloor)
	}
}

func TestResolveErrorsWhenTrunkUnresolved(t *testing.T) {
	r := newTestRepo(t) // no origin remote, no config, no override
	if _, err := Resolve(context.Background(), r, Config{}); err == nil {
		t.Fatal("expected error when trunk cannot be resolved")
	}
}
```

- [ ] **Step 2: Run; fail** (`Resolve`/`Config` undefined).

- [ ] **Step 3: Write the implementation**

```go
package gitflow

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Trunk          string            `yaml:"trunk"`
	TestCommand    string            `yaml:"test_command"`
	SensitivePaths []string          `yaml:"sensitive_paths"`
	ReviewFloor    int               `yaml:"review_floor"`
	Regen          map[string]string `yaml:"regen"`
}

// Resolve fills a Config from override → .cercano/gitflow.yaml → auto-detect.
// An override field is used when non-zero. Trunk falls back to the repo's
// origin default branch; if still empty, Resolve errors (callers must not guess).
func Resolve(ctx context.Context, r *Repo, override Config) (Config, error) {
	var fileCfg Config
	path := filepath.Join(r.Dir, ".cercano", "gitflow.yaml")
	if data, err := os.ReadFile(path); err == nil {
		if err := yaml.Unmarshal(data, &fileCfg); err != nil {
			return Config{}, fmt.Errorf("gitflow: parse %s: %w", path, err)
		}
	} else if !os.IsNotExist(err) {
		return Config{}, fmt.Errorf("gitflow: read %s: %w", path, err)
	}

	out := fileCfg
	if override.Trunk != "" {
		out.Trunk = override.Trunk
	}
	if override.TestCommand != "" {
		out.TestCommand = override.TestCommand
	}
	if len(override.SensitivePaths) > 0 {
		out.SensitivePaths = override.SensitivePaths
	}
	if override.ReviewFloor != 0 {
		out.ReviewFloor = override.ReviewFloor
	}
	if len(override.Regen) > 0 {
		out.Regen = override.Regen
	}

	if out.Trunk == "" {
		if det, err := r.run(ctx, "symbolic-ref", "refs/remotes/origin/HEAD"); err == nil {
			out.Trunk = strings.TrimPrefix(strings.TrimSpace(det), "refs/remotes/origin/")
			out.Trunk = strings.TrimPrefix(out.Trunk, "origin/")
		}
	}
	if out.Trunk == "" {
		return Config{}, fmt.Errorf("gitflow: trunk branch unresolved — set `trunk` in .cercano/gitflow.yaml or pass an override")
	}
	if out.ReviewFloor == 0 {
		out.ReviewFloor = 5
	}
	return out, nil
}
```

- [ ] **Step 4: Run; pass.** `cd source/server && go test ./internal/gitflow/ -run TestResolve -v`

- [ ] **Step 5: Verify yaml dep present:** `cd source/server && go build ./internal/gitflow/` (if `gopkg.in/yaml.v3` is not in go.mod, run `go get gopkg.in/yaml.v3` and commit go.mod/go.sum). Confirm with `grep yaml.v3 go.mod`.

- [ ] **Step 6: Commit**

```bash
git -C <worktree> add source/server/internal/gitflow/config.go source/server/internal/gitflow/config_test.go source/server/go.mod source/server/go.sum
git -C <worktree> commit -m "feat(gitflow): resolve project config (trunk/test/sensitive/floor/regen)"
```

### Task 3: Safety refs

**Files:**
- Create: `source/server/internal/gitflow/safety.go`
- Test: `source/server/internal/gitflow/safety_test.go`

**Interfaces:**
- Consumes: `Repo`.
- Produces: `func (r *Repo) RecordSafety(ctx context.Context, label, rev string) error` (writes `refs/cercano/safety/<label>` → the SHA of `rev`); `func (r *Repo) ReadSafety(ctx context.Context, label string) (string, error)` (returns the recorded SHA, error if none).

- [ ] **Step 1: Write the failing test**

```go
package gitflow

import (
	"context"
	"testing"
)

func TestSafetyRefRoundTrip(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	head, _ := r.RevParse(ctx, "HEAD")
	if err := r.RecordSafety(ctx, "land", "HEAD"); err != nil {
		t.Fatal(err)
	}
	got, err := r.ReadSafety(ctx, "land")
	if err != nil {
		t.Fatal(err)
	}
	if got != head {
		t.Fatalf("safety ref: got %s want %s", got, head)
	}
	if _, err := r.ReadSafety(ctx, "nonexistent"); err == nil {
		t.Fatal("expected error reading missing safety ref")
	}
}
```

- [ ] **Step 2: Run; fail.**

- [ ] **Step 3: Write the implementation**

```go
package gitflow

import (
	"context"
	"fmt"
)

func safetyRefName(label string) string { return "refs/cercano/safety/" + label }

// RecordSafety stores the SHA of rev under a stable per-label ref so a later
// Recover can reset back to it ("undo the last <label> workflow").
func (r *Repo) RecordSafety(ctx context.Context, label, rev string) error {
	sha, err := r.RevParse(ctx, rev)
	if err != nil {
		return fmt.Errorf("gitflow: record safety %q: %w", label, err)
	}
	if _, err := r.run(ctx, "update-ref", safetyRefName(label), sha); err != nil {
		return fmt.Errorf("gitflow: record safety %q: %w", label, err)
	}
	return nil
}

// ReadSafety returns the SHA recorded under label, or an error if none exists.
func (r *Repo) ReadSafety(ctx context.Context, label string) (string, error) {
	out, err := r.run(ctx, "rev-parse", "--verify", "--quiet", safetyRefName(label))
	if err != nil || out == "" {
		return "", fmt.Errorf("gitflow: no safety ref for %q", label)
	}
	return out, nil
}
```

- [ ] **Step 4: Run; pass. Step 5: Commit**

```bash
git -C <worktree> commit -am "feat(gitflow): safety refs (record/read under refs/cercano/safety)"
```

---

## Phase 2 — Worktree + checkpoint (independent, high-value early)

### Task 4: `CreateWorktree`

**Files:**
- Create: `source/server/internal/gitflow/worktree.go`
- Test: `source/server/internal/gitflow/worktree_test.go`

**Interfaces:**
- Produces: `func (r *Repo) CreateWorktree(ctx context.Context, path, branch, trunk string) (string, error)` — creates `branch` off `trunk` and adds a worktree at `path`; returns the worktree path. Errors if the branch already exists or `path` is non-empty.

- [ ] **Step 1: Write the failing test**

```go
package gitflow

import (
	"context"
	"path/filepath"
	"testing"
)

func TestCreateWorktree(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	wt := filepath.Join(t.TempDir(), "feature-x")
	got, err := r.CreateWorktree(ctx, wt, "feature-x", "main")
	if err != nil {
		t.Fatal(err)
	}
	if got != wt {
		t.Fatalf("path: got %q want %q", got, wt)
	}
	// The worktree is a valid repo on the new branch.
	wr, err := Open(wt)
	if err != nil {
		t.Fatal(err)
	}
	if br, _ := wr.CurrentBranch(ctx); br != "feature-x" {
		t.Fatalf("worktree branch: %q", br)
	}
	// Creating the same branch again fails.
	if _, err := r.CreateWorktree(ctx, filepath.Join(t.TempDir(), "dup"), "feature-x", "main"); err == nil {
		t.Fatal("expected error creating an existing branch")
	}
}
```

- [ ] **Step 2: Run; fail. Step 3: Implement**

```go
package gitflow

import (
	"context"
	"fmt"
)

// CreateWorktree creates branch off trunk and adds a linked worktree at path.
func (r *Repo) CreateWorktree(ctx context.Context, path, branch, trunk string) (string, error) {
	if _, err := r.run(ctx, "worktree", "add", "-b", branch, path, trunk); err != nil {
		return "", fmt.Errorf("gitflow: create worktree %q (branch %q off %q): %w", path, branch, trunk, err)
	}
	return path, nil
}
```

- [ ] **Step 4: Run; pass. Step 5: Commit**

```bash
git -C <worktree> commit -am "feat(gitflow): CreateWorktree (branch off trunk + worktree add)"
```

### Task 5: `Checkpoint` (message assembly + guards)

**Files:**
- Create: `source/server/internal/gitflow/checkpoint.go`
- Test: `source/server/internal/gitflow/checkpoint_test.go`

**Interfaces:**
- Consumes: `Repo`.
- Produces: `func (r *Repo) Checkpoint(ctx context.Context, subject, body, trunk string) (string, error)` — stages all changes, builds the message, commits on the current branch; returns the new commit SHA. Errors if: subject empty; subject/body contains case-insensitive `claude`; current branch == trunk; nothing to commit.

- [ ] **Step 1: Write the failing test**

```go
package gitflow

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeFile(t *testing.T, r *Repo, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(r.Dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestCheckpointCommitsOnFeatureBranch(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	if _, err := r.run(ctx, "checkout", "-b", "feature"); err != nil {
		t.Fatal(err)
	}
	writeFile(t, r, "a.txt", "hello")
	sha, err := r.Checkpoint(ctx, "feat: add a", "Adds the a file.", "main")
	if err != nil {
		t.Fatal(err)
	}
	if sha == "" {
		t.Fatal("expected a commit sha")
	}
	msg, _ := r.run(ctx, "log", "-1", "--pretty=%B")
	if !strings.Contains(msg, "feat: add a") || !strings.Contains(msg, "Adds the a file.") {
		t.Fatalf("message not assembled: %q", msg)
	}
}

func TestCheckpointRejectsClaudeAndTrunkAndEmpty(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	writeFile(t, r, "b.txt", "x")
	// On trunk → reject.
	if _, err := r.Checkpoint(ctx, "feat: b", "", "main"); err == nil {
		t.Fatal("expected error committing on trunk")
	}
	// Move to a branch.
	if _, err := r.run(ctx, "checkout", "-b", "feature"); err != nil {
		t.Fatal(err)
	}
	// "claude" anywhere → reject (case-insensitive).
	if _, err := r.Checkpoint(ctx, "feat: b (Claude wrote this)", "", "main"); err == nil {
		t.Fatal("expected error rejecting 'claude' in subject")
	}
	// Empty subject → reject.
	if _, err := r.Checkpoint(ctx, "", "body", "main"); err == nil {
		t.Fatal("expected error on empty subject")
	}
}
```

- [ ] **Step 2: Run; fail. Step 3: Implement**

```go
package gitflow

import (
	"context"
	"fmt"
	"strings"
)

// Checkpoint stages all changes and commits them on the current branch with a
// subject (+ optional body). It refuses to commit on trunk, refuses messages
// containing "claude" (case-insensitive), and never adds a Co-Authored-By
// trailer or pushes.
func (r *Repo) Checkpoint(ctx context.Context, subject, body, trunk string) (string, error) {
	if strings.TrimSpace(subject) == "" {
		return "", fmt.Errorf("gitflow: checkpoint: subject is required")
	}
	if hasClaude(subject) || hasClaude(body) {
		return "", fmt.Errorf("gitflow: checkpoint: commit message must not contain \"Claude\"")
	}
	branch, err := r.CurrentBranch(ctx)
	if err != nil {
		return "", fmt.Errorf("gitflow: checkpoint: %w", err)
	}
	if branch == trunk {
		return "", fmt.Errorf("gitflow: checkpoint: refusing to commit on trunk %q; switch to a feature branch", trunk)
	}
	if _, err := r.run(ctx, "add", "-A"); err != nil {
		return "", fmt.Errorf("gitflow: checkpoint: stage: %w", err)
	}
	clean, err := r.Clean(ctx)
	if err != nil {
		return "", err
	}
	if clean {
		return "", fmt.Errorf("gitflow: checkpoint: nothing to commit")
	}
	args := []string{"commit", "-m", subject}
	if strings.TrimSpace(body) != "" {
		args = append(args, "-m", body)
	}
	if _, err := r.run(ctx, args...); err != nil {
		return "", fmt.Errorf("gitflow: checkpoint: commit: %w", err)
	}
	return r.RevParse(ctx, "HEAD")
}

// hasClaude reports whether s contains "claude" case-insensitively.
func hasClaude(s string) bool { return strings.Contains(strings.ToLower(s), "claude") }
```

- [ ] **Step 4: Run; pass. Step 5: Commit**

```bash
git -C <worktree> commit -am "feat(gitflow): Checkpoint (staged commit on feature branch, message guards)"
```

---

## Phase 3 — Land (the core flow)

### Task 6: `Divergence` report

**Files:**
- Create: `source/server/internal/gitflow/land.go`
- Test: `source/server/internal/gitflow/land_test.go`

**Interfaces:**
- Produces: `type Divergence struct { TrunkAhead, FeatureAhead int; FastForwardable bool }`; `func (r *Repo) Divergence(ctx context.Context, feature, trunk string) (Divergence, error)`. `FastForwardable` = trunk is an ancestor of feature (feature already contains trunk).

- [ ] **Step 1: Write the failing test**

```go
func TestDivergence(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	// feature branches off main, both advance.
	mustRun(t, r, "checkout", "-b", "feature")
	writeFile(t, r, "f1.txt", "1")
	mustRun(t, r, "add", "-A")
	mustRun(t, r, "commit", "-m", "feat: f1")
	mustRun(t, r, "checkout", "main")
	writeFile(t, r, "m1.txt", "1")
	mustRun(t, r, "add", "-A")
	mustRun(t, r, "commit", "-m", "chore: m1")

	d, err := r.Divergence(ctx, "feature", "main")
	if err != nil {
		t.Fatal(err)
	}
	if d.TrunkAhead != 1 || d.FeatureAhead != 1 || d.FastForwardable {
		t.Fatalf("divergence: %+v", d)
	}
}
```

Add a `mustRun` helper to `land_test.go`:
```go
func mustRun(t *testing.T, r *Repo, args ...string) {
	t.Helper()
	if _, err := r.run(context.Background(), args...); err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
}
```

- [ ] **Step 2: Run; fail. Step 3: Implement** in `land.go`:

```go
package gitflow

import "context"

// Divergence summarizes how a feature branch relates to trunk.
type Divergence struct {
	TrunkAhead      int  // commits on trunk not on feature
	FeatureAhead    int  // commits on feature not on trunk
	FastForwardable bool // trunk is already an ancestor of feature
}

// Divergence reports the commit gap between feature and trunk.
func (r *Repo) Divergence(ctx context.Context, feature, trunk string) (Divergence, error) {
	var d Divergence
	base, err := r.MergeBase(ctx, feature, trunk)
	if err != nil {
		return d, err
	}
	if d.TrunkAhead, err = r.CommitsBetween(ctx, base, trunk); err != nil {
		return d, err
	}
	if d.FeatureAhead, err = r.CommitsBetween(ctx, base, feature); err != nil {
		return d, err
	}
	if d.FastForwardable, err = r.IsAncestor(ctx, trunk, feature); err != nil {
		return d, err
	}
	return d, nil
}
```

- [ ] **Step 4: Run; pass. Step 5: Commit**

```bash
git -C <worktree> commit -am "feat(gitflow): Divergence report (trunk/feature ahead counts + ff check)"
```

### Task 7: `Land` reconcile (rebase default / merge override) with conflict pause

**Files:**
- Modify: `source/server/internal/gitflow/land.go`
- Test: `source/server/internal/gitflow/land_test.go`

**Interfaces:**
- Produces:
  ```go
  type Strategy string
  const ( StrategyRebase Strategy = "rebase"; StrategyMerge Strategy = "merge" )
  type LandState struct {
      Reconciled bool      // true if no conflict (ready for test gate + finalize)
      Conflicts  []string  // conflicted file paths when paused
      Strategy   Strategy
  }
  func (r *Repo) Land(ctx context.Context, feature, trunk string, strategy Strategy) (LandState, error)
  ```
- Behavior: must be run from a clean tree. Records a safety ref `land` for `trunk` first. Checks out `feature`. If `StrategyRebase`: `git rebase <trunk>`; if `StrategyMerge`: `git merge <trunk>`. On conflict, returns `LandState{Reconciled:false, Conflicts:[...]}` and leaves the repo mid-operation (does NOT abort). On success, `LandState{Reconciled:true}`.

- [ ] **Step 1: Write the failing test** (clean rebase, and a conflict pause)

```go
func TestLandRebaseClean(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	mustRun(t, r, "checkout", "-b", "feature")
	writeFile(t, r, "f.txt", "feature")
	mustRun(t, r, "add", "-A"); mustRun(t, r, "commit", "-m", "feat: f")
	mustRun(t, r, "checkout", "main")
	writeFile(t, r, "m.txt", "main")
	mustRun(t, r, "add", "-A"); mustRun(t, r, "commit", "-m", "chore: m")

	st, err := r.Land(ctx, "feature", "main", StrategyRebase)
	if err != nil {
		t.Fatal(err)
	}
	if !st.Reconciled || len(st.Conflicts) != 0 {
		t.Fatalf("expected clean reconcile, got %+v", st)
	}
	// feature now contains main's commit (ff-able onto main).
	ff, _ := r.IsAncestor(ctx, "main", "feature")
	if !ff {
		t.Fatal("feature should fast-forward onto main after rebase")
	}
}

func TestLandRebaseConflictPauses(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	writeFile(t, r, "shared.txt", "base")
	mustRun(t, r, "add", "-A"); mustRun(t, r, "commit", "-m", "chore: base")
	mustRun(t, r, "checkout", "-b", "feature")
	writeFile(t, r, "shared.txt", "feature-change")
	mustRun(t, r, "add", "-A"); mustRun(t, r, "commit", "-m", "feat: f")
	mustRun(t, r, "checkout", "main")
	writeFile(t, r, "shared.txt", "main-change")
	mustRun(t, r, "add", "-A"); mustRun(t, r, "commit", "-m", "chore: m")

	st, err := r.Land(ctx, "feature", "main", StrategyRebase)
	if err != nil {
		t.Fatal(err)
	}
	if st.Reconciled || len(st.Conflicts) == 0 {
		t.Fatalf("expected conflict pause, got %+v", st)
	}
	if st.Conflicts[0] != "shared.txt" {
		t.Fatalf("expected shared.txt conflict, got %v", st.Conflicts)
	}
}
```

- [ ] **Step 2: Run; fail. Step 3: Implement** (append to `land.go`):

```go
import (
	"context"
	"fmt"
	"strings"
)

type Strategy string

const (
	StrategyRebase Strategy = "rebase"
	StrategyMerge  Strategy = "merge"
)

type LandState struct {
	Reconciled bool
	Conflicts  []string
	Strategy   Strategy
}

// conflictedFiles returns the unmerged paths (git diff --name-only --diff-filter=U).
func (r *Repo) conflictedFiles(ctx context.Context) ([]string, error) {
	out, err := r.run(ctx, "diff", "--name-only", "--diff-filter=U")
	if err != nil {
		return nil, err
	}
	if out == "" {
		return nil, nil
	}
	return strings.Split(out, "\n"), nil
}

// Land reconciles feature against trunk on the feature branch. On conflict it
// pauses (leaves the repo mid-rebase/merge) and returns the conflicted files.
func (r *Repo) Land(ctx context.Context, feature, trunk string, strategy Strategy) (LandState, error) {
	st := LandState{Strategy: strategy}
	if clean, err := r.Clean(ctx); err != nil {
		return st, err
	} else if !clean {
		return st, fmt.Errorf("gitflow: land: working tree not clean — commit or checkpoint first")
	}
	if err := r.RecordSafety(ctx, "land", trunk); err != nil {
		return st, err
	}
	if _, err := r.run(ctx, "checkout", feature); err != nil {
		return st, fmt.Errorf("gitflow: land: checkout %q: %w", feature, err)
	}
	var op string
	switch strategy {
	case StrategyMerge:
		op = "merge"
	default:
		op = "rebase"
		st.Strategy = StrategyRebase
	}
	if _, err := r.run(ctx, op, trunk); err != nil {
		// Distinguish a conflict (expected) from a hard failure.
		conf, cErr := r.conflictedFiles(ctx)
		if cErr == nil && len(conf) > 0 {
			st.Conflicts = conf
			return st, nil
		}
		return st, fmt.Errorf("gitflow: land: %s %q: %w", op, trunk, err)
	}
	st.Reconciled = true
	return st, nil
}
```

- [ ] **Step 4: Run; pass. Step 5: Commit**

```bash
git -C <worktree> commit -am "feat(gitflow): Land reconcile (rebase/merge) with conflict pause"
```

### Task 8: `LandContinue` (resume after resolution) + `Finalize` (ff trunk) + test gate

**Files:**
- Modify: `source/server/internal/gitflow/land.go`
- Test: `source/server/internal/gitflow/land_test.go`

**Interfaces:**
- Produces:
  ```go
  func (r *Repo) LandContinue(ctx context.Context, strategy Strategy) (LandState, error)
  func (r *Repo) RunTests(ctx context.Context, testCommand string) (string, error) // runs via sh -c in repo dir
  func (r *Repo) Finalize(ctx context.Context, feature, trunk string) error          // checkout trunk; merge --ff-only feature
  ```
- `LandContinue`: if conflicts remain unstaged, returns them again (still paused). Else stages resolved files (`git add -A`) and runs `git rebase --continue` (or `merge --continue`); returns `Reconciled:true` on success or the next conflict set.
- `Finalize`: checks out trunk, `git merge --ff-only feature`. Errors if not fast-forwardable (caller must reconcile first).

- [ ] **Step 1: Write the failing test** (resolve the Task 7 conflict, continue, finalize)

```go
func TestLandContinueAndFinalize(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	writeFile(t, r, "shared.txt", "base")
	mustRun(t, r, "add", "-A"); mustRun(t, r, "commit", "-m", "chore: base")
	mustRun(t, r, "checkout", "-b", "feature")
	writeFile(t, r, "shared.txt", "feature-change")
	mustRun(t, r, "add", "-A"); mustRun(t, r, "commit", "-m", "feat: f")
	mustRun(t, r, "checkout", "main")
	writeFile(t, r, "shared.txt", "main-change")
	mustRun(t, r, "add", "-A"); mustRun(t, r, "commit", "-m", "chore: m")

	st, _ := r.Land(ctx, "feature", "main", StrategyRebase)
	if st.Reconciled {
		t.Fatal("precondition: expected conflict")
	}
	// Resolve the conflict (simulate the agent editing the file).
	writeFile(t, r, "shared.txt", "resolved")
	st2, err := r.LandContinue(ctx, StrategyRebase)
	if err != nil {
		t.Fatal(err)
	}
	if !st2.Reconciled {
		t.Fatalf("expected reconciled after continue, got %+v", st2)
	}
	// Test gate passes.
	if _, err := r.RunTests(ctx, "true"); err != nil {
		t.Fatalf("test gate: %v", err)
	}
	// Finalize: main fast-forwards to feature.
	if err := r.Finalize(ctx, "feature", "main"); err != nil {
		t.Fatal(err)
	}
	ff, _ := r.IsAncestor(ctx, "feature", "main")
	if !ff {
		t.Fatal("main should contain feature after finalize")
	}
}

func TestRunTestsRed(t *testing.T) {
	r := newTestRepo(t)
	if _, err := r.RunTests(context.Background(), "false"); err == nil {
		t.Fatal("expected RunTests to error on a failing command")
	}
}
```

- [ ] **Step 2: Run; fail. Step 3: Implement** (append to `land.go`; add `"os/exec"`):

```go
// LandContinue resumes a paused reconcile after the caller resolved conflicts.
func (r *Repo) LandContinue(ctx context.Context, strategy Strategy) (LandState, error) {
	st := LandState{Strategy: strategy}
	if conf, err := r.conflictedFiles(ctx); err != nil {
		return st, err
	} else if len(conf) > 0 {
		st.Conflicts = conf
		return st, nil // still unresolved
	}
	if _, err := r.run(ctx, "add", "-A"); err != nil {
		return st, fmt.Errorf("gitflow: land --continue: stage: %w", err)
	}
	op := "rebase"
	if strategy == StrategyMerge {
		op = "merge"
	}
	// GIT_EDITOR=true avoids an interactive editor on merge/rebase --continue.
	cmd := exec.CommandContext(ctx, "git", op, "--continue")
	cmd.Dir = r.Dir
	cmd.Env = append(cmd.Environ(), "GIT_EDITOR=true")
	if out, err := cmd.CombinedOutput(); err != nil {
		conf, _ := r.conflictedFiles(ctx)
		if len(conf) > 0 {
			st.Conflicts = conf
			return st, nil
		}
		return st, fmt.Errorf("gitflow: land --continue: %s: %w: %s", op, err, strings.TrimSpace(string(out)))
	}
	st.Reconciled = true
	return st, nil
}

// RunTests runs testCommand via `sh -c` in the repo dir; non-zero exit is an error.
func (r *Repo) RunTests(ctx context.Context, testCommand string) (string, error) {
	cmd := exec.CommandContext(ctx, "sh", "-c", testCommand)
	cmd.Dir = r.Dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return strings.TrimSpace(string(out)), fmt.Errorf("gitflow: test gate failed: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

// Finalize fast-forwards trunk to feature. Errors if not fast-forwardable.
func (r *Repo) Finalize(ctx context.Context, feature, trunk string) error {
	if _, err := r.run(ctx, "checkout", trunk); err != nil {
		return fmt.Errorf("gitflow: finalize: checkout %q: %w", trunk, err)
	}
	if _, err := r.run(ctx, "merge", "--ff-only", feature); err != nil {
		return fmt.Errorf("gitflow: finalize: ff-only merge of %q into %q failed (reconcile first): %w", feature, trunk, err)
	}
	return nil
}
```

- [ ] **Step 4: Run; pass. Step 5: Commit**

```bash
git -C <worktree> commit -am "feat(gitflow): LandContinue + RunTests gate + Finalize (ff-only)"
```

### Task 9: Conflict signals (for the review gate)

**Files:**
- Modify: `source/server/internal/gitflow/land.go`
- Test: `source/server/internal/gitflow/land_test.go`

**Interfaces:**
- Produces:
  ```go
  type ResolutionSignals struct {
      Files         []string // resolved/conflicted files
      HandEdited    int      // files not matched by a Regen glob (i.e. not generated)
      SensitiveHits []string // files matching any sensitive glob
      Diff          string   // the resolution diff (git diff <safetyRef-of-feature>..HEAD), capped
  }
  func (r *Repo) ResolutionSignalsFor(ctx context.Context, files []string, cfg Config) ResolutionSignals
  ```
- Pure computation from the conflicted file list + config: count hand-edited (not matched by any `Regen` glob), collect sensitive matches (match any `SensitivePaths` glob via `path.Match` on each path segment-insensitive — use `filepath.Match` against the path), and capture a capped diff of the working tree (`git diff --staged` or `HEAD~1..HEAD`, capped at e.g. 16 KiB). Keep it pure (no model).

- [ ] **Step 1: Write the failing test**

```go
func TestResolutionSignals(t *testing.T) {
	r := newTestRepo(t)
	cfg := Config{
		SensitivePaths: []string{"internal/server/*"},
		Regen:          map[string]string{"*.pb.go": "protoc ..."},
	}
	sig := r.ResolutionSignalsFor(context.Background(), []string{"internal/server/server.go", "api/agent.pb.go", "internal/x/y.go"}, cfg)
	if sig.HandEdited != 2 { // server.go + y.go; pb.go is generated
		t.Fatalf("hand-edited count: %d", sig.HandEdited)
	}
	if len(sig.SensitiveHits) != 1 || sig.SensitiveHits[0] != "internal/server/server.go" {
		t.Fatalf("sensitive hits: %v", sig.SensitiveHits)
	}
}
```

- [ ] **Step 2: Run; fail. Step 3: Implement** (append; add `"path/filepath"`):

```go
type ResolutionSignals struct {
	Files         []string
	HandEdited    int
	SensitiveHits []string
	Diff          string
}

func matchesAny(globs []string, path string) bool {
	for _, g := range globs {
		if ok, _ := filepath.Match(g, path); ok {
			return true
		}
		// also match against the basename so "*.pb.go" matches "api/x.pb.go"
		if ok, _ := filepath.Match(g, filepath.Base(path)); ok {
			return true
		}
	}
	return false
}

// ResolutionSignalsFor computes deterministic risk signals from the conflicted
// file set and config. Generated files (matched by a Regen glob) are not counted
// as hand-edited. Pure: no model, no mutation.
func (r *Repo) ResolutionSignalsFor(ctx context.Context, files []string, cfg Config) ResolutionSignals {
	sig := ResolutionSignals{Files: files}
	var regenGlobs []string
	for g := range cfg.Regen {
		regenGlobs = append(regenGlobs, g)
	}
	for _, f := range files {
		if !matchesAny(regenGlobs, f) {
			sig.HandEdited++
		}
		if matchesAny(cfg.SensitivePaths, f) {
			sig.SensitiveHits = append(sig.SensitiveHits, f)
		}
	}
	const cap = 16 * 1024
	if d, err := r.run(ctx, "diff", "HEAD"); err == nil {
		if len(d) > cap {
			d = d[:cap] + "\n… (diff truncated)"
		}
		sig.Diff = d
	}
	return sig
}
```

- [ ] **Step 4: Run; pass. Step 5: Commit**

```bash
git -C <worktree> commit -am "feat(gitflow): ResolutionSignals (hand-edited/sensitive/diff for review gate)"
```

---

## Phase 4 — Recover, bisect, history

### Task 10: `Recover`

**Files:**
- Create: `source/server/internal/gitflow/recover.go`
- Test: `source/server/internal/gitflow/recover_test.go`

**Interfaces:**
- Produces: `func (r *Repo) AbortInProgress(ctx context.Context) (string, error)` (abort a rebase or merge if one is in progress; returns what it aborted or "none"); `func (r *Repo) ResetToSafety(ctx context.Context, label, branch string) error` (checkout branch, hard-reset to the recorded safety ref).

- [ ] **Step 1: Write the failing test**

```go
package gitflow

import (
	"context"
	"testing"
)

func TestAbortInProgressMerge(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	writeFile(t, r, "s.txt", "base"); mustRun(t, r, "add", "-A"); mustRun(t, r, "commit", "-m", "base")
	mustRun(t, r, "checkout", "-b", "feature")
	writeFile(t, r, "s.txt", "f"); mustRun(t, r, "add", "-A"); mustRun(t, r, "commit", "-m", "f")
	mustRun(t, r, "checkout", "main")
	writeFile(t, r, "s.txt", "m"); mustRun(t, r, "add", "-A"); mustRun(t, r, "commit", "-m", "m")
	st, _ := r.Land(ctx, "feature", "main", StrategyRebase)
	if st.Reconciled {
		t.Fatal("precondition: expected conflict/in-progress")
	}
	what, err := r.AbortInProgress(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if what != "rebase" {
		t.Fatalf("expected to abort a rebase, got %q", what)
	}
	clean, _ := r.Clean(ctx)
	if !clean {
		t.Fatal("expected clean tree after abort")
	}
}

func TestResetToSafety(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	before, _ := r.RevParse(ctx, "HEAD")
	r.RecordSafety(ctx, "land", "HEAD")
	writeFile(t, r, "x.txt", "x"); mustRun(t, r, "add", "-A"); mustRun(t, r, "commit", "-m", "advance")
	if err := r.ResetToSafety(ctx, "land", "main"); err != nil {
		t.Fatal(err)
	}
	now, _ := r.RevParse(ctx, "HEAD")
	if now != before {
		t.Fatalf("expected reset to %s, at %s", before, now)
	}
}
```

- [ ] **Step 2: Run; fail. Step 3: Implement**

```go
package gitflow

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
)

// AbortInProgress aborts an in-progress rebase or merge, if any.
func (r *Repo) AbortInProgress(ctx context.Context) (string, error) {
	gitDir, err := r.run(ctx, "rev-parse", "--git-dir")
	if err != nil {
		return "", err
	}
	abs := gitDir
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(r.Dir, gitDir)
	}
	if _, statErr := os.Stat(filepath.Join(abs, "rebase-merge")); statErr == nil {
		if _, err := r.run(ctx, "rebase", "--abort"); err != nil {
			return "", err
		}
		return "rebase", nil
	}
	if _, statErr := os.Stat(filepath.Join(abs, "rebase-apply")); statErr == nil {
		if _, err := r.run(ctx, "rebase", "--abort"); err != nil {
			return "", err
		}
		return "rebase", nil
	}
	if _, statErr := os.Stat(filepath.Join(abs, "MERGE_HEAD")); statErr == nil {
		if _, err := r.run(ctx, "merge", "--abort"); err != nil {
			return "", err
		}
		return "merge", nil
	}
	return "none", nil
}

// ResetToSafety checks out branch and hard-resets it to the recorded safety ref.
func (r *Repo) ResetToSafety(ctx context.Context, label, branch string) error {
	sha, err := r.ReadSafety(ctx, label)
	if err != nil {
		return err
	}
	if _, err := r.run(ctx, "checkout", branch); err != nil {
		return fmt.Errorf("gitflow: recover: checkout %q: %w", branch, err)
	}
	if _, err := r.run(ctx, "reset", "--hard", sha); err != nil {
		return fmt.Errorf("gitflow: recover: reset %q to %s: %w", branch, sha, err)
	}
	return nil
}
```

- [ ] **Step 4: Run; pass. Step 5: Commit**

```bash
git -C <worktree> commit -am "feat(gitflow): Recover (abort in-progress op / reset to safety ref)"
```

### Task 11: `BisectRun`

**Files:**
- Create: `source/server/internal/gitflow/bisect.go`
- Test: `source/server/internal/gitflow/bisect_test.go`

**Interfaces:**
- Produces: `func (r *Repo) BisectRun(ctx context.Context, good, bad, testCommand string) (string, error)` — runs `git bisect start <bad> <good>` then `git bisect run sh -c <testCommand>`, parses the first-bad commit SHA from output, always runs `git bisect reset`, returns the SHA.

- [ ] **Step 1: Write the failing test**

```go
package gitflow

import (
	"context"
	"strings"
	"testing"
)

func TestBisectRunFindsBadCommit(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	// good commit: marker file says "good"
	writeFile(t, r, "marker.txt", "good"); mustRun(t, r, "add", "-A"); mustRun(t, r, "commit", "-m", "good")
	good, _ := r.RevParse(ctx, "HEAD")
	writeFile(t, r, "pad.txt", "1"); mustRun(t, r, "add", "-A"); mustRun(t, r, "commit", "-m", "pad1")
	// bad commit: marker flips to "bad"
	writeFile(t, r, "marker.txt", "bad"); mustRun(t, r, "add", "-A"); mustRun(t, r, "commit", "-m", "break")
	bad, _ := r.RevParse(ctx, "HEAD")
	writeFile(t, r, "pad2.txt", "2"); mustRun(t, r, "add", "-A"); mustRun(t, r, "commit", "-m", "pad2")

	// test command: exit 0 while marker says good, 1 once it says bad.
	sha, err := r.BisectRun(ctx, good, "HEAD", `test "$(cat marker.txt)" = good`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(bad, sha) && sha != bad {
		t.Fatalf("expected first-bad %s, got %s", bad, sha)
	}
}
```

- [ ] **Step 2: Run; fail. Step 3: Implement**

```go
package gitflow

import (
	"context"
	"fmt"
	"regexp"
)

var firstBadRe = regexp.MustCompile(`(?m)^([0-9a-f]{7,40}) is the first bad commit`)

// BisectRun bisects good..bad running testCommand at each step (exit 0 = good).
// It always resets the bisect state before returning.
func (r *Repo) BisectRun(ctx context.Context, good, bad, testCommand string) (string, error) {
	if _, err := r.run(ctx, "bisect", "start", bad, good); err != nil {
		return "", fmt.Errorf("gitflow: bisect start: %w", err)
	}
	out, runErr := r.run(ctx, "bisect", "run", "sh", "-c", testCommand)
	_, _ = r.run(ctx, "bisect", "reset") // always reset, ignore reset error
	if runErr != nil {
		return "", fmt.Errorf("gitflow: bisect run: %w", runErr)
	}
	m := firstBadRe.FindStringSubmatch(out)
	if m == nil {
		return "", fmt.Errorf("gitflow: bisect: could not identify first bad commit from output:\n%s", out)
	}
	return m[1], nil
}
```

- [ ] **Step 4: Run; pass. Step 5: Commit**

```bash
git -C <worktree> commit -am "feat(gitflow): BisectRun (automated git bisect with a test command)"
```

### Task 12: History ops (`SquashToOne`, `Autosquash`, `Stash`/`Unstash`)

**Files:**
- Create: `source/server/internal/gitflow/history.go`
- Test: `source/server/internal/gitflow/history_test.go`

**Interfaces:**
- Produces: `func (r *Repo) SquashToOne(ctx context.Context, trunk, subject, body string) (string, error)` (records safety ref `history`; `git reset --soft $(merge-base trunk HEAD)`; commit with guarded message); `func (r *Repo) Stash(ctx context.Context) error`; `func (r *Repo) Unstash(ctx context.Context) error`. (Autosquash is `rebase --autosquash` — include if straightforward; otherwise note as a follow-on. For v1 implement SquashToOne + Stash/Unstash, which are the clearly-deterministic ones.)

- [ ] **Step 1: Write the failing test**

```go
package gitflow

import (
	"context"
	"strings"
	"testing"
)

func TestSquashToOne(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	mustRun(t, r, "checkout", "-b", "feature")
	writeFile(t, r, "a.txt", "1"); mustRun(t, r, "add", "-A"); mustRun(t, r, "commit", "-m", "wip 1")
	writeFile(t, r, "b.txt", "2"); mustRun(t, r, "add", "-A"); mustRun(t, r, "commit", "-m", "wip 2")

	sha, err := r.SquashToOne(ctx, "main", "feat: the feature", "Body here.")
	if err != nil {
		t.Fatal(err)
	}
	if sha == "" {
		t.Fatal("expected a commit")
	}
	// Exactly one commit on feature since main.
	n, _ := r.CommitsBetween(ctx, "main", "feature")
	if n != 1 {
		t.Fatalf("expected 1 commit after squash, got %d", n)
	}
	msg, _ := r.run(ctx, "log", "-1", "--pretty=%B")
	if !strings.Contains(msg, "feat: the feature") {
		t.Fatalf("squashed message wrong: %q", msg)
	}
}

func TestSquashRejectsClaude(t *testing.T) {
	r := newTestRepo(t)
	mustRun(t, r, "checkout", "-b", "feature")
	writeFile(t, r, "a.txt", "1"); mustRun(t, r, "add", "-A"); mustRun(t, r, "commit", "-m", "wip")
	if _, err := r.SquashToOne(context.Background(), "main", "feat: by Claude", ""); err == nil {
		t.Fatal("expected rejection of 'claude' in squash message")
	}
}
```

- [ ] **Step 2: Run; fail. Step 3: Implement**

```go
package gitflow

import (
	"context"
	"fmt"
	"strings"
)

// SquashToOne collapses all commits on the current branch since its merge-base
// with trunk into a single commit with the given guarded message.
func (r *Repo) SquashToOne(ctx context.Context, trunk, subject, body string) (string, error) {
	if strings.TrimSpace(subject) == "" {
		return "", fmt.Errorf("gitflow: squash: subject is required")
	}
	if hasClaude(subject) || hasClaude(body) {
		return "", fmt.Errorf("gitflow: squash: commit message must not contain \"Claude\"")
	}
	if err := r.RecordSafety(ctx, "history", "HEAD"); err != nil {
		return "", err
	}
	base, err := r.MergeBase(ctx, "HEAD", trunk)
	if err != nil {
		return "", err
	}
	if _, err := r.run(ctx, "reset", "--soft", base); err != nil {
		return "", fmt.Errorf("gitflow: squash: reset --soft %s: %w", base, err)
	}
	args := []string{"commit", "-m", subject}
	if strings.TrimSpace(body) != "" {
		args = append(args, "-m", body)
	}
	if _, err := r.run(ctx, args...); err != nil {
		return "", fmt.Errorf("gitflow: squash: commit: %w", err)
	}
	return r.RevParse(ctx, "HEAD")
}

// Stash saves uncommitted changes (including untracked). Unstash pops them.
func (r *Repo) Stash(ctx context.Context) error {
	_, err := r.run(ctx, "stash", "push", "--include-untracked")
	return err
}
func (r *Repo) Unstash(ctx context.Context) error {
	_, err := r.run(ctx, "stash", "pop")
	return err
}
```

- [ ] **Step 4: Run; pass. Step 5: Commit**

```bash
git -C <worktree> commit -am "feat(gitflow): history ops (SquashToOne + Stash/Unstash)"
```

---

## Phase 5 — Capability wrappers (both surfaces)

> Each wrapper follows the existing builtin house style (see `builtins/git_write.go`):
> a struct implementing `Name/Tier/Surfaces/Description/Schema/Execute`, parsing args,
> opening a `gitflow.Repo` at `call.WorkDir` (or an args `cwd`), calling the engine,
> returning `capabilities.NewTextResult(...)` or a `"<name>: ..."`-prefixed error.

### Task 13: `git_worktree`, `checkpoint`, `git_recover`, history capabilities

**Files:**
- Create: `source/server/internal/capabilities/builtins/gitflow_worktree.go`, `gitflow_checkpoint.go`, `gitflow_recover.go`, `gitflow_history.go`, `gitflow_bisect.go`
- Modify: `source/server/internal/capabilities/builtins/builtins.go`
- Modify: `source/server/internal/capabilities/builtins/builtins_test.go` (count)
- Test: `source/server/internal/capabilities/builtins/gitflow_caps_test.go`

**Interfaces:**
- Consumes: `gitflow.*` (Tasks 1–12), `capabilities.Capability`, `Config` resolution.
- Produces constructors: `GitWorktree()`, `Checkpoint()`, `GitRecover()`, `GitSquash()`, `GitBisect()` (all `capabilities.Capability`).

- [ ] **Step 1: Write `checkpoint` (representative wrapper) + its test**

`gitflow_checkpoint.go`:
```go
package builtins

import (
	"context"
	"encoding/json"
	"fmt"

	"cercano/source/server/internal/capabilities"
	"cercano/source/server/internal/gitflow"
)

type checkpointCap struct{}

func Checkpoint() capabilities.Capability { return checkpointCap{} }

func (checkpointCap) Name() string                  { return "checkpoint" }
func (checkpointCap) Tier() capabilities.Tier        { return capabilities.TierW }
func (checkpointCap) Surfaces() capabilities.Surface { return capabilities.SurfaceAgent | capabilities.SurfaceMCP }
func (checkpointCap) Description() string {
	return "Commit a solved unit of work on the current branch. Provide a one-line conventional-commit subject and an optional body. Never commits on the trunk branch and never pushes. Args: {subject: string, body?: string, trunk?: string, cwd?: string}."
}
func (checkpointCap) Schema() capabilities.Schema {
	return capabilities.Schema(`{"type":"object","required":["subject"],"properties":{"subject":{"type":"string"},"body":{"type":"string"},"trunk":{"type":"string"},"cwd":{"type":"string"}}}`)
}

type checkpointArgs struct {
	Subject string `json:"subject"`
	Body    string `json:"body"`
	Trunk   string `json:"trunk"`
	Cwd     string `json:"cwd"`
}

func (checkpointCap) Execute(ctx context.Context, call *capabilities.Call) (*capabilities.Result, error) {
	var a checkpointArgs
	if err := json.Unmarshal(call.Args, &a); err != nil {
		return nil, fmt.Errorf("checkpoint: parse args: %w", err)
	}
	dir := a.Cwd
	if dir == "" {
		dir = call.WorkDir
	}
	r, err := gitflow.Open(dir)
	if err != nil {
		return nil, fmt.Errorf("checkpoint: %w", err)
	}
	cfg, err := gitflow.Resolve(ctx, r, gitflow.Config{Trunk: a.Trunk})
	if err != nil {
		return nil, fmt.Errorf("checkpoint: %w", err)
	}
	sha, err := r.Checkpoint(ctx, a.Subject, a.Body, cfg.Trunk)
	if err != nil {
		return nil, err
	}
	return capabilities.NewTextResult(fmt.Sprintf("checkpoint committed %s on the current branch (not pushed)", sha[:min(12, len(sha))])), nil
}

func min(a, b int) int { // Go 1.26 has builtin min; keep local only if a collision is found, else delete.
	if a < b {
		return a
	}
	return b
}
```
(Note: Go 1.26 has a builtin `min` — delete the local `min` and use the builtin; the local is shown only so the snippet compiles in isolation.)

`gitflow_caps_test.go` (checkpoint):
```go
package builtins

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"cercano/source/server/internal/capabilities"
)

func tempGitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, args := range [][]string{{"init", "-b", "main"}, {"config", "user.email", "t@e"}, {"config", "user.name", "t"}, {"commit", "--allow-empty", "-m", "root"}} {
		c := exec.Command("git", args...)
		c.Dir = dir
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	return dir
}

func TestCheckpointCapability(t *testing.T) {
	cap := Checkpoint()
	if cap.Name() != "checkpoint" || cap.Tier() != capabilities.TierW {
		t.Fatalf("name/tier: %q %q", cap.Name(), cap.Tier())
	}
	dir := tempGitRepo(t)
	exec.Command("git", "-C", dir, "checkout", "-b", "feature").Run()
	os.WriteFile(filepath.Join(dir, "f.txt"), []byte("x"), 0o644)
	args, _ := json.Marshal(map[string]any{"subject": "feat: f", "body": "did f", "trunk": "main", "cwd": dir})
	res, err := cap.Execute(context.Background(), &capabilities.Call{Args: args, WorkDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Text, "checkpoint committed") {
		t.Fatalf("unexpected result: %q", res.Text)
	}
}
```

- [ ] **Step 2: Run; fail. Step 3: Implement the remaining four wrappers** (`gitflow_worktree.go`, `gitflow_recover.go`, `gitflow_history.go`, `gitflow_bisect.go`) following the same shape. Each: parse args, resolve dir (`cwd`→`call.WorkDir`), open repo, resolve config where trunk is needed, call the engine function, return a text result. Concrete arg shapes:
  - `git_worktree` (TierW): `{path, branch, trunk?, cwd?}` → `r.CreateWorktree`.
  - `git_recover` (TierX): `{mode: "abort"|"undo", label?: "land"|"history", branch?, cwd?}` → `AbortInProgress` (abort) or `ResetToSafety` (undo).
  - `git_squash` (TierW): `{subject, body?, trunk?, cwd?}` → `SquashToOne`.
  - `git_bisect` (TierR-ish but use TierW since it checks out commits): `{good, bad?, test_command?, cwd?}` (bad defaults "HEAD"; test_command resolves from config) → `BisectRun`.

- [ ] **Step 4: Register in `builtins.go`** (`reg.MustRegister(...)` for all five) and **bump `TestRegister_Count`** in `builtins_test.go` by 5 (and `git_land` adds 1 more in Task 14 — so this task's bump is +5; confirm the exact starting number by reading the test).

- [ ] **Step 5: Run full builtins tests; build; commit**

```bash
git -C <worktree> commit -am "feat(capabilities): git_worktree/checkpoint/git_recover/git_squash/git_bisect wrappers"
```

### Task 14: `git_land` capability (orchestrates the review seam)

**Files:**
- Create: `source/server/internal/capabilities/builtins/gitflow_land.go`
- Modify: `source/server/internal/capabilities/builtins/builtins.go` (+ count)
- Test: `source/server/internal/capabilities/builtins/gitflow_land_test.go`

**Interfaces:**
- Consumes: `gitflow.{Land,LandContinue,RunTests,Finalize,Divergence,ResolutionSignalsFor}`, `Services.Dispatch`.
- Produces: `GitLand() capabilities.Capability`. Args: `{feature?, trunk?, strategy?: "rebase"|"merge", continue?: bool, cwd?}`. `feature` defaults to the current branch.

**Behavior (the orchestrator):**
1. Parse args; open repo; resolve config (trunk, test command, sensitive/floor/regen).
2. If `continue` is false: report `Divergence` in the result; run `Land(feature, trunk, strategy)`. If conflicts → return a result listing the conflicted files (flagging generated ones via `Regen` globs with the regen command) and instruct "resolve on the feature branch, then call git_land with continue=true." Stop.
3. If `continue` is true: `LandContinue`. If still conflicts → return them again. Else proceed.
4. Once reconciled: `RunTests(cfg.TestCommand)` (if empty, note "no test gate"). Red → stop.
5. **Review gate:** compute `ResolutionSignalsFor`. If a `SensitivePaths` hit OR `HandEdited > cfg.ReviewFloor` → return "human review required before landing" (do NOT finalize). Else, if `call.Svc.Dispatch != nil`, call it with `dispatch.Spec{Mode: OneShot, Role: RoleMain, Source: "gitflow:land-review", Prompt: <review prompt embedding the signals + capped diff>}` and include the verdict in the result; if the verdict text indicates risk (contains "REFUTED"/"risky"), pause for human review rather than finalize. (Structured verdict is a follow-on; until then, surface the verdict and require an explicit `continue` re-call with an override flag to finalize — keep v1 conservative: on any review concern, stop and report.)
6. If clear: `Finalize(feature, trunk)`; return "landed to <trunk>; ready to push: `git push origin <trunk>`" — never push.

- [ ] **Step 1: Write the failing test** — clean-rebase land with `test_command:"true"` and no conflicts (so no review needed) finalizes and reports ready-to-push; assert `git_land` name/tier (TierX), and that after Execute, trunk contains the feature commit. Use a temp repo; inject `Svc` with a stub `Dispatch` that should NOT be called on a clean land.

```go
func TestGitLandCleanFinalizes(t *testing.T) {
	cap := GitLand()
	if cap.Name() != "git_land" || cap.Tier() != capabilities.TierX {
		t.Fatalf("name/tier: %q %q", cap.Name(), cap.Tier())
	}
	dir := tempGitRepo(t)
	sh := func(args ...string) { c := exec.Command("git", append([]string{"-C", dir}, args...)...); if out, err := c.CombinedOutput(); err != nil { t.Fatalf("%v: %s", args, out) } }
	sh("checkout", "-b", "feature")
	os.WriteFile(filepath.Join(dir, "f.txt"), []byte("f"), 0o644); sh("add", "-A"); sh("commit", "-m", "feat: f")
	sh("checkout", "main")
	os.WriteFile(filepath.Join(dir, "m.txt"), []byte("m"), 0o644); sh("add", "-A"); sh("commit", "-m", "chore: m")
	// .cercano/gitflow.yaml with trunk+test_command
	os.MkdirAll(filepath.Join(dir, ".cercano"), 0o755)
	os.WriteFile(filepath.Join(dir, ".cercano", "gitflow.yaml"), []byte("trunk: main\ntest_command: \"true\"\n"), 0o644)

	dispatchCalled := false
	svc := capabilities.Services{Dispatch: func(ctx context.Context, spec dispatch.Spec) (dispatch.Result, error) { dispatchCalled = true; return dispatch.Result{}, nil }}
	args, _ := json.Marshal(map[string]any{"feature": "feature", "cwd": dir})
	res, err := cap.Execute(context.Background(), &capabilities.Call{Args: args, WorkDir: dir, Svc: svc})
	if err != nil {
		t.Fatal(err)
	}
	if dispatchCalled {
		t.Fatal("clean land must not invoke the review dispatch")
	}
	if !strings.Contains(res.Text, "ready to push") {
		t.Fatalf("expected ready-to-push, got %q", res.Text)
	}
	// main now contains feature.
	out, _ := exec.Command("git", "-C", dir, "merge-base", "--is-ancestor", "feature", "main").CombinedOutput()
	_ = out
}
```
(Imports: add `"cercano/source/server/internal/dispatch"`.)

- [ ] **Step 2: Run; fail. Step 3: Implement `gitflow_land.go`** per the behavior above (the orchestrator; `gitflow` does the git, this wires the test gate + review seam + ff). Register in `builtins.go`; bump count by 1.

- [ ] **Step 4: Run; pass. Step 5: Build + full suite; commit**

```bash
git -C <worktree> commit -am "feat(capabilities): git_land (orchestrates reconcile, test gate, review seam, ff)"
```

---

## Phase 6 — Steering + docs

### Task 15: Checkpoint steering nudge

**Files:**
- Modify: `source/server/internal/protocols/steering.go` (the 0b `plainEnglishRules` block, or add a workflow rule line)
- Test: `source/server/internal/protocols/steering_test.go`

Add one always-on rule to the steering block: after completing a solved unit of work, call `checkpoint` to commit it (never push). Keep it one line; assert it appears in the assembled block.

- [ ] **Steps 1–5 (TDD):** test the steering block contains a "checkpoint" reminder; add the line; build; commit.

```bash
git -C <worktree> commit -am "feat(protocols): steering nudge to checkpoint completed work"
```

---

## Deferred / follow-ons (not this plan)

- **Release promotion** (integration trunk → release branch) — separate workflow.
- **`review` structured verdict** (`{risky, reasoning}`) — lets `git_land` gate automatically instead of conservatively stopping on any review concern. Tracked with the dispatch engine.
- **Watchdog (0b Part C)** — automates when the risk review and checkpoint nudges fire.
- **Autosquash / freeform interactive rebase / cherry-pick selection / blame archaeology** — left to low-level git + the model.
- **CLI slash-command wrappers** for the workflows.

---

## Self-Review

- **Spec coverage:** deterministic engine + capabilities (Tasks 1–14); config resolution incl. no-hardcoded-trunk (Task 2); safety refs (Task 3); worktree (Task 4); checkpoint with message guards + never-trunk/never-push (Task 5); land — divergence report, rebase-default/merge-override, conflict pause + continue, test gate, resolution signals, review seam + deterministic floor, ff-only, ready-to-push (Tasks 6–9, 14); recover (Task 10); bisect (Task 11); history ops (Task 12); steering nudge (Task 15). `review`-structured-verdict + watchdog + release promotion explicitly deferred.
- **Single-seam honored:** `internal/gitflow` imports no model/capability/dispatch packages (pure git + `sh -c` test/bisect commands). The only model call is `Services.Dispatch` inside the `git_land` *capability* (Task 14).
- **Push:** no `gitflow` function or capability runs `git push`; land stops at ff and reports the push command.
- **Placeholder scan:** the only deferred concreteness is `git_land`'s structured-verdict gate, which is implemented conservatively (stop on any review concern) with the automatic version called out as a follow-on — not a placeholder.
- **Type consistency:** `Repo`, `Config`, `LandState`, `Strategy`, `Divergence`, `ResolutionSignals`, and the constructors (`Checkpoint`, `GitWorktree`, `GitLand`, `GitRecover`, `GitSquash`, `GitBisect`) are used consistently across tasks. Capability `min` collision with Go 1.26 builtin is flagged in Task 13.
- **Dependency note:** Phase 1 is the base; Phases 2–4 build on it independently; Phase 5 wraps them; Task 14 depends on `Services.Dispatch` (dispatch engine, already built); Task 15 rides 0b (built).
