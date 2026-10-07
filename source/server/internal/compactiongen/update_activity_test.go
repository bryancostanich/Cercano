package compactiongen

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"cercano/source/server/internal/compactor"
)

func updateGenerator(t *testing.T, delay time.Duration) (*Generator, *atomic.Int64) {
	t.Helper()
	g := New(nil, nil, compactor.Config{}, nil, delay)
	var active atomic.Int64
	if err := g.BindWorkAdmission(func() (func(), error) { active.Add(1); return func() { active.Add(-1) }, nil }); err != nil {
		t.Fatal(err)
	}
	g.SetEnabled(true)
	g.SetToolElisionOnly(true)
	g.SetElideOnlyFn(func(context.Context, string) (int, int, int, bool, error) { return 0, 0, 0, false, nil })
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := g.Close(ctx); err != nil {
			t.Error(err)
		}
	})
	return g, &active
}
func TestUpdateCompactionScheduledLeaseSurvivesReset(t *testing.T) {
	g, active := updateGenerator(t, time.Hour)
	g.Schedule("c")
	g.Schedule("c")
	if active.Load() != 1 {
		t.Fatalf("queued timer not counted exactly once: %d", active.Load())
	}
	if err := g.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if active.Load() != 0 {
		t.Fatal("stopped timer retained activity")
	}
}
func TestUpdateCompactionRunningLeaseOutlivesCancellation(t *testing.T) {
	g, active := updateGenerator(t, 5*time.Millisecond)
	entered, finish := make(chan struct{}), make(chan struct{})
	g.SetElideOnlyFn(func(context.Context, string) (int, int, int, bool, error) {
		close(entered)
		<-finish
		return 0, 0, 0, false, nil
	})
	g.Schedule("c")
	select {
	case <-entered:
	case <-time.After(time.Second):
		close(finish)
		t.Fatal("callback did not start")
	}
	if active.Load() < 1 {
		close(finish)
		t.Fatal("callback not retained")
	}
	// Queue another pass while this callback is running. Close must release the
	// pending pass but keep the old callback counted until it really returns.
	g.mu.Lock()
	g.debounce = time.Hour
	g.mu.Unlock()
	g.Schedule("c")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	err := g.Close(ctx)
	if !errors.Is(err, context.DeadlineExceeded) || active.Load() == 0 {
		close(finish)
		t.Fatalf("running callback falsely idle: %d %v", active.Load(), err)
	}
	close(finish)
	done, cancelDone := context.WithTimeout(context.Background(), time.Second)
	defer cancelDone()
	if err = g.Close(done); err != nil {
		t.Fatal(err)
	}
	if active.Load() != 0 {
		t.Fatal("callback activity leaked")
	}
}
func TestUpdateCompactionRefusedAdmissionStartsNothing(t *testing.T) {
	g := New(nil, nil, compactor.Config{}, nil, time.Hour)
	g.SetEnabled(true)
	refused := errors.New("paused")
	if err := g.BindWorkAdmission(func() (func(), error) { return nil, refused }); err != nil {
		t.Fatal(err)
	}
	g.Schedule("c")
	if len(g.timers) != 0 {
		t.Fatal("refused schedule created timer")
	}
	if err := g.CompactNow(context.Background(), "c"); !errors.Is(err, context.Canceled) {
		t.Fatalf("refused immediate work ran: %v", err)
	}
	if err := g.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
}
func TestUpdateCompactionLateBindingRefusesPendingWork(t *testing.T) {
	g := New(nil, nil, compactor.Config{}, nil, time.Hour)
	g.SetEnabled(true)
	g.Schedule("c")
	if err := g.BindWorkAdmission(func() (func(), error) { return func() {}, nil }); err == nil {
		t.Fatal("pending untracked work treated as idle")
	}
	g.Close(context.Background())
}

func TestUpdateCompactionAsyncAcceptedBeforeReturn(t *testing.T) {
	g, active := updateGenerator(t, time.Hour)
	finish := make(chan struct{})
	var once atomic.Bool
	g.SetElideOnlyFn(func(context.Context, string) (int, int, int, bool, error) { <-finish; return 0, 0, 0, false, nil })
	defer func() {
		if once.CompareAndSwap(false, true) {
			close(finish)
		}
	}()
	if err := g.CompactAsync("c"); err != nil {
		t.Fatal(err)
	}
	if active.Load() == 0 {
		t.Fatal("accepted asynchronous work has an idle gap")
	}
	if once.CompareAndSwap(false, true) {
		close(finish)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := g.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if active.Load() != 0 {
		t.Fatal("async work lease leaked")
	}
}
