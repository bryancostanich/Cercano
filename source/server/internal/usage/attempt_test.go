package usage

import (
	"context"
	"sync"
	"testing"

	"cercano/source/server/internal/llm"
)

func TestAttemptLifecycleSnapshots(t *testing.T) {
	var got []AttemptObservation
	ctx := WithAttempts(context.Background(), func(o AttemptObservation) bool { got = append(got, o); return true }, Attribution{Source: "vision", OperationID: "op", SessionID: "session", ConversationID: "conversation", WorkerID: "worker"})
	a := StartAttempt(ctx, "requested-provider", "requested-model")
	snapshot := llm.TokenUsage{Input: llm.ReportedTokens(11), Output: llm.ReportedTokens(7)}
	route := &llm.ServingRoute{Provider: "actual-provider", Model: "actual-model", Profile: "backup", Destination: "cloud"}
	a.Observe(snapshot, route)
	a.Observe(snapshot, nil) // cumulative, not 22/14
	route.Model = "mutated"  // emitted metadata must be copied
	a.Finish(Failed)
	a.Finish(Completed)
	a.Observe(llm.TokenUsage{Output: llm.ReportedTokens(99)}, nil)
	if len(got) != 3 {
		t.Fatalf("observations=%d; want start, changed snapshot, terminal", len(got))
	}
	if got[0].Outcome != Started || got[0].Tokens.TotalsKnown() {
		t.Fatalf("start fabricated counts: %+v", got[0])
	}
	last := got[2]
	if last.Outcome != Failed || last.Tokens.Input.Value != 11 || last.Tokens.Output.Value != 7 || last.Model != "actual-model" {
		t.Fatalf("terminal=%+v", last)
	}
	if last.Attribution.Source != "vision" || last.StartedAt.Location() != last.EndedAt.Location() || last.EndedAt.Before(last.StartedAt) {
		t.Fatalf("metadata=%+v", last)
	}
	for i, o := range got {
		if o.ID == "" || o.ID != last.ID || o.Revision != uint64(i+1) {
			t.Fatalf("unstable identity/revision %+v", got)
		}
	}
}

func TestAttemptUnknownAndReportedZero(t *testing.T) {
	var got []AttemptObservation
	ctx := WithAttempts(context.Background(), func(o AttemptObservation) bool { got = append(got, o); return true }, Attribution{})
	a := StartAttempt(ctx, "provider", "model")
	a.Finish(Interrupted)
	b := StartAttempt(ctx, "provider", "model")
	b.Observe(llm.TokenUsage{Input: llm.ReportedTokens(0), Output: llm.ReportedTokens(0)}, nil)
	b.Finish(Completed)
	if got[1].Tokens.TotalsKnown() {
		t.Fatal("interruption fabricated zero usage")
	}
	if !got[len(got)-1].Tokens.TotalsKnown() {
		t.Fatal("reported zero lost")
	}
	if got[0].ID == got[2].ID {
		t.Fatal("separate attempts share identity")
	}
}

func TestAttemptConcurrentFinish(t *testing.T) {
	var mu sync.Mutex
	var got []AttemptObservation
	ctx := WithAttempts(context.Background(), func(o AttemptObservation) bool { mu.Lock(); defer mu.Unlock(); got = append(got, o); return true }, Attribution{})
	a := StartAttempt(ctx, "provider", "model")
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			a.Observe(llm.TokenUsage{Input: llm.ReportedTokens(11)}, nil)
			a.Finish(Interrupted)
		}()
	}
	wg.Wait()
	terminals := 0
	for _, o := range got {
		if o.Outcome != Started {
			terminals++
		}
	}
	if terminals != 1 {
		t.Fatalf("terminal observations=%d", terminals)
	}
}

func TestAttemptNilSink(t *testing.T) {
	a := StartAttempt(context.Background(), "provider", "model")
	if a != nil {
		t.Fatal("disabled accounting allocated attempt")
	}
	a.Observe(llm.TokenUsage{}, nil)
	a.Finish(Completed)
}

