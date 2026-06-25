package compaction

import (
	"fmt"
	"strings"

	"cercano/source/server/internal/llm"
)

// BuildSummaryPrompt renders messages to a transcript and asks the model for a
// fixed section-tagged summary. The format is parsed by ParseSummary.
func BuildSummaryPrompt(messages []llm.Message) string {
	var b strings.Builder
	b.WriteString("Summarize the following conversation span for later reference.\n")
	b.WriteString("Respond ONLY in this exact format, omitting a section if empty:\n\n")
	b.WriteString("GOAL: <one line: the objective>\n")
	b.WriteString("DECISIONS:\n- <key decision>\n")
	b.WriteString("FILES:\n- <path>: <latest state>\n")
	b.WriteString("OPEN:\n- <unresolved thread>\n")
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
			}
		}
	}
	return b.String()
}

var summaryLabels = map[string]bool{
	"GOAL": true, "DECISIONS": true, "FILES": true, "OPEN": true, "STATE": true,
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
	// "1." / "2)" style
	if i := strings.IndexAny(line, ".)"); i > 0 && i <= 3 {
		if _, err := fmt.Sscanf(line[:i], "%d", new(int)); err == nil {
			return strings.TrimSpace(line[i+1:])
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
	if s.isEmpty() {
		return nil
	}
	return []llm.Message{{Role: llm.RoleUser, Blocks: []llm.Block{s.RenderBlock()}}}
}
