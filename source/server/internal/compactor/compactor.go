// Package compactor orchestrates stateful, frozen-segment context compaction
// over conversation turns + persisted state, reusing the compaction primitives.
// It is pure over its inputs; persistence and triggering live in their callers.
package compactor

import (
	"context"
	"encoding/json"

	"cercano/source/server/internal/agent"
	"cercano/source/server/internal/compaction"
	"cercano/source/server/internal/contextmeter"
	"cercano/source/server/internal/conversation"
	"cercano/source/server/internal/llm"
)

// Config holds the (configurable) compaction thresholds. Defaults are derived
// from the real-session corpus (612 sessions): activate at 40k, freeze 8k
// segments, keep 6 recent turns verbatim.
type Config struct {
	ActivationFloorTokens int
	SegmentTokens         int
	VerbatimRecent        int
}

func DefaultConfig() Config {
	return Config{ActivationFloorTokens: 40000, SegmentTokens: 8000, VerbatimRecent: 6}
}

// BuildSendView assembles the history to send: the consolidated summary preamble
// + the live tail (turns after the frozen boundary), or the full history when
// nothing is frozen yet. Always pairing-valid.
func BuildSendView(turns []conversation.Turn, state conversation.Compaction) ([]llm.Message, error) {
	if state.ConsolidatedJSON == "" {
		return agent.BuildLLMHistory(turns), nil
	}
	var consolidated compaction.StructuredSummary
	if err := json.Unmarshal([]byte(state.ConsolidatedJSON), &consolidated); err != nil {
		// Corrupt state → fail safe to full history.
		return agent.BuildLLMHistory(turns), nil
	}
	live := liveTurns(turns, state.FrozenThrough)
	return compaction.AssembleSendView(consolidated, agent.BuildLLMHistory(live)), nil
}

// liveTurns returns turns strictly after the frozen boundary.
func liveTurns(turns []conversation.Turn, frozenThrough int64) []conversation.Turn {
	out := make([]conversation.Turn, 0, len(turns))
	for _, t := range turns {
		if t.CreatedAt.Unix() > frozenThrough {
			out = append(out, t)
		}
	}
	return out
}

// maxSegmentsPerPass caps how many segments one Advance call summarizes, so a
// large backlog (e.g. after days of failed passes) is digested incrementally —
// each pass fits comfortably inside the generator's runTimeout and progress is
// persisted between passes. The generator reschedules while more remains.
const maxSegmentsPerPass = 4

// reconsolidateThresholdSegments bounds the consolidated summary: when the
// consolidated view renders to more than this many segments' worth of tokens,
// the pass re-consolidates (summarizes the summaries) so compaction output
// shrinks instead of accumulating forever.
const reconsolidateThresholdSegments = 2

