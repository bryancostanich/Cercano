package compaction

import (
	"context"
	"fmt"
	"strings"

	"cercano/source/server/internal/llm"
)

// RetrievalCompactor wraps another contender and appends a deterministic
// recall index of the messages that were summarized away: ordinal, role, and
// a one-line gist per message. The MemGPT-style hypothesis under test:
// summary drift matters less when the original turns stay addressable — an
// agent (or the recall RPC this frame anticipates) can page any original
// message back by ordinal instead of trusting the summary.
type RetrievalCompactor struct {
	Base Compactor // summarizing contender to wrap; nil means RollingCompactor
}

func (c RetrievalCompactor) Name() string {
	return "retrieval(" + c.base().Name() + ")"
}

func (c RetrievalCompactor) base() Compactor {
	if c.Base != nil {
		return c.Base
	}
	return RollingCompactor{}
}

func (c RetrievalCompactor) Compact(ctx context.Context, raw []llm.Message, summarize SummarizeFunc, b Budget) (Result, error) {
	res, err := c.base().Compact(ctx, raw, summarize, b)
	if err != nil {
		return Result{}, err
	}

	// Index exactly the span the base contender folded into its summary — the
	// verbatim tail needs no recall entry.
	older, _ := splitRecent(raw, b.VerbatimRecent)
	if len(older) == 0 {
		return res, nil
	}
	index := llm.Message{Role: llm.RoleUser, Blocks: []llm.Block{{
		Type: llm.BlockText,
		Text: renderRecallIndex(older),
	}}}

	// Insert the index right after the summary preamble (position 0 when the
	// summary is empty the preamble is absent, so prepend).
	view := make([]llm.Message, 0, len(res.SendView)+1)
	if len(res.Summaries) > 0 && !res.Summaries[0].IsEmpty() && len(res.SendView) > 0 {
		view = append(view, res.SendView[0], index)
		view = append(view, res.SendView[1:]...)
	} else {
		view = append(view, index)
		view = append(view, res.SendView...)
	}
	res.SendView = view
	return res, nil
}

// gistLen bounds each recall-index line. Long enough to identify a message,
// short enough that the index stays a small fraction of what it replaces.
const gistLen = 100

// renderRecallIndex produces the machine-readable index block. Ordinals are
// positions in the original (pre-compaction) history, which is what a recall
// lookup would address.
func renderRecallIndex(msgs []llm.Message) string {
	var b strings.Builder
	b.WriteString("[recall index — original messages summarized above; any can be retrieved in full by ordinal]\n")
	for i, m := range msgs {
		fmt.Fprintf(&b, "%d %s: %s\n", i, m.Role, gist(m))
	}
	return b.String()
}

// gist flattens a message to one truncated line.
func gist(m llm.Message) string {
	var b strings.Builder
	for _, blk := range m.Blocks {
		switch blk.Type {
		case llm.BlockText:
			b.WriteString(blk.Text)
		case llm.BlockToolUse:
			b.WriteString("[tool " + blk.ToolName + "]")
		case llm.BlockToolResult:
			b.WriteString(blk.Content)
		}
		b.WriteByte(' ')
	}
	line := normalizeSpace(b.String())
	if len(line) > gistLen {
		line = line[:gistLen] + "…"
	}
	return line
}
