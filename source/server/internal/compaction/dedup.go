package compaction

import (
	"fmt"

	"cercano/source/server/internal/llm"
)

// ElideSupersededToolResults rewrites the Content of tool_result blocks whose
// originating tool call (same ToolName + identical ToolInput bytes) recurs later
// in the history. Only the LAST occurrence of each identical call keeps its full
// result; earlier ones are replaced with a one-line stub. All blocks and ids are
// preserved, so pairing stays valid. Returns the rewritten messages and the
// number of results stubbed. Pure: input is not mutated.
func ElideSupersededToolResults(msgs []llm.Message) ([]llm.Message, int) {
	// key -> last tool_use id for that identical call.
	lastUseForKey := map[string]string{}
	for _, m := range msgs {
		for _, b := range m.Blocks {
			if b.Type == llm.BlockToolUse {
				lastUseForKey[toolKey(b)] = b.ToolUseID
			}
		}
	}
	// id -> key, so a tool_result can find whether its use is the last for its key.
	keyForUse := map[string]string{}
	for _, m := range msgs {
		for _, b := range m.Blocks {
			if b.Type == llm.BlockToolUse {
				keyForUse[b.ToolUseID] = toolKey(b)
			}
		}
	}

	collapsed := 0
	out := make([]llm.Message, len(msgs))
	for i, m := range msgs {
		blocks := make([]llm.Block, len(m.Blocks))
		for j, b := range m.Blocks {
			if b.Type == llm.BlockToolResult {
				if key, ok := keyForUse[b.ToolUseRef]; ok && lastUseForKey[key] != b.ToolUseRef {
					b.Content = fmt.Sprintf("[elided: superseded result, %d chars]", len(b.Content))
					collapsed++
				}
			}
			blocks[j] = b
		}
		out[i] = llm.Message{Role: m.Role, Blocks: blocks}
	}
	return out, collapsed
}

// toolKey identifies an identical tool call by name + raw input bytes. Input is
// compared verbatim, so two semantically-equal calls that differ only in
// whitespace/key-order are treated as distinct (and not deduped). A part-2
// algorithm that wants tighter dedup can normalize ToolInput before keying.
func toolKey(b llm.Block) string {
	return b.ToolName + "\x00" + string(b.ToolInput)
}

// DefaultLossyElisionKeepLast is the recency-window default for
// KeepLastNToolResults. Keep the count deliberately small: tool results are
// often large mechanical evidence, and the model usually needs only the last
// few verbatim outputs to continue the current step. Older raw results remain
// in the persistent store for inspection/export or explicit rehydration.
const DefaultLossyElisionKeepLast = 5

// DefaultLossyElisionMaxResultChars caps any single retained tool_result body.
// A count-only policy is unsafe: one recent grep/read result can be megabytes
// and dominate the next prompt even when it is the newest result.
const DefaultLossyElisionMaxResultChars = 16 * 1024

// DefaultLossyElisionTotalResultChars caps the total verbatim tool_result text
// retained by KeepLastNToolResults. This bounds the live-tail evidence budget
// even when several recent results are individually under the per-result cap.
const DefaultLossyElisionTotalResultChars = 64 * 1024

// KeepLastNToolResults stubs tool_result Content unless the result is recent
// enough AND fits within the per-result and total retained-result budgets. All
// blocks and ids stay in place, so pairing remains valid. Returns the rewritten
// messages and the number of stubbed results.
//
// Unlike ElideSupersededToolResults (byte-identical dedup only, lossless in the
// sense that no information is destroyed), this is a lossy context-window
// policy: it stubs older or oversized tool_results even when they have no
// duplicate. Tool content is reconstructible: the model can re-invoke the tool
// if it needs the content again, and the raw turns are still in the persistent
// store for programmatic rehydration. Callers must gate this behind an explicit
// opt-in.
func KeepLastNToolResults(msgs []llm.Message, n int) ([]llm.Message, int) {
	if n < 0 {
		n = 0
	}
	// Index every tool_result block in document order.
	type ref struct{ m, b int }
	var refs []ref
	for i, m := range msgs {
		for j, blk := range m.Blocks {
			if blk.Type == llm.BlockToolResult {
				refs = append(refs, ref{i, j})
			}
		}
	}
	if len(refs) == 0 {
		return msgs, 0
	}
	keepFrom := len(refs) - n
	if keepFrom < 0 {
		keepFrom = 0
	}

	// Decide keep/stub from newest to oldest so the total budget favors the most
	// recent evidence. A result must pass all three gates: within the last n,
	// below the per-result cap, and within the total retained-result budget.
	keep := make([]bool, len(refs))
	retainedChars := 0
	for idx := len(refs) - 1; idx >= 0; idx-- {
		if idx < keepFrom {
			continue
		}
		r := refs[idx]
		contentLen := len(msgs[r.m].Blocks[r.b].Content)
		if contentLen > DefaultLossyElisionMaxResultChars {
			continue
		}
		if retainedChars+contentLen > DefaultLossyElisionTotalResultChars {
			continue
		}
		keep[idx] = true
		retainedChars += contentLen
	}

	// Copy shallowly, rewriting the Content of stub-eligible tool_results.
	out := make([]llm.Message, len(msgs))
	for i, m := range msgs {
		out[i] = llm.Message{Role: m.Role, Blocks: append([]llm.Block(nil), m.Blocks...)}
	}
	stubbed := 0
	for idx, r := range refs {
		if keep[idx] {
			continue
		}
		blk := out[r.m].Blocks[r.b]
		blk.Content = fmt.Sprintf("[elided: tool result, %d chars]", len(blk.Content))
		out[r.m].Blocks[r.b] = blk
		stubbed++
	}
	return out, stubbed
}
