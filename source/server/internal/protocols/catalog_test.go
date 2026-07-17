package protocols

import (
	"strings"
	"testing"
)

func TestCoreCatalogComplete(t *testing.T) {
	want := []string{"compute-before-simulate", "delegate-git-plumbing", "design-decisions", "systematic-debugging", "verification-strategy", "worktree-first"}
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
	// checkpoint only the intended files.
	if !strings.Contains(p.Body, "Fast Path") {
		t.Fatal("worktree-first body must document a Fast Path section for trivial changes")
	}
	if !strings.Contains(p.Body, "explicit `paths`") || !strings.Contains(p.Body, "allow_trunk") {
		t.Fatal("Fast Path must explain explicit paths and allow_trunk for safe current-branch checkpoints")
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

func TestDesignDecisionsRequiresTerminalSafeGridRollupTable(t *testing.T) {
	p, _ := Get("design-decisions")
	for _, want := range []string{
		"terminal-safe Markdown **grid table**",
		"decision axes as rows",
		"short option labels as columns",
		"visible separators between body rows",
		"Do not use a plain pipe table",
		"one undivided block",
		"Do not use a wide row-per-option table",
		"Do not put long option names in the table header",
		"do not put sentences in cells",
		"Before the table, define a short legend",
		"+---------------+----------------+----------------+----------------+",
		"| Axis          | A              | B              | C              |",
		"+===============+================+================+================+",
		"| Cost          | Low            | Low            | Med            |",
		"| Risk          | Low            | Med            | Med            |",
		"keep each cell under ~4 words",
		"80–100 column terminal",
		"If the grid would wrap, shorten the cells",
		"do not emit an unreadable table",
	} {
		if !strings.Contains(p.Body, want) {
			t.Fatalf("design-decisions missing terminal-safe grid table requirement %q", want)
		}
	}
}

func TestDelegateGitPlumbingNamesDispatchTool(t *testing.T) {
	p, ok := Get("delegate-git-plumbing")
	if !ok {
		t.Fatal("delegate-git-plumbing missing")
	}
	// The point of the protocol is to steer git mechanics onto the dispatch
	// sub-agent. If a future edit rewrites it to generic "delegate it"
	// language, the model won't know which tool to reach for — so both the
	// steering trigger and the body must name the dispatch tool explicitly.
	if !strings.Contains(p.Trigger, "dispatch") {
		t.Fatal("delegate-git-plumbing trigger must name the dispatch tool so the steering block surfaces it")
	}
	if !strings.Contains(p.Body, "dispatch") {
		t.Fatal("delegate-git-plumbing body must name the dispatch tool explicitly")
	}
	// The core hazard the body must call out: a sub-agent's git silently
	// landing in the shared main checkout unless scoped with git -C.
	if !strings.Contains(p.Body, "git -C") {
		t.Fatal("delegate-git-plumbing body must show the git -C <worktree> scoping guardrail")
	}
}
