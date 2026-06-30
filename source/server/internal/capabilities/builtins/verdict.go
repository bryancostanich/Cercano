package builtins

import "strings"

// Verdict is the parsed result of an adversarial review.
type Verdict struct {
	Risky     bool   `json:"risky"`
	Reasoning string `json:"reasoning"`
}

// parseVerdict parses a model review of the form "VERDICT: SAFE|RISKY\nREASONING: ...".
// It is conservative: anything not clearly SAFE is treated as risky.
func parseVerdict(modelText string) Verdict {
	lower := strings.ToLower(modelText)
	risky := strings.Contains(lower, "risky") || strings.Contains(lower, "refuted")
	safe := strings.Contains(lower, "safe") || strings.Contains(lower, "holds")
	if !risky && !safe {
		risky = true // no clear verdict → fail safe
	}
	reasoning := strings.TrimSpace(modelText)
	// Prefer the REASONING: line if present.
	for _, line := range strings.Split(modelText, "\n") {
		if l := strings.TrimSpace(line); len(l) >= 10 && strings.HasPrefix(strings.ToLower(l), "reasoning:") {
			reasoning = strings.TrimSpace(l[len("reasoning:"):])
			break
		}
	}
	return Verdict{Risky: risky, Reasoning: reasoning}
}
