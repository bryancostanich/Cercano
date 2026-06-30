// Package usage records per-call LLM token usage at the provider boundary.
// A RecordingProvider decorates an llm.Provider; every completed Chat or
// fully-drained StreamChat reports one Usage to a sink. This is the single
// chokepoint for cost telemetry across the main loop, co-processor work, and
// dispatched subagents (each provider is exactly one of cloud/local, so the
// tier is known for free). The live-context meter is a separate system and is
// unaffected.
package usage

import (
	"context"

	"cercano/source/server/internal/llm"
)

// Usage is one recorded model call.
type Usage struct {
	Source               string // who initiated it, e.g. "main", "coproc:summarize", "dispatch"
	Model                string
	IsCloud              bool
	InputTokens          int
	OutputTokens         int
	ContentTokensAvoided int  // estimated cloud tokens saved by handling locally
	TokenSaving          bool // true when this call substitutes for a cloud call
}

// Wrap returns a provider that reports a Usage to sink after each call. sink
// must tolerate being called from the goroutine that drains a stream; it is
// nil-safe (a nil sink disables recording).
func Wrap(p llm.Provider, source string, isCloud bool, sink func(Usage)) llm.Provider {
	return &recordingProvider{inner: p, source: source, isCloud: isCloud, sink: sink}
}

type recordingProvider struct {
	inner   llm.Provider
	source  string
	isCloud bool
	sink    func(Usage)
}

func (r *recordingProvider) Name() string                   { return r.inner.Name() }
func (r *recordingProvider) Capabilities() llm.Capabilities { return r.inner.Capabilities() }

func (r *recordingProvider) Chat(ctx context.Context, req llm.ChatRequest) (llm.ChatResponse, error) {
	resp, err := r.inner.Chat(ctx, req)
	if err == nil {
		r.report(req.Model, resp.InputTokens, resp.OutputTokens)
	}
	return resp, err
}

func (r *recordingProvider) StreamChat(ctx context.Context, req llm.ChatRequest) (llm.StreamReader, error) {
	inner, err := r.inner.StreamChat(ctx, req)
	if err != nil {
		return nil, err
	}
	return &recordingReader{inner: inner, model: req.Model, rp: r}, nil
}

func (r *recordingProvider) report(model string, in, out int) {
	if r.sink == nil {
		return
	}
	r.sink(Usage{Source: r.source, Model: model, IsCloud: r.isCloud, InputTokens: in, OutputTokens: out})
}

// recordingReader accumulates token counts off the stream and reports exactly
// once, when the stream is exhausted or closed.
type recordingReader struct {
	inner    llm.StreamReader
	model    string
	rp       *recordingProvider
	in, out  int
	reported bool
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
	rr.rp.report(rr.model, rr.in, rr.out)
}
