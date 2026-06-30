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

	"cercano/source/server/internal/meridian"
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

// broadcastPermissionMode pushes a PermissionModeChanged event to all clients,
// deduped by value: the SetPermissionMode RPC and the file watcher both fire on
// the same write, and a no-op rewrite (same mode) shouldn't spam clients. Only a
// mode that differs from the last broadcast propagates. New subscribers don't
// rely on a broadcast for their initial value — they fetch it once on connect —
// so suppressing identical repeats is safe.
func (s *Server) broadcastPermissionMode(mode string) {
	if s.events == nil {
		return
	}
	s.permBcastMu.Lock()
	if mode == s.lastBcastMode {
		s.permBcastMu.Unlock()
		return
	}
	s.lastBcastMode = mode
	s.permBcastMu.Unlock()

	s.events.broadcast(&proto.ClientEvent{
		Event: &proto.ClientEvent_PermissionModeChanged{
			PermissionModeChanged: &proto.PermissionModeChanged{Mode: mode},
		},
	})
}

// broadcastMeridianStatus pushes a MeridianStatusChanged event for the current
// state of the local Meridian proxy. Called from the meridian.Manager status
// listener wired up in SetupMeridian — there's exactly one source of these
// events, so no dedupe is needed.
func (s *Server) broadcastMeridianStatus(st meridian.Status) {
	if s.events == nil {
		return
	}
	s.events.broadcast(&proto.ClientEvent{
		Event: &proto.ClientEvent_MeridianStatusChanged{
			MeridianStatusChanged: &proto.MeridianStatusChanged{
				Status: meridianStatusToProto(st),
			},
		},
	})
}

// meridianStatusToProto converts the internal meridian.Status struct to the
// wire-level proto. Shared by the event broadcast and the GetCloudProfiles
// initial-fetch path.
func meridianStatusToProto(st meridian.Status) *proto.MeridianStatus {
	return &proto.MeridianStatus{
		State:       st.State.String(),
		Message:     st.Message,
		Port:        int32(st.Port),
		MissingDeps: st.MissingDeps,
	}
}