// Advance runs one stateful compaction pass. It freezes new segments of the
// eligible (older, un-frozen) history and re-reduces; frozen segments are reused
// untouched. It caps the segments summarized per call at maxSegmentsPerPass and
// reports whether more eligible backlog remains (so the caller reschedules).
// Returns the updated state, whether anything changed, and whether more remains.
// Pure: no I/O. Gates: total < ActivationFloorTokens, or eligible < one
// SegmentTokens → unchanged.
func Advance(ctx context.Context, turns []conversation.Turn, state conversation.Compaction,
	summarize compaction.SummarizeFunc, cfg Config, tok contextmeter.Tokenizer) (conversation.Compaction, bool, bool, error) {

	all := agent.BuildLLMHistory(turns)
	if compaction.TotalTokens(tok, all) < cfg.ActivationFloorTokens {
		return state, false, false, nil // activation gate
	}

	live := liveTurns(turns, state.FrozenThrough)
	if len(live) <= cfg.VerbatimRecent {
		return state, false, false, nil // nothing past the verbatim window
	}
	eligible := live[:len(live)-cfg.VerbatimRecent]

	// Never freeze a turn that shares the first verbatim turn's wall-clock second.
	// FrozenThrough is a second-granularity timestamp and liveTurns keeps turns
	// strictly after it, so a frozen turn at the same second as a live one would
	// exclude that live turn forever (neither frozen nor live → dropped). Trimming
	// keeps the boundary strictly below every live turn. Tool-use bursts routinely
	// persist several turns in one second, so this case is common, not theoretical.
	boundarySec := live[len(live)-cfg.VerbatimRecent].CreatedAt.Unix()
	for len(eligible) > 0 && eligible[len(eligible)-1].CreatedAt.Unix() >= boundarySec {
		eligible = eligible[:len(eligible)-1]
	}
	if len(eligible) == 0 {
		return state, false, false, nil // all eligible share the verbatim window's second; wait
	}

	// Build the eligible messages with per-message provenance back to the source
	// turn: capping works on segments of messages, but FrozenThrough is a turn
	// timestamp, so we must map the capped message boundary to a turn.
	eligibleMsgs, turnIdx := eligibleMessagesWithTurns(eligible)
	if compaction.TotalTokens(tok, eligibleMsgs) < cfg.SegmentTokens {
		return state, false, false, nil // cadence gate — let the tail accumulate
	}

	// Segment the eligible history (after mechanical elision, which preserves
	// message count and order so turnIdx stays aligned) and cap the pass.
	elided, _ := compaction.ElideSupersededToolResults(eligibleMsgs)
	segs := compaction.SegmentByTokens(elided, tok, cfg.SegmentTokens)
	more := false
	coveredMsgs := len(elided)
	if len(segs) > maxSegmentsPerPass {
		more = true
		coveredMsgs = 0
		for _, seg := range segs[:maxSegmentsPerPass] {
			coveredMsgs += len(seg.Messages)
		}
	}
	if coveredMsgs == 0 {
		// more=false intentional: identical input can't progress; rescheduling would spin.
		return state, false, false, nil // nothing to freeze
	}

	// Map the capped message boundary back to the last covered eligible turn.
	b := turnIdx[coveredMsgs-1]
	// Same-second trim at the capped boundary — the identical rule applied to the
	// verbatim boundary above: never freeze a turn that shares its wall-clock
	// second with a turn that will remain live (the next un-covered turn), or
	// liveTurns' strict '>' compare would drop that turn forever.
	for b >= 0 && b+1 < len(eligible) && eligible[b].CreatedAt.Unix() >= eligible[b+1].CreatedAt.Unix() {
		b--
	}
	if b < 0 {
		// more=false intentional: identical input can't progress; rescheduling would spin.
		return state, false, false, nil // capped chunk collapses into one second; wait
	}

	// Freeze exactly the messages belonging to turns eligible[:b+1], so the
	// summarized content and the frozen boundary stay in lockstep (a turn is
	// never summarized here yet left live to be summarized again next pass).
	frozen := 0
	for _, ti := range turnIdx {
		if ti > b {
			break
		}
		frozen++
	}
	if frozen == 0 {
		// A pathological same-second mega-burst can trim b below every covered
		// message's turn: never advance FrozenThrough with zero new summaries.
		// more=false intentional: identical input can't progress; rescheduling would spin.
		return state, false, false, nil
	}
	frozenMsgs := elided[:frozen]

	frozenSegs := compaction.SegmentByTokens(frozenMsgs, tok, cfg.SegmentTokens)
	var newParts []compaction.StructuredSummary
	var segErr error
	for _, seg := range frozenSegs {
		s, err := summarize(ctx, seg.Messages)
		if err != nil {
			segErr = err
			break
		}
		newParts = append(newParts, s)
	}
	if segErr != nil {
		// Keep the segments that DID summarize instead of discarding the whole
		// pass. Without this, a pass whose deadline expires on its last segment
		// loses every completed summary, retries the identical work, and times
		// out again — the scheduler livelocks and the backlog only grows.
		// Coverage shrinks to the largest completed-segment prefix whose
		// recomputed boundary keeps summaries and FrozenThrough in lockstep
		// (the same-second trim must not cut into a kept segment).
		if len(newParts) == 0 {
			return state, false, false, segErr
		}
		ok := false
		for k := len(newParts); k >= 1; k-- {
			covered := 0
			for _, seg := range frozenSegs[:k] {
				covered += len(seg.Messages)
			}
			pb := turnIdx[covered-1]
			for pb >= 0 && pb+1 < len(eligible) && eligible[pb].CreatedAt.Unix() >= eligible[pb+1].CreatedAt.Unix() {
				pb--
			}
			if pb < 0 {
				continue
			}
			pfrozen := 0
			for _, ti := range turnIdx {
				if ti > pb {
					break
				}
				pfrozen++
			}
			if pfrozen != covered {
				continue // trim cut into a kept segment; try fewer segments
			}
			newParts = newParts[:k]
			b, frozen = pb, pfrozen
			frozenMsgs = elided[:frozen]
			more = true
			ok = true
			break
		}
		if !ok {
			return state, false, false, segErr
		}
	}

	// Reuse the already-frozen segment summaries; append the new ones.
	var parts []compaction.StructuredSummary
	if state.SegmentSummariesJSON != "" {
		if err := json.Unmarshal([]byte(state.SegmentSummariesJSON), &parts); err != nil {
			parts = nil // corrupt → rebuild from new
		}
	}
	parts = append(parts, newParts...)

	consolidated := compaction.Reduce(parts)

	// Bound the consolidated summary: if it has grown past a couple segments'
	// worth of tokens, re-consolidate (summarize the summaries) so output shrinks
	// instead of accumulating forever. On failure return the ORIGINAL state so a
	// grown state is never persisted.
	bound := reconsolidateThresholdSegments * cfg.SegmentTokens
	if compaction.TotalTokens(tok, compaction.AssembleSendView(consolidated, nil)) > bound {
		re, err := summarize(ctx, compaction.AssembleSendView(consolidated, nil))
		if err != nil {
			return state, false, false, err
		}
		parts = []compaction.StructuredSummary{re}
		consolidated = re
	}

	segJSON, _ := json.Marshal(parts)
	conJSON, _ := json.Marshal(consolidated)
	newState := conversation.Compaction{
		ConversationID:       state.ConversationID,
		FrozenThrough:        eligible[b].CreatedAt.Unix(),
		SegmentSummariesJSON: string(segJSON),
		ConsolidatedJSON:     string(conJSON),
		CompactedTokens:      state.CompactedTokens + compaction.TotalTokens(tok, frozenMsgs),
	}
	return newState, true, more, nil
}

