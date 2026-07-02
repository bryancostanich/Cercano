package protocols

import (
	"strings"
	"testing"
)

func TestCoreCatalogComplete(t *testing.T) {
	want := []string{"compute-before-simulate", "design-decisions", "systematic-debugging", "verification-strategy", "worktree-first"}
	for _, name := range want {
		p, ok := Get(name)
		if !ok {
			t.Fatalf("%s missing", name)
		}
		if p.Domain != DomainCore {
			t.Fatalf("%s should be core", name)
		}
		if len(strings.TrimSpace(p.Body)) < 200 {
			t.Fatalf("%s body looks too short to be the real protocol", name)
		}
		if !strings.HasSuffix(p.Trigger, ".") {
			t.Fatalf("%s trigger should be a full sentence", name)
		}
	}
}

func TestWorktreeFirstNamesTheGitWorktreeTool(t *testing.T) {
	p, ok := Get("worktree-first")
	if !ok {
		t.Fatal("worktree-first missing")
	}
	// The whole point of the protocol is to steer agents to the
	// git_worktree tool. If future edits accidentally rewrite it to
	// generic "use a worktree" language, agents won't know which tool
	// to reach for.
	if !strings.Contains(p.Body, "git_worktree") {
		t.Fatal("worktree-first body must name the git_worktree tool explicitly")
	}
	if !strings.Contains(p.Trigger, "git_worktree") {
		t.Fatal("worktree-first trigger must name the git_worktree tool so the steering block surfaces it")
	}
}

func TestWorktreeFirstRecommendsSiblingDirectoryConvention(t *testing.T) {
	p, _ := Get("worktree-first")
	// The convention on this box (and per the git_worktree design docs)
	// is a sibling directory to the repo root — never a subdirectory of
	// the tracked tree, which triggers git's submodule-pointer handling.
	// This test locks in that the body documents "sibling" and shows the
	// `../<repo>-<slug>` path shape as an example.
	if !strings.Contains(strings.ToLower(p.Body), "sibling") {
		t.Fatal("worktree-first body must call the sibling-directory convention out by name")
	}
	if !strings.Contains(p.Body, "../") {
		t.Fatal("worktree-first body must show the ../<repo>-<slug> sibling path shape so agents know what to pass")
	}
	// Regression guard: earlier versions of the protocol recommended
	// `.claude/worktrees/<slug>` inside the tracked tree, which caused
	// pointer-drift and rebase conflicts. Never again.
	if strings.Contains(p.Body, ".claude/worktrees/<") {
		t.Fatal("worktree-first body must not recommend the nested .claude/worktrees/<slug> path — sibling only")
	}
}

func TestWorktreeFirstDocumentsFastPathForTrivialChanges(t *testing.T) {
	p, _ := Get("worktree-first")
	// The full worktree ceremony is disproportionate for typo/label
	// fixes. The Fast Path section documents when it applies and how to
	// execute it while still routing through git_land (test gate stays).
	if !strings.Contains(p.Body, "Fast Path") {
		t.Fatal("worktree-first body must document a Fast Path section for trivial changes")
	}
	if !strings.Contains(p.Body, "git_land") {
		t.Fatal("Fast Path must still route through git_land so the test gate runs — never skip landing")
	}
}

func TestDesignDecisionsHasMergedSteps(t *testing.T) {
	p, _ := Get("design-decisions")
	if !strings.Contains(p.Body, "Symmetric quantification") && !strings.Contains(p.Body, "symmetric quantification") {
		t.Fatal("design-decisions missing the symmetric quantification rule")
	}
	if !strings.Contains(strings.ToLower(p.Body), "argue against your own recommendation") {
		t.Fatal("design-decisions missing the argue-against-yourself step")
	}
}
