package compaction

import (
	"context"
	"fmt"
	"strings"

	"cercano/source/server/internal/contextmeter"
	"cercano/source/server/internal/llm"
)

// AdaptiveCompactor is the OpenHands-style contender: the first KeepFirst
// messages (the original ask and setup) and the recent tail stay verbatim;
// only the middle is summarized, rolling forward. Pair it with
// BuildAdaptivePrompt, whose sections are conditional on content — the
// hypothesis under test is that "never invent an entry to fill a section"
// plus verbatim anchors reduces the slot-filling hallucination observed in
// the fixed-taxonomy baseline.
type AdaptiveCompactor struct {
	KeepFirst int // messages pinned verbatim at the head; 0 means default 2
}

func (AdaptiveCompactor) Name() string { return "adaptive" }

func (c AdaptiveCompactor) Compact(ctx context.Context, raw []llm.Message, summarize SummarizeFunc, b Budget) (Result, error) {
	elided, _ := ElideSupersededToolResults(raw)

	keep := c.KeepFirst
	if keep <= 0 {
		keep = 2
	}
	head, rest := splitPinnedHead(elided, keep)
	middle, recent := splitRecent(rest, b.VerbatimRecent)

	var sum StructuredSummary
	if len(middle) > 0 {
		tok := contextmeter.Default()
		for _, seg := range SegmentByTokens(middle, tok, segTokens(b)) {
			input := append(renderSummaryMessages(sum), seg.Messages...)
			s, err := summarize(ctx, input)
			if err != nil {
				return Result{}, err
			}
			sum = s
		}
	}

	view := make([]llm.Message, 0, len(head)+1+len(recent))
	view = append(view, head...)
	view = append(view, renderSummaryMessages(sum)...)
	view = append(view, recent...)
	return Result{SendView: view, Summaries: []StructuredSummary{sum}}, nil
}

// splitPinnedHead splits off the first n messages, extending the boundary so a
// tool_result message is never separated from the tool_use it answers.
func splitPinnedHead(msgs []llm.Message, n int) (head, rest []llm.Message) {
	if n >= len(msgs) {
		return msgs, nil
	}
	for n < len(msgs) && hasToolResult(msgs[n]) {
		n++
	}
	return msgs[:n], msgs[n:]
}

// BuildAdaptivePrompt renders the same section labels as BuildSummaryPrompt
// (so ParseSummary applies unchanged) but makes every section conditional and
// leads with the anti-invention rule. Modeled on OpenHands'
// summarizing_prompt.j2: adapt the format to the content, skip what is not
// present, and never fabricate to satisfy the format.
func BuildAdaptivePrompt(messages []llm.Message) string {
	var b strings.Builder
	b.WriteString("You are maintaining a running state summary of a coding-agent conversation.\n")
	b.WriteString("\n")
	b.WriteString("Rules, in priority order:\n")
	b.WriteString("1. NEVER invent content. Every entry must trace to something actually said in the conversation below. If a section has nothing real to hold, omit the section entirely — an omitted section is correct, a fabricated entry is a defect.\n")
	b.WriteString("2. Preserve exact identifiers verbatim: type / function / field names, file paths, YAML keys, config values, model IDs, task or turn IDs, and error strings. Copy them character-for-character; do not paraphrase an identifier.\n")
	b.WriteString("3. Preserve config YAML, code fences, signatures, and enum / tier / option lists verbatim inside their entry.\n")
	b.WriteString("4. A DECISION was confirmed or applied. A PROPOSAL was offered and awaits a verdict. When unsure which, it is a PROPOSAL.\n")
	b.WriteString("5. Each bullet must be unique within its section.\n")
	b.WriteString("\n")
	b.WriteString("Use only these section labels, omitting any that are empty:\n\n")
	b.WriteString("GOAL: <one line>\n")
	b.WriteString("DECISIONS:\n- <confirmed decision>\n")
	b.WriteString("PROPOSALS:\n- <proposal awaiting verdict, with its concrete shape verbatim>\n")
	b.WriteString("FILES:\n- <path>: <latest state>\n")
	b.WriteString("OPEN:\n- <unresolved question or next step>\n")
	b.WriteString("STATE: <one line>\n\n")
	b.WriteString("--- conversation ---\n")
	writeTranscript(&b, messages)
	return b.String()
}

// writeTranscript renders messages in the shared transcript format used by all
// summary prompts.
func writeTranscript(b *strings.Builder, messages []llm.Message) {
	for _, m := range messages {
		for _, blk := range m.Blocks {
			switch blk.Type {
			case llm.BlockText:
				fmt.Fprintf(b, "%s: %s\n", m.Role, blk.Text)
			case llm.BlockToolUse:
				fmt.Fprintf(b, "%s: [tool %s %s]\n", m.Role, blk.ToolName, string(blk.ToolInput))
			case llm.BlockToolResult:
				fmt.Fprintf(b, "%s: [tool result] %s\n", m.Role, blk.Content)
			case llm.BlockImage:
				fmt.Fprintf(b, "%s: [image]\n", m.Role)
			}
		}
	}
}
