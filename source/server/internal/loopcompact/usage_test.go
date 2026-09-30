package loopcompact

import (
	"cercano/source/server/internal/compaction"
	"cercano/source/server/internal/compactor"
	"cercano/source/server/internal/inference"
	"cercano/source/server/internal/llm"
	"cercano/source/server/pkg/config"
	"context"
	"errors"
	"testing"
)

func TestReportedSummarizerUsageReplacesEstimate(t *testing.T) {
	for _, failure := range []bool{false, true} {
		calls := 0
		c := New(Options{Config: compactor.Config{ActivationFloorTokens: 1, SegmentTokens: 100, VerbatimRecent: 2}, Summarize: func(ctx context.Context, _ []llm.Message) (compaction.StructuredSummary, error) {
			calls++
			compaction.RecordSummaryUsage(ctx, compaction.SummaryUsage{ReportedInput: 11, ReportedOutput: 6, Calls: 1})
			if failure {
				return compaction.StructuredSummary{}, errors.New("failed after reported usage")
			}
			return compaction.StructuredSummary{Goal: "task"}, nil
		}})
		_, spent, _ := c.CompactLoopHistory(t.Context(), bigHistory(8, 60))
		if calls == 0 || spent != calls*17 {
			t.Fatalf("failure=%v charged=%d want=%d", failure, spent, calls*17)
		}
	}
}

type usageSummaryProvider struct {
	policyProvider
	response llm.ChatResponse
}

func (p *usageSummaryProvider) Chat(context.Context, llm.ChatRequest) (llm.ChatResponse, error) {
	return p.response, p.err
}
func TestProductionSummaryUsageSources(t *testing.T) {
	for _, tc := range []struct {
		name                string
		usage               llm.TokenUsage
		legacyIn, legacyOut int
		reported            int
		estimate            bool
		fail                bool
	}{
		{name: "normalized includes cache", usage: llm.TokenUsage{Input: llm.ReportedTokens(30), Output: llm.ReportedTokens(7), CacheRead: llm.ReportedTokens(20)}, legacyIn: 10, legacyOut: 7, reported: 37},
		{name: "known zero", usage: llm.TokenUsage{Input: llm.ReportedTokens(0), Output: llm.ReportedTokens(0)}},
		{name: "legacy", legacyIn: 10, legacyOut: 7, reported: 17},
		{name: "unreported", estimate: true},
		{name: "failed", estimate: true, fail: true},
		{name: "failed with reported usage", usage: llm.TokenUsage{Input: llm.ReportedTokens(9), Output: llm.ReportedTokens(3)}, reported: 12, fail: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := &usageSummaryProvider{response: llm.ChatResponse{Blocks: []llm.Block{{Type: llm.BlockText, Text: "GOAL: task"}}, InputTokens: tc.legacyIn, OutputTokens: tc.legacyOut, Usage: tc.usage}}
			if tc.fail {
				p.err = errors.New("transport failure")
			}
			summarize := BuildSummarizer(WiringDeps{Candidates: func() inference.Tiers {
				return inference.Tiers{Destinations: map[config.Destination]inference.Candidate{config.DestinationSecondary: {Provider: p, IsCloud: true}}}
			}})
			ctx, meter := compaction.WithUsageMeter(t.Context())
			_, err := summarize(ctx, []llm.Message{{Role: llm.RoleUser, Blocks: []llm.Block{{Type: llm.BlockText, Text: "work"}}}})
			if (err != nil) != tc.fail {
				t.Fatal(err)
			}
			got := meter.Snapshot()
			if got.Calls != 1 || got.Reported() != tc.reported || (got.Estimated() > 0) != tc.estimate {
				t.Fatalf("usage=%+v", got)
			}
		})
	}
}

func TestRoutingFailureBeforeSummarizerDoesNotConsumeBudget(t *testing.T) {
	summarize := BuildSummarizer(WiringDeps{Candidates: func() inference.Tiers { return inference.Tiers{} }})
	c := New(Options{Config: compactor.Config{ActivationFloorTokens: 1, SegmentTokens: 100, VerbatimRecent: 2}, Summarize: summarize})
	_, spent, err := c.CompactLoopHistory(t.Context(), bigHistory(8, 60))
	if err == nil || spent != 0 {
		t.Fatalf("unattempted provider charged: spent=%d err=%v", spent, err)
	}
}
