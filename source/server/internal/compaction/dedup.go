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
// KeepLastNToolResults. Measured on real long-conversation corpus: keeping the
// last 20 tool results recovers ~58% of tokens while retaining enough recent
// context for the model to continue reasoning about the current work. Tuning
// lower (10) buys ~2 more points; going higher (50) gives up ~7 points.
const DefaultLossyElisionKeepLast = 20

// KeepLastNToolResults stubs the Content of every tool_result block EXCEPT the
// last n in document order, replacing each earlier one with a one-line stub.
// All blocks and ids stay in place, so pairing remains valid. Returns the
// rewritten messages and the number of stubbed results.
//
// Unlike ElideSupersededToolResults (byte-identical dedup only, lossless in the
// sense that no information is destroyed), this is a recency-window policy —
// it stubs older tool_results even when they have no duplicate. Older tool
// content is reconstructible: the model can re-invoke the tool if it needs the
// content again, and the raw turns are still in the persistent store for
// programmatic rehydration. Callers must gate this behind an explicit opt-in.
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
	keepFrom := len(refs) - n
	if keepFrom <= 0 {
		return msgs, 0
	}
	// Copy shallowly, rewriting the Content of stub-eligible tool_results.
	out := make([]llm.Message, len(msgs))
	for i, m := range msgs {
		out[i] = llm.Message{Role: m.Role, Blocks: append([]llm.Block(nil), m.Blocks...)}
	}
	stubbed := 0
	for idx, r := range refs {
		if idx >= keepFrom {
			continue
		}
		blk := out[r.m].Blocks[r.b]
		blk.Content = fmt.Sprintf("[elided: superseded result, %d chars]", len(blk.Content))
		out[r.m].Blocks[r.b] = blk
		stubbed++
	}
	return out, stubbed
}
