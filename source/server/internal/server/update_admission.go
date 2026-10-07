package server

import (
	"context"
	"errors"
	"sync"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// updateAdmission counts actual admitted lifetimes, not broker generations or
// connected observers. Its zero value is ready. No public RPC arms it yet:
// background work sources must be covered before exposing update preparation.
type updateAdmission struct {
	mu      sync.Mutex
	active  uint64
	paused  bool
	changed chan struct{}
}

var errUpdateAdmissionPaused = errors.New("agent update preparation is active; retry shortly")

func (g *updateAdmission) notifyLocked() {
	if g.changed != nil {
		close(g.changed)
	}
	g.changed = make(chan struct{})
}
func (g *updateAdmission) enter() (func(), error) {
	g.mu.Lock()
	if g.paused {
		g.mu.Unlock()
		return nil, errUpdateAdmissionPaused
	}
	g.active++
	g.mu.Unlock()
	var once sync.Once
	return func() { once.Do(func() { g.mu.Lock(); g.active--; g.notifyLocked(); g.mu.Unlock() }) }, nil
}

// pauseWhenIdle does not cancel work or freeze nested calls while work exists.
// Once zero is observed under the admission mutex, new work is refused atomically.
// Cancellation while waiting leaves normal admission untouched. The successful
// caller owns release; its eventual stream/process lifetime will govern release.
func (g *updateAdmission) pauseWhenIdle(ctx context.Context) (func(), error) {
	for {
		g.mu.Lock()
		if err := ctx.Err(); err != nil {
			g.mu.Unlock()
			return nil, err
		}
		if g.paused {
			g.mu.Unlock()
			return nil, errUpdateAdmissionPaused
		}
		if g.active == 0 {
			g.paused = true
			g.mu.Unlock()
			var once sync.Once
			return func() { once.Do(func() { g.mu.Lock(); g.paused = false; g.notifyLocked(); g.mu.Unlock() }) }, nil
		}
		if g.changed == nil {
			g.changed = make(chan struct{})
		}
		changed := g.changed
		g.mu.Unlock()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-changed:
		}
	}
}

func (s *Server) admitUpdateWork() (func(), error) {
	release, err := s.updateWork.enter()
	if err != nil {
		return nil, status.Error(codes.Unavailable, err.Error())
	}
	return release, nil
}
