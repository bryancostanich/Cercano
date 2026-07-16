package compaction

import (
	"fmt"
	"strings"

	"cercano/source/server/internal/llm"
)

// BuildSummaryPrompt renders messages to a transcript and asks the model for a
// fixed section-tagged summary. The format is parsed by ParseSummary.
//
// Prompt design notes:
//   - PROPOSALS exists as its own slot so design/approach proposals that are
//     still awaiting user approval have a home. Without it, the LLM had
//     nowhere to record them (not a DECISION, not a FILE, not an OPEN
//     one-liner) and would silently drop the proposal body — the failure
//     mode that lost the models×tiers design in conversation
//     80109e871fba4e18.
//   - The fidelity guardrail tells the LLM to preserve config YAML, tier /
//     enum / option lists, and function / RPC signatures verbatim rather
//     than paraphrasing them. Those shapes are the load-bearing content of
//     both proposals and decisions.
//   - The dedup guardrail is a cheap fix for LLMs that occasionally loop
//     and produce the same bullet repeated many times.
func BuildSummaryPrompt(messages []llm.Message) string {
	var b strings.Builder
	b.WriteString("Summarize the following conversation span for later reference.\n")
	b.WriteString("\n")
	b.WriteString("Fidelity rules (apply to every section):\n")
	b.WriteString("- Preserve verbatim any config YAML, code fences, tier / enum / field lists, and function / RPC / type signatures. Do NOT paraphrase these — copy the exact identifiers and shapes.\n")
	b.WriteString("- Preserve exact identifier names (types, functions, fields, files, YAML keys) rather than describing them in prose.\n")
	b.WriteString("- Within a section, each bullet must be unique — do not repeat the same bullet.\n")
	b.WriteString("- A DECISION is confirmed or applied. A PROPOSAL is offered but not yet accepted or rejected. Do not promote proposals to decisions; do not drop proposals just because they are unconfirmed.\n")
	b.WriteString("\n")
	b.WriteString("Respond ONLY in this exact format, omitting a section if empty:\n\n")
	b.WriteString("GOAL: <one line: the objective>\n")
	b.WriteString("DECISIONS:\n- <confirmed decision>\n")
	b.WriteString("PROPOSALS:\n- <design / approach / plan proposed but not yet accepted or rejected — include the proposal's concrete shape (config keys, tier names, signatures) verbatim>\n")
	b.WriteString("FILES:\n- <path>: <latest state>\n")
	b.WriteString("OPEN:\n- <unresolved question or next step>\n")
	b.WriteString("STATE: <one line: current state>\n\n")
	b.WriteString("--- conversation ---\n")
	for _, m := range messages {
		for _, blk := range m.Blocks {
			switch blk.Type {
			case llm.BlockText:
				fmt.Fprintf(&b, "%s: %s\n", m.Role, blk.Text)
			case llm.BlockToolUse:
				fmt.Fprintf(&b, "%s: [tool %s %s]\n", m.Role, blk.ToolName, string(blk.ToolInput))
			case llm.BlockToolResult:
				fmt.Fprintf(&b, "%s: [tool result] %s\n", m.Role, blk.Content)
			case llm.BlockImage:
				fmt.Fprintf(&b, "%s: [image]\n", m.Role)
			}
		}
	}
	// Close the transcript and restate the task AFTER it. With the
	// instructions only at the top, a model that has just read thousands of
	// tokens of agent transcript pattern-completes the conversation instead
	// of summarizing it (observed live, deterministic at temperature 0). The
	// last thing the model reads must be the instruction.
	b.WriteString("--- end conversation ---\n")
	b.WriteString("\n")
	b.WriteString("Now summarize the conversation span above. Respond ONLY in the exact sectioned format specified at the top (GOAL / DECISIONS / PROPOSALS / FILES / OPEN / STATE). Do not continue the conversation, do not reply to it, and do not emit tool calls.\n")
	return b.String()
}

var summaryLabels = map[string]bool{
	"GOAL": true, "DECISIONS": true, "PROPOSALS": true, "FILES": true, "OPEN": true, "STATE": true,
}

// ParseSummary leniently extracts the section-tagged summary. Unknown/leading
// prose is ignored; a missing section yields an empty field; it never errors.
func ParseSummary(text string) StructuredSummary {
	s := StructuredSummary{Files: map[string]string{}}
	section := ""
	for _, raw := range strings.Split(text, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		label, rest, hasLabel := splitLabel(line)
		if hasLabel {
			section = label
			switch label {
			case "GOAL":
				s.Goal = strings.TrimSpace(rest)
			case "STATE":
				s.State = strings.TrimSpace(rest)
			}
			continue
		}
		item := stripBullet(line)
		if item == "" {
			continue
		}
		switch section {
		case "DECISIONS":
			s.Decisions = append(s.Decisions, item)
		case "PROPOSALS":
			s.Proposals = append(s.Proposals, item)
		case "OPEN":
			s.OpenThreads = append(s.OpenThreads, item)
		case "FILES":
			if path, state, ok := strings.Cut(item, ":"); ok {
				s.Files[strings.TrimSpace(path)] = strings.TrimSpace(state)
			}
		}
	}
	return s
}

// splitLabel reports whether line begins with a known SECTION: label, returning
// the upper-case label and the inline remainder.
func splitLabel(line string) (label, rest string, ok bool) {
	head, tail, found := strings.Cut(line, ":")
	if !found {
		return "", "", false
	}
	up := strings.ToUpper(strings.TrimSpace(head))
	if summaryLabels[up] {
		return up, tail, true
	}
	return "", "", false
}

// stripBullet removes a leading "-", "*", or "N." marker.
func stripBullet(line string) string {
	line = strings.TrimSpace(line)
	for _, p := range []string{"- ", "* "} {
		if strings.HasPrefix(line, p) {
			return strings.TrimSpace(line[len(p):])
		}
	}
	// "1. " / "2) " style — require the marker to be followed by a space (or end)
	// so a numeric-leading filename like "1.txt: foo" is not mistaken for a bullet.
	if i := strings.IndexAny(line, ".)"); i > 0 && i <= 3 {
		if _, err := fmt.Sscanf(line[:i], "%d", new(int)); err == nil {
			if i+1 >= len(line) || line[i+1] == ' ' {
				return strings.TrimSpace(line[i+1:])
			}
		}
	}
	return line
}

// splitRecent splits off the last n messages as the verbatim window.
func splitRecent(msgs []llm.Message, n int) (older, recent []llm.Message) {
	if n <= 0 {
		return msgs, nil
	}
	if n >= len(msgs) {
		return nil, msgs
	}
	return msgs[:len(msgs)-n], msgs[len(msgs)-n:]
}

// renderSummaryMessages wraps a non-empty summary as a single user message, for
// feeding a prior summary back into the model (rolling).
func renderSummaryMessages(s StructuredSummary) []llm.Message {
	if s.IsEmpty() {
		return nil
	}
	return []llm.Message{{Role: llm.RoleUser, Blocks: []llm.Block{s.RenderBlock()}}}
}
