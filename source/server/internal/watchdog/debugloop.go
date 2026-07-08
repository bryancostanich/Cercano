package watchdog

import (
	"context"
	"fmt"
	"strings"

	"cercano/source/server/internal/llm"
)

type debugLoopCheck struct{}

// DebugLoopCheck flags edits made to fix a bug/test failure with no evidence of
// the systematic-debugging loop in the recent transcript.
func DebugLoopCheck() Check { return debugLoopCheck{} }

func (debugLoopCheck) Name() string { return "debug-loop" }

var editTools = map[string]bool{"edit_file": true, "write_file": true, "rm_file": true, "git_reset_hard": true}

func (debugLoopCheck) Applies(a Action) bool {
	return a.Kind == "tool_call" && editTools[canonical(a.ToolName)]
}

func (debugLoopCheck) Evaluate(ctx context.Context, a Action, oneShot OneShotFunc) (Verdict, error) {
	if oneShot == nil {
		return Verdict{Protocol: "debug-loop"}, nil // no model → fail open
	}
	prompt := buildDebugLoopPrompt(a)
	out, err := oneShot(ctx, prompt)
	if err != nil {
		return Verdict{}, err
	}
	return parseVerdict("debug-loop", out), nil
}

func buildDebugLoopPrompt(a Action) string {
	var b strings.Builder
	b.WriteString("You are a supervisor enforcing the systematic-debugging protocol.\n")
	b.WriteString("The agent is about to run a code-mutating tool. Judge ONLY whether it is fixing a bug or test failure WITHOUT evidence in the recent transcript of the debug loop: reducing to the smallest failing case, observing actual data/output, and confirming the root cause with a probe. Refactors, new features, and edits with clear prior evidence are NOT violations.\n\n")
	fmt.Fprintf(&b, "Proposed action: %s %s\n\n", a.ToolName, string(a.ToolArgs))
	b.WriteString("Recent transcript:\n")
	b.WriteString(transcriptTail(a.Transcript, 12))
	b.WriteString("\n\nRespond EXACTLY:\nVIOLATION: yes|no\nCHALLENGE: <one line, only if yes>\n")
	return b.String()
}

// transcriptTail renders the last n messages as plain text for the prompt.
func transcriptTail(msgs []llm.Message, n int) string {
	if len(msgs) > n {
		msgs = msgs[len(msgs)-n:]
	}
	var b strings.Builder
	for _, m := range msgs {
		for _, blk := range m.Blocks {
			if blk.Type == llm.BlockText && strings.TrimSpace(blk.Text) != "" {
				fmt.Fprintf(&b, "[%s] %s\n", m.Role, strings.TrimSpace(blk.Text))
			}
		}
	}
	return b.String()
}
