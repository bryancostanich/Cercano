// Package compactor orchestrates stateful, frozen-segment context compaction
// over conversation turns + persisted state, reusing the compaction primitives.
// It is pure over its inputs; persistence and triggering live in their callers.
package compactor

import (
	"context"
	"encoding/json"
	"fmt"

	"cercano/source/server/internal/agent"
	"cercano/source/server/internal/compaction"
	"cercano/source/server/internal/contextmeter"
	"cercano/source/server/internal/conversation"
	"cercano/source/server/internal/llm"
)

// Config holds the (configurable) compaction thresholds. Defaults are derived
// from the real-session corpus (612 sessions): activate at 40k, freeze 8k
// segments, keep 6 recent turns verbatim.
//
// CompactedBudgetTokens bounds the whole compacted backlog (the consolidated
// send-view preamble). When the merged ledger renders larger than this, the
// pass shrinks it deterministically (see pruneToFit) instead of re-summarizing
// it. It is a token budget, not a segment count: callers derive it from the
// active model's context window (e.g. a fraction of ModelMax) so a 200k-window
// model isn't crushed to the old fixed 16k. Zero falls back to the legacy
// segment-relative bound so existing construction sites keep working.
//
// TieredRetentionSegments enables gentle, always-on degradation for very long
// sessions (the fallback when a user declines a session rollover). When set to
// R > 0, the newest R segment summaries are kept verbatim, older-but-not-oldest
// segments shed transient chatter (Proposals, OpenThreads), and the oldest
// segments keep only their durable, actionable recall (Goal, State, Files).
// This shapes the ledger by age BEFORE it is Reduced and before the hard
// CompactedBudgetTokens backstop fires, so recent-tail fidelity is preserved
// structurally rather than incidentally. Zero disables tiering (the ledger is
// merged whole and only the hard budget bounds it).
type Config struct {
	ActivationFloorTokens   int
	SegmentTokens           int
	VerbatimRecent          int
	CompactedBudgetTokens   int
	TieredRetentionSegments int
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

// legacyBoundSegments is the fallback bound (in SegmentTokens units) used only
// when Config.CompactedBudgetTokens is unset (0). Live construction sites derive
// a window-relative budget instead; this keeps zero-value Configs (older tests,
// ad-hoc callers) working with the historical behavior's shape.
const legacyBoundSegments = 4

// budgetTokens resolves the token ceiling for the consolidated backlog: the
// explicit window-relative budget when set, else a segment-relative fallback.
func budgetTokens(cfg Config) int {
	if cfg.CompactedBudgetTokens > 0 {
		return cfg.CompactedBudgetTokens
	}
	seg := cfg.SegmentTokens
	if seg <= 0 {
		seg = DefaultConfig().SegmentTokens
	}
	return legacyBoundSegments * seg
}

// pruneToFit shrinks a consolidated summary under a token budget by dropping
// whole ledger entries in recency order (oldest first) until the rendered
// send-view fits. It never paraphrases: an entry is kept verbatim or removed
// whole. Goal and State (single lines, high-signal) are preserved. Files are
// pruned last because a file's "latest state" is often the most actionable
// recall. If even the skeleton exceeds budget the summary is returned as-is —
// dropping Goal/State to hit a byte target would defeat the purpose.
func pruneToFit(s compaction.StructuredSummary, budget int, tok contextmeter.Tokenizer) compaction.StructuredSummary {
	fits := func(v compaction.StructuredSummary) bool {
		return compaction.TotalTokens(tok, compaction.AssembleSendView(v, nil)) <= budget
	}
	if fits(s) {
		return s
	}
	// Prune order: OpenThreads (transient), then Proposals, Decisions, Files —
	// oldest entries first within each. dropOldest removes one entry from the
	// front (the earliest-seen, since MergeSummaries appends in chronological
	// order) of the first non-empty list in this priority.
	for !fits(s) {
		switch {
		case len(s.OpenThreads) > 0:
			s.OpenThreads = s.OpenThreads[1:]
		case len(s.Proposals) > 0:
			s.Proposals = s.Proposals[1:]
		case len(s.Decisions) > 0:
			s.Decisions = s.Decisions[1:]
		case len(s.Files) > 0:
			s.Files = dropOldestFile(s.Files)
		default:
			return s // only Goal/State left — return rather than gut it
		}
	}
	return s
}

// dropOldestFile removes one file entry. Map iteration order is unstable, so it
// drops the lexically-first path for determinism (the alternative — tracking
// insertion order — would require changing Files' type across the codebase).
func dropOldestFile(files map[string]string) map[string]string {
	if len(files) == 0 {
		return files
	}
	var first string
	for p := range files {
		if first == "" || p < first {
			first = p
		}
	}
	delete(files, first)
	return files
}

// applyTieredRetention shapes an ordered (oldest→newest) list of per-segment
// summaries by age, so a very long session degrades gently rather than hitting
// the hard budget as a cliff. It is the fallback engaged when a user declines a
// session rollover: the active thread stays high-fidelity while ancient history
// compresses.
//
// Three tiers, split by position in the list:
//   - recent  (the newest `recent` segments): kept verbatim.
//   - middle  (everything between): Proposals and OpenThreads dropped — these
//     are transient ("awaiting decision", "next steps") and least useful once
//     the conversation has moved well past them. Decisions/Files/Goal/State stay.
//   - ancient (the oldest, when there are more than 2*recent segments): keep
//     only the durable, actionable recall — Goal, State, Files. Decisions,
//     Proposals, OpenThreads dropped.
//
// Goal, State, and Files survive every tier, so MergeSummaries' Goal
// (first-non-empty), State (last-non-empty) and Files (union) semantics are
// never starved. Pure and deterministic: a fixed function of the input list.
// recent<=0 is a no-op (tiering disabled). It returns a new slice; inputs are
// not mutated (Files maps are shallow-copied before keys are dropped).
func applyTieredRetention(parts []compaction.StructuredSummary, recent int) []compaction.StructuredSummary {
	if recent <= 0 || len(parts) <= recent {
		return parts // nothing old enough to compress
	}
	// Ancient tier only opens once there is a full middle band behind the recent
	// window, so a moderately long session degrades in one step (middle) before
	// the harsher ancient step ever applies.
	ancientEnd := 0
	if len(parts) > 2*recent {
		ancientEnd = len(parts) - 2*recent
	}
	recentStart := len(parts) - recent

	out := make([]compaction.StructuredSummary, len(parts))
	for i, s := range parts {
		switch {
		case i >= recentStart:
			out[i] = s // recent: verbatim
		case i < ancientEnd:
			out[i] = compaction.StructuredSummary{
				Goal:  s.Goal,
				State: s.State,
				Files: copyFiles(s.Files),
			}
		default: // middle
			out[i] = compaction.StructuredSummary{
				Goal:      s.Goal,
				State:     s.State,
				Files:     copyFiles(s.Files),
				Decisions: s.Decisions,
			}
		}
	}
	return out
}

// copyFiles shallow-copies a Files map so tier-stripping a segment never mutates
// the caller's stored summaries. A nil map stays nil (Reduce tolerates it).
func copyFiles(m map[string]string) map[string]string {
	if m == nil {
		return nil
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

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
		if s.IsEmpty() {
			// A nil-error empty summary is a summarizer failure in disguise
			// (zero-token cloud completion, unparseable model output). Freezing
			// the segment behind it would silently drop the content from every
			// future send-view — fail the pass loudly instead; the raw turns
			// stay eligible for the next attempt. (63 consecutive segments were
			// lost this way on 2026-07-15 before this guard existed.)
			segErr = fmt.Errorf("summarizer returned an empty summary for a %d-message segment — refusing to freeze content behind nothing", len(seg.Messages))
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

	// Age-shape the ledger before reducing: the recent tail stays verbatim while
	// older segments shed transient (then all-but-durable) detail. This is the
	// gentle-degradation fallback for sessions that decline a rollover; it runs
	// ahead of the hard-budget pruneToFit backstop below and, when disabled
	// (TieredRetentionSegments==0), is a no-op. We reduce a tiered *view* but do
	// NOT persist it back over `parts` — the raw per-segment summaries stay on
	// disk so a later, larger-window model (or a relaxed config) can re-derive a
	// richer consolidation. Only the hard-budget path (below) collapses parts.
	tieredParts := applyTieredRetention(parts, cfg.TieredRetentionSegments)
	consolidated := compaction.Reduce(tieredParts)

	// Bound the consolidated summary against the compaction budget. When it has
	// grown past budget, shrink it DETERMINISTICALLY (pruneToFit) rather than
	// re-summarizing the summaries. The old path fed the already-structured
	// ledger back through the free-text summarizer, which — because the input is
	// already reduced — could only paraphrase or invent (the exact defect
	// reduce.go removed from Reduce), and did so on every re-cross, eroding
	// detail monotonically. Deterministic pruning drops whole entries (recency
	// order) so load-bearing shapes (signatures, config, tier lists) survive
	// verbatim or not at all — never mangled into prose.
	bound := budgetTokens(cfg)
	if compaction.TotalTokens(tok, compaction.AssembleSendView(consolidated, nil)) > bound {
		consolidated = pruneToFit(consolidated, bound, tok)
		// Collapse the per-segment parts into the single pruned ledger: the
		// budget applies to the consolidated view, and keeping the fat
		// pre-prune parts would just re-inflate on the next Reduce.
		parts = []compaction.StructuredSummary{consolidated}
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
