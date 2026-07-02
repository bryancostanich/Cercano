package watchdog

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// argsJSON returns a JSON blob of the run_command Input shape with the given
// cmd argv. Kept local so tests don't reach into other packages.
func argsJSON(cmd ...string) json.RawMessage {
	b, err := json.Marshal(map[string]any{"cmd": cmd})
	if err != nil {
		panic(err)
	}
	return b
}

func TestWorktreeFirst_AppliesToDirectGitCheckoutB(t *testing.T) {
	c := WorktreeFirstCheck()
	a := Action{
		Kind:     "tool_call",
		ToolName: "run_command",
		ToolArgs: argsJSON("git", "checkout", "-b", "feat/foo"),
	}
	if !c.Applies(a) {
		t.Fatal("Applies should fire on `git checkout -b <name>`")
	}
}

func TestWorktreeFirst_AppliesToShellWrappedGitCheckoutB(t *testing.T) {
	c := WorktreeFirstCheck()
	a := Action{
		Kind:     "tool_call",
		ToolName: "run_command",
		ToolArgs: argsJSON("bash", "-lc", "git checkout -b feat/foo && echo ok"),
	}
	if !c.Applies(a) {
		t.Fatal("Applies should fire when the command is wrapped in `bash -lc`")
	}
}

func TestWorktreeFirst_AppliesToGitSwitchC(t *testing.T) {
	// `git switch -c <name>` is the modern equivalent of `git checkout -b`.
	c := WorktreeFirstCheck()
	a := Action{
		Kind:     "tool_call",
		ToolName: "run_command",
		ToolArgs: argsJSON("git", "switch", "-c", "feat/foo"),
	}
	if !c.Applies(a) {
		t.Fatal("Applies should fire on `git switch -c <name>`")
	}
}

func TestWorktreeFirst_DoesNotApplyToPlainCheckout(t *testing.T) {
	// Switching to an existing branch is legitimate and must NOT trip.
	c := WorktreeFirstCheck()
	a := Action{
		Kind:     "tool_call",
		ToolName: "run_command",
		ToolArgs: argsJSON("git", "checkout", "main"),
	}
	if c.Applies(a) {
		t.Fatal("Applies must not fire on `git checkout <existing-branch>`")
	}
}

func TestWorktreeFirst_DoesNotApplyToGitWorktreeAdd(t *testing.T) {
	// The whole point of the protocol is to steer agents TO worktree add;
	// blocking it would be self-defeating.
	c := WorktreeFirstCheck()
	a := Action{
		Kind:     "tool_call",
		ToolName: "run_command",
		ToolArgs: argsJSON("git", "worktree", "add", "-b", "feat/foo", "some/path"),
	}
	if c.Applies(a) {
		t.Fatal("Applies must not fire on `git worktree add -b <name>`")
	}
}

func TestWorktreeFirst_DoesNotApplyToNonRunCommandTool(t *testing.T) {
	c := WorktreeFirstCheck()
	a := Action{
		Kind:     "tool_call",
		ToolName: "edit_file",
		ToolArgs: argsJSON("git", "checkout", "-b", "feat/foo"),
	}
	if c.Applies(a) {
		t.Fatal("Applies must be scoped to run_command; other tools are out of scope")
	}
}

func TestWorktreeFirst_DoesNotApplyToTurnEnd(t *testing.T) {
	c := WorktreeFirstCheck()
	a := Action{Kind: "turn_end", Text: "git checkout -b feat/foo"}
	if c.Applies(a) {
		t.Fatal("Applies must not fire on turn_end actions (checks the intent, not the prose)")
	}
}

func TestWorktreeFirst_EvaluateReturnsDeterministicViolation(t *testing.T) {
	c := WorktreeFirstCheck()
	// The check is deterministic — no LLM required, oneShot may be nil.
	v, err := c.Evaluate(context.Background(), Action{}, nil)
	if err != nil {
		t.Fatalf("Evaluate returned error: %v", err)
	}
	if !v.Violation {
		t.Fatal("Evaluate must return a violation whenever Applies fired")
	}
	if v.Protocol != "worktree-first" {
		t.Fatalf("Verdict.Protocol = %q, want worktree-first", v.Protocol)
	}
	if v.Challenge == "" {
		t.Fatal("Verdict.Challenge must be non-empty so the agent gets a specific nudge")
	}
	if !strings.Contains(v.Challenge, "git_worktree") {
		t.Fatalf("Challenge must name the git_worktree tool so the agent knows the escape hatch, got: %q", v.Challenge)
	}
}
