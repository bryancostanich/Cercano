// Package usage records per-call LLM token usage at the provider boundary.
// A RecordingProvider decorates an inference.Provider; every completed Chat or
// fully-drained StreamChat reports one Usage to a sink. This is the single
// chokepoint for cost telemetry across the main loop, co-processor work, and
// dispatched subagents (each provider is exactly one of cloud/local, so the
// tier is known for free). The live-context meter is a separate system and is
// unaffected.
package usage

import (
	"context"
	"time"

	"cercano/source/server/internal/inference"
	"cercano/source/server/internal/llm"
)

// Usage is one recorded model call.
type Usage struct {
	Source               string // who initiated it, e.g. "main", "coproc:summarize", "dispatch"
	Model                string
	Provider             string // provider name, e.g. "anthropic", "openai-responses", "ollama"
	IsCloud              bool
	InputTokens          int
	OutputTokens         int
	ContentTokensAvoided int  // estimated cloud tokens saved by handling locally
	TokenSaving          bool // true when this call substitutes for a cloud call

	// DurationMs is wall-clock latency for the call. For Chat it spans the
	// round trip. For StreamChat it spans StreamChat() to stream exhaustion —
	// i.e. the full generation, not time-to-first-token — because that is the
	// number that explains a slow turn.
	DurationMs int64
}

// Wrap returns a provider that reports a Usage to sink after each call. sink
// must tolerate being called from the goroutine that drains a stream; it is
// nil-safe (a nil sink disables recording).
func Wrap(p inference.Provider, source string, isCloud bool, sink func(Usage)) inference.Provider {
	return &recordingProvider{inner: p, source: source, isCloud: isCloud, sink: sink}
}

type recordingProvider struct {
	inner   inference.Provider
	source  string
	isCloud bool
	sink    func(Usage)
}

func (r *recordingProvider) Name() string                         { return r.inner.Name() }
func (r *recordingProvider) Capabilities() inference.Capabilities { return r.inner.Capabilities() }

func (r *recordingProvider) Chat(ctx context.Context, req llm.ChatRequest) (llm.ChatResponse, error) {
	start := time.Now()
	resp, err := r.inner.Chat(ctx, req)
	if err == nil {
		r.report(req.Model, resp.InputTokens, resp.OutputTokens, time.Since(start))
	}
	return resp, err
}

func (r *recordingProvider) StreamChat(ctx context.Context, req llm.ChatRequest) (llm.StreamReader, error) {
	// Start the clock before StreamChat so connection setup is charged to the
	// call: for a slow provider that wait is a real part of the latency.
	start := time.Now()
	inner, err := r.inner.StreamChat(ctx, req)
	if err != nil {
		return nil, err
	}
	return &recordingReader{inner: inner, model: req.Model, rp: r, start: start}, nil
}

func (r *recordingProvider) report(model string, in, out int, d time.Duration) {
	if r.sink == nil {
		return
	}
	r.sink(Usage{
		Source:       r.source,
		Model:        model,
		Provider:     r.inner.Name(),
		IsCloud:      r.isCloud,
		InputTokens:  in,
		OutputTokens: out,
		DurationMs:   d.Milliseconds(),
	})
}

// recordingReader accumulates token counts off the stream and reports exactly
// once, when the stream is exhausted or closed.
type recordingReader struct {
	inner    llm.StreamReader
	model    string
	rp       *recordingProvider
	in, out  int
	reported bool
	start    time.Time
}

func (rr *recordingReader) Next() (llm.StreamEvent, bool, error) {
	ev, ok, err := rr.inner.Next()
	if ok {
		if ev.InputTokens > 0 {
			rr.in = ev.InputTokens
		}
		if ev.OutputTokens > 0 {
			rr.out = ev.OutputTokens
		}
	}
	if !ok && err == nil {
		rr.flush()
	}
	return ev, ok, err
}

func (rr *recordingReader) Close() error {
	rr.flush()
	return rr.inner.Close()
}

func (rr *recordingReader) flush() {
	if rr.reported {
		return
	}
	rr.reported = true
	rr.rp.report(rr.model, rr.in, rr.out, time.Since(rr.start))
}
