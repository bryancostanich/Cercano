// Package broker owns the per-conversation turn-exclusivity registry plus
// per-turn event fan-out and replay buffer.
//
// Turn exclusivity: at most one turn is live per conversation. A new turn
// supersedes (cancels) any prior turn and advances the per-conversation
// generation counter so the superseded turn's persistence and event emission
// are fenced out.
//
// Fan-out / replay: Publish fans a runner.Event to all attached subscribers
// under mu, fenced by generation. Attach atomically snapshots the current
// turn's buffer (replay) and registers a new subscriber channel, so every
// event is either in replay (published before Attach) or on ch (published
// after) — never both, never neither.
//
// broker is proto-free and does not import internal/server.
package broker

import (
	"context"
	"sync"

	"cercano/source/server/internal/runner"
)

const subChanCap = 64 // buffered subscriber channel capacity; drop-on-full if full

// convState bundles all per-conversation mutable state under Broker.mu.
type convState struct {
	// turn-exclusivity
	gen    uint64          // current turn generation (monotonic, starts at 0 = no turn)
	handle *turnHandle     // nil when no active turn

	// fan-out + replay
	buffer []runner.Event         // current turn's events (reset each BeginTurn)
	subs   map[int]chan runner.Event // attached subscriber channels
	nextID int                    // monotonic subscriber-id counter
}

// turnHandle tracks one in-flight turn for a conversation.
type turnHandle struct {
	gen    uint64
	cancel context.CancelFunc
}

// Broker is the per-conversation turn registry, fan-out hub, and replay buffer.
// The zero value is not usable; call New.
type Broker struct {
	mu    sync.Mutex
	convs map[string]*convState
}

// New returns a ready-to-use Broker.
func New() *Broker {
	return &Broker{
		convs: make(map[string]*convState),
	}
}

// convLocked returns the convState for conv, creating it if absent.
// Caller holds b.mu.
func (b *Broker) convLocked(conv string) *convState {
	cs, ok := b.convs[conv]
	if !ok {
		cs = &convState{subs: make(map[int]chan runner.Event)}
		b.convs[conv] = cs
	}
	return cs
}

// BeginTurn registers a new turn for conv, superseding any turn already running
// there (cancels its ctx). Returns a ctx that is canceled when this turn is
// itself superseded or when parent is done, this turn's generation, and a
// release func the caller must defer. The fence helpers (IsCurrent) gate
// persistence/emission on the returned gen.
//
// BeginTurn resets the per-conversation replay buffer (fresh turn = empty
// replay for any mid-turn joiner). Existing subscribers are kept attached
// across the turn boundary; they will see the old turn's events stop and the
// new turn's start.
func (b *Broker) BeginTurn(parent context.Context, conv string) (context.Context, uint64, func()) {
	ctx, cancel := context.WithCancel(parent)
	b.mu.Lock()
	cs := b.convLocked(conv)
	if cs.handle != nil {
		cs.handle.cancel() // supersede the turn already running on this conversation
	}
	cs.gen++
	h := &turnHandle{gen: cs.gen, cancel: cancel}
	cs.handle = h
	cs.buffer = nil // reset replay buffer for the new turn
	b.mu.Unlock()

	release := func() {
		cancel()
		b.mu.Lock()
		// Only clear the registration if it's still ours — a superseding turn
		// may have replaced it, and must not have its handle removed by us.
		cs2 := b.convLocked(conv)
		if cs2.handle == h {
			cs2.handle = nil
		}
		b.mu.Unlock()
	}
	return ctx, h.gen, release
}

// IsCurrent reports whether gen is still the live generation for conv — the
// fence that gates a turn's persistence and event emission. A superseded turn
// fails this and goes quiet.
func (b *Broker) IsCurrent(conv string, gen uint64) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	cs, ok := b.convs[conv]
	if !ok {
		return false
	}
	return cs.gen == gen
}

// HasActiveTurn reports whether a turn is currently registered for conv (test
// seam for the release-on-return invariant).
func (b *Broker) HasActiveTurn(conv string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	cs, ok := b.convs[conv]
	if !ok {
		return false
	}
	return cs.handle != nil
}

// Publish delivers ev to all current subscribers and appends it to the
// conversation's replay buffer, but ONLY if gen matches the live generation
// for conv. A stale (superseded) gen is silently dropped.
//
// Subscriber sends are non-blocking (drop-on-full): a wedged subscriber cannot
// stall the publisher or other subscribers.
//
// Everything runs under b.mu, so a concurrent Attach either sees ev in the
// buffer (if Attach runs after Publish returns) or on ch (if Attach ran and
// registered its channel before Publish ran). No event can be in both places
// or in neither.
func (b *Broker) Publish(conv string, gen uint64, ev runner.Event) {
	b.mu.Lock()
	defer b.mu.Unlock()
	cs, ok := b.convs[conv]
	if !ok || cs.gen != gen {
		return // fenced: stale or unknown conversation
	}
	cs.buffer = append(cs.buffer, ev)
	for _, ch := range cs.subs {
		select {
		case ch <- ev:
		default: // drop-on-full; one wedged surface does not stall the turn
		}
	}
}

// Attach returns a defensive copy of the conversation's current replay buffer
// plus a live subscriber channel and a detach function.
//
// Atomicity guarantee: because Publish and Attach both hold b.mu, every event
// published to this conversation is EITHER in replay (published before Attach
// acquired the lock) OR delivered on ch (published after Attach registered the
// channel). No event can appear in both (duplicate) or in neither (gap).
//
// The returned ch has capacity subChanCap (64). If the caller drains slowly,
// events are dropped on full (same non-blocking-drop policy as Publish's fan-
// out loop). The caller must call detach() when done; detach closes ch and
// removes the channel from the subscriber set.
func (b *Broker) Attach(conv string) (replay []runner.Event, ch <-chan runner.Event, detach func()) {
	b.mu.Lock()
	cs := b.convLocked(conv)

	// Defensive copy of the buffer — caller must not see later mutations.
	if len(cs.buffer) > 0 {
		replay = make([]runner.Event, len(cs.buffer))
		copy(replay, cs.buffer)
	}

	c := make(chan runner.Event, subChanCap)
	id := cs.nextID
	cs.nextID++
	cs.subs[id] = c
	b.mu.Unlock()

	detach = func() {
		b.mu.Lock()
		cs2, ok := b.convs[conv]
		if ok {
			delete(cs2.subs, id)
		}
		b.mu.Unlock()
		close(c)
	}
	return replay, c, detach
}
