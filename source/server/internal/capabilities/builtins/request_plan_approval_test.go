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

func TestRequestPlanApproval_Meta(t *testing.T) {
	c := RequestPlanApproval()
	if c.Name() != "request_plan_approval" {
		t.Errorf("Name() = %q", c.Name())
	}
	// X-tier is load-bearing: Permissive mode only prompts for X-tier tools, and
	// this capability changes session mode, so it must never auto-run.
	if c.Tier() != capabilities.TierX {
		t.Errorf("Tier() = %q, want TierX", c.Tier())
	}
	if !c.Surfaces().Has(capabilities.SurfaceAgent) {
		t.Error("missing SurfaceAgent")
	}
	if c.Surfaces().Has(capabilities.SurfaceMCP) {
		t.Error("request_plan_approval must NOT be exposed over MCP")
	}
	// The approval-gate wording must be truthful and conditional: non-git
	// working directories are still supported, but the description must not
	// unconditionally promise a commit.
	for _, want := range []string{
		"In a Git repository, approval commits",
		"a failed commit keeps the session in planning mode",
		"outside a Git repository approval simply leaves planning mode without committing",
	} {
		if !strings.Contains(c.Description(), want) {
			t.Errorf("Description() missing %q", want)
		}
	}
}

func TestRequestPlanApproval_Execute_LeavesPlanningProfile(t *testing.T) {
	dir := tempGitRepo(t)
	os.MkdirAll(filepath.Join(dir, "efforts", "migrate-config-loader"), 0o755)
	os.WriteFile(filepath.Join(dir, "efforts", "migrate-config-loader", "spec.md"), []byte("# Spec\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "efforts", "migrate-config-loader", "plan.md"), []byte("# Plan\n"), 0o644)

	var entered string
	svc := capabilities.Services{EnterProfile: func(convID, name string) error { entered = name; return nil }}
	args, _ := json.Marshal(map[string]any{
		"effort":    "efforts/migrate-config-loader",
		"summary":   "Three phases: loader, migration, cleanup.",
		"spec_path": "efforts/migrate-config-loader/spec.md",
		"plan_path": "efforts/migrate-config-loader/plan.md",
	})
	call := &capabilities.Call{Args: args, WorkDir: dir, Svc: svc}

	res, err := RequestPlanApproval().Execute(context.Background(), call)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if entered != "default" {
		t.Fatalf("EnterProfile called with %q, want default", entered)
	}
	for _, must := range []string{"Plan approved", "efforts/migrate-config-loader", "loader", "spec.md", "plan.md", "request_autonomous_execution", "Execute it autonomously with this run brief"} {
		if !strings.Contains(res.Text, must) {
			t.Fatalf("result %q missing %q", res.Text, must)
		}
	}
	if strings.Contains(res.Text, "suggest_autonomous") || strings.Contains(res.Text, "separate brief approval") {
		t.Fatalf("result should not instruct a second autonomous gate: %q", res.Text)
	}
}

func TestRequestPlanApproval_Execute_NilHookErrors(t *testing.T) {
	call := &capabilities.Call{Args: json.RawMessage(`{}`), Svc: capabilities.Services{}}
	if _, err := RequestPlanApproval().Execute(context.Background(), call); err == nil {
		t.Fatal("expected an error when EnterProfile is nil")
	}
}

// TestRequestPlanApproval_Execute_NoEffortFailsAndKeepsPlanning: an empty effort
// is no longer a bypass around the git checkpoint. Approval checkpoints the
// declared effort's spec.md/plan.md, so without an effort there is nothing safe
// to commit — the error keeps planning mode and points at plan_exit for
// abandoning.
func TestRequestPlanApproval_Execute_NoEffortFailsAndKeepsPlanning(t *testing.T) {
	svc, entered := spyProfiles()
	call := &capabilities.Call{Args: nil, Svc: svc}
	_, err := RequestPlanApproval().Execute(context.Background(), call)
	if err == nil {
		t.Fatal("empty effort must fail the approval")
	}
	if *entered != "" {
		t.Fatalf("failed approval must retain planning mode; EnterProfile = %q", *entered)
	}
	if !strings.Contains(err.Error(), "effort is required") || !strings.Contains(err.Error(), "plan_exit") {
		t.Fatalf("error must name the effort requirement and point at plan_exit: %v", err)
	}
}

// TestRequestPlanApproval_MissingWorkDirFails: without a WorkDir the project
// (and its repository) cannot be reliably resolved, so there is no honest
// checkpoint to perform; refuse the handoff instead of proceeding.
func TestRequestPlanApproval_MissingWorkDirFails(t *testing.T) {
	svc, entered := spyProfiles()
	args, _ := json.Marshal(map[string]any{"effort": "efforts/e1", "summary": "s"})
	_, err := RequestPlanApproval().Execute(context.Background(),
		&capabilities.Call{Args: args, Svc: svc})
	if err == nil {
		t.Fatal("missing WorkDir must fail")
	}
	if *entered != "" {
		t.Fatalf("missing WorkDir must retain planning mode; EnterProfile = %q", *entered)
	}
	for _, want := range []string{"no working directory", "cannot reliably resolve the project", "stays in planning mode"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q missing %q", err, want)
		}
	}
}

// TestRequestPlanApproval_IgnoredDocsKeepPlanning: review-probe regression —
// git status hides ignored files, so ignored planning docs used to look clean
// and approval sailed through as a fake no-op. Ignored docs must fail
// actionably (never force-added), keep planning mode, and leave the docs on
// disk for a retry after the ignore rule is fixed.
func TestRequestPlanApproval_IgnoredDocsKeepPlanning(t *testing.T) {
	dir := tempGitRepo(t)
	os.MkdirAll(filepath.Join(dir, "efforts", "e1"), 0o755)
	os.WriteFile(filepath.Join(dir, "efforts", "e1", "spec.md"), []byte("# Spec\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "efforts", "e1", "plan.md"), []byte("# Plan\n"), 0o644)
	os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("efforts/\n"), 0o644)
	gitOut(t, dir, "add", ".gitignore")
	gitOut(t, dir, "commit", "-m", "add ignore rule")

	headBefore := gitOut(t, dir, "rev-parse", "HEAD")

	svc, entered := spyProfiles()
	args, _ := json.Marshal(map[string]any{"effort": "efforts/e1", "summary": "s"})
	_, err := RequestPlanApproval().Execute(context.Background(),
		&capabilities.Call{Args: args, WorkDir: dir, Svc: svc})
	if err == nil {
		t.Fatal("ignored planning docs must fail the approval")
	}
	if *entered != "" {
		t.Fatalf("ignored docs must retain planning mode; EnterProfile = %q", *entered)
	}
	for _, want := range []string{".gitignore", "not force-added"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q missing %q", err, want)
		}
	}
	if after := gitOut(t, dir, "rev-parse", "HEAD"); after != headBefore {
		t.Fatalf("ignored docs must not produce a commit: %s -> %s", headBefore, after)
	}
	// The docs survive on disk for a retry.
	if _, err := os.Stat(filepath.Join(dir, "efforts", "e1", "plan.md")); err != nil {
		t.Fatalf("planning docs must survive the failed approval: %v", err)
	}
}

// TestRequestPlanApproval_RejectsSymlinkDocs: a symlinked planning doc (or a
// symlinked parent directory under the work dir) can point outside the
// repository, so it must be rejected via Lstat and keep planning mode — never
// silently resolved to its target.
func TestRequestPlanApproval_RejectsSymlinkDocs(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(t *testing.T, dir string)
	}{
		{
			name: "symlinked spec file",
			setup: func(t *testing.T, dir string) {
				outside := t.TempDir()
				os.WriteFile(filepath.Join(outside, "spec.md"), []byte("outside"), 0o644)
				os.MkdirAll(filepath.Join(dir, "efforts", "e1"), 0o755)
				os.WriteFile(filepath.Join(dir, "efforts", "e1", "plan.md"), []byte("# Plan\n"), 0o644)
				os.WriteFile(filepath.Join(dir, "efforts", "e1", "spec.md"), []byte("# Spec\n"), 0o644)
				os.Remove(filepath.Join(dir, "efforts", "e1", "spec.md"))
				if err := os.Symlink(filepath.Join(outside, "spec.md"), filepath.Join(dir, "efforts", "e1", "spec.md")); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "symlinked effort parent dir",
			setup: func(t *testing.T, dir string) {
				sibling := t.TempDir()
				os.MkdirAll(filepath.Join(sibling, "e1"), 0o755)
				os.WriteFile(filepath.Join(sibling, "e1", "spec.md"), []byte("outside spec"), 0o644)
				os.WriteFile(filepath.Join(sibling, "e1", "plan.md"), []byte("outside plan"), 0o644)
				os.MkdirAll(filepath.Join(dir, "efforts"), 0o755)
				if err := os.Symlink(filepath.Join(sibling, "e1"), filepath.Join(dir, "efforts", "e1")); err != nil {
					t.Fatal(err)
				}
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := tempGitRepo(t)
			tc.setup(t, dir)
			svc, entered := spyProfiles()
			args, _ := json.Marshal(map[string]any{"effort": "efforts/e1", "summary": "s"})
			_, err := RequestPlanApproval().Execute(context.Background(),
				&capabilities.Call{Args: args, WorkDir: dir, Svc: svc})
			if err == nil {
				t.Fatal("symlinked planning docs must fail the approval")
			}
			if !strings.Contains(err.Error(), "symlink") {
				t.Fatalf("error must name the symlink rejection; got %v", err)
			}
			if *entered != "" {
				t.Fatalf("symlinked docs must retain planning mode; EnterProfile = %q", *entered)
			}
		})
	}
}

// spyProfiles records profile switches; empty entered means planning mode was
// never exited.
func spyProfiles() (capabilities.Services, *string) {
	entered := new(string)
	return capabilities.Services{EnterProfile: func(convID, n string) error { *entered = n; return nil }}, entered
}

func gitOut(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// TestRequestPlanApproval_CommitsPlanningDocsBeforeExit pins the core fix: on
// approval the capability commits exactly the effort's spec.md and plan.md to
// git on the current branch BEFORE leaving the planning profile. An unrelated
// STAGED change stays staged (never included in the commit) and unrelated
// untracked files stay untracked.
func TestRequestPlanApproval_CommitsPlanningDocsBeforeExit(t *testing.T) {
	dir := tempGitRepo(t) // on main
	os.MkdirAll(filepath.Join(dir, "efforts", "e1"), 0o755)
	os.WriteFile(filepath.Join(dir, "efforts", "e1", "spec.md"), []byte("# Spec\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "efforts", "e1", "plan.md"), []byte("# Plan\n"), 0o644)
	// Unrelated staged change: must NOT be included in the commit.
	os.WriteFile(filepath.Join(dir, "other.txt"), []byte("mine"), 0o644)
	gitOut(t, dir, "add", "other.txt")
	// Unrelated untracked noise: must stay untracked.
	os.WriteFile(filepath.Join(dir, "scratch.txt"), []byte("noise"), 0o644)

	svc, entered := spyProfiles()
	args, _ := json.Marshal(map[string]any{
		"effort":  "efforts/e1",
		"summary": "one phase",
	})
	res, err := RequestPlanApproval().Execute(context.Background(),
		&capabilities.Call{Args: args, WorkDir: dir, Svc: svc})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if *entered != "default" {
		t.Fatalf("EnterProfile = %q, want default", *entered)
	}

	// The commit contains ONLY the two planning docs.
	files := gitOut(t, dir, "show", "--name-only", "--pretty=format:", "HEAD")
	if !strings.Contains(files, "efforts/e1/spec.md") || !strings.Contains(files, "efforts/e1/plan.md") {
		t.Fatalf("commit must contain both planning docs; got %q", files)
	}
	if strings.Contains(files, "other.txt") || strings.Contains(files, "scratch.txt") {
		t.Fatalf("commit must not sweep unrelated files; got %q", files)
	}
	// The unrelated staged change survives the approval, still staged.
	staged := gitOut(t, dir, "diff", "--cached", "--name-only")
	if !strings.Contains(staged, "other.txt") {
		t.Fatalf("unrelated staged change must be preserved and stay staged; got %q", staged)
	}
	// ...and the untracked noise survives untouched.
	if _, err := os.Stat(filepath.Join(dir, "scratch.txt")); err != nil {
		t.Fatalf("unrelated untracked file must be preserved: %v", err)
	}
	if !strings.Contains(res.Text, "Planning docs committed to git on branch main") {
		t.Fatalf("result should report the git checkpoint; got %q", res.Text)
	}
}

// TestRequestPlanApproval_SlugEffortDefaultsPaths: a bare effort slug resolves
// to efforts/<slug>/{spec,plan}.md; omitted spec_path/plan_path default under
// the declared effort.
func TestRequestPlanApproval_SlugEffortDefaultsPaths(t *testing.T) {
	dir := tempGitRepo(t)
	os.MkdirAll(filepath.Join(dir, "efforts", "e1"), 0o755)
	os.WriteFile(filepath.Join(dir, "efforts", "e1", "spec.md"), []byte("# Spec\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "efforts", "e1", "plan.md"), []byte("# Plan\n"), 0o644)

	svc, entered := spyProfiles()
	args, _ := json.Marshal(map[string]any{"effort": "e1", "summary": "s"})
	if _, err := RequestPlanApproval().Execute(context.Background(),
		&capabilities.Call{Args: args, WorkDir: dir, Svc: svc}); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if *entered != "default" {
		t.Fatalf("EnterProfile = %q, want default", *entered)
	}
	files := gitOut(t, dir, "show", "--name-only", "--pretty=format:", "HEAD")
	if !strings.Contains(files, "efforts/e1/spec.md") || !strings.Contains(files, "efforts/e1/plan.md") {
		t.Fatalf("slug effort must default to efforts/e1/{spec,plan}.md; got %q", files)
	}
}

// TestRequestPlanApproval_CleanRetryIsIdempotentNoOp: when the docs are already
// committed and clean, a second approval succeeds, leaves planning mode, and
// does not create another commit.
func TestRequestPlanApproval_CleanRetryIsIdempotentNoOp(t *testing.T) {
	dir := tempGitRepo(t)
	os.MkdirAll(filepath.Join(dir, "efforts", "e1"), 0o755)
	os.WriteFile(filepath.Join(dir, "efforts", "e1", "spec.md"), []byte("# Spec\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "efforts", "e1", "plan.md"), []byte("# Plan\n"), 0o644)
	args, _ := json.Marshal(map[string]any{"effort": "efforts/e1", "summary": "s"})
	svc, _ := spyProfiles()
	if _, err := RequestPlanApproval().Execute(context.Background(),
		&capabilities.Call{Args: args, WorkDir: dir, Svc: svc}); err != nil {
		t.Fatal(err)
	}
	headBefore := gitOut(t, dir, "rev-parse", "HEAD")

	svc2, entered2 := spyProfiles()
	res, err := RequestPlanApproval().Execute(context.Background(),
		&capabilities.Call{Args: args, WorkDir: dir, Svc: svc2})
	if err != nil {
		t.Fatalf("clean retry must not error: %v", err)
	}
	if *entered2 != "default" {
		t.Fatalf("clean retry must still leave planning mode; got %q", *entered2)
	}
	if after := gitOut(t, dir, "rev-parse", "HEAD"); after != headBefore {
		t.Fatalf("clean retry must not move HEAD: %s -> %s", headBefore, after)
	}
	if !strings.Contains(res.Text, "already committed to git and clean") {
		t.Fatalf("clean retry should say no-op; got %q", res.Text)
	}
}

// TestRequestPlanApproval_FailedCommitKeepsPlanningProfile: when the commit
// fails, the session must NOT leave planning mode and the error must be
// actionable.
func TestRequestPlanApproval_FailedCommitKeepsPlanningProfile(t *testing.T) {
	dir := tempGitRepo(t)
	os.MkdirAll(filepath.Join(dir, "efforts", "e1"), 0o755)
	os.WriteFile(filepath.Join(dir, "efforts", "e1", "spec.md"), []byte("# Spec\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "efforts", "e1", "plan.md"), []byte("# Plan\n"), 0o644)
	hook := filepath.Join(dir, ".git", "hooks", "pre-commit")
	if err := os.WriteFile(hook, []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	svc, entered := spyProfiles()
	args, _ := json.Marshal(map[string]any{"effort": "efforts/e1", "summary": "s"})
	_, err := RequestPlanApproval().Execute(context.Background(),
		&capabilities.Call{Args: args, WorkDir: dir, Svc: svc})
	if err == nil {
		t.Fatal("failed commit must surface as an error, not a silent handoff")
	}
	if *entered != "" {
		t.Fatalf("failed commit must keep planning mode; EnterProfile = %q", *entered)
	}
	if !strings.Contains(err.Error(), "request_plan_approval") || !strings.Contains(err.Error(), "stays in planning mode") {
		t.Fatalf("error must be actionable and name the capability: %v", err)
	}
	// The docs survive on disk so a retry can succeed after the fix.
	if _, err := os.Stat(filepath.Join(dir, "efforts", "e1", "plan.md")); err != nil {
		t.Fatalf("planning docs must survive a failed commit: %v", err)
	}
}

// TestRequestPlanApproval_RejectsUnsafeAndOutsideEffortPaths: explicit doc paths
// must be repo-relative regular files inside the declared effort directory;
// anything else is rejected and the session stays in planning mode.
func TestRequestPlanApproval_RejectsUnsafeAndOutsideEffortPaths(t *testing.T) {
	dir := tempGitRepo(t)
	os.MkdirAll(filepath.Join(dir, "efforts", "e1"), 0o755)
	os.WriteFile(filepath.Join(dir, "efforts", "e1", "spec.md"), []byte("# Spec\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "efforts", "e1", "plan.md"), []byte("# Plan\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "outside.md"), []byte("outside effort\n"), 0o644)

	for _, tc := range []struct {
		name   string
		spec   string
		plan   string
		errSub string
	}{
		{"absolute path", "/etc/passwd", "efforts/e1/plan.md", "must be a repo-relative path"},
		{"escaping path", "../outside.md", "efforts/e1/plan.md", "unsafe planning doc path"},
		{"outside effort dir", "outside.md", "efforts/e1/plan.md", "outside the declared effort directory"},
		{"other effort dir", "efforts/e2/spec.md", "efforts/e1/plan.md", "outside the declared effort directory"},
		{"missing explicit file", "efforts/e1/spec.md", "efforts/e1/missing-plan.md", "not found"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, entered := spyProfiles()
			args, _ := json.Marshal(map[string]any{
				"effort": "efforts/e1", "summary": "s", "spec_path": tc.spec, "plan_path": tc.plan})
			_, err := RequestPlanApproval().Execute(context.Background(),
				&capabilities.Call{Args: args, WorkDir: dir, Svc: svc})
			if err == nil {
				t.Fatal("expected an error rejecting the unsafe path")
			}
			if !strings.Contains(err.Error(), tc.errSub) {
				t.Fatalf("error %q missing %q", err, tc.errSub)
			}
			if *entered != "" {
				t.Fatalf("rejection must keep planning mode; EnterProfile = %q", *entered)
			}
		})
	}
}

// TestRequestPlanApproval_MissingPlanningDocsKeepsPlanning: approving with no
// spec.md/plan.md under the effort is an error that keeps planning mode — the
// protocol requires both artifacts before the handoff.
func TestRequestPlanApproval_MissingPlanningDocsKeepsPlanning(t *testing.T) {
	dir := tempGitRepo(t)
	os.MkdirAll(filepath.Join(dir, "efforts", "e1"), 0o755)

	svc, entered := spyProfiles()
	args, _ := json.Marshal(map[string]any{"effort": "efforts/e1", "summary": "s"})
	_, err := RequestPlanApproval().Execute(context.Background(),
		&capabilities.Call{Args: args, WorkDir: dir, Svc: svc})
	if err == nil {
		t.Fatal("expected an error when the planning docs are missing")
	}
	if *entered != "" {
		t.Fatalf("missing docs must keep planning mode; EnterProfile = %q", *entered)
	}
}

// TestRequestPlanApproval_NonGitWorkDirReportsLimitation: a non-git working
// directory preserves the historical handoff, but the result must say plainly
// that no git checkpoint happened rather than claiming one.
func TestRequestPlanApproval_NonGitWorkDirReportsLimitation(t *testing.T) {
	dir := t.TempDir() // no git
	os.MkdirAll(filepath.Join(dir, "efforts", "e1"), 0o755)
	os.WriteFile(filepath.Join(dir, "efforts", "e1", "spec.md"), []byte("# Spec\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "efforts", "e1", "plan.md"), []byte("# Plan\n"), 0o644)

	svc, entered := spyProfiles()
	args, _ := json.Marshal(map[string]any{"effort": "efforts/e1", "summary": "s"})
	res, err := RequestPlanApproval().Execute(context.Background(),
		&capabilities.Call{Args: args, WorkDir: dir, Svc: svc})
	if err != nil {
		t.Fatalf("non-git handoff must still succeed: %v", err)
	}
	if *entered != "default" {
		t.Fatalf("EnterProfile = %q, want default", *entered)
	}
	if !strings.Contains(res.Text, "were NOT committed to git") {
		t.Fatalf("non-git result must report the limitation; got %q", res.Text)
	}
}

// TestRequestPlanApproval_WorktreeLandingRegression is the end-to-end regression
// for the original bug: plan approval on main -> new worktree changes the plan
// -> commit -> fast-forward main. Before the fix the docs sat untracked on main
// and the merge failed; with the approval commit it fast-forwards.
func TestRequestPlanApproval_WorktreeLandingRegression(t *testing.T) {
	dir := tempGitRepo(t) // on main
	os.MkdirAll(filepath.Join(dir, "efforts", "e1"), 0o755)
	os.WriteFile(filepath.Join(dir, "efforts", "e1", "spec.md"), []byte("# Spec\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "efforts", "e1", "plan.md"), []byte("# Plan\n"), 0o644)

	svc, _ := spyProfiles()
	args, _ := json.Marshal(map[string]any{"effort": "efforts/e1", "summary": "s"})
	if _, err := RequestPlanApproval().Execute(context.Background(),
		&capabilities.Call{Args: args, WorkDir: dir, Svc: svc}); err != nil {
		t.Fatalf("approval: %v", err)
	}

	// New worktree off main; change plan.md there and commit.
	wt := filepath.Join(t.TempDir(), "wt")
	if out, err := exec.Command("git", "-C", dir, "worktree", "add", "-b", "feat", wt).CombinedOutput(); err != nil {
		t.Fatalf("worktree add: %v\n%s", err, out)
	}
	os.WriteFile(filepath.Join(wt, "efforts", "e1", "plan.md"), []byte("# Plan changed\n"), 0o644)
	gitOut(t, wt, "add", "efforts/e1/plan.md")
	gitOut(t, wt, "commit", "-m", "feat: change plan")

	// Landing the worktree branch back onto main must fast-forward.
	if out, err := exec.Command("git", "-C", dir, "merge", "--ff-only", "feat").CombinedOutput(); err != nil {
		t.Fatalf("fast-forward main must succeed after approval commit: %v\n%s", err, out)
	}
	got, _ := os.ReadFile(filepath.Join(dir, "efforts", "e1", "plan.md"))
	if string(got) != "# Plan changed\n" {
		t.Fatalf("main should carry the worktree's plan.md; got %q", got)
	}
}

// TestRequestPlanApproval_UncommittedDocsBlockLanding is the contrast case that
// documents WHY the approval commit exists: with the docs still untracked on
// main (the session authored them but never committed), the identical landing
// is refused by git.
func TestRequestPlanApproval_UncommittedDocsBlockLanding(t *testing.T) {
	dir := tempGitRepo(t) // on main, only the root commit

	// A worktree branch lands the docs (plus a plan change) as real commits.
	wt := filepath.Join(t.TempDir(), "wt")
	if out, err := exec.Command("git", "-C", dir, "worktree", "add", "-b", "feat", wt).CombinedOutput(); err != nil {
		t.Fatalf("worktree add: %v\n%s", err, out)
	}
	os.MkdirAll(filepath.Join(wt, "efforts", "e1"), 0o755)
	os.WriteFile(filepath.Join(wt, "efforts", "e1", "spec.md"), []byte("# Spec\n"), 0o644)
	os.WriteFile(filepath.Join(wt, "efforts", "e1", "plan.md"), []byte("# Plan\n"), 0o644)
	gitOut(t, wt, "add", "efforts/e1")
	gitOut(t, wt, "commit", "-m", "feat: docs")
	os.WriteFile(filepath.Join(wt, "efforts", "e1", "plan.md"), []byte("# Plan changed\n"), 0o644)
	gitOut(t, wt, "add", "efforts/e1/plan.md")
	gitOut(t, wt, "commit", "-m", "feat: change plan")

	// Main has the same docs on disk but UNTRACKED — the state a session without
	// the approval commit would be in.
	os.MkdirAll(filepath.Join(dir, "efforts", "e1"), 0o755)
	os.WriteFile(filepath.Join(dir, "efforts", "e1", "spec.md"), []byte("# Spec\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "efforts", "e1", "plan.md"), []byte("# Plan\n"), 0o644)

	if err := exec.Command("git", "-C", dir, "merge", "--ff-only", "feat").Run(); err == nil {
		t.Fatal("contrast case: merge should FAIL while the planning docs sit untracked on main")
	}
	// The untracked docs must not have been clobbered by the refused merge.
	got, _ := os.ReadFile(filepath.Join(dir, "efforts", "e1", "plan.md"))
	if string(got) != "# Plan\n" {
		t.Fatalf("refused merge must not clobber the untracked docs; got %q", got)
	}
}
