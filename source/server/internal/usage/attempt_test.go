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
	if len(got) != 4 {
		t.Fatalf("observations=%d; want start, two snapshots, terminal", len(got))
	}
	if got[0].Outcome != Started || got[0].Tokens.TotalsKnown() {
		t.Fatalf("start fabricated counts: %+v", got[0])
	}
	last := got[3]
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
