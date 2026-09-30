package loopcompact

import (
	"context"
	"errors"
	"strings"
	"testing"

	"cercano/source/server/internal/agent"
	"cercano/source/server/internal/compaction"
	"cercano/source/server/internal/compactor"
	"cercano/source/server/internal/llm"
)

// scopedCtx stamps dispatch correlation exactly as the tool loop does before
// an inline pass, so the telemetry test exercises the real attribution path.
func scopedCtx(convID string, iter int) context.Context {
	return agent.WithLoopCompactionScope(context.Background(), agent.LoopCompactionScope{
		ConversationID: convID,
		Iteration:      iter,
	})
}

// Every attempt must emit exactly one metadata-only event whose outcome
// reflects what actually happened, correlated to the dispatch via the scope
// the tool loop stamps on the context.
func TestPassTelemetryOutcomesAndCorrelation(t *testing.T) {
	var events []PassEvent
	rec := func(ev PassEvent) { events = append(events, ev) }

	// 1) Below the activation floor: the common case — nothing ran.
	c := New(Options{Config: compactor.DefaultConfig(), Summarize: countingSummarizer(new(int)), OnPass: rec})
	if c == nil {
		t.Fatal("nil compactor")
	}
	tiny := bigHistory(1, 1)
	out, _, err := c.CompactLoopHistory(scopedCtx("conv-42", 3), tiny)
	if err != nil || len(out) != len(tiny) {
		t.Fatalf("below-floor pass altered history: %v %v", out, err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	ev := events[0]
	if ev.Duration <= 0 {
		t.Fatal("pass duration not measured")
	}
	if ev.Outcome != OutcomeBelowFloor || ev.Reason != reasonBelowFloor {
		t.Fatalf("below-floor outcome: %q reason=%q", ev.Outcome, ev.Reason)
	}
	if ev.ConversationID != "conv-42" || ev.Iteration != 3 {
		t.Fatalf("correlation not carried: conv=%q iter=%d", ev.ConversationID, ev.Iteration)
	}
	if ev.SummarizerCalls != 0 || ev.SpentTokensEstimated != 0 {
		t.Fatalf("below-floor pass billed work: calls=%d spent=%d", ev.SummarizerCalls, ev.SpentTokensEstimated)
	}
	if ev.Enabled != true || ev.ActivationFloorTokens != compactor.DefaultConfig().ActivationFloorTokens {
		t.Fatalf("configured status not reported: %+v", ev)
	}

	// 2) A real pass past the floor: outcome compacted, with the before/after
	// shape of the history and the summarizer's spend.
	hist := bigHistory(60, 60)
	out, _, err = c.CompactLoopHistory(scopedCtx("conv-42", 9), hist)
	if err != nil {
		t.Fatalf("compaction failed: %v", err)
	}
	if len(out) >= len(hist) {
		t.Fatalf("history not reduced: %d -> %d", len(hist), len(out))
	}
	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}
	ev = events[1]
	if ev.Outcome != OutcomeCompacted {
		t.Fatalf("compacted outcome: %q reason=%q", ev.Outcome, ev.Reason)
	}
	if ev.ConversationID != "conv-42" || ev.Iteration != 9 {
		t.Fatalf("correlation not carried: conv=%q iter=%d", ev.ConversationID, ev.Iteration)
	}
	if ev.HistoryMessagesBefore != len(hist) || ev.HistoryMessagesAfter != len(out) {
		t.Fatalf("message counts wrong: %d -> %d (want %d -> %d)", ev.HistoryMessagesBefore, ev.HistoryMessagesAfter, len(hist), len(out))
	}
	if ev.EstimatedTokensAfter >= ev.EstimatedTokensBefore {
		t.Fatalf("token estimates not reduced: %d -> %d", ev.EstimatedTokensBefore, ev.EstimatedTokensAfter)
	}
	if ev.SummarizerCalls == 0 || ev.SpentTokensEstimated <= 0 {
		t.Fatalf("summarizer work not reported: calls=%d spent=%d", ev.SummarizerCalls, ev.SpentTokensEstimated)
	}

	// 3) A failing summarizer: outcome failed with a content-free reason code;
	// the event reports the failure without echoing the raw error.
	failing := New(Options{Config: compactor.DefaultConfig(), Summarize: func(context.Context, []llm.Message) (compaction.StructuredSummary, error) {
		return compaction.StructuredSummary{}, errors.New("backend exploded")
	}, OnPass: rec})
	if failing == nil {
		t.Fatal("nil compactor")
	}
	outFail, _, err := failing.CompactLoopHistory(scopedCtx("conv-7", 1), bigHistory(60, 60))
	if err == nil || len(outFail) == 0 {
		t.Fatalf("failure pass should error and preserve history: %v", err)
	}
	if len(events) != 3 {
		t.Fatalf("expected 3 events, got %d", len(events))
	}
	ev = events[2]
	if ev.Outcome != OutcomeFailed || ev.Reason != "summarizer_error" {
		t.Fatalf("failed outcome: %q reason=%q", ev.Outcome, ev.Reason)
	}
	if ev.HistoryMessagesAfter != ev.HistoryMessagesBefore {
		t.Fatalf("failure event must report preserved history: %d -> %d", ev.HistoryMessagesBefore, ev.HistoryMessagesAfter)
	}
}

// The log line is the grep-friendly, metadata-only rendering: counts, ids and
// codes only, with the correlation leading.
func TestFormatPassEventIsMetadataOnly(t *testing.T) {
	ev := PassEvent{
		ConversationID: "conv-42", Iteration: 9,
		Enabled: true, ActivationFloorTokens: 100, SegmentTokens: 50, VerbatimRecent: 2, CompactedBudgetTokens: 16000,
		HistoryMessagesBefore: 80, HistoryMessagesAfter: 12,
		EstimatedTokensBefore: 9000, EstimatedTokensAfter: 1200,
		Outcome: OutcomeCompacted, SummarizerCalls: 3, SpentTokensEstimated: 400,
	}
	line := FormatPassEvent(ev)
	for _, want := range []string{
		"conv=conv-42", "iter=9", "outcome=compacted",
		"history_messages=80->12", "est_tokens=9000->1200",
		"summarizer_calls=3", "spent_tokens_est=400",
	} {
		if !strings.Contains(line, want) {
			t.Fatalf("log line missing %q: %s", want, line)
		}
	}
}
