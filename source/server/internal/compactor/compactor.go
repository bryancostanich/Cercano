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

// Advance runs one stateful compaction pass. It freezes new segments of the
// eligible (older, un-frozen) history and re-reduces; frozen segments are reused
// untouched. Returns the updated state and whether anything changed. Pure: no
// I/O. Gates: total < ActivationFloorTokens, or eligible < one SegmentTokens →
// unchanged.
func Advance(ctx context.Context, turns []conversation.Turn, state conversation.Compaction,
	summarize compaction.SummarizeFunc, cfg Config, tok contextmeter.Tokenizer) (conversation.Compaction, bool, error) {

	all := agent.BuildLLMHistory(turns)
	if compaction.TotalTokens(tok, all) < cfg.ActivationFloorTokens {
		return state, false, nil // activation gate
	}

	live := liveTurns(turns, state.FrozenThrough)
	if len(live) <= cfg.VerbatimRecent {
		return state, false, nil // nothing past the verbatim window
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
		return state, false, nil // all eligible share the verbatim window's second; wait
	}

	eligibleMsgs := agent.BuildLLMHistory(eligible)
	if compaction.TotalTokens(tok, eligibleMsgs) < cfg.SegmentTokens {
		return state, false, nil // cadence gate — let the tail accumulate
	}

	// Map each new segment from raw (after mechanical elision).
	elided, _ := compaction.ElideSupersededToolResults(eligibleMsgs)
	var newParts []compaction.StructuredSummary
	for _, seg := range compaction.SegmentByTokens(elided, tok, cfg.SegmentTokens) {
		s, err := summarize(ctx, seg.Messages)
		if err != nil {
			return state, false, err
		}
		newParts = append(newParts, s)
	}

	// Reuse the already-frozen segment summaries; append the new ones.
	var parts []compaction.StructuredSummary
	if state.SegmentSummariesJSON != "" {
		if err := json.Unmarshal([]byte(state.SegmentSummariesJSON), &parts); err != nil {
			parts = nil // corrupt → rebuild from new
		}
	}
	parts = append(parts, newParts...)

	consolidated, err := compaction.Reduce(ctx, parts, summarize)
	if err != nil {
		return state, false, err
	}

	segJSON, _ := json.Marshal(parts)
	conJSON, _ := json.Marshal(consolidated)
	newState := conversation.Compaction{
		ConversationID:       state.ConversationID,
		FrozenThrough:        eligible[len(eligible)-1].CreatedAt.Unix(),
		SegmentSummariesJSON: string(segJSON),
		ConsolidatedJSON:     string(conJSON),
		CompactedTokens:      state.CompactedTokens + compaction.TotalTokens(tok, elided),
	}
	return newState, true, nil
}
