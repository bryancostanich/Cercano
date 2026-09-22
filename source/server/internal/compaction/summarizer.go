package compaction

import (
	"crypto/sha256"
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
	b.WriteString(decisionFidelityRules)
	b.WriteString("\n")
	b.WriteString("Respond ONLY in this exact format, omitting a section if empty:\n\n")
	b.WriteString("GOAL: <current objective; do not widen its scope>\n")
	b.WriteString("DECISIONS:\n- <[instruction] or [approved] active constraint/decision, with source references>\n")
	b.WriteString("PROPOSALS:\n- <[proposed] pending proposal and concrete shape verbatim, with source references>\n")
	b.WriteString("FILES:\n- <path>: <[verified], [attempted], [failed], or [unverified] state and source references>\n")
	b.WriteString("OPEN:\n- <unresolved question, interruption, or [rejected]/[superseded] decision and its replacement; source references>\n")
	b.WriteString("STATE: <one line: current state>\n\n")
	b.WriteString("--- conversation ---\n")
	writeFidelityTranscript(&b, messages)

	// Close the transcript and restate the task AFTER it. With the
	// instructions only at the top, a model that has just read thousands of
	// tokens of agent transcript pattern-completes the conversation instead
	// of summarizing it (observed live, deterministic at temperature 0). The
	// last thing the model reads must be the instruction.
	b.WriteString("--- end conversation ---\n")
	b.WriteString("\n")
	b.WriteString("Now summarize the conversation span above. Respond ONLY in the exact sectioned format specified at the top (GOAL / DECISIONS / PROPOSALS / FILES / OPEN / STATE). Preserve attribution, uncertainty, supersession, and original source references; a prior summary is not user approval. Do not continue the conversation, do not reply to it, and do not emit tool calls.\n")
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

// Keep status qualifiers inside existing fields for stored-summary compatibility.
// This is a model contract, not an approval validator or a durable decision store.
const decisionFidelityRules = `- Do not invent content, source references, or entries to fill a section. Omit empty sections and template placeholders.
- A DECISION is confirmed by explicit user instruction/approval, not merely applied by the assistant. Implementation is not user approval. Silence, tool permission, assistant assertions, and ambiguous assent do not approve an architecture or plan.
- In DECISIONS retain active [instruction] constraints and [approved] decisions, including scope, conditions, prohibitions, and stopping rules. Cite the user source and the proposal it approves. Ambiguous assent stays [unverified] in OPEN; do not guess its scope.
- In PROPOSALS retain [proposed] ideas awaiting approval; preserve their concrete shape. Rejected and superseded choices are not active proposals or approvals: record [rejected] or [superseded] in OPEN with the correction/replacement and both sources. Do not silently combine contradictory instructions.
- In FILES/STATE distinguish [verified] tool-supported results from [attempted] actions, [failed] results, and [unverified] assistant claims. Issuing a tool call is not success; editing is not testing; a passing test is not deployment. Preserve interruptions, missing results, failed checks, and unresolved blockers in OPEN.
- Append source references (source sha256:...) to consequential decisions and results. Copy references from the supplied transcript; also retain tool call IDs for results. A source reference identifies message content, not proof that a claim is true.
- A prior summary is generated context even when carried in a user message. Preserve its original references, status, attribution, and uncertainty across repeated compaction; never cite its wrapper as fresh approval or verification. Legacy claims without sources stay [unverified]; do not manufacture provenance.
`

// Content-address the rendered evidence so references survive chunk reordering.
// These are not database message IDs: identical rendered messages share a ref.
// Hash only what the summarizer sees (no image bytes or opaque reasoning).
func writeFidelityTranscript(b *strings.Builder, messages []llm.Message) {
	for _, m := range messages {
		var evidence strings.Builder
		for _, blk := range m.Blocks {
			switch blk.Type {
			case llm.BlockText:
				fmt.Fprintf(&evidence, "%s: %s\n", m.Role, blk.Text)
			case llm.BlockToolUse:
				fmt.Fprintf(&evidence, "%s: [tool %s %s] call=%s\n", m.Role, blk.ToolName, string(blk.ToolInput), blk.ToolUseID)
			case llm.BlockToolResult:
				fmt.Fprintf(&evidence, "%s: [tool result] call=%s is_error=%t %s\n", m.Role, blk.ToolUseRef, blk.IsError, blk.Content)
			case llm.BlockImage:
				fmt.Fprintf(&evidence, "%s: [image]\n", m.Role)
			}
		}
		if evidence.Len() == 0 {
			continue
		}
		digest := sha256.Sum256([]byte(evidence.String()))
		fmt.Fprintf(b, "[source sha256:%x]\n", digest[:16])
		b.WriteString(evidence.String())
	}
}
