package recap

import "context"

// startWork registers before Close can begin waiting. Parent cancellation and
// generator shutdown both reach the callback without replacing its metadata.
func (g *Generator) startWork(parent context.Context) (context.Context, func(), bool) {
	g.mu.Lock()
	if g.closed {
		g.mu.Unlock()
		return nil, nil, false
	}
	g.workers.Add(1)
	root := g.rootContext
	g.mu.Unlock()
	ctx, cancel := context.WithCancel(parent)
	stop := context.AfterFunc(root, cancel)
	return ctx, func() { stop(); cancel(); g.workers.Done() }, true
}

// Close stops timers, cancels active callbacks, and waits within the caller's
// budget. The owner must call it before draining the shared accounting sink.
// Timeout means producers may still be running and must be reported as such.
func (g *Generator) Close(ctx context.Context) error {
	g.mu.Lock()
	if !g.closed {
		g.closed = true
		for id, timer := range g.timers {
			timer.Stop()
			delete(g.timers, id)
		}
		g.cancelRoot()
		go func() { g.workers.Wait(); close(g.workersDone) }()
	}
	g.mu.Unlock()
	select {
	case <-g.workersDone:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
