package compactiongen

import (
	"context"
	"errors"
	"sync"
)

// startWork registers before Close can begin waiting. Parent cancellation and
// generator shutdown both reach the callback without replacing its metadata.
func (g *Generator) startWork(parent context.Context) (context.Context, func(), bool) {
	g.mu.Lock()
	if g.closed {
		g.mu.Unlock()
		return nil, nil, false
	}
	releaseActivity := func() {}
	if g.workAdmission != nil {
		var err error
		releaseActivity, err = g.workAdmission()
		if err != nil || releaseActivity == nil {
			g.mu.Unlock()
			return nil, nil, false
		}
	}
	g.activeWork++
	g.workers.Add(1)
	root := g.rootContext
	g.mu.Unlock()
	ctx, cancel := context.WithCancel(parent)
	stop := context.AfterFunc(root, cancel)
	var once sync.Once
	return ctx, func() {
		once.Do(func() {
			stop()
			cancel()
			g.mu.Lock()
			g.activeWork--
			g.mu.Unlock()
			releaseActivity()
			g.workers.Done()
		})
	}, true
}

// Close stops timers, cancels active callbacks, and waits within the caller's
// budget. The owner must call it before draining the shared accounting sink.
// Timeout means producers may still be running and must be reported as such.
func (g *Generator) Close(ctx context.Context) error {
	g.mu.Lock()
	if !g.closed {
		g.closed = true
		for id, timer := range g.timers {
			if timer.timer.Stop() {
				timer.release()
			}
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

// BindWorkAdmission is a startup-only hook. Late binding refuses rather than
// pretending existing untracked timers/callbacks are idle.
func (g *Generator) BindWorkAdmission(acquire func() (func(), error)) error {
	if acquire == nil {
		return errors.New("nil compaction work admission")
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.closed || g.workAdmission != nil || g.activeWork != 0 || len(g.timers) != 0 {
		return errors.New("compaction activity cannot be rebound or attached late")
	}
	g.workAdmission = acquire
	return nil
}
