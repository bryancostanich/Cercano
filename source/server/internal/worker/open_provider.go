package worker

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"

	"cercano/source/server/internal/inference"
	"cercano/source/server/internal/llm"
	proto "cercano/source/server/pkg/proto"
)

// streamOpenProvider is the worker's open (local-runtime) inference.Provider. The
// worker process has no runtime manager — that's a host singleton that owns the
// llama-server subprocesses — so it proxies open-model inference to the host
// over the existing RunTurn bidi stream. StreamChat sends an
// OpenInferenceRequest and streams the host's OpenInferenceEvents back, routed
// by monotonic id; Chat accumulates its own stream via CollectStream. Mirrors
// streamCredentialSource.
type streamOpenProvider struct {
	sndr   *sender
	name   string
	caps   inference.Capabilities
	nextID atomic.Uint64

	mu      sync.Mutex
	pending map[uint64]*openStreamReader
}

func newStreamOpenProvider(sndr *sender, name string, caps inference.Capabilities) *streamOpenProvider {
	return &streamOpenProvider{
		sndr:    sndr,
		name:    name,
		caps:    caps,
		pending: make(map[uint64]*openStreamReader),
	}
}

func (p *streamOpenProvider) Name() string                         { return p.name }
func (p *streamOpenProvider) Capabilities() inference.Capabilities { return p.caps }

// StreamChat proxies a streaming open-model call to the host.
func (p *streamOpenProvider) StreamChat(ctx context.Context, req llm.ChatRequest) (llm.StreamReader, error) {
	pr, err := MarshalChatRequest(req)
	if err != nil {
		return nil, fmt.Errorf("worker/open: marshal request: %w", err)
	}
	id := p.nextID.Add(1)
	r := &openStreamReader{
		p:    p,
		id:   id,
		ch:   make(chan llm.StreamEvent, 64),
		done: make(chan struct{}),
		ctx:  ctx,
	}
	p.mu.Lock()
	p.pending[id] = r
	p.mu.Unlock()

	p.sndr.send(&proto.WorkerToHost{Msg: &proto.WorkerToHost_OpenRequest{OpenRequest: &proto.OpenInferenceRequest{
		Id:      id,
		Request: pr,
	}}})
	return r, nil
}

// Chat proxies a non-streaming call by accumulating its own stream.
func (p *streamOpenProvider) Chat(ctx context.Context, req llm.ChatRequest) (llm.ChatResponse, error) {
	rdr, err := p.StreamChat(ctx, req)
	if err != nil {
		return llm.ChatResponse{}, err
	}
	defer rdr.Close()
	return llm.CollectStream(ctx, rdr, nil)
}

// deliver routes an OpenInferenceEvent from the host to the pending reader.
// Called serially from the worker recv loop, so events for a given id arrive in
// order and the terminal (done/error) always follows its events.
func (p *streamOpenProvider) deliver(ev *proto.OpenInferenceEvent) {
	p.mu.Lock()
	r, ok := p.pending[ev.GetId()]
	p.mu.Unlock()
	if !ok {
		return // unknown or already-closed id
	}
	switch ev.GetKind().(type) {
	case *proto.OpenInferenceEvent_Event:
		r.push(UnmarshalStreamEvent(ev.GetEvent()))
	case *proto.OpenInferenceEvent_Error:
		r.finish(fmt.Errorf("%s", ev.GetError()))
	case *proto.OpenInferenceEvent_Done:
		r.finish(nil)
	}
}

// openStreamReader implements llm.StreamReader over the per-id event channel.
// The event channel is never closed (a late deliver would panic); termination
// is signalled by closing done and storing the terminal error.
type openStreamReader struct {
	p    *streamOpenProvider
	id   uint64
	ch   chan llm.StreamEvent
	done chan struct{}
	ctx  context.Context

	once sync.Once
	err  error
}

// push enqueues an event, unblocking if the reader terminates or ctx cancels
// (so it never stalls the recv loop past the reader's lifetime).
func (r *openStreamReader) push(ev llm.StreamEvent) {
	select {
	case r.ch <- ev:
	case <-r.done:
	case <-r.ctx.Done():
	}
}

// finish records the terminal error and signals end-of-stream. Idempotent.
func (r *openStreamReader) finish(err error) {
	r.once.Do(func() {
		r.err = err
		close(r.done)
	})
}

func (r *openStreamReader) Next() (llm.StreamEvent, bool, error) {
	// Drain a buffered event first so trailing events aren't lost to a
	// concurrently-signalled done.
	select {
	case ev := <-r.ch:
		return ev, true, nil
	default:
	}
	select {
	case <-r.ctx.Done():
		return llm.StreamEvent{}, false, r.ctx.Err()
	case ev := <-r.ch:
		return ev, true, nil
	case <-r.done:
		select {
		case ev := <-r.ch:
			return ev, true, nil
		default:
			return llm.StreamEvent{}, false, r.err
		}
	}
}

func (r *openStreamReader) Close() error {
	r.p.mu.Lock()
	delete(r.p.pending, r.id)
	r.p.mu.Unlock()
	r.finish(nil) // idempotent; unblocks a pending push
	return nil
}

// newLlamaServerProxy builds the worker's open-provider proxy for the
// llama-server runtime, advertising the same capabilities the host's llama
// provider reports (SupportsTools).
func newLlamaServerProxy(sndr *sender) *streamOpenProvider {
	return newStreamOpenProvider(sndr, "llama_server", inference.Capabilities{SupportsTools: true})
}
