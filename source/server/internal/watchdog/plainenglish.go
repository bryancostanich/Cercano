package watchdog

import (
	"context"
	"strings"
)

type plainEnglishCheck struct{}

// PlainEnglishCheck flags an assistant reply that talks down, uses LLM/corporate
// jargon, or over-hedges instead of plain colleague-level English.
func PlainEnglishCheck() Check { return plainEnglishCheck{} }

func (plainEnglishCheck) Name() string { return "plain-english" }

// plainEnglishMinLen skips terse acknowledgements ("Done.", "Yes.") so the check
// doesn't fire on trivial replies.
const plainEnglishMinLen = 40

func (plainEnglishCheck) Applies(a Action) bool {
	return a.Kind == "turn_end" && len(strings.TrimSpace(a.Text)) >= plainEnglishMinLen
}

func (plainEnglishCheck) Evaluate(ctx context.Context, a Action, oneShot OneShotFunc) (Verdict, error) {
	if oneShot == nil {
		return Verdict{Protocol: "plain-english"}, nil // fail open
	}
	out, err := oneShot(ctx, buildPlainEnglishPrompt(a.Text))
	if err != nil {
		return Verdict{}, err
	}
	v := parseVerdict("plain-english", out)
	if v.Violation {
		v.Revise = "Rewrite your reply in plain, colleague-level English"
	}
	return v, nil
}

func buildPlainEnglishPrompt(text string) string {
	var b strings.Builder
	b.WriteString("You are a supervisor enforcing a plain-English register for the assistant's replies.\n")
	b.WriteString("Judge ONLY whether the reply below talks down to the user, uses LLM/corporate jargon or filler (e.g. \"delve\", \"leverage\", \"it's important to note\", \"I'd be happy to help!\"), or over-hedges — instead of plain, colleague-level English that assumes the reader knows the domain. Concise, direct, technical prose is NOT a violation.\n\n")
	b.WriteString("Reply:\n")
	b.WriteString(text)
	b.WriteString("\n\nRespond EXACTLY:\nVIOLATION: yes|no\nCHALLENGE: <one line, only if yes>\n")
	return b.String()
}
