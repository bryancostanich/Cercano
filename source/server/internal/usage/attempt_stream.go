package usage

import (
	"context"
	"errors"
	"sync"

	"cercano/source/server/internal/llm"
)

// FinishResponse records counts even when a provider returns an error alongside
// a partial response. Adapters call this for each physical non-streaming attempt.
func (a *Attempt) FinishResponse(response llm.ChatResponse, err error) {
	route := response.Route
	if route == nil && response.Model != "" {
		route = &llm.ServingRoute{Model: response.Model}
	}
	a.Observe(response.Usage, route)
	if err != nil {
		a.Finish(errorOutcome(err))
		return
	}
	a.Finish(Completed)
}

func errorOutcome(err error) Outcome {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return Interrupted
	}
	return Failed
}

// TrackStream belongs INSIDE retries, around a physical adapter stream only.
// It neither creates another attempt nor changes the provider's returned events.
func (a *Attempt) TrackStream(inner llm.StreamReader) llm.StreamReader {
	if a == nil {
		return inner
	}
	return &attemptReader{inner: inner, attempt: a}
}

type attemptReader struct {
	inner     llm.StreamReader
	attempt   *Attempt
	closeOnce sync.Once
	closeErr  error
}

func (r *attemptReader) Next() (llm.StreamEvent, bool, error) {
	ev, ok, err := r.inner.Next()
	// Error events may carry the last available usage; capture BEFORE examining
	// err or ok. Do not infer presence from a nonzero legacy context counter.
	if ev.Usage != (llm.TokenUsage{}) || ev.Route != nil {
		r.attempt.Observe(ev.Usage, ev.Route)
	}
	switch {
	case err != nil:
		r.attempt.Finish(errorOutcome(err))
	case ev.Type == llm.EventError:
		r.attempt.Finish(errorOutcome(ev.Err))
	case ev.Type == llm.EventMessageStop:
		r.attempt.Finish(Completed)
	case !ok:
		r.attempt.Finish(Interrupted) // EOF without terminal framing
	}
	return ev, ok, err
}

func (r *attemptReader) Close() error {
	r.closeOnce.Do(func() {
		// Finalize before potentially blocking provider cleanup. Prior terminal
		// delivery wins; an early close leaves final usage explicitly incomplete.
		r.attempt.Finish(Interrupted)
		r.closeErr = r.inner.Close()
	})
	return r.closeErr
}