// eligibleMessagesWithTurns mirrors agent.BuildLLMHistory but additionally
// returns, for each surviving message, the index (into turns) of the source
// turn. We need this provenance to map a capped, message-granular segment
// boundary back to the correct turn for FrozenThrough. It must stay behaviorally
// identical to agent.BuildLLMHistory (same block construction) and to
// llm.RepairPairing (same keep/drop decisions); the pairing repair is inlined
// here only so the turn index can be carried through the drops. BuildLLMHistory
// yields at most one message per turn (zero when a turn is empty or its only
// blocks are orphaned tool blocks), so the returned slices are the same length.
func eligibleMessagesWithTurns(turns []conversation.Turn) ([]llm.Message, []int) {
	type tagged struct {
		msg llm.Message
		idx int
	}
	pre := make([]tagged, 0, len(turns))
	for i, t := range turns {
		role := llm.RoleUser
		switch t.Role {
		case string(llm.RoleAssistant):
			role = llm.RoleAssistant
		case string(llm.RoleSystem):
			role = llm.RoleSystem
		}
		var blocks []llm.Block
		if t.BlocksJSON != "" {
			if err := json.Unmarshal([]byte(t.BlocksJSON), &blocks); err != nil {
				blocks = nil
			}
		}
		if len(blocks) == 0 {
			if t.Content == "" {
				continue
			}
			blocks = []llm.Block{{Type: llm.BlockText, Text: t.Content}}
		}
		pre = append(pre, tagged{llm.Message{Role: role, Blocks: blocks}, i})
	}

	// Mirror llm.RepairPairing, carrying the source-turn index through the same
	// keep/drop decisions.
	useIdx := map[string]int{}
	for i, tg := range pre {
		for _, blk := range tg.msg.Blocks {
			if blk.Type == llm.BlockToolUse {
				if _, ok := useIdx[blk.ToolUseID]; !ok {
					useIdx[blk.ToolUseID] = i
				}
			}
		}
	}
	resolvedAfter := map[string]bool{}
	for i, tg := range pre {
		for _, blk := range tg.msg.Blocks {
			if blk.Type == llm.BlockToolResult {
				if j, ok := useIdx[blk.ToolUseRef]; ok && i > j {
					resolvedAfter[blk.ToolUseRef] = true
				}
			}
		}
	}
	var msgs []llm.Message
	var idxs []int
	for i, tg := range pre {
		kept := make([]llm.Block, 0, len(tg.msg.Blocks))
		for _, blk := range tg.msg.Blocks {
			switch blk.Type {
			case llm.BlockToolUse:
				if !resolvedAfter[blk.ToolUseID] {
					continue
				}
			case llm.BlockToolResult:
				if j, ok := useIdx[blk.ToolUseRef]; !ok || i <= j {
					continue
				}
			}
			kept = append(kept, blk)
		}
		if len(kept) == 0 {
			continue
		}
		msgs = append(msgs, llm.Message{Role: tg.msg.Role, Blocks: kept})
		idxs = append(idxs, tg.idx)
	}
	return msgs, idxs
}
