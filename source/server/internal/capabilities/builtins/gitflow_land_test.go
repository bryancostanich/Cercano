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
	"cercano/source/server/internal/dispatch"
)

// TestGitLand_Meta checks name, tier, and surfaces.
func TestGitLand_Meta(t *testing.T) {
	cap := GitLand()
	if cap.Name() != "git_land" {
		t.Fatalf("name: %q", cap.Name())
	}
	if cap.Tier() != capabilities.TierX {
		t.Fatalf("tier: %q", cap.Tier())
	}
	if cap.Surfaces() != (capabilities.SurfaceAgent | capabilities.SurfaceMCP) {
		t.Fatalf("surfaces: %v", cap.Surfaces())
	}
}

// TestGitLandCleanFinalizes is the canonical plan test (verbatim from plan.md):
// a clean rebase land with test_command:"true", no conflicts, asserts name/tier (git_land/TierX),
// that the injected Svc.Dispatch stub is NOT called (clean land skips review),
// and the result says "ready to push", and main contains feature afterward.
func TestGitLandCleanFinalizes(t *testing.T) {
	cap := GitLand()
	if cap.Name() != "git_land" || cap.Tier() != capabilities.TierX {
		t.Fatalf("name/tier: %q %q", cap.Name(), cap.Tier())
	}

	dir := tempGitRepo(t)
	sh := func(args ...string) {
		c := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("%v: %s", args, out)
		}
	}

	// Create feature branch with a commit.
	sh("checkout", "-b", "feature")
	if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte("f"), 0o644); err != nil {
		t.Fatal(err)
	}
	sh("add", "-A")
	sh("commit", "-m", "feat: f")

	// Advance main independently — commit .cercano/gitflow.yaml there too.
	sh("checkout", "main")
	if err := os.MkdirAll(filepath.Join(dir, ".cercano"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(dir, ".cercano", "gitflow.yaml"),
		[]byte("trunk: main\ntest_command: \"true\"\n"),
		0o644,
	); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "m.txt"), []byte("m"), 0o644); err != nil {
		t.Fatal(err)
	}
	sh("add", "-A")
	sh("commit", "-m", "chore: m")

	dispatchCalled := false
	svc := capabilities.Services{
		Dispatch: func(_ context.Context, spec dispatch.Spec) (dispatch.Result, error) {
			dispatchCalled = true
			return dispatch.Result{}, nil
		},
	}

	args, _ := json.Marshal(map[string]any{"feature": "feature", "cwd": dir})
	res, err := cap.Execute(context.Background(), &capabilities.Call{
		Args:    args,
		WorkDir: dir,
		Svc:     svc,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Clean land must NOT invoke the review dispatch.
	if dispatchCalled {
		t.Fatal("clean land must not invoke the review dispatch")
	}

	// Result must mention ready-to-push.
	if !strings.Contains(res.Text, "ready to push") {
		t.Fatalf("expected ready-to-push, got %q", res.Text)
	}

	// main must now contain feature (merge-base --is-ancestor exits 0).
	cmd := exec.Command("git", "-C", dir, "merge-base", "--is-ancestor", "feature", "main")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("feature not ancestor of main after land: %v\n%s", err, out)
	}
}

// landConflictRepo builds a repo where landing `feature` onto `main` conflicts
// in shared.txt, writes the given gitflow.yaml on main, then runs git_land
// (continue=false) to pause mid-rebase. shared.txt is left with conflict markers;
// callers resolve it and re-invoke with continue=true. Returns the repo dir.
func landConflictRepo(t *testing.T, gitflowYAML string) string {
	t.Helper()
	dir := tempGitRepo(t)
	sh := func(args ...string) {
		c := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("%v: %s", args, out)
		}
	}
	write := func(name, content string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("shared.txt", "base")
	sh("add", "-A")
	sh("commit", "-m", "chore: base")
	if err := os.MkdirAll(filepath.Join(dir, ".cercano"), 0o755); err != nil {
		t.Fatal(err)
	}
	write(".cercano/gitflow.yaml", gitflowYAML)
	sh("add", "-A")
	sh("commit", "-m", "chore: cfg")
	sh("checkout", "-b", "feature")
	write("shared.txt", "feature-change")
	sh("add", "-A")
	sh("commit", "-m", "feat: f")
	sh("checkout", "main")
	write("shared.txt", "main-change")
	sh("add", "-A")
	sh("commit", "-m", "chore: m")

	args, _ := json.Marshal(map[string]any{"feature": "feature", "cwd": dir})
	res, err := GitLand().Execute(context.Background(), &capabilities.Call{Args: args, WorkDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Text, "Conflicted files") {
		t.Fatalf("expected a conflict pause, got %q", res.Text)
	}
	return dir
}

func mainContainsFeature(dir string) bool {
	return exec.Command("git", "-C", dir, "merge-base", "--is-ancestor", "feature", "main").Run() == nil
}

// TestGitLandFloorRequiresHumanReview: a conflict in a sensitive path must trip
// the deterministic floor — human review required, NOT finalized, and the model
// review dispatch is NOT even consulted (floor is checked first).
func TestGitLandFloorRequiresHumanReview(t *testing.T) {
	dir := landConflictRepo(t, "trunk: main\ntest_command: \"true\"\nsensitive_paths:\n  - \"shared.txt\"\n")
	if err := os.WriteFile(filepath.Join(dir, "shared.txt"), []byte("resolved"), 0o644); err != nil {
		t.Fatal(err)
	}
	dispatchCalled := false
	svc := capabilities.Services{
		Dispatch: func(_ context.Context, _ dispatch.Spec) (dispatch.Result, error) {
			dispatchCalled = true
			return dispatch.Result{}, nil
		},
	}
	args, _ := json.Marshal(map[string]any{"feature": "feature", "continue": true, "cwd": dir})
	res, err := GitLand().Execute(context.Background(), &capabilities.Call{Args: args, WorkDir: dir, Svc: svc})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Text, "human review required") {
		t.Fatalf("expected floor to require human review, got %q", res.Text)
	}
	if dispatchCalled {
		t.Fatal("deterministic floor must trip BEFORE the model review dispatch")
	}
	if mainContainsFeature(dir) {
		t.Fatal("must NOT finalize to trunk when the floor requires human review")
	}
}

// TestGitLandRiskyVerdictStops: a RISKY review verdict must stop the land —
// not finalized to trunk.
func TestGitLandRiskyVerdictStops(t *testing.T) {
	dir := landConflictRepo(t, "trunk: main\ntest_command: \"true\"\n")
	if err := os.WriteFile(filepath.Join(dir, "shared.txt"), []byte("resolved"), 0o644); err != nil {
		t.Fatal(err)
	}
	svc := capabilities.Services{
		Dispatch: func(_ context.Context, _ dispatch.Spec) (dispatch.Result, error) {
			return dispatch.Result{Text: "VERDICT: RISKY\nREASONING: dropped a guard clause"}, nil
		},
	}
	args, _ := json.Marshal(map[string]any{"feature": "feature", "continue": true, "cwd": dir})
	res, err := GitLand().Execute(context.Background(), &capabilities.Call{Args: args, WorkDir: dir, Svc: svc})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Text, "review flagged risk") {
		t.Fatalf("expected risk stop, got %q", res.Text)
	}
	if mainContainsFeature(dir) {
		t.Fatal("must NOT finalize to trunk when review flags risk")
	}
}
