package server

// worker_postturn_test.go — the host-side post-turn bookkeeping for worker
// turns. In-process, runner.Core does context-usage / recap / compaction / usage
// telemetry inside RunTurn via c.d.Agent; the worker child has no Agent and
// skips all of it, so the server compensates in workerPostTurn. Without this,
// worker mode silently loses auto-titles, compaction triggering, the context
// meter, and cost/usage telemetry.

import (
	"context"
	"testing"

	runnersvc "cercano/source/server/internal/runner"
	"cercano/source/server/internal/usage"
	proto "cercano/source/server/pkg/proto"
)

func TestWorkerPostTurn_EmitsUsageAndAdvancesMeter(t *testing.T) {
	srv, store := newServerWithStore(t)
	ctx := context.Background()
	if err := store.EnsureConversation(ctx, "conv-wpt", "", "test-model"); err != nil {
		t.Fatalf("EnsureConversation: %v", err)
	}

	var got []usage.Usage
	srv.SetUsageSink(func(u usage.Usage) { got = append(got, u) })

	// The aggregate result a worker turn returns (token totals + model tier).
	srv.workerPostTurn("conv-wpt", runnersvc.Result{
		Model: "m", IsCloud: true, InputTokens: 4321, OutputTokens: 99,
	})

	// Usage telemetry fired (the previously-missing #4): worker turns emit one
	// aggregate usage event so cost stats don't zero out. Reaching the sink also
	// proves RecordContextUsage/ScheduleRecap/ScheduleCompaction ran before it
	// (sequential, no early return between them).
	if len(got) != 1 {
		t.Fatalf("usage sink fired %d times, want 1 (worker turns must emit an aggregate usage event)", len(got))
	}
	if got[0].InputTokens != 4321 || got[0].OutputTokens != 99 {
		t.Errorf("usage tokens = %d/%d, want 4321/99", got[0].InputTokens, got[0].OutputTokens)
	}
	if got[0].Source != "main" || !got[0].IsCloud {
		t.Errorf("usage source/isCloud = %q/%v, want \"main\"/true", got[0].Source, got[0].IsCloud)
	}

	// Context meter advanced: RecordContextUsage set the model max (functional
	// gap #1 — without it reactive auto-compaction never triggers in worker mode).
	resp, err := srv.GetContextUsage(ctx, &proto.GetContextUsageRequest{ConversationId: "conv-wpt"})
	if err != nil {
		t.Fatalf("GetContextUsage: %v", err)
	}
	if resp.ModelMax <= 0 {
		t.Errorf("ModelMax = %d, want > 0 (RecordContextUsage should have set the meter window)", resp.ModelMax)
	}
}

func TestWorkerPostTurn_NoopWithoutConversation(t *testing.T) {
	srv, _ := newServerWithStore(t)
	fired := 0
	srv.SetUsageSink(func(usage.Usage) { fired++ })

	// Empty conversation id -> no-op (mirrors the runner's persistEnabled guard).
	srv.workerPostTurn("", runnersvc.Result{InputTokens: 10, OutputTokens: 5})

	if fired != 0 {
		t.Errorf("usage sink fired %d times for an empty conversation id, want 0", fired)
	}
}
