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

func TestCheckpointCapabilityAllowTrunkWithExplicitPaths(t *testing.T) {
	cap := Checkpoint()
	dir := tempGitRepo(t)
	os.WriteFile(filepath.Join(dir, "wanted.txt"), []byte("x"), 0o644)
	os.WriteFile(filepath.Join(dir, "local.txt"), []byte("leave me"), 0o644)
	args, _ := json.Marshal(map[string]any{
		"subject":     "fix: wanted",
		"trunk":       "main",
		"cwd":         dir,
		"paths":       []string{"wanted.txt"},
		"allow_trunk": true,
	})
	res, err := cap.Execute(context.Background(), &capabilities.Call{Args: args, WorkDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Text, "checkpoint committed") {
		t.Fatalf("unexpected result: %q", res.Text)
	}
	out, err := exec.Command("git", "-C", dir, "show", "--name-only", "--pretty=format:", "HEAD").CombinedOutput()
	if err != nil {
		t.Fatalf("show commit: %v\n%s", err, out)
	}
	if strings.Contains(string(out), "local.txt") || !strings.Contains(string(out), "wanted.txt") {
		t.Fatalf("explicit checkpoint committed wrong files: %q", out)
	}
}

// TestGitWorktreeCapability_Meta checks name/tier/surfaces.
func TestGitWorktreeCapability_Meta(t *testing.T) {
	cap := GitWorktree()
	if cap.Name() != "git_worktree" {
		t.Fatalf("name: %q", cap.Name())
	}
	if cap.Tier() != capabilities.TierW {
		t.Fatalf("tier: %q", cap.Tier())
	}
	if cap.Surfaces() != (capabilities.SurfaceAgent | capabilities.SurfaceMCP) {
		t.Fatalf("surfaces: %v", cap.Surfaces())
	}
}

// TestGitWorktreeCapability_Execute verifies a worktree is created in a temp repo.
func TestGitWorktreeCapability_Execute(t *testing.T) {
	dir := tempGitRepo(t)
	wtPath := filepath.Join(t.TempDir(), "feat-wt")
	args, _ := json.Marshal(map[string]any{"path": wtPath, "branch": "feat-branch", "trunk": "main", "cwd": dir})
	cap := GitWorktree()
	res, err := cap.Execute(context.Background(), &capabilities.Call{Args: args, WorkDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Text, "created worktree") {
		t.Fatalf("unexpected result: %q", res.Text)
	}
	// Worktree dir should exist.
	if _, err := os.Stat(wtPath); err != nil {
		t.Fatalf("worktree path not created: %v", err)
	}
}

// TestGitRecoverCapability_Meta checks name/tier/surfaces.
func TestGitRecoverCapability_Meta(t *testing.T) {
	cap := GitRecover()
	if cap.Name() != "git_recover" {
		t.Fatalf("name: %q", cap.Name())
	}
	if cap.Tier() != capabilities.TierX {
		t.Fatalf("tier: %q", cap.Tier())
	}
	if cap.Surfaces() != (capabilities.SurfaceAgent | capabilities.SurfaceMCP) {
		t.Fatalf("surfaces: %v", cap.Surfaces())
	}
}

// TestGitSquashCapability_Meta checks name/tier/surfaces.
func TestGitSquashCapability_Meta(t *testing.T) {
	cap := GitSquash()
	if cap.Name() != "git_squash" {
		t.Fatalf("name: %q", cap.Name())
	}
	if cap.Tier() != capabilities.TierW {
		t.Fatalf("tier: %q", cap.Tier())
	}
	if cap.Surfaces() != (capabilities.SurfaceAgent | capabilities.SurfaceMCP) {
		t.Fatalf("surfaces: %v", cap.Surfaces())
	}
}

// TestGitBisectCapability_Meta checks name/tier/surfaces.
func TestGitBisectCapability_Meta(t *testing.T) {
	cap := GitBisect()
	if cap.Name() != "git_bisect" {
		t.Fatalf("name: %q", cap.Name())
	}
	if cap.Tier() != capabilities.TierW {
		t.Fatalf("tier: %q", cap.Tier())
	}
	if cap.Surfaces() != (capabilities.SurfaceAgent | capabilities.SurfaceMCP) {
		t.Fatalf("surfaces: %v", cap.Surfaces())
	}
}

// TestCheckpoint_WorkDirFallback confirms call.WorkDir reaches gitflow caps when no explicit cwd arg is given.
// This is a no-code-change confirming test that locks the dir=call.WorkDir fallback against regression.
func TestCheckpoint_WorkDirFallback(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	dir := tempGitRepo(t)
	exec.Command("git", "-C", dir, "checkout", "-b", "feature").Run()
	os.WriteFile(filepath.Join(dir, "work.txt"), []byte("work"), 0o644)
	// No "cwd" in args — WorkDir must supply the repo location.
	args, _ := json.Marshal(map[string]any{"subject": "test: workdir fallback", "trunk": "main"})
	res, err := Checkpoint().Execute(context.Background(), &capabilities.Call{Args: args, WorkDir: dir, Emit: func(string) {}})
	if err != nil {
		t.Fatalf("checkpoint via WorkDir fallback: %v", err)
	}
	if !strings.Contains(res.Text, "checkpoint committed") {
		t.Fatalf("unexpected result: %q", res.Text)
	}
}
