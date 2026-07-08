// Package watchdog — worktreefirst.go: enforces the worktree-first protocol
// by blocking `git checkout -b` and `git switch -c` when they'd land in the
// shared root workspace. Agents are steered to the git_worktree tool instead.
//
// The check is deterministic — no LLM required. Applies fires whenever the
// pattern is present in a run_command tool call; Evaluate returns a fixed
// violation with a challenge that names the escape hatch (the git_worktree
// tool) and points at the protocol body for the rationale.
package watchdog

import (
	"context"
	"encoding/json"
	"regexp"
	"strings"
)

type worktreeFirstCheck struct{}

// WorktreeFirstCheck refuses raw branch-creation shell commands so agents
// use the git_worktree tool instead.
func WorktreeFirstCheck() Check { return worktreeFirstCheck{} }

func (worktreeFirstCheck) Name() string { return "worktree-first" }

// runCommandToolName is the canonical name of the shell-execution
// capability inside Cercano's tool registry. Kept as a constant so the
// Applies filter stays legible.
const runCommandToolName = "run_command"

// branchCreatePattern matches raw shell forms that create a new branch:
//   - git checkout -b <name>
//   - git checkout -B <name>
//   - git switch -c <name>
//   - git switch -C <name>
//
// Whitespace between tokens is flexible; the pattern anchors on word
// boundaries so `-branch` (a partial match) doesn't trip it.
var branchCreatePattern = regexp.MustCompile(`\bgit\s+(?:checkout\s+-B?|switch\s+-C?)\b`)

// runCommandInput mirrors the Input struct of the run_command capability so
// we can decode ToolArgs without importing the builtin package (which would
// cycle with the server package that owns Gate).
type runCommandInput struct {
	Cmd []string `json:"cmd"`
	Cwd string   `json:"cwd"`
}

func (worktreeFirstCheck) Applies(a Action) bool {
	if a.Kind != "tool_call" || canonical(a.ToolName) != runCommandToolName {
		return false
	}
	var in runCommandInput
	if err := json.Unmarshal(a.ToolArgs, &in); err != nil {
		return false
	}
	// Flatten into one effective command line so both direct
	// invocations (`git checkout -b …`) and shell wrappers
	// (`bash -lc "git checkout -b …"`) both match.
	line := strings.Join(in.Cmd, " ")
	return branchCreatePattern.MatchString(line)
}

// Evaluate returns a fixed violation whenever Applies matched. The challenge
// text names the escape hatch (the git_worktree tool) and points at the
// protocol body so the agent knows where to look for the full rationale.
func (worktreeFirstCheck) Evaluate(_ context.Context, _ Action, _ OneShotFunc) (Verdict, error) {
	return Verdict{
		Violation: true,
		Protocol:  "worktree-first",
		Challenge: "Creating a branch in the shared root workspace is a worktree-first protocol violation. Use the git_worktree tool instead. Run get_protocol('worktree-first') for the full rule.",
	}, nil
}
