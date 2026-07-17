package watchdog

import (
	"context"
	"fmt"
	"strings"
)

type designDecisionsCheck struct{}

// DesignDecisionsCheck flags mutating actions that appear to commit to behavior
// or structure without first running the design-decisions protocol and getting
// human approval.
func DesignDecisionsCheck() Check { return designDecisionsCheck{} }

func (designDecisionsCheck) Name() string { return "design-decisions" }

const designDecisionsTranscriptWindow = 32

func (designDecisionsCheck) Applies(a Action) bool {
	if a.Kind != "tool_call" {
		return false
	}
	tool := canonical(a.ToolName)
	return editTools[tool]
}

func (designDecisionsCheck) Evaluate(ctx context.Context, a Action, oneShot OneShotFunc) (Verdict, error) {
	if oneShot == nil {
		return Verdict{Protocol: "design-decisions"}, nil // no model → fail open
	}
	out, err := oneShot(ctx, buildDesignDecisionsPrompt(a))
	if err != nil {
		return Verdict{}, err
	}
	v := parseVerdict("design-decisions", out)
	if v.Violation {
		v.Challenge = "You're making a structural choice without the design-decisions protocol — comply by calling get_protocol(\"design-decisions\") and presenting options for approval, or justify why this is not a real design decision."
	}
	return v, nil
}

func buildDesignDecisionsPrompt(a Action) string {
	var b strings.Builder
	b.WriteString("You are a supervisor enforcing the design-decisions protocol.\n")
	b.WriteString("The agent is about to run a code-mutating tool. Challenge the action when it appears to create, modify, or commit to behavior, interfaces, data models, module boundaries, state/control flow, prompt policy, tool schemas, or other structural choices, unless the recent transcript clearly shows the design-decisions protocol was followed: options were considered, trade-offs were discussed, a recommendation was made, and the human explicitly approved proceeding.\n\n")
	b.WriteString("Do NOT require the transcript to already prove there are multiple viable approaches before considering the protocol applicable; discovering alternatives is part of the protocol. Do NOT treat a single rationale paragraph, a claim that the edit is small/obvious, or a localized one-line behavior/config/schema/prompt change as protocol compliance. Truly mechanical edits, already-approved plans, test runs, and inspections are not violations when they do not change behavior or structure. When uncertain whether a mutating action is structural, prefer challenging; the agent can comply with the protocol or justify the exemption.\n\n")
	fmt.Fprintf(&b, "Proposed action: %s %s\n\n", a.ToolName, string(a.ToolArgs))
	b.WriteString("Recent transcript:\n")
	b.WriteString(transcriptTail(a.Transcript, designDecisionsTranscriptWindow))
	b.WriteString("\n\nRespond EXACTLY:\nVIOLATION: yes|no\nCHALLENGE: <one line, only if yes>\n")
	return b.String()
}
