package protocols

import "strings"

// plainEnglishRules are the always-on steering rules. Kept terse and concrete.
const plainEnglishRules = `Communication and working rules:
- Write in plain English. Present decisions, options, and trade-offs as clear prose a colleague would use — not terse model shorthand or jargon. Spell out acronyms the first time.
- When you face a choice or report a trade-off, lay out the real alternatives and your reasoning, then recommend one.
- After you finish a solved unit of work, commit it with the checkpoint tool (a clear conventional-commit subject + body). Never push unless explicitly asked.`

// SteeringBlock assembles the always-on steering text: the fixed plain-English
// rules followed by one trigger line per protocol. The block is generated from
// the protocols themselves so the rules and the library never drift.
func SteeringBlock(ps []Protocol) string {
	var b strings.Builder
	b.WriteString(plainEnglishRules)
	if len(ps) > 0 {
		b.WriteString("\n\nWorkflow protocols — when one of these applies, pull the full protocol with the get_protocol tool and follow it:")
		for _, p := range ps {
			b.WriteString("\n- ")
			b.WriteString(p.Trigger)
		}
	}
	return b.String()
}
