package watchdog

import "strings"

// Verdict is a check's judgment of a proposed action.
type Verdict struct {
	Violation bool
	Protocol  string
	Challenge string
	// Revise is the check's corrective instruction shown to the model on a
	// challenge/block — what to DO about the violation (rewrite the prose,
	// perform the announced action, …). Set by check code, not model output.
	Revise string
}

// parseVerdict reads a fast-model completion of the form
// "VIOLATION: yes|no\nCHALLENGE: <one line>". It is conservative: only an
// explicit affirmative counts as a violation (so ambiguous/garbage output
// fails open — no challenge).
func parseVerdict(protocol, modelText string) Verdict {
	v := Verdict{Protocol: protocol}
	for _, line := range strings.Split(modelText, "\n") {
		l := strings.TrimSpace(line)
		low := strings.ToLower(l)
		switch {
		case strings.HasPrefix(low, "violation:"):
			val := strings.TrimSpace(low[len("violation:"):])
			// Prefix match: fast local models sometimes decorate the verdict
			// ("yes.", "yes (clearly)"). Anything else — including "no" and
			// genuine garbage — fails open (no violation).
			v.Violation = strings.HasPrefix(val, "yes") || strings.HasPrefix(val, "true")
		case strings.HasPrefix(low, "challenge:"):
			v.Challenge = strings.TrimSpace(l[len("challenge:"):])
		}
	}
	if v.Violation && v.Challenge == "" {
		v.Challenge = "You appear to be skipping the " + protocol + " protocol — comply or justify."
	}
	if !v.Violation {
		v.Challenge = ""
	}
	return v
}
