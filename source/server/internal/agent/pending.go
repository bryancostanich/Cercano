package agent

import (
	"context"
	"sync"
)

type Decision struct {
	Allow bool
}

type PendingDecisions struct {
	mu       sync.Mutex
	channels map[string]chan Decision
}

func NewPendingDecisions() *PendingDecisions {
	return &PendingDecisions{channels: map[string]chan Decision{}}
}

func (p *PendingDecisions) Wait(ctx context.Context, toolUseID string) (bool, error) {
	ch := make(chan Decision, 1)
	p.mu.Lock()
	p.channels[toolUseID] = ch
	p.mu.Unlock()
	defer func() {
		p.mu.Lock()
		delete(p.channels, toolUseID)
		p.mu.Unlock()
	}()
	select {
	case d := <-ch:
		return d.Allow, nil
	case <-ctx.Done():
		return false, ctx.Err()
	}
}

func (p *PendingDecisions) Resolve(toolUseID string, allow bool) bool {
	p.mu.Lock()
	ch, ok := p.channels[toolUseID]
	p.mu.Unlock()
	if !ok {
		return false
	}
	ch <- Decision{Allow: allow}
	return true
}
