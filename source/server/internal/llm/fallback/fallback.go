// Package fallback wraps a primary and a backup cloud provider in a single
// inference.Provider, failing calls over to the backup when the primary fails in a
// way the backup could serve (see ShouldFailover). The wrapper is invisible to
// everything downstream — the router, coordinator, and tool loop see one
// provider.
package fallback

import (
	"context"
	"errors"

	"cercano/source/server/internal/inference"
	"cercano/source/server/internal/llm"
)

// Provider is the composite. The two providers speak different model
// namespaces, so on failover the request's model must be rewritten — but
// experience-preservingly: a request that carries its capability tier
// (llm.ChatRequest.Tier) is re-resolved to the BACKUP vendor's model for that
// same tier, so economy-tier work (e.g. the compaction summarizer) lands on
// the backup's economy model, not its premium default. Requests without a
// tier get the backup profile's default model.
type Provider struct {
	primary        inference.Provider
	backup         inference.Provider
	backupModelFor func(tier string) string
	onFailover     func(stage string, err error)
}

// New builds the composite. backupModelFor maps a capability-tier name to the
// backup vendor's model for that tier; it is called with "" for untiered
// requests and must then return the backup profile's default model.
// onFailover, when non-nil, is invoked once per failed-over call with the
// stage ("chat", "stream_dial", "stream_first") and the primary's error — the
// server logs it so backup-served turns are visible.
func New(primary, backup inference.Provider, backupModelFor func(tier string) string, onFailover func(stage string, err error)) *Provider {
	return &Provider{primary: primary, backup: backup, backupModelFor: backupModelFor, onFailover: onFailover}
}

// Name reports the primary's name: the composite impersonates the primary
// everywhere except the moment of failover, which is surfaced via onFailover.
func (p *Provider) Name() string { return p.primary.Name() }

func (p *Provider) Capabilities() inference.Capabilities { return p.primary.Capabilities() }

func (p *Provider) notify(stage string, err error) {
	if p.onFailover != nil {
		p.onFailover(stage, err)
	}
}

// backupRequest rewrites the request into the backup provider's model
// namespace, preserving the request's capability tier when it carries one.
func (p *Provider) backupRequest(req llm.ChatRequest) llm.ChatRequest {
	req.Model = p.backupModelFor(req.Tier)
	return req
}

func (p *Provider) Chat(ctx context.Context, req llm.ChatRequest) (llm.ChatResponse, error) {
	resp, err := p.primary.Chat(ctx, req)
	if err == nil || ctx.Err() != nil || !ShouldFailover(err) {
		return resp, err
	}
	p.notify("chat", err)
	return p.backup.Chat(ctx, p.backupRequest(req))
}

// StreamChat fails over in two places: a dial error from the primary, and —
// because some SDKs are lazy and only touch the network on the first read — a
// first-event failure from the primary's reader, provided nothing has been
// emitted yet. Once any real event has flowed, a failure stays an error:
// silently re-serving from the backup would duplicate already-delivered text.
func (p *Provider) StreamChat(ctx context.Context, req llm.ChatRequest) (llm.StreamReader, error) {
	r, err := p.primary.StreamChat(ctx, req)
	if err != nil {
		if ctx.Err() != nil || !ShouldFailover(err) {
			return nil, err
		}
		p.notify("stream_dial", err)
		return p.backup.StreamChat(ctx, p.backupRequest(req))
	}
	return &failoverReader{ctx: ctx, p: p, req: req, inner: r}, nil
}

// failoverReader passes the primary's stream through, but if the very first
// thing the stream produces is a failure (an error return, or an EventError
// event), it closes the primary and re-serves the request from the backup.
type failoverReader struct {
	ctx        context.Context
	p          *Provider
	req        llm.ChatRequest
	inner      llm.StreamReader
	emitted    bool // a real event has been delivered; failover is off the table
	failedOver bool // already on the backup; never cascade
}

func (r *failoverReader) Next() (llm.StreamEvent, bool, error) {
	ev, ok, err := r.inner.Next()
	if r.emitted || r.failedOver {
		return ev, ok, err
	}
	switch {
	case err != nil:
		if r.ctx.Err() != nil || !ShouldFailover(err) {
			return ev, ok, err
		}
		r.p.notify("stream_first", err)
	case ok && ev.Type == llm.EventError:
		// In-band error event before any content; ErrText carries no status
		// code, so this follows the unknown-error rule: fail over.
		r.p.notify("stream_first", errors.New(ev.ErrText))
	default:
		// First real event (or a clean immediate end): the primary stream is
		// live; pass everything through from here on.
		r.emitted = true
		return ev, ok, err
	}
	_ = r.inner.Close()
	backup, berr := r.p.backup.StreamChat(r.ctx, r.p.backupRequest(r.req))
	if berr != nil {
		return llm.StreamEvent{}, false, berr
	}
	r.inner = backup
	r.failedOver = true
	return r.inner.Next()
}

func (r *failoverReader) Close() error { return r.inner.Close() }
