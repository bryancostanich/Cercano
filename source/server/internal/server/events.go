// Package server — server->client event push.
//
// Clients hold a single SubscribeEvents stream open for their whole session.
// The agent broadcasts unsolicited state changes (today: permission-mode
// changes) to every open stream, so clients never poll. The hub is a small
// fan-out: subscribe registers a buffered channel, broadcast does a
// non-blocking send to each. Mode changes are rare, so the buffer never
// realistically fills; a wedged-slow client is dropped a frame rather than
// stalling the broadcast for everyone.
package server

import (
	"sync"

	"cercano/source/server/pkg/proto"
)

// eventHub fans server-originated ClientEvents out to all open subscriber
// streams. Safe for concurrent Subscribe / broadcast / unsubscribe.
type eventHub struct {
	mu   sync.Mutex
	next int
	subs map[int]chan *proto.ClientEvent
}

func newEventHub() *eventHub {
	return &eventHub{subs: make(map[int]chan *proto.ClientEvent)}
}

// subscribe registers a new subscriber and returns its event channel plus an
// unsubscribe func the caller MUST invoke when its stream ends.
func (h *eventHub) subscribe() (<-chan *proto.ClientEvent, func()) {
	ch := make(chan *proto.ClientEvent, 16)
	h.mu.Lock()
	id := h.next
	h.next++
	h.subs[id] = ch
	h.mu.Unlock()
	return ch, func() {
		h.mu.Lock()
		if c, ok := h.subs[id]; ok {
			delete(h.subs, id)
			close(c)
		}
		h.mu.Unlock()
	}
}

// broadcast delivers ev to every current subscriber without blocking on any
// one of them.
func (h *eventHub) broadcast(ev *proto.ClientEvent) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, ch := range h.subs {
		select {
		case ch <- ev:
		default: // subscriber is wedged; drop rather than stall the others
		}
	}
}

// closeAll closes every subscriber channel and empties the hub, ending each
// standing SubscribeEvents handler loop with a nil error. Shutdown-only:
// without this, GracefulStop blocks forever on attached clients. Previously
// returned unsubscribe funcs stay safe — they find their id already deleted.
func (h *eventHub) closeAll() {
	h.mu.Lock()
	defer h.mu.Unlock()
	for id, ch := range h.subs {
		delete(h.subs, id)
		close(ch)
	}
}

// subscriberCount reports how many streams are currently open (test/observability).
func (h *eventHub) subscriberCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.subs)
}

// SubscribeEvents implements proto.AgentServer — a standing server->client
// stream. It blocks, forwarding hub events to the client until the client
// disconnects or the server shuts the stream down.
func (s *Server) SubscribeEvents(_ *proto.SubscribeEventsRequest, stream proto.Agent_SubscribeEventsServer) error {
	if s.events == nil {
		s.events = newEventHub()
	}
	ch, unsubscribe := s.events.subscribe()
	defer unsubscribe()

	ctx := stream.Context()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case ev, ok := <-ch:
			if !ok {
				return nil
			}
			if err := stream.Send(ev); err != nil {
				return err
			}
		}
	}
}

// broadcastConfigChanged pushes a ConfigChanged event for a single field to all
// clients. Both the UpdateConfig RPC and the config-file watcher route through
// the same UpdateConfig path, so this is the one place a value-actually-changed
// event is emitted. No dedupe: UpdateConfig only calls in for fields that
// passed validation and were actually applied, so a repeat broadcast for the
// same value can't happen from the agent side.
func (s *Server) broadcastConfigChanged(field, value string) {
	if s.events == nil {
		return
	}
	s.events.broadcast(&proto.ClientEvent{
		Event: &proto.ClientEvent_ConfigChanged{
			ConfigChanged: &proto.ConfigChanged{Field: field, Value: value},
		},
	})
}

// broadcastPermissionMode pushes a PermissionModeChanged event to all clients.
// Deduplication is handled by the permissions.Broker (both the SetMode RPC path
// and the file-watcher path call through the broker before arriving here).
func (s *Server) broadcastPermissionMode(mode string) {
	if s.events == nil {
		return
	}
	s.events.broadcast(&proto.ClientEvent{
		Event: &proto.ClientEvent_PermissionModeChanged{
			PermissionModeChanged: &proto.PermissionModeChanged{Mode: mode},
		},
	})
}

// Open-runtime status is now built by the single runtime-agnostic readiness
// path in open_runtime_readiness.go (openRuntimeStatus / openRuntimeStatusFrom),
// which replaced the per-runtime buildOpenRuntimeStatus + buildMistralRSStatus
// forks and their `runtime != "llama_server"` gates.

// broadcastOpenRuntimeStatus pushes a OpenRuntimeStatusChanged event for
// the current state of the active local inference runtime. Called from the
// runtime-swap path in UpdateConfig — one event per swap attempt (ok or not)
// so clients always have a fresh chip regardless of whether the runtime
// works or is broken.
//
// status is passed pre-built so callers can populate fields from a
// llamaserver.DetectError, from a healthy post-detect config, or from a
// fresh install-completion event without a helper here having to know all
// three shapes.
func (s *Server) broadcastOpenRuntimeStatus(status *proto.OpenRuntimeStatus) {
	if s.events == nil || status == nil {
		return
	}
	s.events.broadcast(&proto.ClientEvent{
		Event: &proto.ClientEvent_OpenRuntimeStatusChanged{
			OpenRuntimeStatusChanged: &proto.OpenRuntimeStatusChanged{
				Status: status,
			},
		},
	})
}


