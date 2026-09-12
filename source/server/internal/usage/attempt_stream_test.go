package usage

import (
	"context"
	"errors"
	"testing"

	"cercano/source/server/internal/llm"
)

type attemptScriptRead struct {
	event llm.StreamEvent
	ok    bool
	err   error
}
type attemptScript struct {
	reads  []attemptScriptRead
	closes int
}

func (s *attemptScript) Next() (llm.StreamEvent, bool, error) {
	if len(s.reads) == 0 {
		return llm.StreamEvent{}, false, nil
	}
	next := s.reads[0]
	s.reads = s.reads[1:]
	return next.event, next.ok, next.err
}
func (s *attemptScript) Close() error { s.closes++; return nil }

func TestAttemptStreamTerminals(t *testing.T) {
	for _, tc := range []struct {
		name string
		read attemptScriptRead
		want Outcome
	}{
		{"stop", attemptScriptRead{event: llm.StreamEvent{Type: llm.EventMessageStop}, ok: true}, Completed},
		{"error with usage", attemptScriptRead{event: llm.StreamEvent{Usage: llm.TokenUsage{Output: llm.ReportedTokens(7)}}, err: errors.New("failed")}, Failed},
		{"event error", attemptScriptRead{event: llm.StreamEvent{Type: llm.EventError, Err: errors.New("failed")}, ok: true}, Failed},
		{"cancel", attemptScriptRead{err: context.Canceled}, Interrupted},
		{"truncated eof", attemptScriptRead{}, Interrupted},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var got []AttemptObservation
			ctx := WithAttempts(context.Background(), func(o AttemptObservation) bool { got = append(got, o); return true }, Attribution{})
			a := StartAttempt(ctx, "provider", "model")
			inner := &attemptScript{reads: []attemptScriptRead{{event: llm.StreamEvent{Type: llm.EventMessageStart, Usage: llm.TokenUsage{Input: llm.ReportedTokens(11)}}, ok: true}, tc.read}}
			r := a.TrackStream(inner)
			r.Next()
			r.Next()
			last := got[len(got)-1]
			if last.Outcome != tc.want || last.Tokens.Input != llm.ReportedTokens(11) {
				t.Fatalf("terminal=%+v", last)
			}
			if tc.name == "error with usage" && last.Tokens.Output != llm.ReportedTokens(7) {
				t.Fatal("lost error usage")
			}
			n := len(got)
			r.Close()
			r.Close()
			r.Next()
			if len(got) != n || inner.closes != 1 {
				t.Fatalf("non-idempotent terminal/Close: observations=%d closes=%d", len(got), inner.closes)
			}
		})
	}
}

func TestAttemptStreamEarlyClose(t *testing.T) {
	var last AttemptObservation
	ctx := WithAttempts(context.Background(), func(o AttemptObservation) bool { last = o; return true }, Attribution{})
	r := StartAttempt(ctx, "provider", "model").TrackStream(&attemptScript{})
	r.Close()
	if last.Outcome != Interrupted || last.Tokens.TotalsKnown() {
		t.Fatalf("early close=%+v", last)
	}
}

func TestAttemptResponseErrorPreservesUsage(t *testing.T) {
	var last AttemptObservation
	ctx := WithAttempts(context.Background(), func(o AttemptObservation) bool { last = o; return true }, Attribution{})
	a := StartAttempt(ctx, "provider", "requested")
	a.FinishResponse(llm.ChatResponse{Model: "actual", Usage: llm.TokenUsage{Input: llm.ReportedTokens(11), Output: llm.ReportedTokens(0)}}, errors.New("failed"))
	if last.Outcome != Failed || last.Model != "actual" || !last.Tokens.TotalsKnown() {
		t.Fatalf("error response=%+v", last)
	}
}
