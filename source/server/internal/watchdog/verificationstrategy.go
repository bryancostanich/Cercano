package watchdog

import (
	"context"
	"fmt"
	"strings"

	"cercano/source/server/internal/llm"
)

type verificationStrategyCheck struct{}

// VerificationStrategyCheck enforces choosing an appropriate verification tier
// instead of reflexively running too much or too little test coverage.
func VerificationStrategyCheck() Check { return verificationStrategyCheck{} }

func (verificationStrategyCheck) Name() string { return "verification-strategy" }

func (verificationStrategyCheck) Applies(a Action) bool {
	if a.Kind != "tool_call" || canonical(a.ToolName) != runCommandToolName {
		return false
	}
	line, ok := runCommandLine(a.ToolArgs)
	if !ok {
		return false
	}
	return looksLikeVerificationCommand(line) && transcriptMentionsChange(a.Transcript)
}

func (verificationStrategyCheck) Evaluate(ctx context.Context, a Action, oneShot OneShotFunc) (Verdict, error) {
	if oneShot == nil {
		return Verdict{Protocol: "verification-strategy"}, nil // no model → fail open
	}
	out, err := oneShot(ctx, buildVerificationStrategyPrompt(a))
	if err != nil {
		return Verdict{}, err
	}
	v := parseVerdict("verification-strategy", out)
	if v.Violation {
		v.Challenge = "You're running verification without evidence of the verification-strategy protocol — comply by calling get_protocol(\"verification-strategy\") and matching the test tier to the change, or justify why this test command is already the right tier."
	}
	return v, nil
}

func buildVerificationStrategyPrompt(a Action) string {
	var b strings.Builder
	b.WriteString("You are a supervisor enforcing the verification-strategy protocol.\n")
	b.WriteString("The agent is about to run a verification command after making or discussing a change. Judge ONLY whether the transcript lacks evidence that it matched the test tier to the change. Targeted unit/package tests for local logic, integration/smoke tests for interfaces, and full end-to-end suites for sign-off are all valid when justified. Do NOT flag when the transcript already explains why this test tier is appropriate, or when the user explicitly requested this exact command.\n\n")
	fmt.Fprintf(&b, "Proposed verification action: %s %s\n\n", a.ToolName, string(a.ToolArgs))
	b.WriteString("Recent transcript:\n")
	b.WriteString(transcriptTail(a.Transcript, 18))
	b.WriteString("\n\nRespond EXACTLY:\nVIOLATION: yes|no\nCHALLENGE: <one line, only if yes>\n")
	return b.String()
}

func looksLikeVerificationCommand(line string) bool {
	l := strings.ToLower(line)
	patterns := []string{
		" go test", "go test ", "go test", " npm test", "npm test", " yarn test", "yarn test",
		" pnpm test", "pnpm test", " pytest", "pytest", " cargo test", "cargo test",
		" make test", "make test", " make check", "make check", " ctest", "ctest",
		" swift test", "swift test", " mvn test", "mvn test", " gradle test", "gradle test",
		" go vet", "go vet", " staticcheck", "staticcheck", " eslint", "eslint",
	}
	for _, p := range patterns {
		if strings.Contains(l, p) {
			return true
		}
	}
	return false
}

func transcriptMentionsChange(msgs []llm.Message) bool {
	for _, m := range msgs {
		for _, b := range m.Blocks {
			if b.Type == llm.BlockToolUse && (workEditTools[canonical(b.ToolName)] || canonical(b.ToolName) == "git_commit" || canonical(b.ToolName) == "checkpoint") {
				return true
			}
			if b.Type != llm.BlockText {
				continue
			}
			l := strings.ToLower(b.Text)
			if strings.Contains(l, "changed") || strings.Contains(l, "edited") || strings.Contains(l, "implemented") || strings.Contains(l, "fixed") || strings.Contains(l, "interface") || strings.Contains(l, "verification") || strings.Contains(l, "test tier") {
				return true
			}
		}
	}
	return false
}
