// Package compactor orchestrates stateful, frozen-segment context compaction
// over conversation turns + persisted state, reusing the compaction primitives.
// It is pure over its inputs; persistence and triggering live in their callers.
package compactor

import (
	"encoding/json"

	"cercano/source/server/internal/agent"
	"cercano/source/server/internal/compaction"
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
