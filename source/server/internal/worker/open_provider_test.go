package worker

import (
	"context"
	"errors"
	"testing"

	"cercano/source/server/internal/llm"
)

func newTestReader(ctx context.Context) *openStreamReader {
	return &openStreamReader{
		p:    &streamOpenProvider{pending: make(map[uint64]*openStreamReader)},
		id:   1,
		ch:   make(chan llm.StreamEvent, 64),
		done: make(chan struct{}),
		ctx:  ctx,
	}
}

// Events pushed before a clean finish must all be drained, in order, before
// Next reports end-of-stream.
func TestOpenStreamReaderOrderedThenDone(t *testing.T) {
	r := newTestReader(context.Background())
	r.push(llm.StreamEvent{Type: llm.EventTextDelta, TextDelta: "a"})
	r.push(llm.StreamEvent{Type: llm.EventTextDelta, TextDelta: "b"})
	r.finish(nil)

	var got []string
	for {
		ev, ok, err := r.Next()
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if !ok {
			break
		}
		got = append(got, ev.TextDelta)
	}
	if len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Fatalf("events out of order or lost: %v", got)
	}
}

// A terminal error surfaces after any buffered events are drained.
func TestOpenStreamReaderError(t *testing.T) {
	r := newTestReader(context.Background())
	r.push(llm.StreamEvent{TextDelta: "x"})
	r.finish(errors.New("boom"))

	ev, ok, err := r.Next()
	if err != nil || !ok || ev.TextDelta != "x" {
		t.Fatalf("want first event x, got ev=%+v ok=%v err=%v", ev, ok, err)
	}
	if _, ok, err := r.Next(); ok || err == nil || err.Error() != "boom" {
		t.Fatalf("want terminal error boom, got ok=%v err=%v", ok, err)
	}
}

// A cancelled context ends the stream with the ctx error.
func TestOpenStreamReaderCtxCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	r := newTestReader(ctx)
	cancel()
	if _, ok, err := r.Next(); ok || err == nil {
		t.Fatalf("want ctx error, got ok=%v err=%v", ok, err)
	}
}

func TestHostManagedOpenProxyNameFollowsRuntime(t *testing.T) {
	p := newHostManagedOpenProxy(nil, "mistralrs")
	if got := p.Name(); got != "mistralrs" {
		t.Fatalf("proxy Name = %q, want mistralrs", got)
	}
	if !p.Capabilities().SupportsTools {
		t.Fatalf("host-managed open proxy should advertise tool support")
	}
}

func TestHostManagedOpenProxyNameFallback(t *testing.T) {
	p := newHostManagedOpenProxy(nil, "")
	if got := p.Name(); got != "host_managed_open" {
		t.Fatalf("proxy Name = %q, want host_managed_open", got)
	}
}