func TestOperationScopePreservesAttribution(t *testing.T) {
	var got []AttemptObservation
	sink := func(a AttemptObservation) bool { got = append(got, a); return true }
	parent := WithAttempts(t.Context(), sink, Attribution{OperationID: "parent", Source: "main", ConversationID: "conversation", SessionID: "session", WorkerID: "worker"})
	first := ForOperation(parent, nil, "vision", "")
	second := ForOperation(parent, nil, "research", "")
	StartAttempt(first, "provider", "model").Finish(Completed)
	StartAttempt(second, "provider", "model").Finish(Completed)
	a, b := got[0].Attribution, got[2].Attribution
	if a.OperationID == "parent" || a.OperationID == b.OperationID || a.ConversationID != "conversation" || a.SessionID != "session" || a.WorkerID != "worker" || a.Source != "vision" || b.Source != "research" {
		t.Fatalf("nested attribution: %+v %+v", a, b)
	}
	disabled := t.Context()
	if ForOperation(disabled, nil, "vision", "") != disabled {
		t.Fatal("disabled accounting allocated a context")
	}
}

func TestIdenticalAttemptSnapshotsDoNotCreateQueueTraffic(t *testing.T) {
	var observations []AttemptObservation
	ctx := WithAttempts(t.Context(), func(a AttemptObservation) bool { observations = append(observations, a); return true }, Attribution{Source: "main"})
	a := StartAttempt(ctx, "provider", "model")
	tokens := llm.TokenUsage{Input: llm.ReportedTokens(11), Output: llm.ReportedTokens(0)}
	a.Observe(tokens, nil)
	a.Observe(tokens, nil)
	a.Finish(Completed)
	// One start + one changed snapshot + one final, not another revision merely
	// because a streaming text chunk repeated identical cumulative usage.
	if len(observations) != 3 {
		t.Fatalf("identical snapshot emitted: got %d observations, want 3", len(observations))
	}
	if observations[2].Revision != 3 {
		t.Fatalf("no-op changed revision: %d", observations[2].Revision)
	}
}

func TestRouteOnlyChangeStillEmitsObservation(t *testing.T) {
	var got []AttemptObservation
	ctx := WithAttempts(t.Context(), func(a AttemptObservation) bool { got = append(got, a); return true }, Attribution{})
	a := StartAttempt(ctx, "provider", "requested")
	tokens := llm.TokenUsage{Input: llm.ReportedTokens(11)}
	a.Observe(tokens, nil)
	route := &llm.ServingRoute{Provider: "provider", Model: "actual"}
	a.Observe(tokens, route)
	a.Observe(tokens, route)
	a.Finish(Completed)
	if len(got) != 4 || got[2].Model != "actual" || got[3].Tokens.Input.Value != 11 {
		t.Fatalf("route update lost or tokens added twice: %+v", got)
	}
}

func TestFinalityOnlyObservationIsEmitted(t *testing.T) {
	var got []AttemptObservation
	ctx := WithAttempts(t.Context(), func(a AttemptObservation) bool { got = append(got, a); return true }, Attribution{})
	a := StartAttempt(ctx, "fake", "fake")
	partial := llm.TokenUsage{Input: llm.ReportedTokens(11), Output: llm.ReportedTokens(0)}
	a.Observe(partial, nil)
	final := partial
	final.Final = true
	a.Observe(final, nil)
	a.Observe(final, nil)
	a.Finish(Failed)
	if len(got) != 4 || got[1].Tokens.Final || !got[2].Tokens.Complete() || !got[3].Tokens.Complete() || got[3].Outcome != Failed {
		t.Fatalf("observations=%+v", got)
	}
	if got[2].Revision != got[1].Revision+1 {
		t.Fatal("finality did not advance revision")
	}
}
