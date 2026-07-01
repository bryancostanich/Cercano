package watchdog

import (
	"context"
	"fmt"
	"strings"

	"cercano/source/server/internal/llm"
)

type commitCheckpointCheck struct{}

// CommitCheckpointCheck nudges the agent to commit a completed unit of work
// before starting a different one. The trigger is a semantic work boundary, not
// a count.
func CommitCheckpointCheck() Check { return commitCheckpointCheck{} }

func (commitCheckpointCheck) Name() string { return "commit-checkpoint" }

// workEditTools are the code-mutating calls whose accumulation is "uncommitted
// work". git_reset_hard is intentionally excluded (it is not authored work).
var workEditTools = map[string]bool{"edit_file": true, "write_file": true, "rm_file": true}

// commitTools mark a commit boundary. Both the semantic checkpoint capability
// and the lower-level git_commit reset uncommitted work.
var commitTools = map[string]bool{"checkpoint": true, "git_commit": true}

func (commitCheckpointCheck) Applies(a Action) bool {
	if a.Kind != "tool_call" || !workEditTools[a.ToolName] {
		return false
	}
	return uncommittedEditCount(a.Transcript) > 0
}

// uncommittedEditCount counts work-edit tool calls in the transcript that occur
// after the most recent commit tool call.
func uncommittedEditCount(msgs []llm.Message) int {
	n := 0
	for _, m := range msgs {
		for _, b := range m.Blocks {
			if b.Type != llm.BlockToolUse {
				continue
			}
			if commitTools[b.ToolName] {
				n = 0 // a commit clears the running count
			} else if workEditTools[b.ToolName] {
				n++
			}
		}
	}
	return n
}

func (commitCheckpointCheck) Evaluate(ctx context.Context, a Action, oneShot OneShotFunc) (Verdict, error) {
	if oneShot == nil {
		return Verdict{Protocol: "commit-checkpoint"}, nil // fail open
	}
	out, err := oneShot(ctx, buildCommitCheckpointPrompt(a))
	if err != nil {
		return Verdict{}, err
	}
	return parseVerdict("commit-checkpoint", out), nil
}

func buildCommitCheckpointPrompt(a Action) string {
	var b strings.Builder
	b.WriteString("You are a supervisor enforcing commit discipline.\n")
	b.WriteString("The agent has made edits since the last commit and is about to make another. Judge ONLY whether the NEW edit begins a DIFFERENT unit of work than the uncommitted edits — such that the prior work now forms a complete, committable change that should be committed first. A continuation of the same change is NOT a violation. A passing test or build in the transcript is evidence a unit may have completed, but is not by itself a reason to commit (it can be mid-work).\n\n")
	b.WriteString("Uncommitted edits so far (most recent last):\n")
	for _, line := range uncommittedEditSummary(a.Transcript) {
		fmt.Fprintf(&b, "  - %s\n", line)
	}
	fmt.Fprintf(&b, "\nNew edit about to run: %s %s\n\n", a.ToolName, string(a.ToolArgs))
	b.WriteString("Respond EXACTLY:\nVIOLATION: yes|no\nCHALLENGE: <one line, only if yes>\n")
	return b.String()
}

// uncommittedEditSummary lists the work-edits since the last commit as
// "toolname args" lines (args truncated).
func uncommittedEditSummary(msgs []llm.Message) []string {
	var out []string
	for _, m := range msgs {
		for _, b := range m.Blocks {
			if b.Type != llm.BlockToolUse {
				continue
			}
			if commitTools[b.ToolName] {
				out = nil
			} else if workEditTools[b.ToolName] {
				arg := string(b.ToolInput)
				if len(arg) > 120 {
					arg = arg[:120]
				}
				out = append(out, b.ToolName+" "+arg)
			}
		}
	}
	return out
}
